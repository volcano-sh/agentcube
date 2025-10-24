package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/ssh"
)

var (
	apiServer   = flag.String("api", "http://localhost:8080", "pico-apiserver URL")
	sessionID   = flag.String("session", "", "Session ID to connect to")
	token       = flag.String("token", "", "Authorization token")
	sshUser     = flag.String("user", "sandbox", "SSH username")
	sshPassword = flag.String("password", "sandbox", "SSH password")
	command     = flag.String("cmd", "echo 'Hello from sandbox'", "Command to execute")
)

func main() {
	flag.Parse()

	if *sessionID == "" {
		log.Fatal("Session ID is required. Use -session flag")
	}

	log.Printf("Testing tunnel connection to session: %s", *sessionID)

	// Step 1: Establish HTTP CONNECT tunnel
	tunnelConn, err := establishTunnel(*apiServer, *sessionID, *token)
	if err != nil {
		log.Fatalf("Failed to establish tunnel: %v", err)
	}
	defer tunnelConn.Close()

	log.Println("✅ HTTP CONNECT tunnel established successfully")

	// Step 2: Establish SSH connection over the tunnel
	sshClient, err := sshOverTunnel(tunnelConn, *sshUser, *sshPassword)
	if err != nil {
		log.Fatalf("Failed to establish SSH connection: %v", err)
	}
	defer sshClient.Close()

	log.Println("✅ SSH connection established successfully")

	// Step 3: Execute test command
	stdout, stderr, exitCode, err := executeCommand(sshClient, *command)
	if err != nil {
		log.Fatalf("Failed to execute command: %v", err)
	}

	log.Println("✅ Command executed successfully")
	fmt.Println("\n--- Command Output ---")
	fmt.Printf("Command: %s\n", *command)
	fmt.Printf("Exit Code: %d\n", exitCode)
	fmt.Printf("Stdout:\n%s\n", stdout)
	if stderr != "" {
		fmt.Printf("Stderr:\n%s\n", stderr)
	}
	fmt.Println("--- End Output ---\n")

	// Step 4: Additional tests
	log.Println("Running additional tests...")

	// Test 1: Check current directory
	stdout, _, _, err = executeCommand(sshClient, "pwd")
	if err != nil {
		log.Printf("⚠️  pwd test failed: %v", err)
	} else {
		log.Printf("✅ Current directory: %s", stdout)
	}

	// Test 2: Check user
	stdout, _, _, err = executeCommand(sshClient, "whoami")
	if err != nil {
		log.Printf("⚠️  whoami test failed: %v", err)
	} else {
		log.Printf("✅ Current user: %s", stdout)
	}

	// Test 3: Check Python version
	stdout, _, _, err = executeCommand(sshClient, "python --version")
	if err != nil {
		log.Printf("⚠️  Python test failed: %v", err)
	} else {
		log.Printf("✅ Python version: %s", stdout)
	}

	log.Println("\n🎉 All tests completed successfully!")
}

// establishTunnel creates an HTTP CONNECT tunnel to the pico-apiserver
func establishTunnel(apiServer, sessionID, token string) (net.Conn, error) {
	// Extract host from URL
	var host string
	if len(apiServer) > 7 && apiServer[:7] == "http://" {
		host = apiServer[7:]
	} else if len(apiServer) > 8 && apiServer[:8] == "https://" {
		host = apiServer[8:]
		log.Fatal("HTTPS not yet supported in this test tool")
	} else {
		host = apiServer
	}

	// Default to port 8080 if not specified
	hostPort := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		hostPort = net.JoinHostPort(host, "8080")
	}

	log.Printf("Connecting to %s", hostPort)

	// Establish TCP connection
	conn, err := net.DialTimeout("tcp", hostPort, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}

	// Construct HTTP CONNECT request
	connectPath := fmt.Sprintf("/v1/sessions/%s/tunnel", sessionID)
	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\n", connectPath)
	req += fmt.Sprintf("Host: %s\r\n", host)
	if token != "" {
		req += fmt.Sprintf("Authorization: Bearer %s\r\n", token)
	}
	req += "User-Agent: tunnel-test/1.0\r\n"
	req += "\r\n"

	log.Printf("Sending CONNECT request to %s", connectPath)

	// Send CONNECT request
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send CONNECT request: %w", err)
	}

	// Read response
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read CONNECT response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		conn.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CONNECT failed with status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Received response: %s", resp.Status)

	return conn, nil
}

// sshOverTunnel establishes an SSH connection over the tunnel
func sshOverTunnel(conn net.Conn, username, password string) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	log.Printf("Establishing SSH connection as user: %s", username)

	// Create SSH client connection over the tunnel
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, "sandbox", config)
	if err != nil {
		return nil, fmt.Errorf("failed to establish SSH connection: %w", err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)
	return client, nil
}

// executeCommand executes a command over SSH and returns the output
func executeCommand(client *ssh.Client, command string) (stdout, stderr string, exitCode int, err error) {
	session, err := client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Capture stdout and stderr
	var stdoutBuf, stderrBuf []byte
	session.Stdout = &bufferWriter{buf: &stdoutBuf}
	session.Stderr = &bufferWriter{buf: &stderrBuf}

	// Execute command
	err = session.Run(command)

	stdout = string(stdoutBuf)
	stderr = string(stderrBuf)

	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return stdout, stderr, exitErr.ExitStatus(), nil
		}
		return stdout, stderr, -1, err
	}

	return stdout, stderr, 0, nil
}

// bufferWriter is a simple buffer that implements io.Writer
type bufferWriter struct {
	buf *[]byte
}

func (w *bufferWriter) Write(p []byte) (n int, err error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}
