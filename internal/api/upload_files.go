package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"time"

	"noovertime/internal/storage"

	"github.com/jackc/pgx/v5"
)

const (
	punchPhotoUploadPath      = "/api/v1/punch-photos/upload"
	logUploadPath             = "/api/v1/logs/upload"
	maxPunchPhotoUploadBytes  = 16 << 20
	maxLogUploadBytes         = 2 << 20
	uploadRetentionDays       = 60
	expiredUploadCleanupLimit = 20
)

type uploadFileResponse struct {
	URL       string `json:"url"`
	ObjectKey string `json:"object_key"`
	ExpiresAt string `json:"expires_at"`
	RequestID string `json:"request_id"`
}

type uploadAuthResolverDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

type punchPhotoUploadInput struct {
	PunchRecordID string
	LocalDate     time.Time
	PunchType     string
	File          multipart.File
	FileHeader    *multipart.FileHeader
	ContentType   string
}

type logUploadInput struct {
	LogDate     time.Time
	LogKind     string
	File        multipart.File
	FileHeader  *multipart.FileHeader
	ContentType string
}

type uploadedObjectRecord struct {
	UserID      string
	DeviceID    string
	ObjectKey   string
	RemoteURL   string
	ContentType string
	FileSize    int64
	UploadedAt  time.Time
	ExpiresAt   time.Time
	PunchRecord string
	LocalDate   time.Time
	PunchType   string
	LogDate     time.Time
	LogKind     string
}

func (s *Server) punchPhotoUploadHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}
	if s.punchPhotoStore == nil {
		return fmt.Errorf("upload storage is not configured")
	}

	header, err := parseMobileTokenHeaders(r)
	if err != nil {
		return err
	}
	auth, err := resolveUploadAuthContext(r.Context(), s.db, header)
	if err != nil {
		return err
	}
	if !isMembershipActive(auth.MembershipTier, auth.MembershipExpiresAt, s.now()) {
		return membershipRequired()
	}

	input, err := parsePunchPhotoUploadInput(w, r)
	if err != nil {
		return err
	}
	defer cleanupMultipartForm(r)
	defer input.File.Close()

	objectKey := buildPunchPhotoObjectKey(auth.UserID, input.LocalDate, input.PunchRecordID, input.ContentType, input.FileHeader.Filename)
	record, err := uploadIncomingFile(r.Context(), s.punchPhotoStore, storage.PutRequest{
		Key:         objectKey,
		Body:        input.File,
		ContentType: input.ContentType,
	})
	if err != nil {
		return err
	}

	now := s.now().UTC().Truncate(time.Second)
	expiresAt := uploadExpiryTime(now)
	replacedObjectKey, err := upsertPunchPhotoUploadRecord(r.Context(), s.db, uploadedObjectRecord{
		UserID:      auth.UserID,
		DeviceID:    auth.DeviceID,
		ObjectKey:   record.Key,
		RemoteURL:   record.URL,
		ContentType: input.ContentType,
		FileSize:    record.SizeBytes,
		UploadedAt:  now,
		ExpiresAt:   expiresAt,
		PunchRecord: input.PunchRecordID,
		LocalDate:   input.LocalDate,
		PunchType:   input.PunchType,
	})
	if err != nil {
		return err
	}
	cleanupReplacedUploadObject(r.Context(), s.punchPhotoStore, replacedObjectKey, record.Key)

	cleanupExpiredPunchPhotoUploads(r.Context(), s.db, s.punchPhotoStore, now)
	return writeUploadFileResponse(w, r, record.Key, record.URL, expiresAt)
}

func (s *Server) logUploadHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}
	if s.logStore == nil {
		return fmt.Errorf("upload storage is not configured")
	}

	header, err := parseMobileTokenHeaders(r)
	if err != nil {
		return err
	}
	auth, err := resolveUploadAuthContext(r.Context(), s.db, header)
	if err != nil {
		return err
	}

	input, err := parseLogUploadInput(w, r)
	if err != nil {
		return err
	}
	defer cleanupMultipartForm(r)
	defer input.File.Close()

	objectKey := buildLogObjectKey(auth.UserID, auth.DeviceID, input.LogKind, input.LogDate, input.ContentType, input.FileHeader.Filename)
	record, err := uploadIncomingFile(r.Context(), s.logStore, storage.PutRequest{
		Key:         objectKey,
		Body:        input.File,
		ContentType: input.ContentType,
	})
	if err != nil {
		return err
	}

	now := s.now().UTC().Truncate(time.Second)
	expiresAt := uploadExpiryTime(now)
	replacedObjectKey, err := upsertLogUploadRecord(r.Context(), s.db, uploadedObjectRecord{
		UserID:      auth.UserID,
		DeviceID:    auth.DeviceID,
		ObjectKey:   record.Key,
		RemoteURL:   record.URL,
		ContentType: input.ContentType,
		FileSize:    record.SizeBytes,
		UploadedAt:  now,
		ExpiresAt:   expiresAt,
		LogDate:     input.LogDate,
		LogKind:     input.LogKind,
	})
	if err != nil {
		return err
	}
	cleanupReplacedUploadObject(r.Context(), s.logStore, replacedObjectKey, record.Key)

	cleanupExpiredLogUploads(r.Context(), s.db, s.logStore, now)
	return writeUploadFileResponse(w, r, record.Key, record.URL, expiresAt)
}

func resolveUploadAuthContext(ctx context.Context, db HealthChecker, header mobileTokenHeader) (mobileAuthContext, error) {
	if resolver, ok := db.(mobileAuthDirectResolver); ok {
		auth, err := resolver.resolveMobileAuthContextDirect(header)
		if err != nil {
			return mobileAuthContext{}, err
		}
		auth.MembershipTier = normalizeMembershipTier(auth.MembershipTier)
		return auth, nil
	}

	txDB, ok := db.(uploadAuthResolverDB)
	if !ok {
		return mobileAuthContext{}, fmt.Errorf("database transaction is not available")
	}

	var auth mobileAuthContext
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		loaded, err := loadMobileAuthContext(ctx, tx, header, true)
		if err != nil {
			return err
		}
		loaded, err = bindAnonymousMobileToken(ctx, tx, loaded)
		if err != nil {
			return err
		}
		membership, err := loadUserMembership(ctx, tx, loaded.UserID)
		if err != nil {
			return err
		}
		loaded.MembershipTier = membership.Tier
		loaded.MembershipExpiresAt = membership.ExpiresAt
		auth = loaded
		return nil
	})
	if err != nil {
		return mobileAuthContext{}, err
	}
	return auth, nil
}

func parsePunchPhotoUploadInput(w http.ResponseWriter, r *http.Request) (punchPhotoUploadInput, error) {
	if err := parseMultipartForm(w, r, maxPunchPhotoUploadBytes); err != nil {
		return punchPhotoUploadInput{}, err
	}

	punchRecordID, err := requireUUID("punch_record_id", r.FormValue("punch_record_id"))
	if err != nil {
		return punchPhotoUploadInput{}, err
	}
	localDate, err := parseUploadDateField("local_date", r.FormValue("local_date"))
	if err != nil {
		return punchPhotoUploadInput{}, err
	}
	punchType, err := parsePunchTypeField(r.FormValue("punch_type"))
	if err != nil {
		return punchPhotoUploadInput{}, err
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		return punchPhotoUploadInput{}, invalidArgument("file is required")
	}

	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		file.Close()
		return punchPhotoUploadInput{}, invalidArgument("file must be an image")
	}

	return punchPhotoUploadInput{
		PunchRecordID: punchRecordID,
		LocalDate:     localDate,
		PunchType:     punchType,
		File:          file,
		FileHeader:    header,
		ContentType:   contentType,
	}, nil
}

func parseLogUploadInput(w http.ResponseWriter, r *http.Request) (logUploadInput, error) {
	if err := parseMultipartForm(w, r, maxLogUploadBytes); err != nil {
		return logUploadInput{}, err
	}

	logDate, err := parseUploadDateField("log_date", r.FormValue("log_date"))
	if err != nil {
		return logUploadInput{}, err
	}
	logKind, err := parseLogKindField(r.FormValue("log_kind"))
	if err != nil {
		return logUploadInput{}, err
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		return logUploadInput{}, invalidArgument("file is required")
	}

	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "text/") {
		file.Close()
		return logUploadInput{}, invalidArgument("file must be a text log")
	}

	return logUploadInput{
		LogDate:     logDate,
		LogKind:     logKind,
		File:        file,
		FileHeader:  header,
		ContentType: contentType,
	}, nil
}

func parseMultipartForm(w http.ResponseWriter, r *http.Request, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			return invalidArgument("uploaded file is too large")
		}
		return invalidArgument("multipart form is invalid")
	}
	return nil
}

func cleanupMultipartForm(r *http.Request) {
	if r.MultipartForm != nil {
		r.MultipartForm.RemoveAll()
	}
}

func parseUploadDateField(field, raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, invalidArgument(fmt.Sprintf("%s is required", field))
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, invalidArgument(fmt.Sprintf("%s must be in YYYY-MM-DD format", field))
	}
	return parsed.UTC(), nil
}

func parsePunchTypeField(raw string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "START", "END":
		return strings.ToUpper(strings.TrimSpace(raw)), nil
	default:
		return "", invalidArgument("punch_type must be START or END")
	}
}

func parseLogKindField(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return "all", nil
	case "error":
		return "error", nil
	case "info":
		return "info", nil
	default:
		return "", invalidArgument("log_kind must be all, error, or info")
	}
}

func buildPunchPhotoObjectKey(userID string, localDate time.Time, punchRecordID, contentType, filename string) string {
	return path.Join(
		"punch-photos",
		userID,
		localDate.Format("2006-01-02"),
		punchRecordID+uploadFileExtension(contentType, filename, ".jpg"),
	)
}

func buildLogObjectKey(userID, deviceID, logKind string, logDate time.Time, contentType, filename string) string {
	return path.Join(
		"logs",
		userID,
		deviceID,
		logKind,
		logDate.Format("2006-01-02")+uploadFileExtension(contentType, filename, ".log"),
	)
}

func uploadFileExtension(contentType, filename, defaultExt string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	case "text/plain", "text/plain; charset=utf-8":
		return ".log"
	}

	if extensions, err := mime.ExtensionsByType(contentType); err == nil && len(extensions) > 0 {
		return extensions[0]
	}
	if dot := strings.LastIndex(strings.TrimSpace(filename), "."); dot >= 0 {
		return filename[dot:]
	}
	return defaultExt
}

type uploadedObjectResult struct {
	Key       string
	URL       string
	SizeBytes int64
}

func uploadIncomingFile(ctx context.Context, store storage.ObjectStore, req storage.PutRequest) (uploadedObjectResult, error) {
	reader := &countingReader{reader: req.Body}
	result, err := store.Put(ctx, storage.PutRequest{
		Key:         req.Key,
		Body:        reader,
		ContentType: req.ContentType,
	})
	if err != nil {
		return uploadedObjectResult{}, err
	}
	if reader.size == 0 {
		_ = store.Delete(ctx, result.Key)
		return uploadedObjectResult{}, invalidArgument("file must not be empty")
	}
	return uploadedObjectResult{
		Key:       result.Key,
		URL:       result.URL,
		SizeBytes: reader.size,
	}, nil
}

func upsertPunchPhotoUploadRecord(ctx context.Context, db HealthChecker, record uploadedObjectRecord) (string, error) {
	txDB, ok := db.(uploadAuthResolverDB)
	if !ok {
		return "", fmt.Errorf("database transaction is not available")
	}
	var previousObjectKey string
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		previousObjectKey, err = loadExistingUploadObjectKey(
			ctx,
			tx,
			`
SELECT object_key
  FROM punch_photo_uploads
 WHERE user_id = $1::uuid
   AND punch_record_id = $2::uuid
   AND deleted_at IS NULL
 LIMIT 1
`,
			record.UserID,
			record.PunchRecord,
		)
		if err != nil {
			return err
		}

		const query = `
INSERT INTO punch_photo_uploads (
	user_id,
	device_id,
	punch_record_id,
	local_date,
	punch_type,
	object_key,
	remote_url,
	content_type,
	file_size_bytes,
	uploaded_at,
	expires_at,
	deleted_at,
	updated_at
) VALUES (
	$1::uuid,
	$2::uuid,
	$3::uuid,
	$4::date,
	$5::punch_type,
	$6,
	$7,
	$8,
	$9,
	$10,
	$11,
	NULL,
	$10
)
ON CONFLICT (user_id, punch_record_id) DO UPDATE
SET device_id = EXCLUDED.device_id,
    local_date = EXCLUDED.local_date,
    punch_type = EXCLUDED.punch_type,
    object_key = EXCLUDED.object_key,
    remote_url = EXCLUDED.remote_url,
    content_type = EXCLUDED.content_type,
    file_size_bytes = EXCLUDED.file_size_bytes,
    uploaded_at = EXCLUDED.uploaded_at,
    expires_at = EXCLUDED.expires_at,
    deleted_at = NULL,
    updated_at = EXCLUDED.updated_at
`
		_, err = tx.Exec(
			ctx,
			query,
			record.UserID,
			record.DeviceID,
			record.PunchRecord,
			record.LocalDate.Format("2006-01-02"),
			record.PunchType,
			record.ObjectKey,
			record.RemoteURL,
			record.ContentType,
			record.FileSize,
			record.UploadedAt,
			record.ExpiresAt,
		)
		return err
	})
	if err != nil {
		return "", err
	}
	if previousObjectKey == record.ObjectKey {
		return "", nil
	}
	return previousObjectKey, nil
}

func upsertLogUploadRecord(ctx context.Context, db HealthChecker, record uploadedObjectRecord) (string, error) {
	txDB, ok := db.(uploadAuthResolverDB)
	if !ok {
		return "", fmt.Errorf("database transaction is not available")
	}
	var previousObjectKey string
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		previousObjectKey, err = loadExistingUploadObjectKey(
			ctx,
			tx,
			`
SELECT object_key
  FROM app_log_uploads
 WHERE user_id = $1::uuid
   AND device_id = $2::uuid
   AND log_date = $3::date
   AND log_kind = $4
   AND deleted_at IS NULL
 LIMIT 1
`,
			record.UserID,
			record.DeviceID,
			record.LogDate.Format("2006-01-02"),
			record.LogKind,
		)
		if err != nil {
			return err
		}

		const query = `
INSERT INTO app_log_uploads (
	user_id,
	device_id,
	log_date,
	log_kind,
	object_key,
	remote_url,
	content_type,
	file_size_bytes,
	uploaded_at,
	expires_at,
	deleted_at,
	updated_at
) VALUES (
	$1::uuid,
	$2::uuid,
	$3::date,
	$4,
	$5,
	$6,
	$7,
	$8,
	$9,
	$10,
	NULL,
	$9
)
ON CONFLICT (user_id, device_id, log_date, log_kind) DO UPDATE
SET object_key = EXCLUDED.object_key,
    remote_url = EXCLUDED.remote_url,
    content_type = EXCLUDED.content_type,
    file_size_bytes = EXCLUDED.file_size_bytes,
    uploaded_at = EXCLUDED.uploaded_at,
    expires_at = EXCLUDED.expires_at,
    deleted_at = NULL,
    updated_at = EXCLUDED.updated_at
`
		_, err = tx.Exec(
			ctx,
			query,
			record.UserID,
			record.DeviceID,
			record.LogDate.Format("2006-01-02"),
			record.LogKind,
			record.ObjectKey,
			record.RemoteURL,
			record.ContentType,
			record.FileSize,
			record.UploadedAt,
			record.ExpiresAt,
		)
		return err
	})
	if err != nil {
		return "", err
	}
	if previousObjectKey == record.ObjectKey {
		return "", nil
	}
	return previousObjectKey, nil
}

func loadExistingUploadObjectKey(ctx context.Context, tx pgx.Tx, query string, args ...any) (string, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	if !rows.Next() {
		return "", rows.Err()
	}

	var objectKey string
	if err := rows.Scan(&objectKey); err != nil {
		return "", err
	}
	return objectKey, rows.Err()
}

func cleanupReplacedUploadObject(ctx context.Context, store storage.ObjectStore, replacedObjectKey, currentObjectKey string) {
	if store == nil || replacedObjectKey == "" || replacedObjectKey == currentObjectKey {
		return
	}
	if err := store.Delete(ctx, replacedObjectKey); err != nil {
		log.Printf("cleanup replaced upload object_key=%s failed: %v", replacedObjectKey, err)
	}
}

func cleanupExpiredPunchPhotoUploads(ctx context.Context, db HealthChecker, store storage.ObjectStore, now time.Time) {
	candidates, err := listExpiredUploadObjectKeys(ctx, db, `
SELECT object_key
  FROM punch_photo_uploads
 WHERE expires_at <= $1
   AND deleted_at IS NULL
 ORDER BY expires_at ASC
 LIMIT $2
`, now)
	if err != nil {
		log.Printf("cleanup expired punch photo uploads failed: %v", err)
		return
	}

	for _, objectKey := range candidates {
		if err := store.Delete(ctx, objectKey); err != nil {
			log.Printf("cleanup expired punch photo object_key=%s failed: %v", objectKey, err)
			continue
		}
		if err := markUploadDeleted(ctx, db, "punch_photo_uploads", objectKey, now); err != nil {
			log.Printf("mark expired punch photo object_key=%s deleted failed: %v", objectKey, err)
		}
	}
}

func cleanupExpiredLogUploads(ctx context.Context, db HealthChecker, store storage.ObjectStore, now time.Time) {
	candidates, err := listExpiredUploadObjectKeys(ctx, db, `
SELECT object_key
  FROM app_log_uploads
 WHERE expires_at <= $1
   AND deleted_at IS NULL
 ORDER BY expires_at ASC
 LIMIT $2
`, now)
	if err != nil {
		log.Printf("cleanup expired log uploads failed: %v", err)
		return
	}

	for _, objectKey := range candidates {
		if err := store.Delete(ctx, objectKey); err != nil {
			log.Printf("cleanup expired log object_key=%s failed: %v", objectKey, err)
			continue
		}
		if err := markUploadDeleted(ctx, db, "app_log_uploads", objectKey, now); err != nil {
			log.Printf("mark expired log object_key=%s deleted failed: %v", objectKey, err)
		}
	}
}

func listExpiredUploadObjectKeys(ctx context.Context, db HealthChecker, query string, now time.Time) ([]string, error) {
	txDB, ok := db.(uploadAuthResolverDB)
	if !ok {
		return nil, fmt.Errorf("database transaction is not available")
	}

	objectKeys := make([]string, 0, expiredUploadCleanupLimit)
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, now, expiredUploadCleanupLimit)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var objectKey string
			if err := rows.Scan(&objectKey); err != nil {
				return err
			}
			objectKeys = append(objectKeys, objectKey)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return objectKeys, nil
}

func markUploadDeleted(ctx context.Context, db HealthChecker, tableName, objectKey string, now time.Time) error {
	txDB, ok := db.(uploadAuthResolverDB)
	if !ok {
		return fmt.Errorf("database transaction is not available")
	}
	return txDB.WithTx(ctx, func(tx pgx.Tx) error {
		query := fmt.Sprintf(`
UPDATE %s
   SET deleted_at = $2,
       updated_at = $2
 WHERE object_key = $1
   AND deleted_at IS NULL
`, tableName)
		_, err := tx.Exec(ctx, query, objectKey, now)
		return err
	})
}

func uploadExpiryTime(uploadedAt time.Time) time.Time {
	return uploadedAt.UTC().AddDate(0, 0, uploadRetentionDays)
}

func writeUploadFileResponse(w http.ResponseWriter, r *http.Request, objectKey, url string, expiresAt time.Time) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(uploadFileResponse{
		URL:       url,
		ObjectKey: objectKey,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		RequestID: requestIDFromContext(r.Context()),
	})
}

type countingReader struct {
	reader io.Reader
	size   int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.size += int64(n)
	return n, err
}
