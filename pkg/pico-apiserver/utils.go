package picoapiserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// ErrorResponse represents an API error response
type ErrorResponse struct {
	Error     string                 `json:"error"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	RequestID string                 `json:"requestId,omitempty"`
}

// respondJSON sends a JSON response
func respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		// If encoding fails, log error but don't try to write response
		// since WriteHeader has already been called
		return
	}
}

// respondError sends an error response
func respondError(w http.ResponseWriter, statusCode int, errorCode, message string) {
	response := ErrorResponse{
		Error:     errorCode,
		Message:   message,
		Timestamp: time.Now(),
	}
	respondJSON(w, statusCode, response)
}

// getIntQueryParam gets an integer value from query parameters, returns default value if not present
func getIntQueryParam(r *http.Request, key string, defaultValue int) int {
	valueStr := r.URL.Query().Get(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}

	return value
}
