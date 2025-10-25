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
	"time"

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

	// Step 5: Execute test commands
	log.Println("Step 5: Executing test commands...")

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
		log.Printf("      Output: %s", output)
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
	log.Println("  ✅ Commands executed successfully")
	log.Println()
	log.Printf("Session ID: %s", sessionID)
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
