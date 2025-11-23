package picod

import (
	"encoding/base64"
	"fmt"
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
func UploadFileHandler(c *gin.Context) {
	contentType := c.ContentType()

	// Determine request type: multipart or JSON
	if strings.HasPrefix(contentType, "multipart/form-data") {
		handleMultipartUpload(c)
	} else {
		handleJSONBase64Upload(c)
	}
}

func handleMultipartUpload(c *gin.Context) {
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
	safePath, err := sanitizePath(path)
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

	// Save file
	if err := c.SaveUploadedFile(fileHeader, safePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to save file: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	// Set file permissions
	modeStr := c.PostForm("mode")
	if modeStr != "" {
		mode, err := strconv.ParseUint(modeStr, 8, 32)
		if err != nil {
			log.Printf("Warning: Invalid file mode '%s': %v", modeStr, err)
		} else {
			if err := os.Chmod(safePath, os.FileMode(mode)); err != nil {
				log.Printf("Warning: Failed to set file mode for '%s': %v", safePath, err)
			}
		}
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

func handleJSONBase64Upload(c *gin.Context) {
	var req UploadFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  http.StatusBadRequest,
		})
		return
	}

	// Ensure path safety
	safePath, err := sanitizePath(req.Path)
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

	// Write file
	err = os.WriteFile(safePath, decodedContent, 0644)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to write file: %v", err),
			"code":  http.StatusInternalServerError,
		})
		return
	}

	// Set file permissions
	if req.Mode != "" {
		mode, err := strconv.ParseUint(req.Mode, 8, 32)
		if err != nil {
			log.Printf("Warning: Invalid file mode '%s': %v", req.Mode, err)
		} else {
			if err := os.Chmod(safePath, os.FileMode(mode)); err != nil {
				log.Printf("Warning: Failed to set file mode for '%s': %v", safePath, err)
			}
		}
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
func DownloadFileHandler(c *gin.Context) {
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
	safePath, err := sanitizePath(path)
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

// sanitizePath ensures path is within allowed scope, preventing directory traversal attacks
func sanitizePath(p string) (string, error) {
	// Clean path
	cleanPath := filepath.Clean(p)

	// Check if attempting to access parent directory
	if strings.HasPrefix(cleanPath, "..") || strings.Contains(cleanPath, "/../") {
		return "", fmt.Errorf("invalid path: directory traversal detected")
	}

	// If already absolute path, return directly
	if filepath.IsAbs(cleanPath) {
		return cleanPath, nil
	}

	// Relative paths remain unchanged, allowing operations in current working directory
	return cleanPath, nil
}
