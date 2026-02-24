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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseFileMode(t *testing.T) {
	tests := []struct {
		name     string
		modeStr  string
		expected os.FileMode
	}{
		{
			name:     "empty string defaults to 0644",
			modeStr:  "",
			expected: 0644,
		},
		{
			name:     "valid mode 0644",
			modeStr:  "0644",
			expected: 0644,
		},
		{
			name:     "valid mode 0755",
			modeStr:  "0755",
			expected: 0755,
		},
		{
			name:     "valid mode 0777",
			modeStr:  "0777",
			expected: 0777,
		},
		{
			name:     "valid mode 0600",
			modeStr:  "0600",
			expected: 0600,
		},
		{
			name:     "valid mode 0000",
			modeStr:  "0000",
			expected: 0000,
		},
		{
			name:     "valid mode 0444",
			modeStr:  "0444",
			expected: 0444,
		},
		{
			name:     "invalid mode - non-numeric",
			modeStr:  "abc",
			expected: 0644, // defaults to 0644
		},
		{
			name:     "invalid mode - malformed string",
			modeStr:  "0x644",
			expected: 0644, // defaults to 0644
		},
		{
			name:     "invalid mode - decimal number",
			modeStr:  "644",
			expected: 0644, // defaults to 0644
		},
		{
			name:     "invalid mode - hex string",
			modeStr:  "0x1ff",
			expected: 0644, // defaults to 0644
		},
		{
			name:     "invalid mode - negative number",
			modeStr:  "-1",
			expected: 0644, // defaults to 0644
		},
		{
			name:     "mode 0778 exceeds max",
			modeStr:  "0778",
			expected: 0644, // defaults to 0644
		},
		{
			name:     "mode 10000 exceeds max",
			modeStr:  "10000",
			expected: 0644, // defaults to 0644
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseFileMode(tt.modeStr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizePath(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "picod-test-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	server := &Server{
		workspaceDir: tmpDir,
	}

	tests := []struct {
		name      string
		path      string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "valid relative path",
			path:      "test.txt",
			wantError: false,
		},
		{
			name:      "valid nested path",
			path:      "subdir/file.txt",
			wantError: false,
		},
		{
			name:      "path traversal attack - ../",
			path:      "../etc/passwd",
			wantError: true,
			errorMsg:  "escapes workspace jail",
		},
		{
			name:      "path traversal attack - ../../",
			path:      "../../etc/passwd",
			wantError: true,
			errorMsg:  "escapes workspace jail",
		},
		{
			name:      "path traversal attack - multiple ../",
			path:      "../../../root/.ssh/id_rsa",
			wantError: true,
			errorMsg:  "escapes workspace jail",
		},
		{
			name:      "path traversal in middle",
			path:      "subdir/../other/file.txt",
			wantError: false, // This is normalized and should be safe
		},
		{
			name:      "absolute path",
			path:      "/absolute/path",
			wantError: false, // Absolute paths are treated as relative
		},
		{
			name:      "path with .. at start",
			path:      "../test",
			wantError: true,
			errorMsg:  "escapes workspace jail",
		},
		{
			name:      "just ..",
			path:      "..",
			wantError: true,
			errorMsg:  "escapes workspace jail",
		},
		{
			name:      "empty path",
			path:      "",
			wantError: false,
		},
		{
			name:      "current directory",
			path:      ".",
			wantError: false,
		},
		{
			name:      "deep nested path",
			path:      "a/b/c/d/e/f.txt",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := server.sanitizePath(tt.path)
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				// Verify the result is within the workspace
				relPath, relErr := filepath.Rel(tmpDir, result)
				assert.NoError(t, relErr)
				assert.NotRegexp(t, `^\.\.`, relPath, "Path should not escape workspace")
			}
		})
	}
}

func TestSanitizePath_WorkspaceNotInitialized(t *testing.T) {
	server := &Server{
		workspaceDir: "",
	}

	_, err := server.sanitizePath("test.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace directory not initialized")
}

func TestSetWorkspace(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		wantErr  bool
		checkAbs bool
	}{
		{
			name:     "valid directory",
			dir:      "/tmp",
			wantErr:  false,
			checkAbs: true,
		},
		{
			name:     "relative directory",
			dir:      "test-dir",
			wantErr:  false,
			checkAbs: true,
		},
		{
			name:     "current directory",
			dir:      ".",
			wantErr:  false,
			checkAbs: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &Server{}
			server.setWorkspace(tt.dir)

			if tt.checkAbs {
				// Verify workspace is set to absolute path
				assert.True(t, filepath.IsAbs(server.workspaceDir), "Workspace should be absolute path")
			}
		})
	}
}

func TestSetWorkspace_WithTemporaryDirectory(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "picod-workspace-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	server := &Server{}
	server.setWorkspace(tmpDir)

	assert.Equal(t, tmpDir, server.workspaceDir)
}
