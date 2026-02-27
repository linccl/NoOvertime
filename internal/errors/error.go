package apperrors

import "net/http"

// APIError is the baseline typed error used by HTTP handlers.
type APIError struct {
	HTTPStatus int
	Code       string
	Message    string
}

// ErrorResponse is the standard API error response payload.
type ErrorResponse struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// New builds a typed APIError with explicit HTTP status.
func New(httpStatus int, code, message string) APIError {
	return APIError{
		HTTPStatus: httpStatus,
		Code:       code,
		Message:    message,
	}
}

// StatusCode returns a safe HTTP status for this error.
func (e APIError) StatusCode() int {
	if e.HTTPStatus <= 0 {
		return http.StatusInternalServerError
	}
	return e.HTTPStatus
}

func (e APIError) Error() string {
	return e.Code + ": " + e.Message
}
