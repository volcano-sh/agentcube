package picoapiserver

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/ssh"
)

// handleTunnel handles HTTP CONNECT requests, establishing a transparent SSH tunnel to the sandbox pod
// This is the core functionality: acting as a transparent proxy between client and sandbox pod
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	// Check if session exists
	session := s.sessionStore.Get(sessionID)
	if session == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Check if method is CONNECT
	if r.Method != http.MethodConnect {
		http.Error(w, "Method not allowed, use CONNECT", http.StatusMethodNotAllowed)
		return
	}

	// Get sandbox pod IP and SSH port
	podIP, err := s.k8sClient.GetSandboxPodIP(r.Context(), session.SandboxName)
	if err != nil {
		log.Printf("Failed to get pod IP for session %s: %v", sessionID, err)
		http.Error(w, "Sandbox not ready", http.StatusServiceUnavailable)
		return
	}

	// Connect to sandbox pod SSH service
	sshAddr := net.JoinHostPort(podIP, strconv.Itoa(s.config.SSHPort))
	log.Printf("Establishing SSH tunnel to %s for session %s", sshAddr, sessionID)

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

	log.Printf("HTTP CONNECT tunnel established for session %s", sessionID)

	// Start bidirectional transparent proxy
	go s.proxyData(clientConn, backendConn, sessionID, "client->backend")
	go s.proxyData(backendConn, clientConn, sessionID, "backend->client")

	// Update session last activity time
	session.LastActivityAt = time.Now()
	s.sessionStore.Set(sessionID, session)
}

// proxyData transparently forwards data between two connections
func (s *Server) proxyData(dst io.WriteCloser, src io.ReadCloser, sessionID, direction string) {
	defer dst.Close()
	defer src.Close()

	written, err := io.Copy(dst, src)
	if err != nil {
		log.Printf("Proxy %s for session %s closed with error (transferred %d bytes): %v",
			direction, sessionID, written, err)
	} else {
		log.Printf("Proxy %s for session %s closed gracefully (transferred %d bytes)",
			direction, sessionID, written)
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
