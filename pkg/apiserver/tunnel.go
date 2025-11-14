package apiserver

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/ssh"
)

// handleTunnel handles HTTP CONNECT requests, establishing a transparent SSH tunnel to the sandbox pod
// This is the core functionality: acting as a transparent proxy between client and sandbox pod
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sandboxID := vars["sandboxId"]

	// Check if sandbox exists
	sandbox := s.sandboxStore.Get(sandboxID)
	if sandbox == nil {
		http.Error(w, "Sandbox not found", http.StatusNotFound)
		return
	}

	// Extract user information from context for authorization
	_, _, _, serviceAccountName := extractUserInfo(r)
	if serviceAccountName == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Check if user has access to this sandbox
	if !s.checkSandboxAccess(sandbox, serviceAccountName) {
		http.Error(w, "Forbidden: You don't have permission to access this sandbox", http.StatusForbidden)
		return
	}

	// Check if method is CONNECT
	if r.Method != http.MethodConnect {
		http.Error(w, "Method not allowed, use CONNECT", http.StatusMethodNotAllowed)
		return
	}

	// Get sandbox pod IP and SSH port
	podIP, err := s.k8sClient.GetSandboxPodIP(r.Context(), sandbox.Namespace, sandbox.SandboxName)
	if err != nil {
		log.Printf("Failed to get pod IP for sandbox %s: %v", sandboxID, err)
		http.Error(w, "Sandbox not ready", http.StatusServiceUnavailable)
		return
	}

	// Connect to sandbox pod SSH service
	sshAddr := net.JoinHostPort(podIP, strconv.Itoa(s.config.SSHPort))
	log.Printf("Establishing SSH tunnel to %s for sandbox %s", sshAddr, sandboxID)

	backendConn, err := net.DialTimeout("tcp", sshAddr, 10*time.Second)
	if err != nil {
		log.Printf("Failed to connect to SSH backend %s: %v", sshAddr, err)
		http.Error(w, "Failed to connect to sandbox", http.StatusBadGateway)
		return
	}

	// Hijack HTTP connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		backendConn.Close()
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		backendConn.Close()
		log.Printf("Failed to hijack connection: %v", err)
		return
	}

	// Send HTTP 200 Connection Established response
	response := "HTTP/1.1 200 Connection Established\r\n\r\n"
	if _, err := clientConn.Write([]byte(response)); err != nil {
		clientConn.Close()
		backendConn.Close()
		log.Printf("Failed to write CONNECT response: %v", err)
		return
	}

	log.Printf("HTTP CONNECT tunnel established for sandbox %s", sandboxID)

	// Update sandbox last activity time
	sandbox.LastActivityAt = time.Now()
	s.sandboxStore.Set(sandboxID, sandbox)

	// Start bidirectional transparent proxy with proper synchronization
	var wg sync.WaitGroup
	wg.Add(2)

	// Client -> Backend
	go func() {
		defer wg.Done()
		// update sandbox last activity timestamp
		// TODO: improve here to reduce the processing latency of the cmd command
		err := s.k8sClient.UpdateSandboxLastActivityWithPatch(r.Context(), sandbox.Namespace, sandbox.SandboxName, sandbox.LastActivityAt)
		if err != nil {
			log.Printf("Failed to update last activity time for sandbox %s, err %v", sandbox.SandboxName, err)
		}
		s.proxyDataOneWay(backendConn, clientConn, sandboxID, "client->backend")
	}()

	// Backend -> Client
	go func() {
		defer wg.Done()
		s.proxyDataOneWay(clientConn, backendConn, sandboxID, "backend->client")
	}()

	// Wait for both directions to complete
	wg.Wait()

	// Close connections after both directions are done
	clientConn.Close()
	backendConn.Close()

	log.Printf("HTTP CONNECT tunnel closed for sandbox %s", sandboxID)
}

// proxyDataOneWay forwards data in one direction without closing connections
// Connection closing is handled by the caller to avoid double-close issues
func (s *Server) proxyDataOneWay(dst io.Writer, src io.Reader, sandboxID, direction string) {
	written, err := io.Copy(dst, src)
	if err != nil {
		log.Printf("Proxy %s for sandbox %s closed with error (transferred %d bytes): %v",
			direction, sandboxID, written, err)
	} else {
		log.Printf("Proxy %s for sandbox %s closed gracefully (transferred %d bytes)",
			direction, sandboxID, written)
	}

	// Attempt TCP half-close if supported (close write side but keep read side open)
	// This allows the other direction to finish sending remaining data
	if tcpConn, ok := dst.(*net.TCPConn); ok {
		tcpConn.CloseWrite()
	}
}

// SSHClient encapsulates SSH connection to sandbox pod
// This structure is used for direct SSH operations (if server-side command execution is needed)
type SSHClient struct {
	client *ssh.Client
	config *ssh.ClientConfig
}

// NewSSHClient creates a new SSH client connection to the specified pod
func NewSSHClient(ctx context.Context, host string, port int, username string, password string) (*SSHClient, error) {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Note: Production should verify host key
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SSH: %w", err)
	}

	return &SSHClient{
		client: client,
		config: config,
	}, nil
}

// ExecuteCommand executes a command in an SSH session
func (c *SSHClient) ExecuteCommand(ctx context.Context, command string) (stdout, stderr string, exitCode int, err error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Capture stdout and stderr
	var stdoutBuf, stderrBuf []byte
	session.Stdout = &writeBuffer{buf: &stdoutBuf}
	session.Stderr = &writeBuffer{buf: &stderrBuf}

	// Execute command
	if err := session.Run(command); err != nil {
		// SSH command failed, try to extract exit code
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return string(stdoutBuf), string(stderrBuf), exitErr.ExitStatus(), nil
		}
		return string(stdoutBuf), string(stderrBuf), -1, err
	}

	return string(stdoutBuf), string(stderrBuf), 0, nil
}

// Close closes the SSH connection
func (c *SSHClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// writeBuffer is a simple byte buffer that implements io.Writer
type writeBuffer struct {
	buf *[]byte
}

func (w *writeBuffer) Write(p []byte) (n int, err error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}
