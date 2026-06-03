package notifications

import (
	"strings"
	"testing"
)

func TestValidateNotificationURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want error
	}{
		{name: "valid", raw: "https://example.com/notify"},
		{name: "valid ip", raw: "https://127.0.0.1/notify"},
		{name: "required", raw: "", want: ErrNotificationURLRequired},
		{name: "http rejected", raw: "http://example.com/notify", want: ErrNotificationURLScheme},
		{name: "host required", raw: "https:///notify", want: ErrNotificationURLInvalid},
		{name: "hostname invalid", raw: "https://bad host/notify", want: ErrNotificationURLInvalid},
		{name: "user info rejected", raw: "https://user:pass@example.com/notify", want: ErrNotificationURLInvalid},
		{name: "control rejected", raw: "https://example.com/notify\nx", want: ErrNotificationURLInvalid},
		{name: "too long", raw: "https://example.com/" + strings.Repeat("a", MaxNotificationURLLength), want: ErrNotificationURLTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateNotificationURL(tt.raw)
			if got != tt.want {
				t.Fatalf("ValidateNotificationURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateNotificationToken(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want error
	}{
		{name: "valid", raw: "secret-token"},
		{name: "trimmed valid", raw: " secret-token "},
		{name: "required", raw: "", want: ErrNotificationTokenRequired},
		{name: "control rejected", raw: "secret\nvalue", want: ErrNotificationTokenInvalid},
		{name: "too long", raw: strings.Repeat("a", MaxNotificationTokenLength+1), want: ErrNotificationTokenTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateNotificationToken(tt.raw)
			if got != tt.want {
				t.Fatalf("ValidateNotificationToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaskingHelpers(t *testing.T) {
	if got := MaskNotificationURL("https://example.com/notify?token=secret"); got != "https://example.com/..." {
		t.Fatalf("MaskNotificationURL() = %q", got)
	}
	if got := MaskToken("secret-token-value"); got != "secr...alue" {
		t.Fatalf("MaskToken() = %q", got)
	}
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got := MaskHash(hash); got != "012345...abcdef" {
		t.Fatalf("MaskHash() = %q", got)
	}
	if got := RedactAuthorizationHeader("Bearer secret-token-value"); got != "Authorization: Bearer <redacted-token>" {
		t.Fatalf("RedactAuthorizationHeader() = %q", got)
	}
}

func TestHashNotificationURL(t *testing.T) {
	left := HashNotificationURL(" https://example.com/notify ")
	right := HashNotificationURL("https://example.com/notify")
	if left != right {
		t.Fatalf("trimmed hashes differ: %q != %q", left, right)
	}
	if left == "" || len(left) != 64 {
		t.Fatalf("hash = %q", left)
	}
}

func TestRedactErrorMessage(t *testing.T) {
	url := "https://example.com/notify?token=secret-token-value"
	token := "secret-token-value"
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	message := "POST " + url + " failed with Authorization: Bearer " + token + " hash " + hash

	got := RedactErrorMessage(message, url, token, hash)
	for _, leaked := range []string{url, token, hash, "Authorization: Bearer secret-token-value"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted message leaked %q: %q", leaked, got)
		}
	}
	if !strings.Contains(got, "https://example.com/...") {
		t.Fatalf("redacted message missing masked url: %q", got)
	}
	if !strings.Contains(got, "<redacted-token>") && !strings.Contains(got, "secr...alue") {
		t.Fatalf("redacted message missing masked token: %q", got)
	}
	if len(got) > MaxStoredErrorLength {
		t.Fatalf("redacted message length = %d", len(got))
	}
}

func TestRedactErrorMessageTruncates(t *testing.T) {
	got := RedactErrorMessage(strings.Repeat("a", MaxStoredErrorLength+10))
	if len(got) > MaxStoredErrorLength {
		t.Fatalf("len(got) = %d", len(got))
	}
}
