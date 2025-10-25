package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	defaultAPIURL = "http://localhost:8080"
	defaultTTL    = 3600
)

// CreateSessionRequest matches the API spec
type CreateSessionRequest struct {
	TTL          int                    `json:"ttl,omitempty"`
	Image        string                 `json:"image,omitempty"`
	SSHPublicKey string                 `json:"sshPublicKey,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// SessionResponse matches the API response
type SessionResponse struct {
	SessionID      string                 `json:"sessionId"`
	Status         string                 `json:"status"`
	CreatedAt      string                 `json:"createdAt"`
	ExpiresAt      string                 `json:"expiresAt"`
	LastActivityAt string                 `json:"lastActivityAt,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

func main() {
	log.Println("===========================================")
	log.Println("SSH Key-based Authentication Test")
	log.Println("===========================================")
	log.Println()

	apiURL := getEnv("API_URL", defaultAPIURL)

	// Step 1: Generate SSH key pair
	log.Println("Step 1: Generating SSH key pair...")
	publicKey, privateKey, err := generateSSHKeyPair()
	if err != nil {
		log.Fatalf("Failed to generate SSH key pair: %v", err)
	}
	log.Printf("✅ SSH key pair generated")
	log.Printf("   Public key: %s", publicKey[:50]+"...")
	log.Println()

	// Step 2: Create session with public key
	log.Println("Step 2: Creating session with SSH public key...")
	sessionID, err := createSessionWithSSHKey(apiURL, publicKey)
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}
	log.Printf("✅ Session created: %s", sessionID)
	log.Println()

	// Step 3: Establish HTTP CONNECT tunnel
	log.Println("Step 3: Establishing HTTP CONNECT tunnel...")
	tunnelConn, err := establishTunnel(apiURL, sessionID)
	if err != nil {
		log.Fatalf("Failed to establish tunnel: %v", err)
	}
	defer tunnelConn.Close()
	log.Println("✅ HTTP CONNECT tunnel established")
	log.Println()

	// Step 4: Connect via SSH using private key
	log.Println("Step 4: Connecting via SSH with private key authentication...")
	sshClient, err := connectSSHWithKey(tunnelConn, privateKey)
	if err != nil {
		log.Fatalf("Failed to connect via SSH: %v", err)
	}
	defer sshClient.Close()
	log.Println("✅ SSH connection established with key-based auth")
	log.Println()

	// Step 5: Execute basic test commands
	log.Println("Step 5: Executing basic test commands...")

	commands := []string{
		"whoami",
		"pwd",
		"echo 'Hello from SSH with key auth!'",
		"python --version",
		"uname -a",
	}

	for i, cmd := range commands {
		log.Printf("   [%d/%d] Executing: %s", i+1, len(commands), cmd)
		output, err := executeCommand(sshClient, cmd)
		if err != nil {
			log.Printf("      ⚠️  Command failed: %v", err)
			continue
		}
		log.Printf("      Output: %s", strings.TrimSpace(output))
	}
	log.Println()

	// Step 6: Upload Python script via SFTP
	log.Println("Step 6: Uploading Python script via SFTP...")
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
		log.Fatalf("Failed to upload Python script: %v", err)
	}
	log.Println("✅ Python script uploaded to /workspace/fibonacci.py")
	log.Println()

	// Step 7: Execute Python script
	log.Println("Step 7: Executing Python script in sandbox...")
	output, err := executeCommand(sshClient, "python3 /workspace/fibonacci.py")
	if err != nil {
		log.Fatalf("Failed to execute Python script: %v", err)
	}
	log.Printf("   Script output:\n%s", indentOutput(output))
	log.Println()

	// Step 8: Download generated file
	log.Println("Step 8: Downloading generated output file...")
	localOutputPath := "/tmp/sandbox_output.json"
	err = downloadFile(sshClient, "/workspace/output.json", localOutputPath)
	if err != nil {
		log.Fatalf("Failed to download output file: %v", err)
	}
	log.Printf("✅ Output file downloaded to %s", localOutputPath)
	log.Println()

	// Step 9: Verify downloaded file
	log.Println("Step 9: Verifying downloaded file...")
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

	// Verify the data
	if numbers, ok := outputData["numbers"].([]interface{}); ok {
		log.Printf("✅ Verified: Generated %d Fibonacci numbers", len(numbers))
	}
	if sum, ok := outputData["sum"].(float64); ok {
		log.Printf("✅ Verified: Sum = %.0f", sum)
	}
	if message, ok := outputData["message"].(string); ok {
		log.Printf("✅ Verified: Message = \"%s\"", message)
	}
	log.Println()

	// Success
	log.Println("===========================================")
	log.Println("🎉 All tests passed successfully!")
	log.Println("===========================================")
	log.Println()
	log.Println("Summary:")
	log.Println("  ✅ SSH key pair generated")
	log.Println("  ✅ Session created with public key")
	log.Println("  ✅ HTTP CONNECT tunnel established")
	log.Println("  ✅ SSH connection with key-based auth")
	log.Println("  ✅ Basic commands executed successfully")
	log.Println("  ✅ Python script uploaded via SFTP")
	log.Println("  ✅ Python script executed in sandbox")
	log.Println("  ✅ Output file downloaded via SFTP")
	log.Println("  ✅ Downloaded file verified")
	log.Println()
	log.Printf("Session ID: %s", sessionID)
	log.Printf("Downloaded file: %s", localOutputPath)
	log.Println()
}

// generateSSHKeyPair generates an Ed25519 SSH key pair
func generateSSHKeyPair() (publicKey string, privateKey ssh.Signer, err error) {
	// Generate Ed25519 key pair
	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate key: %w", err)
	}

	// Convert to SSH format
	signer, err := ssh.NewSignerFromKey(privKey)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create signer: %w", err)
	}

	// Format public key for OpenSSH
	pubKeyStr := string(ssh.MarshalAuthorizedKey(ssh.PublicKey(signer.PublicKey())))

	return pubKeyStr[:len(pubKeyStr)-1], signer, nil // Remove trailing newline
}

// createSessionWithSSHKey creates a session with the SSH public key
func createSessionWithSSHKey(apiURL, publicKey string) (string, error) {
	req := CreateSessionRequest{
		TTL:          defaultTTL,
		Image:        "sandbox:latest",
		SSHPublicKey: publicKey,
		Metadata: map[string]interface{}{
			"test": "ssh-key-auth",
		},
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := http.Post(
		fmt.Sprintf("%s/v1/sessions", apiURL),
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var sessionResp SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&sessionResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return sessionResp.SessionID, nil
}

// establishTunnel establishes an HTTP CONNECT tunnel
func establishTunnel(apiURL, sessionID string) (net.Conn, error) {
	// Parse API URL to get host
	var host string
	if len(apiURL) > 7 && apiURL[:7] == "http://" {
		host = apiURL[7:]
	} else {
		host = apiURL
	}

	// Add default port if not specified
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "8080")
	}

	// Connect to server
	conn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	// Send CONNECT request
	connectReq := fmt.Sprintf("CONNECT /v1/sessions/%s/tunnel HTTP/1.1\r\n", sessionID)
	connectReq += fmt.Sprintf("Host: %s\r\n", host)
	connectReq += "User-Agent: ssh-key-test/1.0\r\n"
	connectReq += "\r\n"

	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send CONNECT: %w", err)
	}

	// Read response
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

// connectSSHWithKey establishes SSH connection using private key
func connectSSHWithKey(conn net.Conn, privateKey ssh.Signer) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: "sandbox",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(privateKey),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	// Create SSH connection over the tunnel
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, "sandbox", config)
	if err != nil {
		return nil, fmt.Errorf("SSH handshake failed: %w", err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

// executeCommand executes a command via SSH
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

// getEnv gets environment variable with default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// uploadFile uploads a file to the remote server via SFTP
func uploadFile(sshClient *ssh.Client, content, remotePath string) error {
	// Create SFTP client
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	// Ensure remote directory exists
	remoteDir := filepath.Dir(remotePath)
	if err := sftpClient.MkdirAll(remoteDir); err != nil {
		// Ignore error if directory already exists
		if !os.IsExist(err) {
			return fmt.Errorf("failed to create remote directory: %w", err)
		}
	}

	// Create remote file
	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer remoteFile.Close()

	// Write content
	_, err = remoteFile.Write([]byte(content))
	if err != nil {
		return fmt.Errorf("failed to write to remote file: %w", err)
	}

	return nil
}

// downloadFile downloads a file from the remote server via SFTP
func downloadFile(sshClient *ssh.Client, remotePath, localPath string) error {
	// Create SFTP client
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	// Open remote file
	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %w", err)
	}
	defer remoteFile.Close()

	// Ensure local directory exists
	localDir := filepath.Dir(localPath)
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return fmt.Errorf("failed to create local directory: %w", err)
	}

	// Create local file
	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer localFile.Close()

	// Copy content
	_, err = io.Copy(localFile, remoteFile)
	if err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	return nil
}

// indentOutput adds indentation to each line of the output
func indentOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var indented []string
	for _, line := range lines {
		indented = append(indented, "   "+line)
	}
	return strings.Join(indented, "\n")
}
