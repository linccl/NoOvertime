package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	apperrors "noovertime/internal/errors"
)

const migrationRequestBodyMaxBytes = 1 << 20

var (
	allowedMigrationModes   = map[string]struct{}{"NORMAL": {}}
	allowedMigrationActions = map[string]struct{}{"CONFIRM": {}}
	pairingCodeRegex        = regexp.MustCompile(`^[0-9]{8}$`)
	recoveryCodeRegex       = regexp.MustCompile(`^[A-Z0-9]{16}$`)
)

type migrationRequestCreateBody struct {
	UserID       string `json:"user_id"`
	FromDeviceID string `json:"from_device_id"`
	ToDeviceID   string `json:"to_device_id"`
	Mode         string `json:"mode"`
	ExpiresAt    string `json:"expires_at"`
}

type migrationRequestCreateInput struct {
	UserID       string
	FromDeviceID string
	ToDeviceID   string
	Mode         string
	ExpiresAt    time.Time
}

type migrationConfirmBody struct {
	Action           string `json:"action"`
	OperatorDeviceID string `json:"operator_device_id"`
}

type migrationConfirmInput struct {
	MigrationRequestID string
	Action             string
	OperatorDeviceID   string
}

type migrationForcedTakeoverBody struct {
	PairingCode  string `json:"pairing_code"`
	RecoveryCode string `json:"recovery_code"`
	ToDeviceID   string `json:"to_device_id"`
}

type migrationForcedTakeoverInput struct {
	PairingCode  string
	RecoveryCode string
	ToDeviceID   string
}

func parseMigrationRequestCreateInput(reader io.Reader) (migrationRequestCreateInput, error) {
	var body migrationRequestCreateBody
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return migrationRequestCreateInput{}, err
	}

	userID, err := requireUUID("user_id", body.UserID)
	if err != nil {
		return migrationRequestCreateInput{}, err
	}
	fromDeviceID, err := requireUUID("from_device_id", body.FromDeviceID)
	if err != nil {
		return migrationRequestCreateInput{}, err
	}
	toDeviceID, err := requireUUID("to_device_id", body.ToDeviceID)
	if err != nil {
		return migrationRequestCreateInput{}, err
	}
	if fromDeviceID == toDeviceID {
		return migrationRequestCreateInput{}, invalidArgument("to_device_id must be different from from_device_id")
	}

	mode, err := parseRequiredEnum("mode", body.Mode, allowedMigrationModes)
	if err != nil {
		return migrationRequestCreateInput{}, err
	}
	expiresAt, err := parseUTCTime("expires_at", body.ExpiresAt)
	if err != nil {
		return migrationRequestCreateInput{}, err
	}

	return migrationRequestCreateInput{
		UserID:       userID,
		FromDeviceID: fromDeviceID,
		ToDeviceID:   toDeviceID,
		Mode:         mode,
		ExpiresAt:    expiresAt,
	}, nil
}

func parseMigrationConfirmInput(migrationRequestID string, reader io.Reader) (migrationConfirmInput, error) {
	normalizedMigrationID, err := requireUUID("migration_request_id", migrationRequestID)
	if err != nil {
		return migrationConfirmInput{}, err
	}

	var body migrationConfirmBody
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return migrationConfirmInput{}, err
	}

	action, err := parseRequiredEnum("action", body.Action, allowedMigrationActions)
	if err != nil {
		return migrationConfirmInput{}, err
	}
	operatorDeviceID, err := requireUUID("operator_device_id", body.OperatorDeviceID)
	if err != nil {
		return migrationConfirmInput{}, err
	}

	return migrationConfirmInput{
		MigrationRequestID: normalizedMigrationID,
		Action:             action,
		OperatorDeviceID:   operatorDeviceID,
	}, nil
}

func parseMigrationForcedTakeoverInput(reader io.Reader) (migrationForcedTakeoverInput, error) {
	var body migrationForcedTakeoverBody
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return migrationForcedTakeoverInput{}, err
	}

	pairingCode, err := parsePairingCode(body.PairingCode)
	if err != nil {
		return migrationForcedTakeoverInput{}, err
	}
	recoveryCode, err := parseRecoveryCode(body.RecoveryCode)
	if err != nil {
		return migrationForcedTakeoverInput{}, err
	}
	toDeviceID, err := requireUUID("to_device_id", body.ToDeviceID)
	if err != nil {
		return migrationForcedTakeoverInput{}, err
	}

	return migrationForcedTakeoverInput{
		PairingCode:  pairingCode,
		RecoveryCode: recoveryCode,
		ToDeviceID:   toDeviceID,
	}, nil
}

func decodeStrictMigrationJSON(reader io.Reader, payload any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, migrationRequestBodyMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(payload); err != nil {
		return toMigrationDecodeError(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return invalidArgument("request body must contain a single JSON object")
	}
	return nil
}

func toMigrationDecodeError(err error) error {
	message := strings.TrimSpace(err.Error())
	if strings.Contains(message, "unknown field") {
		return apperrors.New(http.StatusBadRequest, unknownFieldCode, message)
	}
	return invalidArgument(message)
}

func parseRequiredEnum(field, rawValue string, allowed map[string]struct{}) (string, error) {
	value := strings.TrimSpace(rawValue)
	if value == "" {
		return "", invalidArgument(fmt.Sprintf("%s is required", field))
	}
	if _, ok := allowed[value]; !ok {
		return "", invalidArgument(fmt.Sprintf("%s is invalid", field))
	}
	return value, nil
}

func parsePairingCode(rawValue string) (string, error) {
	value := strings.TrimSpace(rawValue)
	if value == "" {
		return "", invalidArgument("pairing_code is required")
	}
	if !pairingCodeRegex.MatchString(value) {
		return "", apperrors.New(http.StatusBadRequest, pairingCodeFormatInvalidCode, "pairing_code must be 8 digits")
	}
	return value, nil
}

func parseRecoveryCode(rawValue string) (string, error) {
	value := strings.ToUpper(strings.TrimSpace(rawValue))
	if value == "" {
		return "", invalidArgument("recovery_code is required")
	}
	if !recoveryCodeRegex.MatchString(value) {
		return "", invalidArgument("recovery_code must be 16 alphanumeric characters")
	}
	return value, nil
}
