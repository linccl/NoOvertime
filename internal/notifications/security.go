package notifications

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode"
)

const (
	MaxNotificationURLLength   = 2048
	MaxNotificationTokenLength = 4096
	MaxStoredErrorLength       = 500
)

var (
	ErrNotificationURLRequired   = errors.New("notification url is required")
	ErrNotificationURLTooLong    = errors.New("notification url is too long")
	ErrNotificationURLInvalid    = errors.New("notification url is invalid")
	ErrNotificationURLScheme     = errors.New("notification url must use https")
	ErrNotificationTokenRequired = errors.New("notification token is required")
	ErrNotificationTokenTooLong  = errors.New("notification token is too long")
	ErrNotificationTokenInvalid  = errors.New("notification token is invalid")
)

func ValidateNotificationURL(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ErrNotificationURLRequired
	}
	if len(value) > MaxNotificationURLLength {
		return ErrNotificationURLTooLong
	}
	if containsControl(value) {
		return ErrNotificationURLInvalid
	}

	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed == nil || parsed.Host == "" {
		return ErrNotificationURLInvalid
	}
	if parsed.Scheme != "https" {
		return ErrNotificationURLScheme
	}
	if parsed.User != nil {
		return ErrNotificationURLInvalid
	}
	if !isValidHostname(parsed.Hostname()) {
		return ErrNotificationURLInvalid
	}
	return nil
}

func ValidateNotificationToken(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ErrNotificationTokenRequired
	}
	if len(value) > MaxNotificationTokenLength {
		return ErrNotificationTokenTooLong
	}
	if containsControl(value) {
		return ErrNotificationTokenInvalid
	}
	return nil
}

func HashNotificationURL(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

func MaskNotificationURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return truncate("<redacted-url>", MaxStoredErrorLength)
	}

	host := parsed.Hostname()
	if len(host) > 32 {
		host = host[:16] + "..." + host[len(host)-8:]
	}
	return truncate(parsed.Scheme+"://"+host+"/...", MaxStoredErrorLength)
}

func MaskToken(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "<redacted-token>"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func MaskHash(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if len(value) <= 12 {
		return "<redacted-hash>"
	}
	return value[:6] + "..." + value[len(value)-6:]
}

func RedactAuthorizationHeader(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "Authorization: Bearer <redacted-token>"
}

func RedactErrorMessage(message string, secrets ...string) string {
	redacted := strings.TrimSpace(message)
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		redacted = strings.ReplaceAll(redacted, secret, replacementForSecret(secret))
	}

	redacted = redactBearerTokens(redacted)
	redacted = redactURLs(redacted)
	return truncate(redacted, MaxStoredErrorLength)
}

func replacementForSecret(secret string) string {
	if strings.HasPrefix(secret, "http://") || strings.HasPrefix(secret, "https://") {
		return MaskNotificationURL(secret)
	}
	if len(secret) == 64 && isHex(secret) {
		return MaskHash(secret)
	}
	return MaskToken(secret)
}

func redactBearerTokens(value string) string {
	parts := strings.Fields(value)
	for i := 0; i < len(parts)-1; i++ {
		if strings.EqualFold(parts[i], "Bearer") {
			parts[i+1] = "<redacted-token>"
		}
	}
	return strings.Join(parts, " ")
}

func redactURLs(value string) string {
	parts := strings.Fields(value)
	for i, part := range parts {
		cleaned := strings.Trim(part, ".,;)]}'\"")
		if strings.HasPrefix(cleaned, "http://") || strings.HasPrefix(cleaned, "https://") {
			parts[i] = strings.Replace(part, cleaned, MaskNotificationURL(cleaned), 1)
		}
	}
	return strings.Join(parts, " ")
}

func isValidHostname(hostname string) bool {
	if hostname == "" || len(hostname) > 253 {
		return false
	}
	if ip := net.ParseIP(hostname); ip != nil {
		return true
	}
	if strings.HasSuffix(hostname, ".") {
		hostname = strings.TrimSuffix(hostname, ".")
	}
	labels := strings.Split(hostname, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func containsControl(value string) bool {
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return true
		}
	}
	return false
}

func isHex(value string) bool {
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') || (ch >= '0' && ch <= '9') {
			continue
		}
		return false
	}
	return true
}

func truncate(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	if maxLen <= 3 {
		return value[:maxLen]
	}
	return value[:maxLen-3] + "..."
}
