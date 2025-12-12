package picod

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	TimeoutExitCode = 124 // Standard timeout exit code used by GNU timeout command.
)

// ExecuteRequest defines command execution request body
type ExecuteRequest struct {
	Command    []string          `json:"command" binding:"required"` // The command and its arguments to execute. The first element is the executable.
	Timeout    string            `json:"timeout"`                    // Optional: Timeout for the command execution (e.g., "30s", "500ms"). Defaults to "30s".
	WorkingDir string            `json:"working_dir"`                // Optional: The working directory for the command.
	Env        map[string]string `json:"env"`                        // Optional: Environment variables to set for the command.
}

// ExecuteResponse defines command execution response body
type ExecuteResponse struct {
	Stdout    string    `json:"stdout"`     // Standard output of the executed command.
	Stderr    string    `json:"stderr"`     // Standard error of the executed command.
	ExitCode  int       `json:"exit_code"`  // The exit code of the executed command. Timeout is indicated by TimeoutExitCode (124).
	Duration  float64   `json:"duration"`   // The duration of the command execution in seconds.
	StartTime time.Time `json:"start_time"` // The start time of the command execution.
	EndTime   time.Time `json:"end_time"`   // The end time of the command execution.
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
	timeoutDuration := 60 * time.Second // Default timeout
	if req.Timeout != "" {
		var err error
		timeoutDuration, err = time.ParseDuration(req.Timeout)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Invalid timeout format: %v", err),
				"code":  http.StatusBadRequest,
			})
			return
		}
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
	defer cancel()

	// Execute command with context
	// Use the first element as the command and the rest as arguments
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...) //nolint:gosec // This is an agent designed to execute arbitrary commands

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
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		exitCode = TimeoutExitCode
		stderr.WriteString(fmt.Sprintf("Command timed out after %.0f seconds", timeoutDuration.Seconds()))
	} else if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else {
		exitCode = 1
		if stderr.Len() > 0 {
			stderr.WriteString("\n")
		}
		// If there's an error from cmd.Run() and no ProcessState, append it to stderr
		if err != nil {
			stderr.WriteString(err.Error())
		}
	}

	c.JSON(http.StatusOK, ExecuteResponse{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  exitCode,
		Duration:  duration,
		StartTime: start,
		EndTime:   endTime,
	})
}
