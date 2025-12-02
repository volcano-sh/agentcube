package workloadmanager

import (
	"time"

	"github.com/gin-gonic/gin"
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
func respondJSON(c *gin.Context, statusCode int, data interface{}) {
	c.JSON(statusCode, data)
}

// respondError sends an error response
func respondError(c *gin.Context, statusCode int, errorCode, message string) {
	response := ErrorResponse{
		Error:     errorCode,
		Message:   message,
		Timestamp: time.Now(),
	}
	respondJSON(c, statusCode, response)
}
