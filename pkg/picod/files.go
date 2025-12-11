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
	fileMode := os.FileMode(0644) // Default permissions
	if modeStr != "" {
		if mode, err := strconv.ParseUint(modeStr, 8, 32); err == nil && mode <= maxFileMode {
			fileMode = os.FileMode(mode)
		} else {
			log.Printf("Warning: Invalid or out-of-range file mode '%s', using default 0644.", modeStr)
		}
	}

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
		Path:     path,
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
	fileMode := os.FileMode(0644) // default
	if req.Mode != "" {
		mode, err := strconv.ParseUint(req.Mode, 8, 32)
		if err != nil {
			log.Printf("Warning: Invalid file mode '%s': %v, using default 0644", req.Mode, err)
		} else if mode > maxFileMode {
			log.Printf("Warning: Invalid file mode '%s': exceeds 0777, using default 0644", req.Mode)
		} else {
			fileMode = os.FileMode(mode)
		}
	}

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
		Path:     req.Path,
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

	cleanPath := filepath.Clean(p)

	if filepath.IsAbs(cleanPath) {
		cleanPath = strings.TrimPrefix(cleanPath, "/")
	}

	fullPath := filepath.Join(s.workspaceDir, cleanPath)

	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	cleanWorkspace := filepath.Clean(s.workspaceDir)
	sep := string(os.PathSeparator)
	if !strings.HasPrefix(absPath, cleanWorkspace+sep) && absPath != cleanWorkspace {
		return "", fmt.Errorf("access denied: path '%s' escapes workspace jail '%s'", p, cleanWorkspace)
	}

	return absPath, nil
}
