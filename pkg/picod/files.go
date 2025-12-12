package picod

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

	c.JSON(http.StatusOK, FileInfo{
		Path:     safePath,
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

	c.JSON(http.StatusOK, FileInfo{
		Path:     safePath,
		Size:     stat.Size(),
		Mode:     stat.Mode().String(),
		Modified: stat.ModTime(),
	})
}

// DownloadFileHandler handles file download requests
func (s *Server) DownloadFileHandler(c *gin.Context) {
	path := c.Param("path")
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

	var files []FileEntry
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			log.Printf("Warning: Failed to get info for entry '%s': %v", entry.Name(), err)
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
		log.Printf("Warning: Invalid file mode '%s': %v, using default 0644", modeStr, err)
		return 0644
	}
	if mode > maxFileMode {
		log.Printf("Warning: Invalid file mode '%s': exceeds 0777, using default 0644", modeStr)
		return 0644
	}
	return os.FileMode(mode)
}

// setWorkspace sets the global workspace directory
func (s *Server) setWorkspace(dir string) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		log.Printf("Warning: Failed to resolve absolute path for workspace '%s': %v", dir, err)
		s.workspaceDir = dir // Fallback to provided path
	} else {
		s.workspaceDir = absDir
	}
}

// sanitizePath ensures path is within allowed scope, preventing directory traversal attacks
func (s *Server) sanitizePath(p string) (string, error) {
	if s.workspaceDir == "" {
		return "", fmt.Errorf("workspace directory not initialized")
	}

	// Resolve workspace directory to handle cases where workspace itself is a symlink
	resolvedWorkspace, err := filepath.EvalSymlinks(s.workspaceDir)
	if err != nil {
		// Fallback if workspace doesn't exist or error (shouldn't happen in normal operation)
		if abs, err2 := filepath.Abs(s.workspaceDir); err2 == nil {
			resolvedWorkspace = abs
		} else {
			resolvedWorkspace = filepath.Clean(s.workspaceDir)
		}
	}

	cleanPath := filepath.Clean(p)
	if filepath.IsAbs(cleanPath) {
		cleanPath = strings.TrimPrefix(cleanPath, "/")
	}

	fullPath := filepath.Join(resolvedWorkspace, cleanPath)

	// Try to resolve the full path (handles existing files/dirs)
	resolvedPath, err := filepath.EvalSymlinks(fullPath)
	if err == nil {
		// Path exists, verify it's within workspace
		if !strings.HasPrefix(resolvedPath, resolvedWorkspace+string(os.PathSeparator)) && resolvedPath != resolvedWorkspace {
			return "", fmt.Errorf("access denied: path escapes workspace jail")
		}
		return resolvedPath, nil
	}

	// If path doesn't exist (e.g. creating new file), verify ancestors
	if os.IsNotExist(err) {
		currentPath := filepath.Dir(fullPath)
		for {
			resolved, err := filepath.EvalSymlinks(currentPath)
			if err == nil {
				// Ancestor exists, verify it's within workspace
				if !strings.HasPrefix(resolved, resolvedWorkspace+string(os.PathSeparator)) && resolved != resolvedWorkspace {
					return "", fmt.Errorf("access denied: parent path '%s' resolves to '%s' outside workspace", currentPath, resolved)
				}
				// Ancestor is safe, so creating file under it is safe
				return filepath.Abs(fullPath)
			}

			// Go up one level
			parent := filepath.Dir(currentPath)
			if parent == currentPath {
				break // Reached root
			}
			currentPath = parent
		}
		// If no existing ancestor found (unlikely if workspace exists), fallback to Abs
		return filepath.Abs(fullPath)
	}

	return "", fmt.Errorf("failed to resolve path: %w", err)
}
