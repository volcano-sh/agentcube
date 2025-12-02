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
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to generate RSA key pair
func generateRSAKeys() (*rsa.PrivateKey, *rsa.PublicKey, string) {
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	publicKey := &privateKey.PublicKey

	pubASN1, _ := x509.MarshalPKIXPublicKey(publicKey)
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: pubASN1,
	})

	return privateKey, publicKey, string(pubPEM)
}

// Helper to create signed JWT
func createToken(key *rsa.PrivateKey, claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, _ := token.SignedString(key)
	return tokenString
}

func TestPicoD_EndToEnd(t *testing.T) {
	// 1. Setup Keys
	bootstrapPriv, _, bootstrapPubStr := generateRSAKeys()
	sessionPriv, _, sessionPubStr := generateRSAKeys()

	// 2. Setup Server Environment
	tmpDir, err := os.MkdirTemp("", "picod_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Switch to temp dir to avoid polluting source tree and ensure relative path tests work
	originalWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(originalWd)

	config := Config{
		Port:         0, // Test server handles port
		BootstrapKey: bootstrapPubStr,
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
		execReq := ExecuteRequest{Command: "echo hello"}
		body, _ := json.Marshal(execReq)
		req, _ := http.NewRequest("POST", ts.URL+"/api/execute", bytes.NewBuffer(body))
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("Initialization", func(t *testing.T) {
		// Success Case
		initClaims := jwt.MapClaims{
			"session_public_key": sessionPubStr,
			"iat":                time.Now().Unix(),
			"exp":                time.Now().Add(time.Minute).Unix(),
		}
		initToken := createToken(bootstrapPriv, initClaims)

		req, _ := http.NewRequest("POST", ts.URL+"/api/init", nil)
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
		doExec := func(cmd string, env map[string]string, timeout float64) ExecuteResponse {
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
				"exp":         time.Now().Add(time.Minute).Unix(),
			}
			token := createToken(sessionPriv, claims)

			req, _ := http.NewRequest("POST", ts.URL+"/api/execute", bytes.NewBuffer(bodyBytes))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			var execResp ExecuteResponse
			json.NewDecoder(resp.Body).Decode(&execResp)
			return execResp
		}

		// 1. Basic Execution
		resp := doExec("echo hello", nil, 0)
		assert.Equal(t, "hello\n", resp.Stdout)
		assert.Equal(t, 0, resp.ExitCode)

		// 2. Environment Variables
		resp = doExec("echo $TEST_VAR", map[string]string{"TEST_VAR": "picod_env"}, 0)
		assert.Equal(t, "picod_env\n", resp.Stdout)

		// 3. Stderr and Exit Code
		resp = doExec("echo error_msg >&2; exit 1", nil, 0)
		assert.Equal(t, "error_msg\n", resp.Stderr)
		assert.Equal(t, 1, resp.ExitCode)

		// 4. Timeout
		// Use a command that sleeps longer than the timeout
		// Timeout is in seconds. Set timeout to 0.5s, sleep 2s.
		resp = doExec("sleep 2", nil, 0.5)
		assert.Equal(t, 124, resp.ExitCode) // Timeout exit code set in execute.go
		assert.Contains(t, resp.Stderr, "Command timed out")
	})

	t.Run("File Operations", func(t *testing.T) {
		// Helper to create auth headers
		getAuthHeaders := func(body []byte) http.Header {
			claims := jwt.MapClaims{
				"iat": time.Now().Unix(),
				"exp": time.Now().Add(time.Minute).Unix(),
			}
			if len(body) > 0 {
				hash := sha256.Sum256(body)
				claims["body_sha256"] = fmt.Sprintf("%x", hash)
			}
			token := createToken(sessionPriv, claims)

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
		part.Write([]byte("multipart content"))
		writer.WriteField("path", "multipart.txt")
		writer.Close()

		// Note: Multipart requests don't need body_sha256 in claims as per AuthMiddleware logic
		claims := jwt.MapClaims{
			"iat": time.Now().Unix(),
			"exp": time.Now().Add(time.Minute).Unix(),
		}
		token := createToken(sessionPriv, claims)

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
	})

	t.Run("Security Checks", func(t *testing.T) {
		// 1. Body Integrity Check Failure
		// Sign a hash for "A", but send "B"
		reqBody := ExecuteRequest{Command: "echo malicious"}
		realBody, _ := json.Marshal(reqBody)

		fakeBody := []byte("some other content")
		hash := sha256.Sum256(fakeBody)

		claims := jwt.MapClaims{
			"body_sha256": fmt.Sprintf("%x", hash),
			"iat":         time.Now().Unix(),
			"exp":         time.Now().Add(time.Minute).Unix(),
		}
		token := createToken(sessionPriv, claims)

		req, _ := http.NewRequest("POST", ts.URL+"/api/execute", bytes.NewBuffer(realBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		// 2. Invalid Token Signature
		// Sign with bootstrap key instead of session key for execution
		claims = jwt.MapClaims{
			"body_sha256": fmt.Sprintf("%x", sha256.Sum256(realBody)),
			"iat":         time.Now().Unix(),
		}
		// Wrong key for this endpoint/phase (middleware uses session key after init)
		// Wait, AuthMiddleware uses `am.publicKey` which is the session key.
		// If we sign with `bootstrapPriv`, verification against `sessionPub` should fail.
		token = createToken(bootstrapPriv, claims) // bootstrapPriv corresponds to bootstrapPub, not sessionPub

		req, _ = http.NewRequest("POST", ts.URL+"/api/execute", bytes.NewBuffer(realBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}
