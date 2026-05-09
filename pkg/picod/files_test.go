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
			err := server.setWorkspace(tt.dir)
			assert.NoError(t, err)

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
	err = server.setWorkspace(tmpDir)
	assert.NoError(t, err)

	assert.Equal(t, tmpDir, server.workspaceDir)
}

func TestMkdirSafe(t *testing.T) {
	// Create a temporary workspace directory
	tmpDir, err := os.MkdirTemp("", "picod-mkdirsafe-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	server := &Server{workspaceDir: tmpDir}

	t.Run("normal mkdir within workspace succeeds", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "subdir", "nested")
		err := server.mkdirSafe(dir)
		assert.NoError(t, err)

		info, statErr := os.Stat(dir)
		assert.NoError(t, statErr)
		assert.True(t, info.IsDir())
	})

	t.Run("symlink pointing outside workspace is blocked", func(t *testing.T) {
		// Create an external directory that the symlink will point to
		externalDir, err := os.MkdirTemp("", "picod-external-*")
		assert.NoError(t, err)
		defer os.RemoveAll(externalDir)

		// Create a symlink inside the workspace pointing to the external directory
		symlink := filepath.Join(tmpDir, "escape-link")
		err = os.Symlink(externalDir, symlink)
		assert.NoError(t, err)

		// Attempt to mkdirSafe through the symlink — this should fail
		targetDir := filepath.Join(symlink, "should-not-exist")
		err = server.mkdirSafe(targetDir)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "escapes workspace jail")

		// Verify the directory was NOT created outside the workspace
		_, statErr := os.Stat(filepath.Join(externalDir, "should-not-exist"))
		assert.True(t, os.IsNotExist(statErr), "directory should not exist outside workspace")
	})
}

