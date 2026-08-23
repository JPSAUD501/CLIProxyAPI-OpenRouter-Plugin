package openrouter

import (
	"fmt"
	"net/http"
	"strings"
)

type StatusError struct {
	Code       string
	Message    string
	HTTPStatus int
	Retryable  bool
}

func (e *StatusError) Error() string { return e.Message }

func statusError(code, message string, status int, retryable bool) *StatusError {
	return &StatusError{Code: code, Message: strings.TrimSpace(message), HTTPStatus: status, Retryable: retryable}
}

func upstreamError(status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(status)
	}
	if len(message) > 4096 {
		message = message[:4096]
	}
	return statusError("openrouter_upstream_error", fmt.Sprintf("OpenRouter returned HTTP %d: %s", status, message), status, status == http.StatusTooManyRequests || status >= 500)
}
