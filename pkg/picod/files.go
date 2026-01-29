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
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

const (
	maxFileMode = 0777 // Maximum allowed file permission mode
)

// FileInfo defines file information response body
type FileInfo struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified"`
}

// UploadFileRequest defines JSON upload request body
type UploadFileRequest struct {
	Path    string `json:"path" binding:"required"`
	Content string `json:"content" binding:"required"` // Base64 encoded content
	Mode    string `json:"mode"`
}

// UploadFileHandler handles file upload requests
func (s *Server) UploadFileHandler(c *gin.Context) {
	contentType := c.ContentType()

	// Determine request type: multipart or JSON
	if strings.HasPrefix(contentType, "multipart/form-data") {
		s.handleMultipartUpload(c)
	} else {
		s.handleJSONBase64Upload(c)
	}
}

func (s *Server) handleMultipartUpload(c *gin.Context) {
	path := c.PostForm("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Missing 'path' field",
			"code":  http.StatusBadRequest,
		})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Failed to get file: %v", err),
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Ensure path safety
	safePath, err := s.sanitizePath(path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Create directory
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to create directory: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	// Parse mode first
	modeStr := c.PostForm("mode")
	fileMode := parseFileMode(modeStr)

	// Open source file
	src, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file", "code": http.StatusInternalServerError})
		return
	}
	defer src.Close()

	// Create destination file with correct permissions
	dst, err := os.OpenFile(safePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create destination file", "code": http.StatusInternalServerError})
		return
	}
	defer dst.Close()

	// Copy content
	if _, err := io.Copy(dst, src); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file content", "code": http.StatusInternalServerError})
		return
	}

	stat, err := os.Stat(safePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get file info: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	relPath, err := filepath.Rel(s.workspaceDir, safePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get relative path: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, FileInfo{
		Path:     relPath,
		Size:     stat.Size(),
		Mode:     stat.Mode().String(),
		Modified: stat.ModTime(),
	})
}

func (s *Server) handleJSONBase64Upload(c *gin.Context) {
	var req UploadFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Ensure path safety
	safePath, err := s.sanitizePath(req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Decode Base64 content
	decodedContent, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid base64 content: %v", err),
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Create directory
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to create directory: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	// Parse and validate file permissions
	fileMode := parseFileMode(req.Mode)

	// Write file with the specified permissions
	err = os.WriteFile(safePath, decodedContent, fileMode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to write file: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	stat, err := os.Stat(safePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get file info: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	relPath, err := filepath.Rel(s.workspaceDir, safePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get relative path: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, FileInfo{
		Path:     relPath,
		Size:     stat.Size(),
		Mode:     stat.Mode().String(),
		Modified: stat.ModTime(),
	})
}

// DownloadFileHandler handles file download requests
func (s *Server) DownloadFileHandler(c *gin.Context) {
	path := c.Param("path")
	klog.Infof("DownloadFileHandler: received path param: %q", path)
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Missing file path",
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Remove leading /
	path = strings.TrimPrefix(path, "/")
	// Ensure path safety
	safePath, err := s.sanitizePath(path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  http.StatusBadRequest,
		})
		return
	}

	fileInfo, err := os.Stat(safePath)
	if err != nil {
		klog.Errorf("DownloadFileHandler: file stat failed for %q: %v", safePath, err)
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "File not found",
				"code":  http.StatusNotFound,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to get file info: %v", err),
				"code":  http.StatusInternalServerError,
			})
		}
		return
	}

	klog.Infof("DownloadFileHandler: file found: %q, size: %d", safePath, fileInfo.Size())

	if fileInfo.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Path is a directory, not a file",
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Try to guess Content-Type based on file extension
	contentType := mime.TypeByExtension(filepath.Ext(safePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(safePath)))
	c.Header("Content-Type", contentType)
	c.File(safePath)
}

// FileEntry defines a single file entry in the list response
type FileEntry struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Mode     string    `json:"mode"`
	IsDir    bool      `json:"is_dir"`
}

// ListFilesResponse defines file listing response body
type ListFilesResponse struct {
	Files []FileEntry `json:"files"`
}

// ListFilesHandler handles file listing requests
func (s *Server) ListFilesHandler(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Missing 'path' query parameter",
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Ensure path safety
	safePath, err := s.sanitizePath(path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  http.StatusBadRequest,
		})
		return
	}

	entries, err := os.ReadDir(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "Directory not found",
				"code":  http.StatusNotFound,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to read directory: %v", err),
				"code":  http.StatusInternalServerError,
			})
		}
		return
	}

	files := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			klog.Warningf("Failed to get info for entry '%s': %v", entry.Name(), err)
			continue // Skip files with errors
		}
		files = append(files, FileEntry{
			Name:     entry.Name(),
			Size:     info.Size(),
			Modified: info.ModTime(),
			Mode:     info.Mode().String(),
			IsDir:    entry.IsDir(),
		})
	}

	c.JSON(http.StatusOK, ListFilesResponse{
		Files: files,
	})
}

// parseFileMode parses file mode string
func parseFileMode(modeStr string) os.FileMode {
	if modeStr == "" {
		return 0644
	}
	mode, err := strconv.ParseUint(modeStr, 8, 32)
	if err != nil {
		klog.Warningf("Invalid file mode '%s': %v, using default 0644", modeStr, err)
		return 0644
	}
	if mode > maxFileMode {
		klog.Warningf("Invalid file mode '%s': exceeds 0777, using default 0644", modeStr)
		return 0644
	}
	return os.FileMode(mode)
}

// setWorkspace sets the global workspace directory
func (s *Server) setWorkspace(dir string) {
	klog.Infof("setWorkspace called with dir: %q", dir)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		klog.Warningf("Failed to resolve absolute path for workspace '%s': %v", dir, err)
		s.workspaceDir = dir // Fallback to provided path
	} else {
		s.workspaceDir = absDir
		klog.Infof("Resolved workspace to absolute path: %q", s.workspaceDir)
	}
}

// sanitizePath ensures path is within allowed scope, preventing directory traversal attacks
func (s *Server) sanitizePath(p string) (string, error) {
	if s.workspaceDir == "" {
		return "", fmt.Errorf("workspace directory not initialized")
	}

	resolvedWorkspace, err := filepath.EvalSymlinks(s.workspaceDir)
	if err != nil {
		if abs, err2 := filepath.Abs(s.workspaceDir); err2 == nil {
			resolvedWorkspace = abs
		} else {
			resolvedWorkspace = filepath.Clean(s.workspaceDir)
		}
	}
	// Ensure base workspace path is clean and absolute for reliable comparison with filepath.Rel
	resolvedWorkspace = filepath.Clean(resolvedWorkspace)

	// Clean the input path; if it's absolute, treat it as relative to the workspace root.
	cleanPath := filepath.Clean(p)
	if filepath.IsAbs(cleanPath) {
		cleanPath = strings.TrimPrefix(cleanPath, string(os.PathSeparator))
	}

	// Construct the full absolute path candidate within the workspace.
	// filepath.Join handles cases like "a/../b", but we also need to filepath.Clean it afterwards
	// to normalize any ".." components that might be introduced by `Join` if `cleanPath` itself
	// contained them.
	fullPathCandidate := filepath.Join(resolvedWorkspace, cleanPath)
	fullPathCandidate = filepath.Clean(fullPathCandidate)

	// Robustly check if fullPathCandidate is truly within resolvedWorkspace using filepath.Rel.
	// filepath.Rel returns a path relative to base, or an error if target cannot be made relative to base.
	relPath, relErr := filepath.Rel(resolvedWorkspace, fullPathCandidate)
	if relErr != nil {
		return "", fmt.Errorf("access denied: path '%s' escapes workspace jail (rel error: %w)", p, relErr)
	}
	// Also explicitly check for ".." at the start of the relative path, which indicates traversal outside the workspace,
	// or if the path is ".." itself, both implying an escape.
	if strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) || relPath == ".." {
		return "", fmt.Errorf("access denied: path '%s' escapes workspace jail (relative path traversal: %s)", p, relPath)
	}

	// At this point, fullPathCandidate is proven to be within resolvedWorkspace or is resolvedWorkspace itself.
	// Now, attempt to resolve symlinks for the final path. If this resolution leads outside the workspace,
	// it indicates a symlink attack.
	resolvedFinalPath, err := filepath.EvalSymlinks(fullPathCandidate)
	if err == nil {
		// If resolvedFinalPath exists and is a symlink, re-check it against the workspace
		finalRelPath, finalRelErr := filepath.Rel(resolvedWorkspace, resolvedFinalPath)
		if finalRelErr != nil || strings.HasPrefix(finalRelPath, ".."+string(os.PathSeparator)) || finalRelPath == ".." {
			return "", fmt.Errorf("access denied: resolved path '%s' (from '%s') escapes workspace jail via symlink", resolvedFinalPath, p)
		}
		return resolvedFinalPath, nil
	}

	// If the path does not exist (e.g., a new file/directory is being created),
	// we have already verified that fullPathCandidate (the intended location) is safe.
	// Return its absolute, cleaned form.
	return fullPathCandidate, nil
}
