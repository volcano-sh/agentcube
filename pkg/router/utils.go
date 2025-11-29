package router

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
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

// getSandboxStatus extracts status from Sandbox CRD conditions
func getSandboxStatus(sandbox *sandboxv1alpha1.Sandbox) string {
	// Check conditions for Ready status
	for _, condition := range sandbox.Status.Conditions {
		if condition.Type == string(sandboxv1alpha1.SandboxConditionReady) && condition.Status == metav1.ConditionTrue {
			return "running"
		}
	}
	return "paused"
}
