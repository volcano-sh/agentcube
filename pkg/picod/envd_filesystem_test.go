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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupFilesystemTestServer(t *testing.T) (*Server, string) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	os.Setenv(PublicKeyEnvVar, string(pubKeyPEM))

	tmpDir, err := os.MkdirTemp("", "picod-filesystem-test-*")
	require.NoError(t, err)

	config := Config{
		Port:      0,
		Workspace: tmpDir,
	}

	server := NewServer(config)
	return server, tmpDir
}

func TestEnvdUploadHandler_JSONBase64(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := FilesystemUploadRequest{
		Path:    "test.txt",
		Content: base64.StdEncoding.EncodeToString([]byte("hello world")),
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/filesystem/upload", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdUploadHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var info EnvdFileInfo
	err := json.Unmarshal(w.Body.Bytes(), &info)
	require.NoError(t, err)
	assert.Equal(t, "test.txt", info.Name)
	assert.Equal(t, "file", info.Type)
	assert.Equal(t, int64(11), info.Size)
}

func TestEnvdUploadHandler_Multipart(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("path", "multi.txt")
	part, _ := writer.CreateFormFile("file", "multi.txt")
	_, _ = part.Write([]byte("multipart content"))
	writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/filesystem/upload", &buf)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	server.EnvdUploadHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var info EnvdFileInfo
	err := json.Unmarshal(w.Body.Bytes(), &info)
	require.NoError(t, err)
	assert.Equal(t, "multi.txt", info.Name)
	assert.Equal(t, "file", info.Type)
}

func TestEnvdUploadHandler_MissingPath(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := FilesystemUploadRequest{
		Path:    "",
		Content: base64.StdEncoding.EncodeToString([]byte("data")),
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/filesystem/upload", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdUploadHandler(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestEnvdDownloadHandler(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// Create a file
	filePath := filepath.Join(tmpDir, "download.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("download me"), 0600))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/filesystem/download?path=download.txt", nil)

	server.EnvdDownloadHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "download me", w.Body.String())
}

func TestEnvdDownloadHandler_NotFound(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/filesystem/download?path=nonexistent.txt", nil)

	server.EnvdDownloadHandler(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestEnvdDownloadHandler_Directory(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/filesystem/download?path=subdir", nil)

	server.EnvdDownloadHandler(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestEnvdListHandler(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "dir1"), 0755))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/filesystem/list?path=.", nil)

	server.EnvdListHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp FilesystemListResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Len(t, resp.Entries, 2)

	names := make(map[string]string)
	for _, e := range resp.Entries {
		names[e.Name] = e.Type
	}
	assert.Equal(t, "file", names["a.txt"])
	assert.Equal(t, "directory", names["dir1"])
}

func TestEnvdListHandler_NotFound(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/filesystem/list?path=nonexistent", nil)

	server.EnvdListHandler(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestEnvdMkdirHandler(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := FilesystemMkdirRequest{
		Path:    "newdir",
		Parents: false,
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/filesystem/mkdir", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdMkdirHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var info EnvdFileInfo
	err := json.Unmarshal(w.Body.Bytes(), &info)
	require.NoError(t, err)
	assert.Equal(t, "newdir", info.Name)
	assert.Equal(t, "directory", info.Type)
}

func TestEnvdMkdirHandler_Parents(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := FilesystemMkdirRequest{
		Path:    "parent/child/grandchild",
		Parents: true,
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/filesystem/mkdir", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdMkdirHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	_, err := os.Stat(filepath.Join(tmpDir, "parent", "child", "grandchild"))
	require.NoError(t, err)
}

func TestEnvdMkdirHandler_AlreadyExists(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "existing"), 0755))

	req := FilesystemMkdirRequest{
		Path:    "existing",
		Parents: false,
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/filesystem/mkdir", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdMkdirHandler(c)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestEnvdMoveHandler(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "source.txt"), []byte("data"), 0600))

	req := FilesystemMoveRequest{
		SourcePath: "source.txt",
		TargetPath: "target.txt",
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/filesystem/move", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdMoveHandler(c)

	assert.Equal(t, http.StatusNoContent, w.Code)

	_, err := os.Stat(filepath.Join(tmpDir, "source.txt"))
	assert.True(t, os.IsNotExist(err))
	data, err := os.ReadFile(filepath.Join(tmpDir, "target.txt"))
	require.NoError(t, err)
	assert.Equal(t, "data", string(data))
}

func TestEnvdRemoveHandler(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "remove.txt"), []byte("data"), 0600))

	req := FilesystemRemoveRequest{
		Path: "remove.txt",
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/envd/filesystem/remove", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdRemoveHandler(c)

	assert.Equal(t, http.StatusNoContent, w.Code)

	_, err := os.Stat(filepath.Join(tmpDir, "remove.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestEnvdStatHandler(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "stat.txt"), []byte("stats"), 0600))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/filesystem/stat?path=stat.txt", nil)

	server.EnvdStatHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var info EnvdFileInfo
	err := json.Unmarshal(w.Body.Bytes(), &info)
	require.NoError(t, err)
	assert.Equal(t, "stat.txt", info.Name)
	assert.Equal(t, "file", info.Type)
	assert.Equal(t, int64(5), info.Size)
}

func TestEnvdStatHandler_NotFound(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/envd/filesystem/stat?path=missing.txt", nil)

	server.EnvdStatHandler(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestEnvdUploadHandler_PathTraversal(t *testing.T) {
	server, tmpDir := setupFilesystemTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	req := FilesystemUploadRequest{
		Path:    "../../../etc/passwd",
		Content: base64.StdEncoding.EncodeToString([]byte("bad")),
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/envd/filesystem/upload", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.EnvdUploadHandler(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "access denied")
}
