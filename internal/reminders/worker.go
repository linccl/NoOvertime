package reminders

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"noovertime/internal/notifications"

	"github.com/jackc/pgx/v5"
)

const (
	StatusPending   = "PENDING"
	StatusSending   = "SENDING"
	StatusFailed    = "FAILED"
	StatusSent      = "SENT"
	StatusCancelled = "CANCELLED"
	StatusSkipped   = "SKIPPED"

	cancelReasonConfigDisabled = "CONFIG_DISABLED"
	cancelReasonEndSynced      = "END_SYNCED"
	cancelReasonURLInvalid     = "URL_INVALID"

	errorCodeHTTP    = "HTTP_STATUS"
	errorCodeNetwork = "NETWORK_ERROR"
)

type TxRunner interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

type Store interface {
	ClaimDue(ctx context.Context, now time.Time, limit, maxRetry int, lockFor time.Duration) ([]Event, error)
	LoadSendTarget(ctx context.Context, event Event) (SendTarget, error)
	MarkSent(ctx context.Context, event Event, now time.Time) error
	MarkFailed(ctx context.Context, event Event, attemptCount, maxRetry int, nextRetryAt time.Time, code, message string, now time.Time) error
	MarkCancelled(ctx context.Context, event Event, reason string, now time.Time) error
	MarkSkipped(ctx context.Context, event Event, reason string, now time.Time) error
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	ScanInterval time.Duration
	BatchSize    int
	HTTPTimeout  time.Duration
	MaxRetry     int
	RetryBackoff time.Duration
	LockDuration time.Duration
}

type Event struct {
	ID                         string
	UserID                     string
	LocalDate                  string
	ReminderType               string
	AdjustMinutes              int
	ScheduledAfterStartMinutes int
	AttemptCount               int
	ClaimLockedUntil           time.Time
	Message                    string
}

type SendTarget struct {
	Configured bool
	Enabled    bool
	HasEnd     bool
	URL        string
	Token      string
}

type Worker struct {
	store  Store
	client HTTPClient
	cfg    Config
	now    func() time.Time
}

func NewWorker(store Store, client HTTPClient, cfg Config) *Worker {
	if client == nil {
		client = NewHTTPClient(cfg.HTTPTimeout)
	}
	if cfg.LockDuration <= 0 {
		cfg.LockDuration = cfg.HTTPTimeout + cfg.RetryBackoff
	}
	return &Worker{
		store:  store,
		client: client,
		cfg:    cfg,
		now:    time.Now,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.ScanOnce(ctx); err != nil {
		log.Printf("reminder_worker scan error: %v", err)
	}

	ticker := time.NewTicker(w.cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.ScanOnce(ctx); err != nil {
				log.Printf("reminder_worker scan error: %v", err)
			}
		}
	}
}

func (w *Worker) ScanOnce(ctx context.Context) error {
	now := w.now().UTC()
	events, err := w.store.ClaimDue(ctx, now, w.cfg.BatchSize, w.cfg.MaxRetry, w.cfg.LockDuration)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := w.processEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) processEvent(ctx context.Context, event Event) error {
	now := w.now().UTC()
	target, err := w.store.LoadSendTarget(ctx, event)
	if err != nil {
		return err
	}
	switch {
	case target.HasEnd:
		return ignoreExpiredClaim(w.store.MarkSkipped(ctx, event, cancelReasonEndSynced, now))
	case !target.Configured || !target.Enabled:
		return ignoreExpiredClaim(w.store.MarkCancelled(ctx, event, cancelReasonConfigDisabled, now))
	}
	if err := notifications.ValidateNotificationURL(target.URL); err != nil {
		return ignoreExpiredClaim(w.store.MarkCancelled(ctx, event, cancelReasonURLInvalid, now))
	}
	if err := notifications.ValidateNotificationToken(target.Token); err != nil {
		return ignoreExpiredClaim(w.store.MarkCancelled(ctx, event, cancelReasonConfigDisabled, now))
	}

	err = SendNotification(ctx, w.client, target.URL, target.Token, event.ID, event.Message)
	if err == nil {
		return ignoreExpiredClaim(w.store.MarkSent(ctx, event, now))
	}

	nextRetryAt := now.Add(w.cfg.RetryBackoff)
	message := notifications.RedactErrorMessage(err.Error(), target.URL, target.Token)
	return ignoreExpiredClaim(w.store.MarkFailed(ctx, event, event.AttemptCount+1, w.cfg.MaxRetry, nextRetryAt, errorCodeForSendError(err), message, now))
}

func SendNotification(ctx context.Context, client HTTPClient, rawURL, token, idempotencyKey, message string) error {
	if err := notifications.ValidateNotificationURL(rawURL); err != nil {
		return err
	}
	if err := notifications.ValidateNotificationToken(token); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(rawURL), bytes.NewBufferString(message))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("X-Idempotency-Key", idempotencyKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errNetwork, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	return fmt.Errorf("%w: %d", errHTTPStatus, resp.StatusCode)
}

type HTTPNotificationClient struct {
	client *http.Client
}

func NewHTTPClient(timeout time.Duration) *HTTPNotificationClient {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPNotificationClient{
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *HTTPNotificationClient) Do(req *http.Request) (*http.Response, error) {
	return c.client.Do(req)
}

var (
	errHTTPStatus   = errors.New("http status")
	errNetwork      = errors.New("network error")
	ErrClaimExpired = errors.New("reminder claim expired")
)

func ignoreExpiredClaim(err error) error {
	if errors.Is(err, ErrClaimExpired) {
		return nil
	}
	return err
}

func errorCodeForSendError(err error) string {
	if errors.Is(err, errHTTPStatus) {
		return errorCodeHTTP
	}
	return errorCodeNetwork
}
