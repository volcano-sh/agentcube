package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultPicoDURL = "http://localhost:8080"
	sessionKeyFile  = "picod_session.key"
)

// Configuration flags
var (
	picoDURL     string
	bootstrapKey string
	generateKeys bool
	useAuth      bool
)

// API Structures matching pkg/picod
type ExecuteRequest struct {
	Command    string            `json:"command"`
	Timeout    float64           `json:"timeout,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

type ExecuteResponse struct {
	Stdout    string  `json:"stdout"`
	Stderr    string  `json:"stderr"`
	ExitCode  int     `json:"exit_code"`
	Duration  float64 `json:"duration"`
	ProcessID int     `json:"process_id"`
}

type ListFilesRequest struct {
	Path string `json:"path"`
}

type FileEntry struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Mode     string    `json:"mode"`
	IsDir    bool      `json:"is_dir"`
}

type ListFilesResponse struct {
	Files []FileEntry `json:"files"`
}

type InitRequest struct {
	// Init endpoint uses JWT claims, body is empty
}

// RSA Key Management
type RSAKeyPair struct {
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
}

func main() {
	parseFlags()

	log.Println("===========================================")
	log.Println("      PicoD Client Example (JWT)           ")
	log.Println("===========================================")

	if generateKeys {
		runGenerateKeys()
		return
	}

	// Load Session Key (Client Identity)
	sessionKey, err := loadOrGenerateSessionKey()
	if err != nil {
		log.Fatalf("Failed to setup session key: %v", err)
	}

	// Perform Initialization if Bootstrap Key is provided
	if bootstrapKey != "" {
		log.Println("🔄 Phase 1: Initialization (Handshake)")
		if err := performHandshake(sessionKey); err != nil {
			log.Fatalf("Handshake failed: %v", err)
		}
		log.Println("✅ Handshake successful! Session established.")
		log.Println()
	} else {
		log.Println("⚠️  Skipping Initialization (No bootstrap key provided).")
		log.Println("   Assuming session is already established.")
		log.Println()
	}

	// Execute Commands
	log.Println("🚀 Phase 2: Command Execution")
	runCommandTests(sessionKey)

	// File Operations
	log.Println("📂 Phase 3: File Operations")
	runFileTests(sessionKey)

	log.Println("===========================================")
	log.Println("🎉 All demonstrations completed!")
}

func parseFlags() {
	flag.StringVar(&picoDURL, "url", defaultPicoDURL, "PicoD Server URL")
	flag.StringVar(&bootstrapKey, "bootstrap-key", "", "Path to Bootstrap PRIVATE Key (for init)")
	flag.BoolVar(&generateKeys, "gen-keys", false, "Generate a pair of Bootstrap Keys and exit")
	flag.Parse()
}

// ---

// Key Management

func runGenerateKeys() {
	log.Println("Generating RSA-2048 Bootstrap Key Pair...")
	priv, pub, err := generateRSAKeyPair()
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	saveKey("bootstrap_private.pem", priv, "RSA PRIVATE KEY")
	saveKey("bootstrap_public.pem", pub, "RSA PUBLIC KEY") // Standard PEM format for public key

	// Convert public key to the format PicoD config likely expects (often raw string in JSON or similar)
	// But here we just save standard PEM.

	log.Println("✅ Keys generated:")
	log.Println("   - bootstrap_private.pem (Use with -bootstrap-key to init session)")
	log.Println("   - bootstrap_public.pem  (Configure PicoD server with this contents)")
	log.Println("\nTo start PicoD with this key:")
	log.Println("   export PICOD_BOOTSTRAP_KEY=\"$(cat bootstrap_public.pem)\" ")
	log.Println("   ./bin/picod")
}

func generateRSAKeyPair() ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	privBytes := x509.MarshalPKCS1PrivateKey(key)
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes})

	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: pubBytes})

	return privPEM, pubPEM, nil
}

func saveKey(filename string, data []byte, typeHeader string) {
	if err := os.WriteFile(filename, data, 0600); err != nil {
		log.Fatalf("Failed to write %s: %v", filename, err)
	}
}

func loadOrGenerateSessionKey() (*rsa.PrivateKey, error) {
	// Always generate a fresh session key for this run in memory
	// In a real persistent client, you might load this from disk.
	// But for this example, fresh is fine as long as we Init.
	if _, err := os.Stat(sessionKeyFile); err == nil {
		log.Printf("Loading existing session key from %s...", sessionKeyFile)
		data, err := os.ReadFile(sessionKeyFile)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(data)
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	}

	log.Println("Generating new ephemeral session key...")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	// Save it just in case we want to reuse (though this logic is simple)
	privBytes := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes})
	_ = os.WriteFile(sessionKeyFile, pemBytes, 0600)

	return key, nil
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// ---

// JWT & Auth

func createToken(key *rsa.PrivateKey, claims jwt.MapClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

func getSessionPublicKeyPEM(key *rsa.PrivateKey) string {
	pubBytes, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: pubBytes}))
}

// ---

// Operations

func performHandshake(sessionKey *rsa.PrivateKey) error {
	bootstrapPriv, err := loadPrivateKey(bootstrapKey)
	if err != nil {
		return fmt.Errorf("failed to load bootstrap key: %w", err)
	}

	sessionPubPEM := getSessionPublicKeyPEM(sessionKey)

	// Create JWT signed by Bootstrap Key
	claims := jwt.MapClaims{
		"iss":                "example-client",
		"iat":                time.Now().Unix(),
		"exp":                time.Now().Add(1 * time.Minute).Unix(),
		"session_public_key": sessionPubPEM,
	}

	token, err := createToken(bootstrapPriv, claims)
	if err != nil {
		return fmt.Errorf("signing failed: %w", err)
	}

	// Send Request
	req, _ := http.NewRequest("POST", picoDURL+"/init", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("init failed (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func authenticatedRequest(method, endpoint string, payload interface{}, sessionKey *rsa.PrivateKey) (*http.Response, error) {
	var bodyBytes []byte
	var contentType string

	// Prepare Body and Content-Type
	if payload != nil {
		if _, ok := payload.(*multipart.Writer); ok {
			// Multipart (Special case, we can't easily hash the body for signature in this simple example
			// without buffering. The Server auth middleware might skip body hash for multipart?
			// Checking pkg/picod/auth.go: Yes, it validates hash ONLY if "body_sha256" claim exists.
			// For multipart, we usually omit the hash claim or hash the buffered body.
			// Let's assume for this example we just buffer it (not ideal for huge files but fine for example).
			// Wait, the helper above takes *multipart.Writer, but we need the buffer.
			return nil, fmt.Errorf("multipart not fully implemented in helper")
		} else {
			// JSON
			bodyBytes, _ = json.Marshal(payload)
			contentType = "application/json"
		}
	}

	// Create JWT Claims
	claims := jwt.MapClaims{
		"iss": "example-client",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(1 * time.Minute).Unix(),
	}

	// Add Body Hash if body exists
	if len(bodyBytes) > 0 {
		hash := sha256.Sum256(bodyBytes)
		claims["body_sha256"] = fmt.Sprintf("%x", hash)
	}

	token, err := createToken(sessionKey, claims)
	if err != nil {
		return nil, err
	}

	// Create Request
	url := picoDURL + endpoint
	var req *http.Request
	if len(bodyBytes) > 0 {
		req, _ = http.NewRequest(method, url, bytes.NewBuffer(bodyBytes))
	} else {
		req, _ = http.NewRequest(method, url, nil)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return http.DefaultClient.Do(req)
}

func runCommandTests(sessionKey *rsa.PrivateKey) {
	cmds := []string{
		"echo 'Hello JWT World'",
		"date",
		"ps aux",
	}

	for _, cmd := range cmds {
		log.Printf("Exec: %s", cmd)
		req := ExecuteRequest{Command: cmd, Timeout: 5}

		resp, err := authenticatedRequest("POST", "/api/execute", req, sessionKey)
		if err != nil {
			log.Printf("❌ Request failed: %v", err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			log.Printf("❌ Server Error (%d): %s", resp.StatusCode, string(body))
			continue
		}

		var result ExecuteResponse
		json.NewDecoder(resp.Body).Decode(&result)
		log.Printf("   STDOUT: %s", strings.TrimSpace(result.Stdout))
		log.Printf("   PID: %d, Duration: %.4fs", result.ProcessID, result.Duration)
	}
}

func runFileTests(sessionKey *rsa.PrivateKey) {
	// 1. Upload (JSON Base64)
	fileName := "example_test.txt"
	content := "This file was uploaded by the JWT example client."
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	uploadReq := map[string]string{
		"path":    fileName,
		"content": encoded,
		"mode":    "0644",
	}

	log.Printf("Uploading %s...", fileName)
	resp, err := authenticatedRequest("POST", "/api/files", uploadReq, sessionKey)
	if err != nil || resp.StatusCode != 200 {
		log.Printf("❌ Upload failed")
	} else {
		log.Printf("✅ Upload success")
	}
	if resp != nil {
		resp.Body.Close()
	}

	// 2. List Files
	log.Println("Listing files...")
	listReq := ListFilesRequest{Path: "."}
	resp, err = authenticatedRequest("POST", "/api/files/list", listReq, sessionKey)
	if err != nil {
		log.Printf("❌ List failed: %v", err)
	} else {
		defer resp.Body.Close()
		var listResp ListFilesResponse
		if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
			log.Printf("❌ Decode list failed: %v", err)
		} else {
			log.Printf("✅ Found %d files:", len(listResp.Files))
			for _, f := range listResp.Files {
				log.Printf("   - %s (%d bytes) %s", f.Name, f.Size, f.Mode)
			}
		}
	}

	// 3. Download
	log.Printf("Downloading %s...", fileName)
	resp, err = authenticatedRequest("GET", "/api/files/"+fileName, nil, sessionKey)
	if err != nil {
		log.Printf("❌ Download failed: %v", err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			data, _ := io.ReadAll(resp.Body)
			log.Printf("✅ Downloaded content: %s", string(data))
		} else {
			log.Printf("❌ Download failed with status %d", resp.StatusCode)
		}
	}
}
