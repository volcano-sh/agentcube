package apiserver

import (
	"strconv"
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

// getIntQueryParam gets an integer value from query parameters, returns default value if not present
func getIntQueryParam(c *gin.Context, key string, defaultValue int) int {
	valueStr := c.Query(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}

	return value
}
