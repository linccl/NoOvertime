package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	apperrors "noovertime/internal/errors"
	"noovertime/internal/storage"
)

var (
	notifyContext  = signal.NotifyContext
	listenAndServe = func(s *http.Server) error { return s.ListenAndServe() }
	shutdownServer = func(s *http.Server, ctx context.Context) error { return s.Shutdown(ctx) }
)

const (
	requestIDHeader      = "X-Request-ID"
	internalErrorCode    = "INTERNAL_ERROR"
	internalErrorMessage = "internal server error"
	uploadCleanupPeriod  = 12 * time.Hour
)

type requestIDContextKey struct{}

type appHandler func(w http.ResponseWriter, r *http.Request) error

type componentHealth struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type healthResponse struct {
	App      componentHealth `json:"app"`
	Database componentHealth `json:"database"`
}

type ServerOption func(*Server)

// HealthChecker defines the dependency that can report health.
type HealthChecker interface {
	Health(ctx context.Context) error
}

// Server is a minimal HTTP server bootstrap for the API service.
type Server struct {
	httpServer        *http.Server
	mux               *http.ServeMux
	db                HealthChecker
	migrationRateGate *migrationRateGate
	now               func() time.Time
	punchPhotoStore   storage.ObjectStore
	logStore          storage.ObjectStore
	localUploadDirs   []string
}

// NewServer builds an API server with the required dependencies.
func NewServer(addr string, db HealthChecker, opts ...ServerOption) *Server {
	mux := http.NewServeMux()
	s := &Server{
		mux:               mux,
		db:                db,
		migrationRateGate: newMigrationRateGate(),
		now:               time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}

	s.handle("/health", s.healthHandler)
	s.handle(tokensIssuePath, s.tokenIssueHandler)
	s.handle(tokensRotatePath, s.tokenRotateHandler)
	s.handle("/api/v1/sync/commits", s.syncCommitsHandler)
	if s.punchPhotoStore != nil {
		s.handle(punchPhotoUploadPath, s.punchPhotoUploadHandler)
	}
	if s.logStore != nil {
		s.handle(logUploadPath, s.logUploadHandler)
	}
	s.handle("/api/v1/migrations/requests", s.migrationRequestsHandler)
	s.handle(migrationsConfirmPathPattern, s.migrationConfirmHandler)
	s.handle(migrationsTakeoverPath, pausedEndpointHandler("migration takeover"))
	s.handle(migrationsForcedTakeoverPath, pausedEndpointHandler("migration forced takeover"))
	s.handle(pairingCodeQueryPath, pausedEndpointHandler("pairing code query"))
	s.handle(pairingCodeResetPath, pausedEndpointHandler("pairing code reset"))
	s.handle(recoveryCodeGeneratePath, pausedEndpointHandler("recovery code generate"))
	s.handle(recoveryCodeResetPath, pausedEndpointHandler("recovery code reset"))
	s.handle(webReadBindingsPath, pausedEndpointHandler("web read binding"))
	s.handle(webReadBindingsAuthPath, pausedEndpointHandler("web read binding auth"))
	s.handle(webMonthSummariesQueryPath, s.webMonthSummariesQueryHandler)
	s.handle(webDaySummariesQueryPath, s.webDaySummariesQueryHandler)

	mux.Handle("/web/", http.StripPrefix("/web/", http.FileServer(http.Dir("./web"))))
	if len(s.localUploadDirs) > 0 {
		mux.Handle("/uploads/", http.StripPrefix("/uploads/", newUploadFileHandler(s.localUploadDirs)))
	}
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.requestIDMiddleware(s.requestLogMiddleware(s.recoveryMiddleware(mux))),
	}

	return s
}

func WithObjectStore(store storage.ObjectStore) ServerOption {
	return func(s *Server) {
		s.punchPhotoStore = store
		s.logStore = store
	}
}

func WithLocalUploadDir(dir string) ServerOption {
	return WithLocalUploadDirs(dir)
}

func WithPunchPhotoObjectStore(store storage.ObjectStore) ServerOption {
	return func(s *Server) {
		s.punchPhotoStore = store
	}
}

func WithLogObjectStore(store storage.ObjectStore) ServerOption {
	return func(s *Server) {
		s.logStore = store
	}
}

func WithLocalUploadDirs(dirs ...string) ServerOption {
	return func(s *Server) {
		for _, dir := range dirs {
			trimmed := strings.TrimSpace(dir)
			if trimmed == "" || slicesContainsString(s.localUploadDirs, trimmed) {
				continue
			}
			s.localUploadDirs = append(s.localUploadDirs, trimmed)
		}
	}
}

// Run starts the server and shuts it down gracefully on process signal.
func (s *Server) Run() error {
	errCh := make(chan error, 1)
	cleanupStop := s.startUploadCleanupLoop()
	defer cleanupStop()

	go func() {
		errCh <- listenAndServe(s.httpServer)
	}()

	sigCtx, stop := notifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sigCtx.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := shutdownServer(s.httpServer, ctx)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) startUploadCleanupLoop() func() {
	if s.punchPhotoStore == nil && s.logStore == nil {
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	ticker := time.NewTicker(uploadCleanupPeriod)

	go func() {
		defer ticker.Stop()
		s.runUploadCleanup(context.Background())

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runUploadCleanup(ctx)
			}
		}
	}()

	return cancel
}

func (s *Server) runUploadCleanup(ctx context.Context) {
	now := s.now().UTC().Truncate(time.Second)
	if s.punchPhotoStore != nil {
		cleanupExpiredPunchPhotoUploads(ctx, s.db, s.punchPhotoStore, now)
	}
	if s.logStore != nil {
		cleanupExpiredLogUploads(ctx, s.db, s.logStore, now)
	}
}

func newUploadFileHandler(roots []string) http.Handler {
	normalized := make([]string, 0, len(roots))
	for _, root := range roots {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" || slicesContainsString(normalized, trimmed) {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 1 {
		return http.FileServer(http.Dir(normalized[0]))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if key == "" || key == "." || strings.HasPrefix(key, "../") {
			http.NotFound(w, r)
			return
		}
		for _, root := range normalized {
			targetPath := filepath.Join(root, filepath.FromSlash(key))
			info, err := os.Stat(targetPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				http.Error(w, "file lookup failed", http.StatusInternalServerError)
				return
			}
			if info.IsDir() {
				continue
			}
			http.ServeFile(w, r, targetPath)
			return
		}
		http.NotFound(w, r)
	})
}

func slicesContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Server) handle(path string, handler appHandler) {
	s.mux.Handle(path, s.wrapHandler(handler))
}

func (s *Server) wrapHandler(handler appHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := handler(w, r); err != nil {
			s.writeError(w, r, err)
		}
	})
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) error {
	response := healthResponse{
		App: componentHealth{
			Status: "ok",
		},
		Database: componentHealth{
			Status: "ok",
		},
	}
	statusCode := http.StatusOK
	if err := s.db.Health(r.Context()); err != nil {
		response.App.Status = "degraded"
		response.Database.Status = "down"
		response.Database.Message = "database unavailable"
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	return nil
}

func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(requestIDHeader))
		if requestID == "" {
			requestID = generateRequestID()
		}

		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		r = r.WithContext(ctx)
		w.Header().Set(requestIDHeader, requestID)

		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(recorder, r)

		log.Printf(
			"request method=%s path=%s status=%d request_id=%s duration_ms=%d",
			r.Method,
			r.URL.Path,
			recorder.statusCode,
			requestIDFromContext(r.Context()),
			time.Since(start).Milliseconds(),
		)
	})
}

func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic recovered request_id=%s panic=%v", requestIDFromContext(r.Context()), recovered)
				s.writeError(w, r, apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := normalizeError(err)
	response := apperrors.ErrorResponse{
		ErrorCode: apiErr.Code,
		Message:   apiErr.Message,
		RequestID: requestIDFromContext(r.Context()),
	}

	if response.RequestID == "" {
		response.RequestID = generateRequestID()
	}

	w.Header().Set(requestIDHeader, response.RequestID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(apiErr.StatusCode())
	_ = json.NewEncoder(w).Encode(response)
}

func normalizeError(err error) apperrors.APIError {
	defaultErr := apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)

	if err == nil {
		return defaultErr
	}

	var apiErr apperrors.APIError
	if errors.As(err, &apiErr) {
		if strings.TrimSpace(apiErr.Code) == "" {
			apiErr.Code = defaultErr.Code
		}
		if strings.TrimSpace(apiErr.Message) == "" {
			apiErr.Message = defaultErr.Message
		}
		if apiErr.HTTPStatus <= 0 {
			apiErr.HTTPStatus = defaultErr.StatusCode()
		}
		return apiErr
	}

	return defaultErr
}

func requestIDFromContext(ctx context.Context) string {
	value, ok := ctx.Value(requestIDContextKey{}).(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func generateRequestID() string {
	var payload [16]byte
	if _, err := rand.Read(payload[:]); err == nil {
		return hex.EncodeToString(payload[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.written {
		return
	}
	w.statusCode = code
	w.written = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(payload []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(payload)
}
