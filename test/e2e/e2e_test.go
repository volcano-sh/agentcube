package e2e

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	defaultAPIURL       = "http://localhost:8080"
	defaultTTL          = 3600
	defaultSandboxImage = "sandbox:latest"
)

var (
	// authToken is the Bearer token for authentication
	// Set via API_TOKEN environment variable
	authToken string
)

// CreateSandboxRequest matches the API spec
type CreateSandboxRequest struct {
	TTL          int                    `json:"ttl,omitempty"`
	Image        string                 `json:"image,omitempty"`
	SSHPublicKey string                 `json:"sshPublicKey,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// Sandbox matches the API response
type Sandbox struct {
	SandboxID      string                 `json:"sandboxId"`
	Status         string                 `json:"status"`
	CreatedAt      string                 `json:"createdAt"`
	ExpiresAt      string                 `json:"expiresAt"`
	LastActivityAt string                 `json:"lastActivityAt,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

func TestSandboxLifecycle(t *testing.T) {
	apiURL := getEnv("API_URL", defaultAPIURL)
	sandboxImage := getEnv("SANDBOX_IMAGE", defaultSandboxImage)
	authToken = os.Getenv("API_TOKEN")

	t.Logf("Configuration:")
	t.Logf("  API URL: %s", apiURL)
	t.Logf("  Sandbox Image: %s", sandboxImage)

	if authToken == "" {
		t.Log("⚠️  WARNING: API_TOKEN environment variable not set")
		t.Log("   Attempting to proceed without authentication token")
	} else {
		t.Log("✅ API authentication token loaded from environment")
	}

	// Step 1: Generate SSH key pair
	t.Log("Step 1: Generating SSH key pair...")
	publicKey, privateKey, err := generateSSHKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate SSH key pair: %v", err)
	}
	t.Logf("✅ SSH key pair generated")

	// Step 2: Create sandbox with public key
	t.Log("Step 2: Creating sandbox with SSH public key...")
	sandboxID, err := createSandboxWithSSHKey(apiURL, publicKey, sandboxImage)
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	t.Logf("✅ Sandbox created: %s", sandboxID)

	// Ensure cleanup
	defer func() {
		t.Log("Cleaning up sandbox...")
		if err := deleteSandbox(apiURL, sandboxID); err != nil {
			t.Errorf("Failed to delete sandbox: %v", err)
		} else {
			t.Log("✅ Sandbox deleted")
		}
	}()

	// Step 3: Establish HTTP CONNECT tunnel
	t.Log("Step 3: Establishing HTTP CONNECT tunnel...")
	tunnelConn, err := establishTunnel(apiURL, sandboxID)
	if err != nil {
		t.Fatalf("Failed to establish tunnel: %v", err)
	}
	defer tunnelConn.Close()
	t.Log("✅ HTTP CONNECT tunnel established")

	// Step 4: Connect via SSH using private key
	t.Log("Step 4: Connecting via SSH with private key authentication...")
	sshClient, err := connectSSHWithKey(tunnelConn, privateKey)
	if err != nil {
		t.Fatalf("Failed to connect via SSH: %v", err)
	}
	defer sshClient.Close()
	t.Log("✅ SSH connection established with key-based auth")

	// Step 5: Execute basic test commands
	t.Log("Step 5: Executing basic test commands...")

	commands := []string{
		"whoami",
		"pwd",
		"echo 'Hello from SSH with key auth!'",
		"python3 --version",
		"uname -a",
	}

	for i, cmd := range commands {
		t.Logf("   [%d/%d] Executing: %s", i+1, len(commands), cmd)
		output, err := executeCommand(sshClient, cmd)
		if err != nil {
			t.Errorf("      ⚠️  Command failed: %v", err)
			continue
		}
		t.Logf("      Output: %s", strings.TrimSpace(output))
	}

	// Step 6: Upload Python script via SFTP
	t.Log("Step 6: Uploading Python script via SFTP...")
	pythonScript := `#!/usr/bin/env python3
# Fibonacci generator script
import sys
import json
from datetime import datetime

def generate_fibonacci(n):
    """Generate first n Fibonacci numbers"""
    fib = [0, 1]
    for i in range(2, n):
        fib.append(fib[i-1] + fib[i-2])
    return fib[:n]

def main():
    # Generate Fibonacci numbers
    n = 20
    fibonacci = generate_fibonacci(n)
    
    # Create output data
    output_data = {
        "timestamp": datetime.now().isoformat(),
        "algorithm": "Fibonacci Sequence",
        "count": n,
        "numbers": fibonacci,
        "sum": sum(fibonacci),
        "message": "Generated successfully in sandbox!"
    }
    
    # Write to output file
    with open('/workspace/output.json', 'w') as f:
        json.dump(output_data, f, indent=2)
    
    print(f"✅ Generated {n} Fibonacci numbers")
    print(f"   Sum: {sum(fibonacci)}")
    print(f"   Output written to: /workspace/output.json")

if __name__ == "__main__":
    main()
`

	err = uploadFile(sshClient, pythonScript, "/workspace/fibonacci.py")
	if err != nil {
		t.Fatalf("Failed to upload Python script: %v", err)
	}
	t.Log("✅ Python script uploaded to /workspace/fibonacci.py")

	// Step 7: Execute Python script
	t.Log("Step 7: Executing Python script in sandbox...")
	output, err := executeCommand(sshClient, "python3 /workspace/fibonacci.py")
	if err != nil {
		t.Fatalf("Failed to execute Python script: %v", err)
	}
	t.Logf("   Script output:\n%s", indentOutput(output))

	// Step 8: Download generated file
	t.Log("Step 8: Downloading generated output file...")
	localOutputPath := filepath.Join(os.TempDir(), "sandbox_output.json")
	err = downloadFile(sshClient, "/workspace/output.json", localOutputPath)
	if err != nil {
		t.Fatalf("Failed to download output file: %v", err)
	}
	t.Logf("✅ Output file downloaded to %s", localOutputPath)

	// Step 9: Verify downloaded file
	t.Log("Step 9: Verifying downloaded file...")
	fileContent, err := os.ReadFile(localOutputPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	var outputData map[string]interface{}
	if err := json.Unmarshal(fileContent, &outputData); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	// Verify the data
	if numbers, ok := outputData["numbers"].([]interface{}); ok {
		t.Logf("✅ Verified: Generated %d Fibonacci numbers", len(numbers))
		if len(numbers) != 20 {
			t.Errorf("Expected 20 numbers, got %d", len(numbers))
		}
	} else {
		t.Error("Failed to verify numbers")
	}

	if sumVal, ok := outputData["sum"].(float64); ok {
		t.Logf("✅ Verified: Sum = %.0f", sumVal)
	} else {
		t.Error("Failed to verify sum")
	}
}

// Helper functions

func generateSSHKeyPair() (publicKey string, privateKey ssh.Signer, err error) {
	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate key: %w", err)
	}

	signer, err := ssh.NewSignerFromKey(privKey)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create signer: %w", err)
	}

	pubKeyStr := string(ssh.MarshalAuthorizedKey(ssh.PublicKey(signer.PublicKey())))
	return pubKeyStr[:len(pubKeyStr)-1], signer, nil
}

func createSandboxWithSSHKey(apiURL, publicKey, image string) (string, error) {
	req := CreateSandboxRequest{
		TTL:          defaultTTL,
		Image:        image,
		SSHPublicKey: publicKey,
		Metadata: map[string]interface{}{
			"test": "ssh-key-auth",
		},
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(
		"POST",
		fmt.Sprintf("%s/v1/sandboxes", apiURL),
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var sandbox Sandbox
	if err := json.NewDecoder(resp.Body).Decode(&sandbox); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return sandbox.SandboxID, nil
}

func deleteSandbox(apiURL, sandboxID string) error {
	httpReq, err := http.NewRequest(
		"DELETE",
		fmt.Sprintf("%s/v1/sandboxes/%s", apiURL, sandboxID),
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func establishTunnel(apiURL, sandboxID string) (net.Conn, error) {
	var host string
	if len(apiURL) > 7 && apiURL[:7] == "http://" {
		host = apiURL[7:]
	} else {
		host = apiURL
	}

	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "8080")
	}

	conn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	connectReq := fmt.Sprintf("CONNECT /v1/sandboxes/%s HTTP/1.1\r\n", sandboxID)
	connectReq += fmt.Sprintf("Host: %s\r\n", host)
	connectReq += "User-Agent: e2e-test/1.0\r\n"
	if authToken != "" {
		connectReq += fmt.Sprintf("Authorization: Bearer %s\r\n", authToken)
	}
	connectReq += "\r\n"

	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send CONNECT: %w", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("CONNECT failed with status %d", resp.StatusCode)
	}

	return conn, nil
}

func connectSSHWithKey(conn net.Conn, privateKey ssh.Signer) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: "sandbox",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(privateKey),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, "sandbox", config)
	if err != nil {
		return nil, fmt.Errorf("SSH handshake failed: %w", err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

func executeCommand(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	var stdout bytes.Buffer
	session.Stdout = &stdout

	if err := session.Run(command); err != nil {
		return "", fmt.Errorf("command failed: %w", err)
	}

	return stdout.String(), nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func uploadFile(sshClient *ssh.Client, content, remotePath string) error {
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	remoteDir := filepath.Dir(remotePath)
	if err := sftpClient.MkdirAll(remoteDir); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("failed to create remote directory: %w", err)
		}
	}

	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer remoteFile.Close()

	_, err = remoteFile.Write([]byte(content))
	if err != nil {
		return fmt.Errorf("failed to write to remote file: %w", err)
	}

	return nil
}

func downloadFile(sshClient *ssh.Client, remotePath, localPath string) error {
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %w", err)
	}
	defer remoteFile.Close()

	localDir := filepath.Dir(localPath)
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return fmt.Errorf("failed to create local directory: %w", err)
	}

	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer localFile.Close()

	_, err = io.Copy(localFile, remoteFile)
	if err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	return nil
}

func indentOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var indented []string
	for _, line := range lines {
		indented = append(indented, "   "+line)
	}
	return strings.Join(indented, "\n")
}
