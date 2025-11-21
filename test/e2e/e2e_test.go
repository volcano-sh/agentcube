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
	"net/url"
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

type testEnv struct {
	apiURL       string
	authToken    string
	sandboxImage string
	t            *testing.T
}

func newTestEnv(t *testing.T) *testEnv {
	return &testEnv{
		apiURL:       getEnv("API_URL", defaultAPIURL),
		authToken:    os.Getenv("API_TOKEN"),
		sandboxImage: getEnv("SANDBOX_IMAGE", defaultSandboxImage),
		t:            t,
	}
}

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

func (e *testEnv) setupSandbox() (string, *ssh.Client, func()) {
	publicKey, privateKey, err := generateSSHKeyPair()
	if err != nil {
		e.t.Fatalf("Failed to generate SSH key pair: %v", err)
	}

	sandboxID, err := e.createSandboxWithSSHKey(publicKey)
	if err != nil {
		e.t.Fatalf("Failed to create sandbox: %v", err)
	}

	cleanup := func() {
		if err := e.deleteSandbox(sandboxID); err != nil {
			e.t.Errorf("Failed to delete sandbox: %v", err)
		}
	}

	tunnelConn, err := e.establishTunnel(sandboxID)
	if err != nil {
		cleanup()
		e.t.Fatalf("Failed to establish tunnel: %v", err)
	}

	sshClient, err := connectSSHWithKey(tunnelConn, privateKey)
	if err != nil {
		tunnelConn.Close()
		cleanup()
		e.t.Fatalf("Failed to connect via SSH: %v", err)
	}

	return sandboxID, sshClient, func() {
		sshClient.Close()
		tunnelConn.Close()
		cleanup()
	}
}

func TestSandboxLifecycle(t *testing.T) {
	env := newTestEnv(t)

	publicKey, _, err := generateSSHKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate SSH key pair: %v", err)
	}

	sandboxID, err := env.createSandboxWithSSHKey(publicKey)
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}

	// Use getSandbox to check the sandbox exists
	sandbox, err := env.getSandbox(sandboxID)
	if err != nil {
		t.Fatalf("Failed to get sandbox: %v", err)
	}
	if sandbox == nil {
		t.Fatalf("Sandbox %s not found after creation", sandboxID)
	}
	if sandbox.SandboxID != sandboxID {
		t.Errorf("Expected sandbox ID %s, got %s", sandboxID, sandbox.SandboxID)
	}

	// Use listSandboxes to verify the sandbox is in the list
	listResp, err := env.listSandboxes()
	if err != nil {
		t.Fatalf("Failed to list sandboxes: %v", err)
	}
	found := false
	for _, sb := range listResp.Sandboxes {
		if sb.SandboxID == sandboxID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Sandbox %s not found in sandbox list", sandboxID)
	}

	if err := env.deleteSandbox(sandboxID); err != nil {
		t.Errorf("Failed to delete sandbox: %v", err)
	}
}

func TestExecuteCommands(t *testing.T) {
	env := newTestEnv(t)
	_, sshClient, cleanup := env.setupSandbox()
	defer cleanup()

	commands := map[string]string{
		"echo 'Hello from e2e test!'": "Hello from e2e test!",
	}

	for cmd, expectedOutput := range commands {
		output, err := executeCommand(sshClient, cmd)
		if err != nil {
			t.Errorf("Command failed: %v", err)
			continue
		}
		// Remove trailing newline for comparison
		output = strings.TrimSpace(output)
		if output != expectedOutput {
			t.Errorf("Command '%s' output mismatch:\n  Expected: %q\n  Got:      %q", cmd, expectedOutput, output)
		}
	}
}

func TestFileUploadAndDownload(t *testing.T) {
	env := newTestEnv(t)
	_, sshClient, cleanup := env.setupSandbox()
	defer cleanup()

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

	err := uploadFile(sshClient, pythonScript, "/workspace/fibonacci.py")
	if err != nil {
		t.Fatalf("Failed to upload Python script: %v", err)
	}

	_, err = executeCommand(sshClient, "python3 /workspace/fibonacci.py")
	if err != nil {
		t.Fatalf("Failed to execute Python script: %v", err)
	}

	localOutputPath := filepath.Join(os.TempDir(), "sandbox_output.json")
	err = downloadFile(sshClient, "/workspace/output.json", localOutputPath)
	if err != nil {
		t.Fatalf("Failed to download output file: %v", err)
	}

	fileContent, err := os.ReadFile(localOutputPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	var outputData map[string]interface{}
	if err := json.Unmarshal(fileContent, &outputData); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if numbers, ok := outputData["numbers"].([]interface{}); ok {
		if len(numbers) != 20 {
			t.Errorf("Expected 20 numbers, got %d", len(numbers))
		}
	} else {
		t.Error("Failed to verify numbers")
	}

	if _, ok := outputData["sum"].(float64); !ok {
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

func (e *testEnv) createSandboxWithSSHKey(publicKey string) (string, error) {
	req := CreateSandboxRequest{
		TTL:          defaultTTL,
		Image:        e.sandboxImage,
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
		fmt.Sprintf("%s/v1/sandboxes", e.apiURL),
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
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

func (e *testEnv) getSandbox(sandboxID string) (*Sandbox, error) {
	httpReq, err := http.NewRequest(
		"GET",
		fmt.Sprintf("%s/v1/sandboxes/%s", e.apiURL, sandboxID),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Sandbox not found
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var sandbox Sandbox
	if err := json.NewDecoder(resp.Body).Decode(&sandbox); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &sandbox, nil
}

type ListSandboxesResponse struct {
	Sandboxes []Sandbox `json:"sandboxes"`
	Total     int       `json:"total"`
	Limit     int       `json:"limit"`
	Offset    int       `json:"offset"`
}

func (e *testEnv) listSandboxes() (*ListSandboxesResponse, error) {
	httpReq, err := http.NewRequest(
		"GET",
		fmt.Sprintf("%s/v1/sandboxes", e.apiURL),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var listResp ListSandboxesResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &listResp, nil
}

func (e *testEnv) deleteSandbox(sandboxID string) error {
	httpReq, err := http.NewRequest(
		"DELETE",
		fmt.Sprintf("%s/v1/sandboxes/%s", e.apiURL, sandboxID),
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
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

func (e *testEnv) establishTunnel(sandboxID string) (net.Conn, error) {
	u, err := url.Parse(e.apiURL)
	if err != nil {
		return nil, fmt.Errorf("invalid API URL: %w", err)
	}
	host := u.Host

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
	if e.authToken != "" {
		connectReq += fmt.Sprintf("Authorization: Bearer %s\r\n", e.authToken)
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
		return fmt.Errorf("failed to create remote directory: %w", err)
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
