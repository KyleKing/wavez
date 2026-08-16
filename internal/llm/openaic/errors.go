package openaic

import (
	"errors"
	"fmt"
)

// ErrNotConfigured reports a Client missing a base URL or model, both
// required before Stream can build a request.
var ErrNotConfigured = errors.New("client not configured with base URL and model")

// StatusError reports a non-2xx HTTP response from a provider.
type StatusError struct {
	Provider   string
	Body       string
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("openaic: %s: status %d: %s", e.Provider, e.StatusCode, e.Body)
}

// StreamError reports an error payload a provider sent mid-stream instead of
// completing normally.
type StreamError struct {
	Provider string
	Message  string
	Type     string
	Code     string
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("openaic: %s: stream error (%s/%s): %s", e.Provider, e.Type, e.Code, e.Message)
}
