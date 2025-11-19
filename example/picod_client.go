package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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
)

var (
	// accessToken 是用于认证的 Bearer token
	// 通过 PICOD_ACCESS_TOKEN 环境变量设置
	accessToken string
)

// ExecuteRequest 命令执行请求
type ExecuteRequest struct {
	Command    string            `json:"command"`
	Timeout    float64           `json:"timeout,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

// ExecuteResponse 命令执行响应
type ExecuteResponse struct {
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
	ExitCode int     `json:"exit_code"`
	Duration float64 `json:"duration"`
}

// FileInfo 文件信息响应
type FileInfo struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified"`
}

func main() {
	log.Println("===========================================")
	log.Println("PicoD REST API Direct Test")
	log.Println("===========================================")
	log.Println()

	picodURL := getEnv("PICOD_URL", defaultPicoDURL)
	accessToken = os.Getenv("PICOD_ACCESS_TOKEN")

	log.Printf("Configuration:")
	log.Printf("  PicoD URL: %s", picodURL)

	if accessToken == "" {
		log.Println("⚠️  WARNING: PICOD_ACCESS_TOKEN environment variable not set")
		log.Println("   Attempting to proceed without authentication token")
		log.Println()
	} else {
		log.Printf("✅ Access token loaded: %s...", accessToken[:10])
		log.Println()
	}

	// Step 0: Health check
	log.Println("Step 0: Health check...")
	if err := healthCheck(picodURL); err != nil {
		log.Fatalf("Health check failed: %v", err)
	}
	log.Println("✅ PicoD server is healthy")
	log.Println()

	// Step 1: Execute basic commands
	log.Println("Step 1: Executing basic test commands...")
	commands := []string{
		"whoami",
		"pwd",
		"echo 'Hello from PicoD REST API!'",
		"python3 --version",
		"uname -a",
	}

	for i, cmd := range commands {
		log.Printf("   [%d/%d] Executing: %s", i+1, len(commands), cmd)
		output, err := executeCommand(picodURL, cmd)
		if err != nil {
			log.Printf("      ⚠️  Command failed: %v", err)
			continue
		}
		log.Printf("      Output: %s", strings.TrimSpace(output))
	}
	log.Println()

	// Step 2: Upload file via multipart
	log.Println("Step 2: Uploading file via multipart/form-data...")
	uploadContent := "Hello from PicoD!\nThis file was uploaded via REST API multipart."
	if err := uploadFileMultipart(picodURL, "/workspace/upload.txt", uploadContent); err != nil {
		log.Fatalf("Failed to upload file: %v", err)
	}
	log.Println("✅ File uploaded to /workspace/upload.txt")
	log.Println()

	// Step 3: Verify uploaded file
	log.Println("Step 3: Verifying uploaded file...")
	output, err := executeCommand(picodURL, "cat /workspace/upload.txt")
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}
	log.Printf("   File content: %s", strings.TrimSpace(output))
	log.Println()

	// Step 4: Write Python script via JSON+Base64
	log.Println("Step 4: Writing Python script via JSON+Base64...")
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

with open('/workspace/output.json', 'w') as f:
    json.dump(output_data, f, indent=2)

print(f"Generated {n} Fibonacci numbers")
print(f"Sum: {sum(fibonacci)}")
`

	if err := uploadFileJSON(picodURL, "/workspace/fibonacci.py", pythonScript); err != nil {
		log.Fatalf("Failed to write Python script: %v", err)
	}
	log.Println("✅ Python script written to /workspace/fibonacci.py")
	log.Println()

	// Step 5: Execute Python script
	log.Println("Step 5: Executing Python script...")
	output, err = executeCommand(picodURL, "python3 /workspace/fibonacci.py")
	if err != nil {
		log.Fatalf("Failed to execute Python script: %v", err)
	}
	log.Printf("   Script output:\n%s", indentOutput(output))
	log.Println()

	// Step 6: Download generated file
	log.Println("Step 6: Downloading generated output file...")
	localOutputPath := "/tmp/picod_output.json"
	if err := downloadFile(picodURL, "/workspace/output.json", localOutputPath); err != nil {
		log.Fatalf("Failed to download output file: %v", err)
	}
	log.Printf("✅ Output file downloaded to %s", localOutputPath)
	log.Println()

	// Step 7: Verify downloaded file
	log.Println("Step 7: Verifying downloaded file...")
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
	log.Println("🎉 All tests passed successfully!")
	log.Println("===========================================")
	log.Println()
	log.Println("Summary:")
	log.Println("  ✅ Health check passed")
	log.Println("  ✅ Basic commands executed")
	log.Println("  ✅ File uploaded via multipart")
	log.Println("  ✅ File written via JSON+Base64")
	log.Println("  ✅ Python script executed")
	log.Println("  ✅ Output file downloaded")
	log.Println("  ✅ Downloaded file verified")
	log.Println()
}

// healthCheck 执行健康检查
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

// executeCommand 执行命令
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
	if accessToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	}

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

// uploadFileMultipart 通过 multipart/form-data 上传文件
func uploadFileMultipart(baseURL, remotePath, content string) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// 添加 path 字段
	if err := writer.WriteField("path", remotePath); err != nil {
		return err
	}

	// 添加 file 字段
	part, err := writer.CreateFormFile("file", "upload.txt")
	if err != nil {
		return err
	}
	if _, err := part.Write([]byte(content)); err != nil {
		return err
	}

	// 添加 mode 字段
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
	if accessToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
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

	return nil
}

// uploadFileJSON 通过 JSON+Base64 上传文件
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
	if accessToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
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

	return nil
}

// downloadFile 下载文件
func downloadFile(baseURL, remotePath, localPath string) error {
	// 移除前导 /
	cleanPath := strings.TrimPrefix(remotePath, "/")

	httpReq, err := http.NewRequest("GET", fmt.Sprintf("%s/api/files/%s", baseURL, cleanPath), nil)
	if err != nil {
		return err
	}

	if accessToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
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

	// 创建本地文件
	out, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// getEnv 获取环境变量，如果不存在则返回默认值
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// indentOutput 为输出的每一行添加缩进
func indentOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var indented []string
	for _, line := range lines {
		indented = append(indented, "   "+line)
	}
	return strings.Join(indented, "\n")
}

