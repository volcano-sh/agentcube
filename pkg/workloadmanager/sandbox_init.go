package workloadmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SandboxInitRequest represents the request payload for sandbox initialization
type SandboxInitRequest struct {
	SessionID string            `json:"sessionId"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// SandboxInitResponse represents the response from sandbox initialization
type SandboxInitResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// InitCodeInterpreterSandbox initializes a code interpreter sandbox by calling its /init endpoint
func (s *Server) InitCodeInterpreterSandbox(ctx context.Context, endpoint, sessionID, publicKey string, metadata map[string]string) error {
	// Generate JWT token for authentication
	claims := map[string]interface{}{
		"sessionId":          sessionID,
		"purpose":            "sandbox_init",
		"session_public_key": publicKey,
	}

	token, err := s.jwtManager.GenerateToken(claims)
	if err != nil {
		return fmt.Errorf("failed to generate JWT token: %w", err)
	}

	// Prepare request payload
	initRequest := SandboxInitRequest{
		SessionID: sessionID,
		Metadata:  metadata,
	}

	requestBody, err := json.Marshal(initRequest)
	if err != nil {
		return fmt.Errorf("failed to marshal init request: %w", err)
	}

	// Construct init endpoint URL
	initURL := fmt.Sprintf("%s/init", endpoint)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, initURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send init request to sandbox: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sandbox init failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var initResponse SandboxInitResponse
	if err := json.Unmarshal(body, &initResponse); err != nil {
		// If we can't parse the response but got 200, consider it successful
		return nil
	}

	return nil
}
