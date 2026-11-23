/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
	Timeout    string            `json:"timeout"`                    // Optional: Timeout for the command execution (e.g., "30s", "500ms"). Defaults to "60s".
	WorkingDir string            `json:"working_dir"`                // Optional: The working directory for the command.
	Env        map[string]string `json:"env"`                        // Optional: Environment variables to set for the command.
}

// ExecuteResponse defines command execution response body
type ExecuteResponse struct {
	Stdout          string    `json:"stdout"`                     // Standard output of the executed command.
	Stderr          string    `json:"stderr"`                     // Standard error of the executed command.
	ExitCode        int       `json:"exit_code"`                  // The exit code of the executed command. Timeout is indicated by TimeoutExitCode (124).
	Duration        float64   `json:"duration"`                   // The duration of the command execution in seconds.
	StartTime       time.Time `json:"start_time"`                 // The start time of the command execution.
	EndTime         time.Time `json:"end_time"`                   // The end time of the command execution.
	StdoutTruncated bool      `json:"stdout_truncated,omitempty"` // True if stdout exceeded the output limit and was cut off.
	StderrTruncated bool      `json:"stderr_truncated,omitempty"` // True if stderr exceeded the output limit and was cut off.
}

// cappedBuffer keeps the first limit bytes written to it and discards the rest.
// Write always reports the full length as written, so the command keeps running
// instead of failing on a short write or broken pipe.
//
// It deliberately does not embed bytes.Buffer: os/exec copies output with io.Copy,
// which would pick up an embedded ReadFrom method and bypass the limit.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := b.limit - b.buf.Len(); n > remaining {
		p = p[:max(remaining, 0)]
		b.truncated = true
	}
	b.buf.Write(p)
	return n, nil
}

// outputLimit returns the per-stream output limit, falling back to DefaultMaxOutputSize.
func (s *Server) outputLimit() int {
	if s.config.MaxOutputSize <= 0 {
		return DefaultMaxOutputSize
	}
	return s.config.MaxOutputSize
}

// appendTruncationNotes tells the caller in stderr which streams were cut off.
// It writes to stderr.buf directly so the notes are never lost to the output limit.
func appendTruncationNotes(stdout, stderr *cappedBuffer, limit int) {
	for _, out := range []struct {
		name string
		buf  *cappedBuffer
	}{{"stdout", stdout}, {"stderr", stderr}} {
		if out.buf.truncated {
			if stderr.buf.Len() > 0 {
				stderr.buf.WriteString("\n")
			}
			stderr.buf.WriteString(fmt.Sprintf("%s truncated: output exceeded %d bytes", out.name, limit))
		}
	}
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

	// Default working directory to workspace; override if the request specifies one.
	cmd.Dir = s.workspaceDir
	if req.WorkingDir != "" {
		safeWorkingDir, err := s.sanitizePath(req.WorkingDir)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Invalid working directory: %v", err),
				"code":  http.StatusBadRequest,
			})
			return
		}
		if _, statErr := os.Stat(safeWorkingDir); os.IsNotExist(statErr) {
			if err := s.mkdirSafe(safeWorkingDir); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": fmt.Sprintf("Failed to create working directory: %v", err),
					"code":  http.StatusInternalServerError,
				})
				return
			}
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

	outputLimit := s.outputLimit()
	stdout := &cappedBuffer{limit: outputLimit}
	stderr := &cappedBuffer{limit: outputLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start).Seconds()
	endTime := time.Now()

	// Messages below are written to stderr.buf directly so they are never lost to the output limit.
	var exitCode int
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		exitCode = TimeoutExitCode
		stderr.buf.WriteString(fmt.Sprintf("Command timed out after %.0f seconds", timeoutDuration.Seconds()))
	} else if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else {
		exitCode = 1
		if stderr.buf.Len() > 0 {
			stderr.buf.WriteString("\n")
		}
		// If there's an error from cmd.Run() and no ProcessState, append it to stderr
		if err != nil {
			stderr.buf.WriteString(err.Error())
		}
	}

	appendTruncationNotes(stdout, stderr, outputLimit)

	c.JSON(http.StatusOK, ExecuteResponse{
		Stdout:          stdout.buf.String(),
		Stderr:          stderr.buf.String(),
		ExitCode:        exitCode,
		Duration:        duration,
		StartTime:       start,
		EndTime:         endTime,
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
	})
}
