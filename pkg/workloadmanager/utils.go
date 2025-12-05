package workloadmanager

import (
	"math/rand"
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

// RandString generates a random string from lowercase alphanumeric characters.
// The length of the string is n.
func RandString(n int) string {
	letters := []byte("abcdefghijklmnopqrstuvwxyz0123456789")
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Int63()%int64(len(letters))]
	}
	return string(b)
}
