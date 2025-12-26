package picod

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"
)

// JupyterManager manages Jupyter Server and kernel lifecycle
type JupyterManager struct {
	serverCmd    *exec.Cmd
	serverURL    string
	kernelID     string
	wsConn       *websocket.Conn
	mutex        sync.Mutex // Ensures single execution at a time
	token        string
	workspaceDir string
	httpClient   *http.Client
}

// ExecutionResult captures Python execution output
type ExecutionResult struct {
	Output         string `json:"output"`
	Error          string `json:"error"`
	Status         string `json:"status"` // "ok" or "error"
	ExecutionCount int    `json:"execution_count"`
}

// NewJupyterManager creates and initializes Jupyter Server
func NewJupyterManager(workspaceDir string) (*JupyterManager, error) {
	jm := &JupyterManager{
		serverURL:    "http://127.0.0.1:8888",
		token:        generateJupyterToken(),
		workspaceDir: workspaceDir,
		httpClient:   &http.Client{Timeout: 120 * time.Second},
	}

	if err := jm.startJupyterServer(); err != nil {
		return nil, fmt.Errorf("failed to start Jupyter Server: %w", err)
	}

	if err := jm.createKernel(); err != nil {
		return nil, fmt.Errorf("failed to create kernel: %w", err)
	}

	if err := jm.connectWebSocket(); err != nil {
		return nil, fmt.Errorf("failed to connect WebSocket: %w", err)
	}

	klog.Info("Jupyter Server initialized successfully")
	return jm, nil
}

// startJupyterServer launches Jupyter Server process
func (jm *JupyterManager) startJupyterServer() error {
	// Ensure workspace directory exists
	if err := os.MkdirAll(jm.workspaceDir, 0755); err != nil {
		return fmt.Errorf("failed to create workspace directory: %w", err)
	}

	cmd := exec.Command(
		"jupyter-server",
		"--no-browser",
		"--ip=127.0.0.1",
		"--port=8888",
		"--allow-root", // Required for running in container as root
		fmt.Sprintf("--ServerApp.token=%s", jm.token),
		fmt.Sprintf("--ServerApp.root_dir=%s", jm.workspaceDir),
		"--ServerApp.allow_origin=*",
	)

	// Capture output for debugging
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	klog.Infof("Starting Jupyter Server with command: %v", cmd.Args)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start jupyter server: %w", err)
	}

	jm.serverCmd = cmd

	// Wait for server to be ready
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	return jm.waitForServer(ctx)
}

// waitForServer polls until Jupyter Server is responsive
func (jm *JupyterManager) waitForServer(ctx context.Context) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Jupyter Server")
		case <-ticker.C:
			resp, err := http.Get(fmt.Sprintf("%s/api?token=%s", jm.serverURL, jm.token))
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
	}
}

// createKernel creates a persistent Python kernel
func (jm *JupyterManager) createKernel() error {
	reqBody := map[string]string{"name": "python3"}
	bodyBytes, _ := json.Marshal(reqBody)

	resp, err := http.Post(
		fmt.Sprintf("%s/api/kernels?token=%s", jm.serverURL, jm.token),
		"application/json",
		bytes.NewBuffer(bodyBytes),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to create kernel: status %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	jm.kernelID = result["id"].(string)
	klog.Infof("Created kernel: %s", jm.kernelID)
	return nil
}

// connectWebSocket establishes WebSocket connection to kernel
func (jm *JupyterManager) connectWebSocket() error {
	wsURL := fmt.Sprintf("ws://127.0.0.1:8888/api/kernels/%s/channels?token=%s",
		jm.kernelID, jm.token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to dial WebSocket: %w", err)
	}

	jm.wsConn = conn
	klog.Infof("WebSocket connected to kernel %s", jm.kernelID)
	return nil
}

// ExecuteCode executes Python code and returns results (no timeout - blocks until completion)
func (jm *JupyterManager) ExecuteCode(code string) (*ExecutionResult, error) {
	// Requirement 3: Acquire mutex for exclusive execution
	jm.mutex.Lock()
	defer jm.mutex.Unlock()

	result, err := jm.executeViaWebSocket(code)

	// Requirement 2: Soft reset environment using %reset -f
	if resetErr := jm.softReset(); resetErr != nil {
		klog.Errorf("Failed to soft reset kernel: %v", resetErr)
	}

	return result, err
}

// executeViaWebSocket executes code via Jupyter WebSocket (no timeout)
func (jm *JupyterManager) executeViaWebSocket(code string) (*ExecutionResult, error) {

	// Generate message ID
	msgID := uuid.New().String()

	// Create execute_request message
	executeMsg := map[string]interface{}{
		"header": map[string]interface{}{
			"msg_id":   msgID,
			"username": "picod",
			"session":  jm.kernelID,
			"msg_type": "execute_request",
			"version":  "5.3",
		},
		"parent_header": map[string]interface{}{},
		"metadata":      map[string]interface{}{},
		"content": map[string]interface{}{
			"code":             code,
			"silent":           false,
			"store_history":    true,
			"user_expressions": map[string]interface{}{},
			"allow_stdin":      false,
			"stop_on_error":    true,
		},
		"buffers": []interface{}{},
	}

	// Send execute request
	if err := jm.wsConn.WriteJSON(executeMsg); err != nil {
		return nil, fmt.Errorf("failed to send execute request: %w", err)
	}

	// Collect results
	result := &ExecutionResult{Status: "ok"}
	var outputBuffer, errorBuffer strings.Builder
	executionCount := 0

	// Read messages until we get execute_reply
	for {
		var msg map[string]interface{}
		if err := jm.wsConn.ReadJSON(&msg); err != nil {
			return nil, fmt.Errorf("failed to read message: %w", err)
		}

		header, ok := msg["header"].(map[string]interface{})
		if !ok {
			continue
		}

		msgType, ok := header["msg_type"].(string)
		if !ok {
			continue
		}

		content, _ := msg["content"].(map[string]interface{})

		switch msgType {
		case "stream":
			if name, ok := content["name"].(string); ok {
				if text, ok := content["text"].(string); ok {
					if name == "stdout" {
						outputBuffer.WriteString(text)
					} else if name == "stderr" {
						errorBuffer.WriteString(text)
					}
				}
			}

		case "execute_result", "display_data":
			if data, ok := content["data"].(map[string]interface{}); ok {
				if textPlain, ok := data["text/plain"].(string); ok {
					outputBuffer.WriteString(textPlain)
					outputBuffer.WriteString("\n")
				}
			}
			if count, ok := content["execution_count"].(float64); ok {
				executionCount = int(count)
			}

		case "error":
			result.Status = "error"
			if ename, ok := content["ename"].(string); ok {
				errorBuffer.WriteString(ename)
				errorBuffer.WriteString(": ")
			}
			if evalue, ok := content["evalue"].(string); ok {
				errorBuffer.WriteString(evalue)
				errorBuffer.WriteString("\n")
			}
			if traceback, ok := content["traceback"].([]interface{}); ok {
				for _, line := range traceback {
					if lineStr, ok := line.(string); ok {
						errorBuffer.WriteString(lineStr)
						errorBuffer.WriteString("\n")
					}
				}
			}

		case "execute_reply":
			if count, ok := content["execution_count"].(float64); ok {
				executionCount = int(count)
			}
			result.Output = outputBuffer.String()
			result.Error = errorBuffer.String()
			result.ExecutionCount = executionCount
			return result, nil
		}
	}
}

// softReset performs soft reset using %reset -f magic command
func (jm *JupyterManager) softReset() error {
	resetCode := "%reset -f"
	_, err := jm.executeViaWebSocket(resetCode)
	return err
}

// Shutdown gracefully stops Jupyter Server
func (jm *JupyterManager) Shutdown() error {
	if jm.wsConn != nil {
		jm.wsConn.Close()
	}

	if jm.serverCmd != nil && jm.serverCmd.Process != nil {
		if err := jm.serverCmd.Process.Kill(); err != nil {
			return err
		}
	}

	return nil
}

// generateJupyterToken generates a unique token for Jupyter Server
func generateJupyterToken() string {
	return fmt.Sprintf("picod-%d", time.Now().Unix())
}
