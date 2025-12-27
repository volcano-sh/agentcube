package picod

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to generate RSA key pair
func generateRSAKeys(t *testing.T) (*rsa.PrivateKey, string) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	publicKey := &privateKey.PublicKey

	pubASN1, err := x509.MarshalPKIXPublicKey(publicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubASN1,
	})

	return privateKey, string(pubPEM)
}

// Helper to create signed JWT
func createToken(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(t, err)
	return tokenString
}

func TestPicoD_EndToEnd(t *testing.T) {
	// 1. Setup Keys
	bootstrapPriv, bootstrapPubStr := generateRSAKeys(t)
	sessionPriv, sessionPubStr := generateRSAKeys(t)
	// 2. Setup Server Environment
	tmpDir, err := os.MkdirTemp("", "picod_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	// Switch to temp dir to avoid polluting source tree and ensure relative path tests work
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	config := Config{
		Port:         0, // Test server handles port
		BootstrapKey: []byte(bootstrapPubStr),
		Workspace:    tmpDir, // Set workspace to temp dir
	}

	server := NewServer(config)
	ts := httptest.NewServer(server.engine)
	defer ts.Close()

	client := ts.Client()

	t.Run("Health Check", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/health")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("Unauthenticated Access", func(t *testing.T) {
		// Execute without init
		execReq := ExecuteRequest{Command: []string{"echo", "hello"}}
		body, _ := json.Marshal(execReq)
		req, _ := http.NewRequest("POST", ts.URL+"/api/execute", bytes.NewBuffer(body))
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("Initialization", func(t *testing.T) {
		// Success Case
		sessionPubB64 := base64.RawStdEncoding.EncodeToString([]byte(sessionPubStr))
		initClaims := jwt.MapClaims{
			"session_public_key": sessionPubB64,
			"iat":                time.Now().Unix(),
			"exp":                time.Now().Add(time.Hour * 6).Unix(),
		}
		initToken := createToken(t, bootstrapPriv, initClaims)

		req, _ := http.NewRequest("POST", ts.URL+"/init", nil)
		req.Header.Set("Authorization", "Bearer "+initToken)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Re-initialization Attempt (Should Fail)
		resp, err = client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("Command Execution", func(t *testing.T) {
		// Helper to make authenticated execute requests
		doExec := func(cmd []string, env map[string]string, timeout string) ExecuteResponse {
			reqBody := ExecuteRequest{
				Command: cmd,
				Env:     env,
				Timeout: timeout,
			}
			bodyBytes, _ := json.Marshal(reqBody)
			hash := sha256.Sum256(bodyBytes)

			claims := jwt.MapClaims{
				"body_sha256": fmt.Sprintf("%x", hash),
				"iat":         time.Now().Unix(),
				"exp":         time.Now().Add(time.Hour * 6).Unix(),
			}
			token := createToken(t, sessionPriv, claims)

			req, _ := http.NewRequest("POST", ts.URL+"/api/execute", bytes.NewBuffer(bodyBytes))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			var execResp ExecuteResponse
			err = json.NewDecoder(resp.Body).Decode(&execResp)
			require.NoError(t, err)
			return execResp
		}

		// 1. Basic Execution
		resp := doExec([]string{"echo", "hello"}, nil, "")
		assert.Equal(t, "hello\n", resp.Stdout)
		assert.Equal(t, 0, resp.ExitCode)
		assert.False(t, resp.StartTime.IsZero())
		assert.False(t, resp.EndTime.IsZero())

		// 2. Environment Variables
		// Use sh -c to allow environment variable expansion by the shell
		resp = doExec([]string{"sh", "-c", "echo $TEST_VAR"}, map[string]string{"TEST_VAR": "picod_env"}, "")
		assert.Equal(t, "picod_env\n", resp.Stdout)

		// 3. Stderr and Exit Code
		// Use sh -c to allow redirection >&2 and exit command
		resp = doExec([]string{"sh", "-c", "echo error_msg >&2; exit 1"}, nil, "")
		assert.Equal(t, "error_msg\n", resp.Stderr)
		assert.Equal(t, 1, resp.ExitCode)

		// 4. Timeout
		// Use a command that sleeps longer than the timeout
		// Timeout is in seconds. Set timeout to 0.5s, sleep 2s.
		resp = doExec([]string{"sleep", "2"}, nil, "0.5s")
		assert.Equal(t, 124, resp.ExitCode) // Timeout exit code set in execute.go
		assert.Contains(t, resp.Stderr, "Command timed out")

		// 5. Working Directory Escape (Should Fail)
		// Attempt to set working directory outside workspace
		escapeReq := ExecuteRequest{
			Command:    []string{"ls"},
			WorkingDir: "../",
		}
		escapeBody, _ := json.Marshal(escapeReq)
		hash := sha256.Sum256(escapeBody)
		claims := jwt.MapClaims{
			"body_sha256": fmt.Sprintf("%x", hash),
			"iat":         time.Now().Unix(),
			"exp":         time.Now().Add(time.Hour * 6).Unix(),
		}
		token := createToken(t, sessionPriv, claims)

		req, _ := http.NewRequest("POST", ts.URL+"/api/execute", bytes.NewBuffer(escapeBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		httpResp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, httpResp.StatusCode)
	})

	t.Run("File Operations", func(t *testing.T) {
		// Helper to create auth headers
		getAuthHeaders := func(body []byte) http.Header {
			claims := jwt.MapClaims{
				"iat": time.Now().Unix(),
				"exp": time.Now().Add(time.Hour * 6).Unix(),
			}
			if len(body) > 0 {
				hash := sha256.Sum256(body)
				claims["body_sha256"] = fmt.Sprintf("%x", hash)
			}
			token := createToken(t, sessionPriv, claims)

			h := make(http.Header)
			h.Set("Authorization", "Bearer "+token)
			return h
		}

		// 1. JSON Upload
		content := "hello file"
		contentB64 := base64.StdEncoding.EncodeToString([]byte(content))
		uploadReq := UploadFileRequest{
			Path:    "test.txt",
			Content: contentB64,
			Mode:    "0644",
		}
		body, _ := json.Marshal(uploadReq)

		req, _ := http.NewRequest("POST", ts.URL+"/api/files", bytes.NewBuffer(body))
		req.Header = getAuthHeaders(body)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify on disk
		fileContent, err := os.ReadFile("test.txt")
		require.NoError(t, err)
		assert.Equal(t, content, string(fileContent))

		// 2. Download File
		req, _ = http.NewRequest("GET", ts.URL+"/api/files/test.txt", nil)
		req.Header = getAuthHeaders(nil)
		resp, err = client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		downloaded, _ := io.ReadAll(resp.Body)
		assert.Equal(t, content, string(downloaded))

		// 3. Download Directory (Should Fail)
		err = os.Mkdir("testdir", 0755)
		require.NoError(t, err)
		req, _ = http.NewRequest("GET", ts.URL+"/api/files/testdir", nil)
		req.Header = getAuthHeaders(nil)
		resp, err = client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		// 4. Multipart Upload
		bodyBuf := &bytes.Buffer{}
		writer := multipart.NewWriter(bodyBuf)
		part, _ := writer.CreateFormFile("file", "multipart.txt")
		_, err = part.Write([]byte("multipart content"))
		require.NoError(t, err)
		err = writer.WriteField("path", "multipart.txt")
		require.NoError(t, err)
		writer.Close()

		// Note: Multipart requests don't need body_sha256 in claims as per AuthMiddleware logic
		claims := jwt.MapClaims{
			"iat": time.Now().Unix(),
			"exp": time.Now().Add(time.Hour * 6).Unix(),
		}
		token := createToken(t, sessionPriv, claims)

		req, _ = http.NewRequest("POST", ts.URL+"/api/files", bodyBuf)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		resp, err = client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify multipart file
		fileContent, err = os.ReadFile("multipart.txt")
		require.NoError(t, err)
		assert.Equal(t, "multipart content", string(fileContent))

		// 5. List Files
		req, _ = http.NewRequest("GET", ts.URL+"/api/files?path=.", nil)
		req.Header = getAuthHeaders(nil)
		resp, err = client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var listResp ListFilesResponse
		err = json.NewDecoder(resp.Body).Decode(&listResp)
		require.NoError(t, err)
		// We expect at least test.txt and multipart.txt
		assert.GreaterOrEqual(t, len(listResp.Files), 2)
		found := false
		for _, f := range listResp.Files {
			if f.Name == "test.txt" {
				found = true
				assert.Equal(t, int64(10), f.Size) // "hello file" length is 10
				break
			}
		}
		assert.True(t, found, "test.txt should be found in listing")

		// 6. Jail Escape Attempt (Should Fail)
		// Attempt to access /etc/passwd or relative ../../../
		escapeReq := UploadFileRequest{
			Path:    "../outside.txt",
			Content: contentB64,
		}
		escapeBody, _ := json.Marshal(escapeReq)
		req, _ = http.NewRequest("POST", ts.URL+"/api/files", bytes.NewBuffer(escapeBody))
		req.Header = getAuthHeaders(escapeBody)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		require.NoError(t, err)
		// Expect Bad Request or similar because sanitizePath should block it
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		// Attempt absolute path outside workspace
		absEscapeReq := UploadFileRequest{
			Path:    "/etc/passwd",
			Content: contentB64,
		}
		absEscapeBody, _ := json.Marshal(absEscapeReq)
		req, _ = http.NewRequest("POST", ts.URL+"/api/files", bytes.NewBuffer(absEscapeBody))
		req.Header = getAuthHeaders(absEscapeBody)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		require.NoError(t, err)
		// Since we trim leading /, "/etc/passwd" becomes "{workspace}/etc/passwd", which IS allowed.
		// This is the correct behavior: treating absolute paths as relative to workspace root.
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify it was written inside workspace, not actual /etc/passwd
		_, err = os.Stat(filepath.Join(tmpDir, "etc", "passwd"))
		assert.NoError(t, err, "File should be created inside workspace")
		// Actual /etc/passwd should be untouched (obviously we can't check that easily without root, but logic holds)
	})

	t.Run("Security Checks", func(t *testing.T) {
		// 1. Invalid Token Signature
		// Sign with bootstrap key instead of session key for execution
		claims := jwt.MapClaims{
			"iat": time.Now().Unix(),
		}
		// Wrong key for this endpoint/phase (middleware uses session key after init)
		// If we sign with `bootstrapPriv`, verification against `sessionPub` should fail.
		token := createToken(t, bootstrapPriv, claims) // bootstrapPriv corresponds to bootstrapPub, not sessionPub

		reqBody := ExecuteRequest{Command: []string{"echo", "malicious"}}
		realBody, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", ts.URL+"/api/execute", bytes.NewBuffer(realBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

func TestPicoD_DefaultWorkspace(t *testing.T) {
	// Setup temporary directory for test
	tmpDir, err := os.MkdirTemp("", "picod_default_workspace_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Switch to temp dir
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	// Initialize server with empty workspace
	_, bootstrapPubStr := generateRSAKeys(t)
	config := Config{
		Port:         0,
		BootstrapKey: []byte(bootstrapPubStr),
		Workspace:    "", // Empty workspace to trigger default behavior
	}

	server := NewServer(config)

	// Verify workspaceDir is set to current working directory
	cwd, err := os.Getwd()
	require.NoError(t, err)

	// Use filepath.Abs to ensure consistent comparison
	absCwd, err := filepath.Abs(cwd)
	require.NoError(t, err)

	assert.Equal(t, absCwd, server.workspaceDir)
}

func TestPicoD_SetWorkspace(t *testing.T) {
	// Setup temp dir
	tmpDir, err := os.MkdirTemp("", "picod_setworkspace_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a real directory
	realDir := filepath.Join(tmpDir, "real")
	err = os.Mkdir(realDir, 0755)
	require.NoError(t, err)

	// Create a symlink
	linkDir := filepath.Join(tmpDir, "link")
	err = os.Symlink(realDir, linkDir)
	require.NoError(t, err)

	server := &Server{}

	// Helper to resolve path for comparison (handles /var vs /private/var on macOS)
	resolve := func(p string) string {
		path, err := filepath.EvalSymlinks(p)
		if err != nil {
			return p
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return path
		}
		return path
	}

	// Case 1: Absolute Path
	absPath, err := filepath.Abs(realDir)
	require.NoError(t, err)
	server.setWorkspace(realDir)
	assert.Equal(t, resolve(absPath), resolve(server.workspaceDir))

	// Case 2: Relative Path
	// Change to tmpDir so we can use relative path "real"
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	server.setWorkspace("real")
	assert.Equal(t, resolve(absPath), resolve(server.workspaceDir))

	// Case 3: Symlink
	// setWorkspace uses filepath.Abs, so it preserves the symlink path structure (doesn't resolve it).
	// We verify that it matches the absolute path of the symlink itself.
	absLinkPath, err := filepath.Abs(linkDir)
	require.NoError(t, err)
	server.setWorkspace(linkDir)
	// Here we compare the paths directly because we expect the symlink path to be stored as is (absolute),
	// but we still resolve /var aliasing for the parent directory part.
	assert.Equal(t, resolve(absLinkPath), resolve(server.workspaceDir))
}

// TestParseFileMode tests filesystem utility for file mode parsing
func TestParseFileMode(t *testing.T) {
	tests := []struct {
		name     string
		modeStr  string
		expected os.FileMode
		desc     string
	}{
		{
			name:     "Valid octal mode",
			modeStr:  "0644",
			expected: 0644,
			desc:     "Should parse valid octal mode",
		},
		{
			name:     "Empty mode defaults to 0644",
			modeStr:  "",
			expected: 0644,
			desc:     "Should default to 0644",
		},
		{
			name:     "Invalid mode defaults to 0644",
			modeStr:  "invalid",
			expected: 0644,
			desc:     "Should default on invalid input",
		},
		{
			name:     "Mode exceeding max defaults to 0644",
			modeStr:  "10000",
			expected: 0644,
			desc:     "Should default when exceeding 0777",
		},
		{
			name:     "Valid executable mode",
			modeStr:  "0755",
			expected: 0755,
			desc:     "Should parse executable mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseFileMode(tt.modeStr)
			assert.Equal(t, tt.expected, result, tt.desc)
		})
	}
}

// TestLoadBootstrapKey tests auth helper error paths
func TestLoadBootstrapKey(t *testing.T) {
	tests := []struct {
		name      string
		keyData   []byte
		expectErr bool
		desc      string
	}{
		{
			name:      "Empty key data",
			keyData:   []byte{},
			expectErr: true,
			desc:      "Should reject empty key",
		},
		{
			name:      "Invalid PEM format",
			keyData:   []byte("not a pem"),
			expectErr: true,
			desc:      "Should reject invalid PEM",
		},
		{
			name:      "Valid RSA public key",
			keyData:   func() []byte { _, pub := generateRSAKeys(t); return []byte(pub) }(),
			expectErr: false,
			desc:      "Should accept valid RSA key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am := NewAuthManager()
			err := am.LoadBootstrapKey(tt.keyData)
			if tt.expectErr {
				assert.Error(t, err, tt.desc)
			} else {
				assert.NoError(t, err, tt.desc)
			}
		})
	}
}

// TestExecuteHandler_ErrorPaths tests execution pipeline error paths and normal command execution
func TestExecuteHandler_ErrorPaths(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod_execute_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	_, bootstrapPubStr := generateRSAKeys(t)
	server := NewServer(Config{
		BootstrapKey: []byte(bootstrapPubStr),
		Workspace:    tmpDir,
	})

	tests := []struct {
		name       string
		request    string
		statusCode int
		desc       string
		validate   func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:       "Empty command",
			request:    `{"command": []}`,
			statusCode: http.StatusBadRequest,
			desc:       "Should reject empty command",
		},
		{
			name:       "Invalid JSON",
			request:    `{"command": invalid}`,
			statusCode: http.StatusBadRequest,
			desc:       "Should reject invalid JSON",
		},
		{
			name:       "Invalid timeout format",
			request:    `{"command": ["echo", "test"], "timeout": "invalid"}`,
			statusCode: http.StatusBadRequest,
			desc:       "Should reject invalid timeout",
		},
		{
			name:       "Normal command execution",
			request:    `{"command": ["echo", "hello"]}`,
			statusCode: http.StatusOK,
			desc:       "Should execute normal command successfully",
			validate: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp ExecuteResponse
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, "hello\n", resp.Stdout)
				assert.Equal(t, 0, resp.ExitCode)
				assert.False(t, resp.StartTime.IsZero())
				assert.False(t, resp.EndTime.IsZero())
				assert.Greater(t, resp.Duration, 0.0)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/execute", bytes.NewBufferString(tt.request))
			req.Header.Set("Content-Type", "application/json")

			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = req

			server.ExecuteHandler(ctx)

			assert.Equal(t, tt.statusCode, w.Code, tt.desc)
			if tt.validate != nil {
				tt.validate(t, w)
			}
		})
	}
}

// TestLoadPublicKey tests loading public key from file
func TestLoadPublicKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod_load_public_key_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name        string
		setup       func(*testing.T, string) *AuthManager
		expectErr   bool
		errContains string
		validate    func(*testing.T, *AuthManager)
	}{
		{
			name: "FileNotExists",
			setup: func(t *testing.T, tmpDir string) *AuthManager {
				am := NewAuthManager()
				am.keyFile = filepath.Join(tmpDir, "test_key.pem")
				return am
			},
			expectErr:   true,
			errContains: "no public key file found",
		},
		{
			name: "InvalidPEM",
			setup: func(t *testing.T, tmpDir string) *AuthManager {
				am := NewAuthManager()
				am.keyFile = filepath.Join(tmpDir, "test_key.pem")
				err := os.WriteFile(am.keyFile, []byte("not a pem"), 0644)
				require.NoError(t, err)
				return am
			},
			expectErr:   true,
			errContains: "failed to decode PEM block",
		},
		{
			name: "ValidKey",
			setup: func(t *testing.T, tmpDir string) *AuthManager {
				am := NewAuthManager()
				am.keyFile = filepath.Join(tmpDir, "test_key.pem")
				_, pubStr := generateRSAKeys(t)
				err := os.WriteFile(am.keyFile, []byte(pubStr), 0644)
				require.NoError(t, err)
				return am
			},
			expectErr: false,
			validate: func(t *testing.T, am *AuthManager) {
				assert.True(t, am.IsInitialized())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am := tt.setup(t, tmpDir)
			err := am.LoadPublicKey()
			if tt.expectErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, am)
				}
			}
		})
	}
}

// TestInitHandler_ErrorPaths tests InitHandler error paths
func TestInitHandler_ErrorPaths(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod_init_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	bootstrapPriv, bootstrapPubStr := generateRSAKeys(t)

	tests := []struct {
		name       string
		setup      func(*testing.T) (*AuthManager, *http.Request)
		statusCode int
		desc       string
	}{
		{
			name: "MissingAuthHeader",
			setup: func(t *testing.T) (*AuthManager, *http.Request) {
				am := NewAuthManager()
				am.keyFile = filepath.Join(tmpDir, "picod_public_key.pem")
				err := am.LoadBootstrapKey([]byte(bootstrapPubStr))
				require.NoError(t, err)
				req := httptest.NewRequest("POST", "/init", nil)
				return am, req
			},
			statusCode: http.StatusUnauthorized,
			desc:       "Should reject missing authorization header",
		},
		{
			name: "InvalidAuthHeaderFormat",
			setup: func(t *testing.T) (*AuthManager, *http.Request) {
				am := NewAuthManager()
				am.keyFile = filepath.Join(tmpDir, "picod_public_key.pem")
				err := am.LoadBootstrapKey([]byte(bootstrapPubStr))
				require.NoError(t, err)
				req := httptest.NewRequest("POST", "/init", nil)
				req.Header.Set("Authorization", "InvalidFormat token")
				return am, req
			},
			statusCode: http.StatusUnauthorized,
			desc:       "Should reject invalid authorization header format",
		},
		{
			name: "InvalidToken",
			setup: func(t *testing.T) (*AuthManager, *http.Request) {
				am := NewAuthManager()
				am.keyFile = filepath.Join(tmpDir, "picod_public_key.pem")
				err := am.LoadBootstrapKey([]byte(bootstrapPubStr))
				require.NoError(t, err)
				req := httptest.NewRequest("POST", "/init", nil)
				req.Header.Set("Authorization", "Bearer invalid-token")
				return am, req
			},
			statusCode: http.StatusUnauthorized,
			desc:       "Should reject invalid token",
		},
		{
			name: "MissingSessionPublicKey",
			setup: func(t *testing.T) (*AuthManager, *http.Request) {
				am := NewAuthManager()
				am.keyFile = filepath.Join(tmpDir, "picod_public_key.pem")
				err := am.LoadBootstrapKey([]byte(bootstrapPubStr))
				require.NoError(t, err)
				claims := jwt.MapClaims{
					"iat": time.Now().Unix(),
					"exp": time.Now().Add(time.Hour).Unix(),
				}
				token := createToken(t, bootstrapPriv, claims)
				req := httptest.NewRequest("POST", "/init", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				return am, req
			},
			statusCode: http.StatusBadRequest,
			desc:       "Should reject missing session public key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am, req := tt.setup(t)
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = req

			am.InitHandler(ctx)
			assert.Equal(t, tt.statusCode, w.Code, tt.desc)
		})
	}
}

// TestAuthMiddleware_ErrorPaths tests AuthMiddleware error paths
func TestAuthMiddleware_ErrorPaths(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*testing.T) (*AuthManager, *http.Request)
		statusCode int
		desc       string
	}{
		{
			name: "MissingAuthHeader",
			setup: func(t *testing.T) (*AuthManager, *http.Request) {
				tmpDir, err := os.MkdirTemp("", "picod_auth_test")
				require.NoError(t, err)
				t.Cleanup(func() { os.RemoveAll(tmpDir) })

				am := NewAuthManager()
				_, pubStr := generateRSAKeys(t)
				am.keyFile = filepath.Join(tmpDir, "key.pem")
				err = os.WriteFile(am.keyFile, []byte(pubStr), 0644)
				require.NoError(t, err)
				err = am.LoadPublicKey()
				require.NoError(t, err)

				req := httptest.NewRequest("POST", "/api/execute", nil)
				return am, req
			},
			statusCode: http.StatusUnauthorized,
			desc:       "Should reject missing authorization header",
		},
		{
			name: "InvalidAuthHeaderFormat",
			setup: func(t *testing.T) (*AuthManager, *http.Request) {
				tmpDir, err := os.MkdirTemp("", "picod_auth_test")
				require.NoError(t, err)
				t.Cleanup(func() { os.RemoveAll(tmpDir) })

				am := NewAuthManager()
				_, pubStr := generateRSAKeys(t)
				am.keyFile = filepath.Join(tmpDir, "key.pem")
				err = os.WriteFile(am.keyFile, []byte(pubStr), 0644)
				require.NoError(t, err)
				err = am.LoadPublicKey()
				require.NoError(t, err)

				req := httptest.NewRequest("POST", "/api/execute", nil)
				req.Header.Set("Authorization", "InvalidFormat token")
				return am, req
			},
			statusCode: http.StatusUnauthorized,
			desc:       "Should reject invalid authorization header format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am, req := tt.setup(t)
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = req

			handler := am.AuthMiddleware()
			handler(ctx)

			assert.True(t, ctx.IsAborted())
			assert.Equal(t, tt.statusCode, w.Code, tt.desc)
		})
	}
}

// TestDownloadFileHandler_ErrorPaths tests DownloadFileHandler error paths
func TestDownloadFileHandler_ErrorPaths(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod_download_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	_, bootstrapPubStr := generateRSAKeys(t)
	server := NewServer(Config{
		BootstrapKey: []byte(bootstrapPubStr),
		Workspace:    tmpDir,
	})

	tests := []struct {
		name       string
		path       string
		statusCode int
		desc       string
	}{
		{
			name:       "FileNotFound",
			path:       "/nonexistent.txt",
			statusCode: http.StatusNotFound,
			desc:       "Should return not found for non-existent file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/files"+tt.path, nil)
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = req
			ctx.Params = gin.Params{gin.Param{Key: "path", Value: tt.path}}

			server.DownloadFileHandler(ctx)
			assert.Equal(t, tt.statusCode, w.Code, tt.desc)
		})
	}
}

// TestListFilesHandler_ErrorPaths tests ListFilesHandler error paths
func TestListFilesHandler_ErrorPaths(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "picod_list_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	_, bootstrapPubStr := generateRSAKeys(t)
	server := NewServer(Config{
		BootstrapKey: []byte(bootstrapPubStr),
		Workspace:    tmpDir,
	})

	tests := []struct {
		name       string
		path       string
		statusCode int
		desc       string
	}{
		{
			name:       "MissingPathParameter",
			path:       "",
			statusCode: http.StatusBadRequest,
			desc:       "Should reject missing path parameter",
		},
		{
			name:       "DirectoryNotFound",
			path:       "nonexistent",
			statusCode: http.StatusNotFound,
			desc:       "Should return not found for non-existent directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			url := "/api/files"
			if tt.path != "" {
				url += "?path=" + tt.path
			}
			req := httptest.NewRequest("GET", url, nil)
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = req

			server.ListFilesHandler(ctx)
			assert.Equal(t, tt.statusCode, w.Code, tt.desc)
		})
	}
}
