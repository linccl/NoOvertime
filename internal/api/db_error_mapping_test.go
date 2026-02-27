package api

import (
	"net/http"
	"testing"
)

func TestMapRuleErrorKeyToAPIErrorBatch2Keys(t *testing.T) {
	tests := []struct {
		name       string
		errorKey   string
		wantCode   string
		wantStatus int
	}{
		{
			name:       "web binding reactivate denied",
			errorKey:   "WEB_BINDING_REACTIVATE_DENIED",
			wantCode:   webBindingReactivateDeniedCode,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "web binding version mismatch",
			errorKey:   "WEB_BINDING_VERSION_MISMATCH",
			wantCode:   webBindingVersionMismatchCode,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "web binding version immutable",
			errorKey:   "WEB_BINDING_VERSION_IMMUTABLE",
			wantCode:   webBindingVersionImmutableCode,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "web binding user immutable",
			errorKey:   "WEB_BINDING_USER_ID_IMMUTABLE",
			wantCode:   webBindingUserImmutableCode,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "pairing miss after reset",
			errorKey:   "RULE_PAIRING_CODE_LOOKUP_MISS_AFTER_RESET",
			wantCode:   pairingCodeInvalidCode,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "recovery mismatch after reset",
			errorKey:   "RULE_RECOVERY_CODE_HASH_MISMATCH_AFTER_RESET",
			wantCode:   recoveryCodeInvalidCode,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "rotate pairing user not found",
			errorKey:   "ROTATE_PAIRING_USER_NOT_FOUND",
			wantCode:   userNotFoundCode,
			wantStatus: http.StatusConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err, ok := mapRuleErrorKeyToAPIError(tc.errorKey)
			if !ok {
				t.Fatalf("mapping not found for key %s", tc.errorKey)
			}
			if err.Code != tc.wantCode {
				t.Fatalf("error_code = %q, want %q", err.Code, tc.wantCode)
			}
			if err.StatusCode() != tc.wantStatus {
				t.Fatalf("status = %d, want %d", err.StatusCode(), tc.wantStatus)
			}
		})
	}
}

func TestBatch2ErrorCodesDefined(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "pairing code generate failed",
			got:  pairingCodeGenerateFailedCode,
			want: "PAIRING_CODE_GENERATE_FAILED",
		},
		{
			name: "recovery code already initialized",
			got:  recoveryCodeAlreadyInitializedCode,
			want: "RECOVERY_CODE_ALREADY_INITIALIZED",
		},
		{
			name: "unauthorized device",
			got:  unauthorizedDeviceCode,
			want: "UNAUTHORIZED_DEVICE",
		},
		{
			name: "unauthorized web token",
			got:  unauthorizedWebTokenCode,
			want: "UNAUTHORIZED_WEB_TOKEN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("constant = %q, want %q", tc.got, tc.want)
			}
		})
	}
}
