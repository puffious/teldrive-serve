package models

import (
	"net/http"
	"time"
)

// APIResponse represents a standard API response structure
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *APIError   `json:"error,omitempty"`
}

// APIError represents an API error response
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// DownloadMetrics represents download statistics
type DownloadMetrics struct {
	FileID      string        `json:"file_id"`
	Filename    string        `json:"filename"`
	Size        int64         `json:"size"`
	StartTime   time.Time     `json:"start_time"`
	Duration    time.Duration `json:"duration,omitempty"`
	ClientIP    string        `json:"client_ip"`
	UserAgent   string        `json:"user_agent,omitempty"`
	BytesServed int64         `json:"bytes_served,omitempty"`
	StatusCode  int           `json:"status_code,omitempty"`
	Success     bool          `json:"success"`
}

// NewSuccessResponse creates a successful API response
func NewSuccessResponse(data interface{}) *APIResponse {
	return &APIResponse{
		Success: true,
		Data:    data,
	}
}

// NewErrorResponse creates an error API response
func NewErrorResponse(code, message, details string) *APIResponse {
	return &APIResponse{
		Success: false,
		Error: &APIError{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
}

// HTTPStatusCode returns the appropriate HTTP status code for the API error
func (e *APIError) HTTPStatusCode() int {
	switch e.Code {
	case "NOT_FOUND":
		return http.StatusNotFound
	case "UNAUTHORIZED":
		return http.StatusUnauthorized
	case "BAD_REQUEST":
		return http.StatusBadRequest
	case "TOO_MANY_REQUESTS":
		return http.StatusTooManyRequests
	case "SERVICE_UNAVAILABLE":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}