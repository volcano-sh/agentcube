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

import "time"

// ProcessState represents the state of a managed process
type ProcessState string

const (
	// ProcessStateStarting indicates the process is starting
	ProcessStateStarting ProcessState = "starting"
	// ProcessStateRunning indicates the process is running
	ProcessStateRunning ProcessState = "running"
	// ProcessStateExited indicates the process has exited
	ProcessStateExited ProcessState = "exited"
	// ProcessStateKilled indicates the process was killed
	ProcessStateKilled ProcessState = "killed"
)

// ProcessEventType represents the type of a process event
type ProcessEventType string

const (
	// ProcessEventTypeInfo indicates an info event
	ProcessEventTypeInfo ProcessEventType = "info"
	// ProcessEventTypeStdout indicates stdout data
	ProcessEventTypeStdout ProcessEventType = "stdout"
	// ProcessEventTypeStderr indicates stderr data
	ProcessEventTypeStderr ProcessEventType = "stderr"
	// ProcessEventTypeExit indicates process exit
	ProcessEventTypeExit ProcessEventType = "exit"
	// ProcessEventTypeError indicates an error
	ProcessEventTypeError ProcessEventType = "error"
)

// FileEntry represents a single entry in a directory listing
type FileEntry struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified"`
}

// EnvdFileInfo represents file metadata in E2B envd format
type EnvdFileInfo struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified"`
}

// ProcessStartRequest represents a request to start a new process
type ProcessStartRequest struct {
	Cmd     []string          `json:"cmd" binding:"required"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Timeout int               `json:"timeout,omitempty"` // seconds
	Pty     bool              `json:"pty,omitempty"`
}

// ProcessInputRequest represents a request to send input to a process
type ProcessInputRequest struct {
	ProcessID string `json:"process_id" binding:"required"`
	Data      string `json:"data" binding:"required"`
}

// ProcessCloseStdinRequest represents a request to close a process stdin
type ProcessCloseStdinRequest struct {
	ProcessID string `json:"process_id" binding:"required"`
}

// ProcessSignalRequest represents a request to send a signal to a process
type ProcessSignalRequest struct {
	ProcessID string `json:"process_id" binding:"required"`
	Signal    int    `json:"signal" binding:"required"`
}

// ProcessEvent represents an event emitted by a managed process
type ProcessEvent struct {
	Type     ProcessEventType `json:"type"`
	Data     string           `json:"data,omitempty"`
	ExitCode *int             `json:"exit_code,omitempty"`
	Error    string           `json:"error,omitempty"`
	Time     time.Time        `json:"timestamp"`
}

// ManagedProcess represents a running or finished process
type ManagedProcess struct {
	ProcessID string       `json:"process_id"`
	PID       int          `json:"pid"`
	Cmd       []string     `json:"cmd"`
	Cwd       string       `json:"cwd"`
	Env       []string     `json:"env"`
	State     ProcessState `json:"state"`
	ExitCode  *int         `json:"exit_code,omitempty"`
	Pty       bool         `json:"pty"`
	StartedAt time.Time    `json:"started_at"`
	ExitedAt  *time.Time   `json:"exited_at,omitempty"`
}

// FilesystemUploadRequest represents a request to upload a file
type FilesystemUploadRequest struct {
	Path    string `json:"path" binding:"required"`
	Content string `json:"content,omitempty"` // base64 encoded
}

// FilesystemListResponse represents a directory listing response
type FilesystemListResponse struct {
	Entries []FileEntry `json:"entries"`
}

// FilesystemMkdirRequest represents a request to create a directory
type FilesystemMkdirRequest struct {
	Path    string `json:"path" binding:"required"`
	Parents bool   `json:"parents,omitempty"`
}

// FilesystemMoveRequest represents a request to move a file or directory
type FilesystemMoveRequest struct {
	SourcePath string `json:"source_path" binding:"required"`
	TargetPath string `json:"target_path" binding:"required"`
}

// FilesystemRemoveRequest represents a request to remove a file or directory
type FilesystemRemoveRequest struct {
	Path string `json:"path" binding:"required"`
}

// FilesystemStatRequest represents a request to get file metadata
type FilesystemStatRequest struct {
	Path string `json:"path" binding:"required"`
}
