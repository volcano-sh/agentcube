package main

import (
	"bytes"
	"crypto"
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
)

const (
	defaultPicoDURL = "http://localhost:9527"
	keyFile         = "picod_client_keys.pem"
)

// ExecuteRequest command execution request
type ExecuteRequest struct {
	Command    string            `json:"command"`
	Timeout    float64           `json:"timeout,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

// ExecuteResponse command execution response
type ExecuteResponse struct {
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
	ExitCode int     `json:"exit_code"`
	Duration float64 `json:"duration"`
}

// FileInfo file information response
type FileInfo struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified"`
}

// RSAKeyPair contains RSA public and private keys
type RSAKeyPair struct {
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
}

// InitRequest initialization request
type InitRequest struct {
	PublicKey string `json:"public_key"`
}

// InitResponse initialization response
type InitResponse struct {
	Message string `json:"message"`
	Success bool   `json:"success"`
}

func main() {
	// Parse command line flags
	useAuth := flag.Bool("auth", false, "Use RSA authentication mode")
	flag.Parse()

	log.Println("===========================================")
	if *useAuth {
		log.Println("PicoD Authenticated Client Test")
	} else {
		log.Println("PicoD REST API Direct Test")
	}
	log.Println("===========================================")
	log.Println()

	picodURL := getEnv("PICOD_URL", defaultPicoDURL)

	log.Printf("Configuration:")
	log.Printf("  PicoD URL: %s", picodURL)
	log.Printf("  Auth Mode: %v", *useAuth)

	if picodURL == defaultPicoDURL {
		log.Println("  ℹ️  To use a different PicoD server:")
		log.Println("      export PICOD_URL=http://localhost:9529")
	}
	log.Println()

	var keyPair *RSAKeyPair
	var err error

	// Step 0: If auth mode, load/generate RSA key pair
	if *useAuth {
		log.Println("Step 0: Loading/Generating RSA key pair...")
		keyPair, err = loadOrGenerateKeyPair()
		if err != nil {
			log.Fatalf("Failed to load/generate key pair: %v", err)
		}
		log.Println("✅ RSA key pair ready")
		log.Println()
	}

	// Health check
	var stepNum int
	if *useAuth {
		stepNum = 1
	} else {
		stepNum = 0
	}
	log.Printf("Step %d: Health check...", stepNum)
	if err := healthCheck(picodURL); err != nil {
		log.Fatalf("Health check failed: %v", err)
	}
	log.Println("✅ PicoD server is healthy")
	log.Println()

	// If auth mode, initialize server first
	if *useAuth {
		stepNum++
		log.Printf("Step %d: Initializing Picod server with public key...", stepNum)
		publicKeyPEM, err := exportPublicKey(keyPair.PublicKey)
		if err != nil {
			log.Fatalf("Failed to export public key: %v", err)
		}

		if err := initializeServer(picodURL, publicKeyPEM); err != nil {
			log.Fatalf("Failed to initialize server: %v", err)
		}
		log.Println("✅ Server initialized successfully")
		log.Println()
	}

	// Execute basic commands
	stepNum++
	log.Printf("Step %d: Executing basic test commands...", stepNum)
	commands := []string{
		"whoami",
		"pwd",
		"echo 'Hello from PicoD REST API!'",
		"python3 --version",
		"uname -a",
	}

	for i, cmd := range commands {
		log.Printf("   [%d/%d] Executing: %s", i+1, len(commands), cmd)
		var output string
		if *useAuth {
			output, err = executeAuthenticatedCommand(picodURL, keyPair, cmd)
		} else {
			output, err = executeCommand(picodURL, cmd)
		}
		if err != nil {
			log.Printf("      ⚠️  Command failed: %v", err)
			continue
		}
		log.Printf("      Output: %s", strings.TrimSpace(output))
	}
	log.Println()

	// Upload file
	stepNum++
	log.Printf("Step %d: Uploading file...", stepNum)
	uploadContent := "Hello from PicoD!\nThis file was uploaded via REST API."
	if *useAuth {
		uploadContent = "Hello from authenticated PicoD client!\nThis file was uploaded with RSA signature verification."
		if err := uploadFileAuthenticated(picodURL, keyPair, "./authenticated_upload.txt", uploadContent); err != nil {
			log.Fatalf("Failed to upload file: %v", err)
		}
		log.Println("✅ File uploaded to ./authenticated_upload.txt")
	} else {
		if err := uploadFileMultipart(picodURL, "./upload.txt", uploadContent); err != nil {
			log.Fatalf("Failed to upload file: %v", err)
		}
		log.Println("✅ File uploaded to ./upload.txt")
	}
	log.Println()

	// Verify uploaded file
	stepNum++
	log.Printf("Step %d: Verifying uploaded file...", stepNum)
	var output string
	var fileName string
	if *useAuth {
		fileName = "./authenticated_upload.txt"
	} else {
		fileName = "./upload.txt"
	}

	if *useAuth {
		output, err = executeAuthenticatedCommand(picodURL, keyPair, "cat "+fileName)
	} else {
		output, err = executeCommand(picodURL, "cat "+fileName)
	}

	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}
	log.Printf("   File content: %s", strings.TrimSpace(output))
	log.Println()

	// Write Python script
	stepNum++
	log.Printf("Step %d: Writing Python script via JSON+Base64...", stepNum)
	pythonScript := `#!/usr/bin/env python3
import json
from datetime import datetime

def generate_fibonacci(n):
    fib = [0, 1]
    for i in range(2, n):
        fib.append(fib[i-1] + fib[i-2])
    return fib[:n]

n = 20
fibonacci = generate_fibonacci(n)

output_data = {
    "timestamp": datetime.now().isoformat(),
    "algorithm": "Fibonacci Sequence",
    "count": n,
    "numbers": fibonacci,
    "sum": sum(fibonacci),
    "message": "Generated successfully via PicoD!"
}

with open('./output.json', 'w') as f:
    json.dump(output_data, f, indent=2)

print(f"Generated {n} Fibonacci numbers")
print(f"Sum: {sum(fibonacci)}")
`

	if *useAuth {
		if err := uploadFileAuthenticated(picodURL, keyPair, "./fibonacci.py", pythonScript); err != nil {
			log.Fatalf("Failed to write Python script: %v", err)
		}
	} else {
		if err := uploadFileJSON(picodURL, "./fibonacci.py", pythonScript); err != nil {
			log.Fatalf("Failed to write Python script: %v", err)
		}
	}
	log.Println("✅ Python script written to ./fibonacci.py")
	log.Println()

	// Execute Python script
	stepNum++
	log.Printf("Step %d: Executing Python script...", stepNum)
	if *useAuth {
		output, err = executeAuthenticatedCommand(picodURL, keyPair, "python3 ./fibonacci.py")
	} else {
		output, err = executeCommand(picodURL, "python3 ./fibonacci.py")
	}
	if err != nil {
		log.Fatalf("Failed to execute Python script: %v", err)
	}
	log.Printf("   Script output:\n%s", indentOutput(output))
	log.Println()

	// Download generated file
	stepNum++
	log.Printf("Step %d: Downloading generated output file...", stepNum)
	localOutputPath := "/tmp/picod_output.json"
	if *useAuth {
		if err := downloadFileAuthenticated(picodURL, keyPair, "./output.json", localOutputPath); err != nil {
			log.Fatalf("Failed to download output file: %v", err)
		}
	} else {
		if err := downloadFile(picodURL, "./output.json", localOutputPath); err != nil {
			log.Fatalf("Failed to download output file: %v", err)
		}
	}
	log.Printf("✅ Output file downloaded to %s", localOutputPath)
	log.Println()

	// Verify downloaded file
	stepNum++
	log.Printf("Step %d: Verifying downloaded file...", stepNum)
	fileContent, err := os.ReadFile(localOutputPath)
	if err != nil {
		log.Fatalf("Failed to read downloaded file: %v", err)
	}

	var outputData map[string]interface{}
	if err := json.Unmarshal(fileContent, &outputData); err != nil {
		log.Fatalf("Failed to parse JSON output: %v", err)
	}

	log.Println("   File contents:")
	prettyJSON, _ := json.MarshalIndent(outputData, "   ", "  ")
	log.Printf("%s\n", prettyJSON)

	if numbers, ok := outputData["numbers"].([]interface{}); ok {
		log.Printf("✅ Verified: Generated %d Fibonacci numbers", len(numbers))
	}
	if sum, ok := outputData["sum"].(float64); ok {
		log.Printf("✅ Verified: Sum = %.0f", sum)
	}
	log.Println()

	// Success
	log.Println("===========================================")
	if *useAuth {
		log.Println("🎉 All authenticated tests passed!")
	} else {
		log.Println("🎉 All tests passed successfully!")
	}
	log.Println("===========================================")
	log.Println()
	log.Println("Summary:")
	log.Println("  ✅ Health check passed")
	if *useAuth {
		log.Println("  ✅ RSA key pair generated/loaded")
		log.Println("  ✅ Server initialized with public key")
		log.Println("  ✅ Authenticated commands executed")
		log.Println("  ✅ Authenticated file upload")
	} else {
		log.Println("  ✅ Basic commands executed")
		log.Println("  ✅ File uploaded via multipart")
		log.Println("  ✅ File written via JSON+Base64")
	}
	log.Println("  ✅ Python script executed")
	log.Println("  ✅ Output file downloaded")
	log.Println("  ✅ Downloaded file verified")
	log.Println()
	if *useAuth {
		log.Println("To test without authentication:")
		log.Println("  go run example/picod/main.go")
		log.Println()
	} else {
		log.Println("To test with RSA authentication:")
		log.Println("  go run example/picod/main.go -auth")
		log.Println()
	}
}

// healthCheck performs health check
func healthCheck(baseURL string) error {
	resp, err := http.Get(fmt.Sprintf("%s/health", baseURL))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed with status %d", resp.StatusCode)
	}

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return err
	}

	log.Printf("   Server status: %s", health["status"])
	log.Printf("   Service: %s v%s", health["service"], health["version"])
	log.Printf("   Uptime: %s", health["uptime"])

	return nil
}

// executeCommand executes command
func executeCommand(baseURL, command string) (string, error) {
	req := ExecuteRequest{
		Command: command,
		Timeout: 30,
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/api/execute", baseURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result ExecuteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.ExitCode != 0 {
		return "", fmt.Errorf("command failed (exit code %d): %s", result.ExitCode, result.Stderr)
	}

	return result.Stdout, nil
}

// uploadFileMultipart uploads file via multipart/form-data
func uploadFileMultipart(baseURL, remotePath, content string) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add path field
	if err := writer.WriteField("path", remotePath); err != nil {
		return err
	}

	// Add file field
	part, err := writer.CreateFormFile("file", "upload.txt")
	if err != nil {
		return err
	}
	if _, err := part.Write([]byte(content)); err != nil {
		return err
	}

	// Add mode field
	if err := writer.WriteField("mode", "0644"); err != nil {
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/api/files", baseURL), &buf)
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// uploadFileJSON uploads file via JSON+Base64
func uploadFileJSON(baseURL, remotePath, content string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	payload := map[string]string{
		"path":    remotePath,
		"content": encoded,
		"mode":    "0644",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/api/files", baseURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// downloadFile downloads file
func downloadFile(baseURL, remotePath, localPath string) error {
	// Remove leading /
	cleanPath := strings.TrimPrefix(remotePath, "/")

	httpReq, err := http.NewRequest("GET", fmt.Sprintf("%s/api/files/%s", baseURL, cleanPath), nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Create local file
	out, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// getEnv gets environment variable, returns default if not exists
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// indentOutput adds indentation to each line of output
func indentOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var indented []string
	for _, line := range lines {
		indented = append(indented, "   "+line)
	}
	return strings.Join(indented, "\n")
}

// loadOrGenerateKeyPair loads existing key pair or generates a new one
func loadOrGenerateKeyPair() (*RSAKeyPair, error) {
	// Try to load existing key pair
	if data, err := os.ReadFile(keyFile); err == nil {
		block, _ := pem.Decode(data)
		if block != nil && block.Type == "RSA PRIVATE KEY" {
			privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err == nil {
				return &RSAKeyPair{
					PrivateKey: privateKey,
					PublicKey:  &privateKey.PublicKey,
				}, nil
			}
		}
	}

	// Generate new key pair
	log.Println("   Generating new RSA-2048 key pair...")
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key pair: %v", err)
	}

	// Save private key to file
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	if err := os.WriteFile(keyFile, privateKeyPEM, 0600); err != nil {
		return nil, fmt.Errorf("failed to save private key: %v", err)
	}

	log.Printf("   RSA key pair saved to %s", keyFile)
	return &RSAKeyPair{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
	}, nil
}

// exportPublicKey exports public key to PEM format
func exportPublicKey(publicKey *rsa.PublicKey) (string, error) {
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("failed to marshal public key: %v", err)
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	})

	return string(publicKeyPEM), nil
}

// initializeServer initializes the Picod server with public key
func initializeServer(baseURL, publicKey string) error {
	req := InitRequest{
		PublicKey: publicKey,
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/api/init", baseURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("init request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result InitResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if !result.Success {
		return fmt.Errorf("server initialization failed: %s", result.Message)
	}

	return nil
}

// signRequest signs a request with RSA private key
func signRequest(keyPair *RSAKeyPair, timestamp string, body string) (string, error) {
	message := timestamp + body
	hashed := sha256.Sum256([]byte(message))

	signature, err := rsa.SignPKCS1v15(rand.Reader, keyPair.PrivateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

// executeAuthenticatedCommand executes command with RSA signature
func executeAuthenticatedCommand(baseURL string, keyPair *RSAKeyPair, command string) (string, error) {
	req := ExecuteRequest{
		Command: command,
		Timeout: 30,
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	timestamp := time.Now().Format(time.RFC3339)
	signature, err := signRequest(keyPair, timestamp, string(jsonData))
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/api/execute", baseURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Timestamp", timestamp)
	httpReq.Header.Set("X-Signature", signature)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result ExecuteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.ExitCode != 0 {
		return "", fmt.Errorf("command failed (exit code %d): %s", result.ExitCode, result.Stderr)
	}

	return result.Stdout, nil
}

// uploadFileAuthenticated uploads file with RSA signature
func uploadFileAuthenticated(baseURL string, keyPair *RSAKeyPair, remotePath, content string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	payload := map[string]string{
		"path":    remotePath,
		"content": encoded,
		"mode":    "0644",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	timestamp := time.Now().Format(time.RFC3339)
	signature, err := signRequest(keyPair, timestamp, string(jsonData))
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/api/files", baseURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Timestamp", timestamp)
	httpReq.Header.Set("X-Signature", signature)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// downloadFileAuthenticated downloads file with RSA signature
func downloadFileAuthenticated(baseURL string, keyPair *RSAKeyPair, remotePath, localPath string) error {
	// Remove leading /
	cleanPath := strings.TrimPrefix(remotePath, "/")

	// Create empty body for GET request
	body := ""
	timestamp := time.Now().Format(time.RFC3339)
	signature, err := signRequest(keyPair, timestamp, body)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequest("GET", fmt.Sprintf("%s/api/files/%s", baseURL, cleanPath), nil)
	if err != nil {
		return err
	}

	httpReq.Header.Set("X-Timestamp", timestamp)
	httpReq.Header.Set("X-Signature", signature)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Create local file
	out, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
