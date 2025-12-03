package picod

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gin-gonic/gin"
)

// ExecuteRequest defines command execution request body
type ExecuteRequest struct {
	Command    string            `json:"command" binding:"required"`
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
func ExecuteHandler(c *gin.Context) {
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Use bash to execute command
	cmd := exec.Command("bash", "-c", req.Command)

	// Set working directory
	if req.WorkingDir != "" {
		cmd.Dir = req.WorkingDir
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

	// Set timeout
	timeout := 30 * time.Second // Default timeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	done := make(chan error, 1)
	start := time.Now()

	go func() {
		done <- cmd.Run()
	}()

	var exitCode int
	select {
	case err := <-done:
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			} else {
				exitCode = 1
				stderr.WriteString(err.Error())
			}
		} else {
			exitCode = 0
		}
	case <-time.After(timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		exitCode = 124 // Timeout exit code
		stderr.WriteString(fmt.Sprintf("Command timed out after %.0f seconds", timeout.Seconds()))
	}

	duration := time.Since(start).Seconds()
	endTime := time.Now()

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
