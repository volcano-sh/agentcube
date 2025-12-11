package picod

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gin-gonic/gin"
)

// ExecuteRequest defines command execution request body
type ExecuteRequest struct {
	Command    []string          `json:"command" binding:"required"`
	Timeout    float64           `json:"timeout"`
	WorkingDir string            `json:"working_dir"`
	Env        map[string]string `json:"env"`
}

// ExecuteResponse defines command execution response body
type ExecuteResponse struct {
	Stdout    string    `json:"stdout"`
	Stderr    string    `json:"stderr"`
	ExitCode  int       `json:"exit_code"`
	Duration  float64   `json:"duration"`
	ProcessID int       `json:"process_id"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

// ExecuteHandler handles command execution requests
func (s *Server) ExecuteHandler(c *gin.Context) {
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  http.StatusBadRequest,
		})
		return
	}

	if len(req.Command) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "command cannot be empty",
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Set timeout
	timeoutDuration := 30 * time.Second // Default timeout
	if req.Timeout > 0 {
		timeoutDuration = time.Duration(req.Timeout) * time.Second
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
	defer cancel()

	// execute command with context
	// Use the first element as the command and the rest as arguments
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)

	// Set working directory
	if req.WorkingDir != "" {
		safeWorkingDir, err := s.sanitizePath(req.WorkingDir)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Invalid working directory: %v", err),
				"code":  http.StatusBadRequest,
			})
			return
		}
		cmd.Dir = safeWorkingDir
	}

	// Set environment variables
	if len(req.Env) > 0 {
		currentEnv := os.Environ()
		for k, v := range req.Env {
			currentEnv = append(currentEnv, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = currentEnv
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start).Seconds()
	endTime := time.Now()

	var exitCode int
	if ctx.Err() == context.DeadlineExceeded {
		exitCode = 124 // Timeout exit code
		stderr.WriteString(fmt.Sprintf("Command timed out after %.0f seconds", timeoutDuration.Seconds()))
	} else if err != nil {
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		} else {
			exitCode = 1
			if stderr.Len() == 0 {
				stderr.WriteString(err.Error())
			}
		}
	} else {
		exitCode = 0
	}

	var pid int
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	c.JSON(http.StatusOK, ExecuteResponse{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  exitCode,
		Duration:  duration,
		ProcessID: pid,
		StartTime: start,
		EndTime:   endTime,
	})
}
