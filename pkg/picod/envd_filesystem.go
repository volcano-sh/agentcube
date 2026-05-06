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
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

// EnvdUploadHandler handles POST /envd/filesystem/upload
func (s *Server) EnvdUploadHandler(c *gin.Context) {
	contentType := c.ContentType()
	if strings.HasPrefix(contentType, "multipart/form-data") {
		s.envdHandleMultipartUpload(c)
	} else {
		s.envdHandleJSONBase64Upload(c)
	}
}

func (s *Server) envdHandleMultipartUpload(c *gin.Context) {
	path := c.PostForm("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	safePath, err := s.sanitizePath(path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("failed to get file: %v", err)})
		return
	}

	if err := s.mkdirSafe(filepath.Dir(safePath)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create directory: %v", err)})
		return
	}

	if err := c.SaveUploadedFile(fileHeader, safePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save file: %v", err)})
		return
	}

	info, err := os.Stat(safePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stat file: %v", err)})
		return
	}

	c.JSON(http.StatusOK, EnvdFileInfo{
		Name:     info.Name(),
		Path:     path,
		Type:     fileType(info),
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		Modified: info.ModTime(),
	})
}

func (s *Server) envdHandleJSONBase64Upload(c *gin.Context) {
	var req FilesystemUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	safePath, err := s.sanitizePath(req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.mkdirSafe(filepath.Dir(safePath)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create directory: %v", err)})
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid base64 content: %v", err)})
		return
	}

	mode := os.FileMode(0644)
	if err := os.WriteFile(safePath, data, mode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to write file: %v", err)})
		return
	}

	info, err := os.Stat(safePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stat file: %v", err)})
		return
	}

	c.JSON(http.StatusOK, EnvdFileInfo{
		Name:     info.Name(),
		Path:     req.Path,
		Type:     fileType(info),
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		Modified: info.ModTime(),
	})
}

// EnvdDownloadHandler handles GET /envd/filesystem/download
func (s *Server) EnvdDownloadHandler(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path query parameter is required"})
		return
	}

	safePath, err := s.sanitizePath(path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	info, err := os.Stat(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stat file: %v", err)})
		}
		return
	}

	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory"})
		return
	}

	contentType := mime.TypeByExtension(filepath.Ext(safePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	c.Header("Content-Type", contentType)
	c.File(safePath)
}

// EnvdListHandler handles GET /envd/filesystem/list
func (s *Server) EnvdListHandler(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "."
	}

	safePath, err := s.sanitizePath(path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entries, err := os.ReadDir(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to read directory: %v", err)})
		}
		return
	}

	result := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			klog.Warningf("failed to get info for entry %s: %v", entry.Name(), err)
			continue
		}

		entryType := "file"
		if info.IsDir() {
			entryType = "directory"
		}

		result = append(result, FileEntry{
			Name:     entry.Name(),
			Type:     entryType,
			Size:     info.Size(),
			Mode:     info.Mode().String(),
			Modified: info.ModTime(),
		})
	}

	c.JSON(http.StatusOK, FilesystemListResponse{Entries: result})
}

// EnvdMkdirHandler handles POST /envd/filesystem/mkdir
func (s *Server) EnvdMkdirHandler(c *gin.Context) {
	var req FilesystemMkdirRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	safePath, err := s.sanitizePath(req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	perm := os.FileMode(0755)
	if req.Parents {
		if err := s.mkdirSafe(safePath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create directory: %v", err)})
			return
		}
	} else {
		if err := os.Mkdir(safePath, perm); err != nil {
			if os.IsExist(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "directory already exists"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create directory: %v", err)})
			}
			return
		}
	}

	info, err := os.Stat(safePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stat directory: %v", err)})
		return
	}

	c.JSON(http.StatusOK, EnvdFileInfo{
		Name:     info.Name(),
		Path:     req.Path,
		Type:     "directory",
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		Modified: info.ModTime(),
	})
}

// EnvdMoveHandler handles POST /envd/filesystem/move
func (s *Server) EnvdMoveHandler(c *gin.Context) {
	var req FilesystemMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	sourcePath, err := s.sanitizePath(req.SourcePath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	targetPath, err := s.sanitizePath(req.TargetPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.mkdirSafe(filepath.Dir(targetPath)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create target directory: %v", err)})
		return
	}

	if err := os.Rename(sourcePath, targetPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to move file: %v", err)})
		return
	}

	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

// EnvdRemoveHandler handles DELETE /envd/filesystem/remove
func (s *Server) EnvdRemoveHandler(c *gin.Context) {
	var req FilesystemRemoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	safePath, err := s.sanitizePath(req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := os.RemoveAll(safePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to remove: %v", err)})
		return
	}

	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

// EnvdStatHandler handles GET /envd/filesystem/stat
func (s *Server) EnvdStatHandler(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path query parameter is required"})
		return
	}

	safePath, err := s.sanitizePath(path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	info, err := os.Stat(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stat file: %v", err)})
		}
		return
	}

	c.JSON(http.StatusOK, EnvdFileInfo{
		Name:     info.Name(),
		Path:     path,
		Type:     fileType(info),
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		Modified: info.ModTime(),
	})
}

func fileType(info os.FileInfo) string {
	if info.IsDir() {
		return "directory"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "symlink"
	}
	return "file"
}
