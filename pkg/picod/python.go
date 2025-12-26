package picod

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// RunPythonRequest defines Python execution request
type RunPythonRequest struct {
	Code string `json:"code" binding:"required"`
}

// RunPythonResponse defines Python execution response
type RunPythonResponse struct {
	Output         string  `json:"output"`
	Error          string  `json:"error"`
	Status         string  `json:"status"` // "ok" or "error"
	ExecutionCount int     `json:"execution_count"`
	Duration       float64 `json:"duration"`
}

// RunPythonHandler handles Python code execution requests
func (s *Server) RunPythonHandler(c *gin.Context) {
	var req RunPythonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Execute code without timeout (blocking until completion)
	start := time.Now()
	result, err := s.jupyterManager.ExecuteCode(req.Code)
	duration := time.Since(start).Seconds()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, RunPythonResponse{
		Output:         result.Output,
		Error:          result.Error,
		Status:         result.Status,
		ExecutionCount: result.ExecutionCount,
		Duration:       duration,
	})
}
