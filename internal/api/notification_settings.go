package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	apperrors "noovertime/internal/errors"
	"noovertime/internal/notifications"

	"github.com/jackc/pgx/v5"
)

const (
	notificationSettingsPath         = "/api/v1/notification-settings"
	notificationSettingsCancelReason = "CONFIG_DISABLED"
)

type notificationSettingsRequest struct {
	ServerEndReminderEnabled *bool  `json:"server_end_reminder_enabled"`
	NotificationURL          string `json:"notification_url"`
	NotificationToken        string `json:"notification_token"`
}

type notificationSettingsInput struct {
	ServerEndReminderEnabled bool
	NotificationURL          string
	NotificationToken        string
}

type notificationSettingsResponse struct {
	ServerEndReminderEnabled bool   `json:"server_end_reminder_enabled"`
	NotificationURLMasked    string `json:"notification_url_masked"`
	NotificationConfigured   bool   `json:"notification_configured"`
	ConfigVersion            int64  `json:"config_version"`
	UpdatedAt                string `json:"updated_at"`
}

type notificationSettingsSnapshot struct {
	ServerEndReminderEnabled bool
	NotificationURL          string
	ConfigVersion            int64
	UpdatedAt                time.Time
	Configured               bool
}

type notificationSettingsTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

func (s *Server) notificationSettingsHandler(w http.ResponseWriter, r *http.Request) error {
	header, err := parseMobileTokenHeaders(r)
	if err != nil {
		return err
	}

	var response notificationSettingsResponse
	switch r.Method {
	case http.MethodGet:
		response, err = loadNotificationSettings(r.Context(), s.db, header)
	case http.MethodPut:
		var input notificationSettingsInput
		input, err = parseNotificationSettingsBody(r)
		if err == nil {
			response, err = saveNotificationSettings(r.Context(), s.db, header, input)
		}
	case http.MethodDelete:
		response, err = deleteNotificationSettings(r.Context(), s.db, header)
	default:
		return apperrors.New(http.StatusMethodNotAllowed, methodNotAllowedCode, "method not allowed")
	}
	if err != nil {
		if mappedErr, ok := mapNotificationSettingsPersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func parseNotificationSettingsBody(r *http.Request) (notificationSettingsInput, error) {
	var body notificationSettingsRequest
	if err := decodeStrictMigrationJSON(r.Body, &body); err != nil {
		return notificationSettingsInput{}, err
	}
	if body.ServerEndReminderEnabled == nil {
		return notificationSettingsInput{}, invalidArgument("server_end_reminder_enabled is required")
	}

	notificationURL := strings.TrimSpace(body.NotificationURL)
	if err := notifications.ValidateNotificationURL(notificationURL); err != nil {
		return notificationSettingsInput{}, invalidArgument("notification_url is invalid")
	}

	notificationToken := strings.TrimSpace(body.NotificationToken)
	if err := notifications.ValidateNotificationToken(notificationToken); err != nil {
		return notificationSettingsInput{}, invalidArgument("notification_token is invalid")
	}

	return notificationSettingsInput{
		ServerEndReminderEnabled: *body.ServerEndReminderEnabled,
		NotificationURL:          notificationURL,
		NotificationToken:        notificationToken,
	}, nil
}

func loadNotificationSettings(
	ctx context.Context,
	db HealthChecker,
	header mobileTokenHeader,
) (notificationSettingsResponse, error) {
	txDB, ok := db.(notificationSettingsTxDB)
	if !ok {
		return notificationSettingsResponse{}, errors.New("database transaction is not available")
	}

	var snapshot notificationSettingsSnapshot
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		auth, err := loadMobileAuthContext(ctx, tx, header, false)
		if err != nil {
			return err
		}
		if err := requireBoundMobileUser(auth); err != nil {
			return err
		}
		loaded, err := loadNotificationSettingsSnapshot(ctx, tx, auth.UserID)
		if err != nil {
			return err
		}
		snapshot = loaded
		return nil
	})
	if err != nil {
		return notificationSettingsResponse{}, err
	}
	return toNotificationSettingsResponse(snapshot), nil
}

func saveNotificationSettings(
	ctx context.Context,
	db HealthChecker,
	header mobileTokenHeader,
	input notificationSettingsInput,
) (notificationSettingsResponse, error) {
	txDB, ok := db.(notificationSettingsTxDB)
	if !ok {
		return notificationSettingsResponse{}, errors.New("database transaction is not available")
	}

	var snapshot notificationSettingsSnapshot
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		auth, err := loadMobileAuthContext(ctx, tx, header, false)
		if err != nil {
			return err
		}
		if err := requireBoundMobileUser(auth); err != nil {
			return err
		}
		saved, err := upsertNotificationSettings(ctx, tx, auth.UserID, input)
		if err != nil {
			return err
		}
		if !input.ServerEndReminderEnabled {
			if err := cancelUnsentReminderEvents(ctx, tx, auth.UserID); err != nil {
				return err
			}
		}
		snapshot = saved
		return nil
	})
	if err != nil {
		return notificationSettingsResponse{}, err
	}
	return toNotificationSettingsResponse(snapshot), nil
}

func deleteNotificationSettings(
	ctx context.Context,
	db HealthChecker,
	header mobileTokenHeader,
) (notificationSettingsResponse, error) {
	txDB, ok := db.(notificationSettingsTxDB)
	if !ok {
		return notificationSettingsResponse{}, errors.New("database transaction is not available")
	}

	var snapshot notificationSettingsSnapshot
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		auth, err := loadMobileAuthContext(ctx, tx, header, false)
		if err != nil {
			return err
		}
		if err := requireBoundMobileUser(auth); err != nil {
			return err
		}
		deleted, err := disableNotificationSettings(ctx, tx, auth.UserID)
		if err != nil {
			return err
		}
		if err := cancelUnsentReminderEvents(ctx, tx, auth.UserID); err != nil {
			return err
		}
		snapshot = deleted
		return nil
	})
	if err != nil {
		return notificationSettingsResponse{}, err
	}
	return toNotificationSettingsResponse(snapshot), nil
}

func loadNotificationSettingsSnapshot(ctx context.Context, tx pgx.Tx, userID string) (notificationSettingsSnapshot, error) {
	const query = `
SELECT server_end_reminder_enabled,
       notification_url,
       config_version,
       updated_at
  FROM user_notification_settings
 WHERE user_id = $1::uuid
`

	var snapshot notificationSettingsSnapshot
	if err := tx.QueryRow(ctx, query, userID).Scan(
		&snapshot.ServerEndReminderEnabled,
		&snapshot.NotificationURL,
		&snapshot.ConfigVersion,
		&snapshot.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notificationSettingsSnapshot{}, nil
		}
		return notificationSettingsSnapshot{}, err
	}
	snapshot.Configured = true
	return snapshot, nil
}

func upsertNotificationSettings(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	input notificationSettingsInput,
) (notificationSettingsSnapshot, error) {
	const query = `
INSERT INTO user_notification_settings (
  user_id,
  server_end_reminder_enabled,
  notification_url,
  notification_token,
  notification_url_hash,
  config_version,
  created_at,
  updated_at
)
VALUES ($1::uuid, $2, $3, $4, $5, 1, now(), now())
ON CONFLICT (user_id) DO UPDATE
   SET server_end_reminder_enabled = EXCLUDED.server_end_reminder_enabled,
       notification_url = EXCLUDED.notification_url,
       notification_token = EXCLUDED.notification_token,
       notification_url_hash = EXCLUDED.notification_url_hash,
       config_version = user_notification_settings.config_version + 1,
       updated_at = now()
RETURNING server_end_reminder_enabled,
          notification_url,
          config_version,
          updated_at
`

	var snapshot notificationSettingsSnapshot
	if err := tx.QueryRow(
		ctx,
		query,
		userID,
		input.ServerEndReminderEnabled,
		input.NotificationURL,
		input.NotificationToken,
		notifications.HashNotificationURL(input.NotificationURL),
	).Scan(
		&snapshot.ServerEndReminderEnabled,
		&snapshot.NotificationURL,
		&snapshot.ConfigVersion,
		&snapshot.UpdatedAt,
	); err != nil {
		return notificationSettingsSnapshot{}, err
	}
	snapshot.Configured = true
	return snapshot, nil
}

func disableNotificationSettings(ctx context.Context, tx pgx.Tx, userID string) (notificationSettingsSnapshot, error) {
	const query = `
UPDATE user_notification_settings
   SET server_end_reminder_enabled = FALSE,
       config_version = config_version + 1,
       updated_at = now()
 WHERE user_id = $1::uuid
RETURNING server_end_reminder_enabled,
          notification_url,
          config_version,
          updated_at
`

	var snapshot notificationSettingsSnapshot
	err := tx.QueryRow(ctx, query, userID).Scan(
		&snapshot.ServerEndReminderEnabled,
		&snapshot.NotificationURL,
		&snapshot.ConfigVersion,
		&snapshot.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notificationSettingsSnapshot{}, nil
		}
		return notificationSettingsSnapshot{}, err
	}
	snapshot.Configured = true
	return snapshot, nil
}

func cancelUnsentReminderEvents(ctx context.Context, tx pgx.Tx, userID string) error {
	const query = `
UPDATE punch_reminder_events
   SET status = 'CANCELLED',
       cancelled_at = now(),
       cancel_reason = $2,
       locked_until = NULL,
       updated_at = now()
 WHERE user_id = $1::uuid
   AND status IN ('PENDING', 'SENDING', 'FAILED')
`
	_, err := tx.Exec(ctx, query, userID, notificationSettingsCancelReason)
	return err
}

func toNotificationSettingsResponse(snapshot notificationSettingsSnapshot) notificationSettingsResponse {
	response := notificationSettingsResponse{
		ServerEndReminderEnabled: false,
		NotificationConfigured:   false,
		ConfigVersion:            snapshot.ConfigVersion,
	}
	if !snapshot.Configured {
		return response
	}

	response.ServerEndReminderEnabled = snapshot.ServerEndReminderEnabled
	response.NotificationURLMasked = notifications.MaskNotificationURL(snapshot.NotificationURL)
	response.NotificationConfigured = strings.TrimSpace(snapshot.NotificationURL) != ""
	if !snapshot.UpdatedAt.IsZero() {
		response.UpdatedAt = snapshot.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return response
}

func mapNotificationSettingsPersistenceError(err error) (error, bool) {
	var apiErr apperrors.APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}
