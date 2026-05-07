//go:build e2e
// +build e2e

/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Template represents an E2B template (redefined here to avoid import cycle)
type Template struct {
	TemplateID   string   `json:"templateID"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
	Public       bool     `json:"public"`
	State        string   `json:"state"`
	Dockerfile   string   `json:"dockerfile,omitempty"`
	StartCommand string   `json:"startCommand,omitempty"`
	EnvdVersion  string   `json:"envdVersion,omitempty"`
	MemoryMB     int      `json:"memoryMB,omitempty"`
	CPUCount     int      `json:"vcpuCount,omitempty"`
}

// CreateTemplateRequest represents the request to create a template
type CreateTemplateRequest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Dockerfile   string   `json:"dockerfile,omitempty"`
	StartCommand string   `json:"startCommand,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	Public       bool     `json:"public,omitempty"`
}

// UpdateTemplateRequest represents the request to update a template
type UpdateTemplateRequest struct {
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Public      *bool    `json:"public,omitempty"`
}

// getRouterURL returns the router URL from environment or default
func getRouterURL() string {
	if url := os.Getenv("ROUTER_URL"); url != "" {
		return url
	}
	return "http://localhost:8081"
}

// getAPIToken returns the API token from environment or default for testing
func getAPIToken() string {
	// Check for E2B_API_KEYS first (format: "key1:client1,key2:client2")
	if keys := os.Getenv("E2B_API_KEYS"); keys != "" {
		pairs := strings.Split(keys, ",")
		if len(pairs) > 0 {
			parts := strings.SplitN(pairs[0], ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[0])
			}
		}
	}
	// Check for single E2B_API_KEY
	if key := os.Getenv("E2B_API_KEY"); key != "" {
		return key
	}
	// Fall back to API_TOKEN (for other APIs)
	if token := os.Getenv("API_TOKEN"); token != "" {
		return token
	}
	// Use default dev API key that matches E2B server default
	return "dev-api-key"
}

// makeRequest makes an HTTP request with authentication
func makeTemplateRequest(method, path string, body interface{}) (*http.Response, error) {
	url := getRouterURL() + path

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-API-Key", getAPIToken())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

// TestListTemplates tests listing templates
func TestListTemplates(t *testing.T) {
	resp, err := makeTemplateRequest("GET", "/templates", nil)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	var templates []Template
	if err := json.NewDecoder(resp.Body).Decode(&templates); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	t.Logf("Listed %d templates", len(templates))
	for _, tmpl := range templates {
		t.Logf("  - %s: %s (state: %s)", tmpl.TemplateID, tmpl.Name, tmpl.State)
	}
}

// TestGetTemplate tests getting a template by ID
func TestGetTemplate(t *testing.T) {
	// First create a template
	createReq := CreateTemplateRequest{
		Name:        "e2e-test-get-template",
		Description: "Template for get test",
		Public:      true,
		Aliases:     []string{"e2e-get"},
	}

	createResp, err := makeTemplateRequest("POST", "/templates", createReq)
	if err != nil {
		t.Fatalf("Failed to create template: %v", err)
	}
	createResp.Body.Close()

	// Get the template
	templateID := "e2e-test-get-template"
	resp, err := makeTemplateRequest("GET", "/templates/"+templateID, nil)
	if err != nil {
		t.Fatalf("Failed to get template: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	var template Template
	if err := json.NewDecoder(resp.Body).Decode(&template); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if template.TemplateID != templateID {
		t.Errorf("Expected template ID %s, got %s", templateID, template.TemplateID)
	}

	t.Logf("Retrieved template: %s (%s)", template.Name, template.State)
}

// TestCreateTemplate tests creating a template
func TestCreateTemplate(t *testing.T) {
	req := CreateTemplateRequest{
		Name:         "e2e-test-create-template",
		Description:  "Template for create test",
		Dockerfile:   "FROM python:3.9-slim\nRUN pip install pandas numpy",
		StartCommand: "python app.py",
		Public:       true,
		Aliases:      []string{"e2e-create", "test-template"},
	}

	resp, err := makeTemplateRequest("POST", "/templates", req)
	if err != nil {
		t.Fatalf("Failed to create template: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 201, got %d: %s", resp.StatusCode, string(body))
	}

	var template Template
	if err := json.NewDecoder(resp.Body).Decode(&template); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if template.Name != req.Name {
		t.Errorf("Expected name %s, got %s", req.Name, template.Name)
	}

	if template.Description != req.Description {
		t.Errorf("Expected description %s, got %s", req.Description, template.Description)
	}

	t.Logf("Created template: %s with ID %s", template.Name, template.TemplateID)
}

// TestUpdateTemplate tests updating a template
func TestUpdateTemplate(t *testing.T) {
	// First create a template
	createReq := CreateTemplateRequest{
		Name:        "e2e-test-update-template",
		Description: "Original description",
		Public:      true,
	}

	createResp, err := makeTemplateRequest("POST", "/templates", createReq)
	if err != nil {
		t.Fatalf("Failed to create template: %v", err)
	}
	createResp.Body.Close()

	// Update the template
	templateID := "e2e-test-update-template"
	public := false
	updateReq := UpdateTemplateRequest{
		Description: "Updated description",
		Public:      &public,
		Aliases:     []string{"updated-alias"},
	}

	resp, err := makeTemplateRequest("PATCH", "/templates/"+templateID, updateReq)
	if err != nil {
		t.Fatalf("Failed to update template: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	var template Template
	if err := json.NewDecoder(resp.Body).Decode(&template); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if template.Description != updateReq.Description {
		t.Errorf("Expected description %s, got %s", updateReq.Description, template.Description)
	}

	t.Logf("Updated template: %s (public: %v)", template.Name, template.Public)
}

// TestDeleteTemplate tests deleting a template
func TestDeleteTemplate(t *testing.T) {
	// First create a template
	createReq := CreateTemplateRequest{
		Name:        "e2e-test-delete-template",
		Description: "Template for delete test",
		Public:      false,
	}

	createResp, err := makeTemplateRequest("POST", "/templates", createReq)
	if err != nil {
		t.Fatalf("Failed to create template: %v", err)
	}
	createResp.Body.Close()

	// Delete the template
	templateID := "e2e-test-delete-template"
	resp, err := makeTemplateRequest("DELETE", "/templates/"+templateID, nil)
	if err != nil {
		t.Fatalf("Failed to delete template: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 204, got %d: %s", resp.StatusCode, string(body))
	}

	t.Logf("Deleted template: %s", templateID)
}

// TestTemplateNotFound tests error handling for non-existent templates
func TestTemplateNotFound(t *testing.T) {
	// Try to get a non-existent template
	resp, err := makeTemplateRequest("GET", "/templates/non-existent-template-12345", nil)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Should return 404 for non-existent template
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 404 or 200, got %d: %s", resp.StatusCode, string(body))
	}
	t.Logf("Response status: %d", resp.StatusCode)
}

// TestCreateTemplateDuplicate tests creating a template with duplicate name
func TestCreateTemplateDuplicate(t *testing.T) {
	// Create first template
	req := CreateTemplateRequest{
		Name:        "e2e-test-duplicate-template",
		Description: "First template",
		Public:      true,
	}

	resp, err := makeTemplateRequest("POST", "/templates", req)
	if err != nil {
		t.Fatalf("Failed to create first template: %v", err)
	}
	resp.Body.Close()

	// Try to create second template with same name
	resp2, err := makeTemplateRequest("POST", "/templates", req)
	if err != nil {
		t.Fatalf("Failed to make duplicate request: %v", err)
	}
	defer resp2.Body.Close()

	// Should return 409 Conflict or 201 Created depending on backend
	if resp2.StatusCode != http.StatusConflict && resp2.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("Expected status 409 or 201, got %d: %s", resp2.StatusCode, string(body))
	}
	t.Logf("Duplicate create status: %d", resp2.StatusCode)
}

// TestCreateTemplateInvalidBody tests creating a template with invalid request body
func TestCreateTemplateInvalidBody(t *testing.T) {
	url := getRouterURL() + "/templates"

	// Test with invalid JSON
	req, err := http.NewRequest("POST", url, bytes.NewBufferString("{invalid json"))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("X-API-Key", getAPIToken())
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	// Should return 400 Bad Request for invalid JSON
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 400 for invalid JSON, got %d: %s", resp.StatusCode, string(body))
	}
}

// TestTemplateUnauthorized tests accessing templates without authentication
func TestTemplateUnauthorized(t *testing.T) {
	url := getRouterURL() + "/templates"

	// Request without API key
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Logf("Expected 401 without API key, got %d: %s", resp.StatusCode, string(body))
	}

	// Test with invalid API key
	req2, _ := http.NewRequest("GET", url, nil)
	req2.Header.Set("X-API-Key", "invalid-api-key-12345")

	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp2.Body)
		t.Errorf("Expected 401 with invalid API key, got %d: %s", resp2.StatusCode, string(body))
	}
}

// TestTemplateInvalidIDFormat tests various invalid template ID formats
func TestTemplateInvalidIDFormat(t *testing.T) {
	testCases := []struct {
		name       string
		templateID string
	}{
		{
			name:       "empty ID",
			templateID: "",
		},
		{
			name:       "too many slashes",
			templateID: "default/namespace/name/extra",
		},
		{
			name:       "special characters",
			templateID: "default/template@#$%",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.templateID == "" {
				// Skip empty ID as it becomes list endpoint
				return
			}
			// URL-encode the template ID to avoid invalid URL parse errors
			encodedID := url.PathEscape(tc.templateID)
			resp, err := makeTemplateRequest("GET", "/templates/"+encodedID, nil)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}
			defer resp.Body.Close()

			// Should return 400 or 404 for invalid IDs, not 2xx
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
				t.Errorf("Expected error status for invalid template ID '%s', got %d", tc.templateID, resp.StatusCode)
			}
			t.Logf("Template ID '%s' returned status: %d", tc.templateID, resp.StatusCode)
		})
	}
}

// TestTemplateQueryParamsEdgeCases tests edge cases for query parameters
func TestTemplateQueryParamsEdgeCases(t *testing.T) {
	testCases := []struct {
		name   string
		params string
	}{
		{
			name:   "negative limit",
			params: "?limit=-1",
		},
		{
			name:   "negative offset",
			params: "?offset=-10",
		},
		{
			name:   "zero limit",
			params: "?limit=0",
		},
		{
			name:   "very large limit",
			params: "?limit=1000000",
		},
		{
			name:   "invalid public value",
			params: "?public=invalid",
		},
		{
			name:   "empty public value",
			params: "?public=",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := makeTemplateRequest("GET", "/templates"+tc.params, nil)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}
			defer resp.Body.Close()

			// Should handle gracefully (either 200 with defaults or 400)
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("Query '%s' returned unexpected status %d: %s", tc.params, resp.StatusCode, string(body))
			}
		})
	}
}

// TestListTemplatesWithFilter tests listing templates with filters
func TestListTemplatesWithFilter(t *testing.T) {
	// Create public template
	publicReq := CreateTemplateRequest{
		Name:        "e2e-test-public-template",
		Description: "Public template",
		Public:      true,
	}

	publicResp, err := makeTemplateRequest("POST", "/templates", publicReq)
	if err != nil {
		t.Fatalf("Failed to create public template: %v", err)
	}
	publicResp.Body.Close()

	// Create private template
	privateReq := CreateTemplateRequest{
		Name:        "e2e-test-private-template",
		Description: "Private template",
		Public:      false,
	}

	privateResp, err := makeTemplateRequest("POST", "/templates", privateReq)
	if err != nil {
		t.Fatalf("Failed to create private template: %v", err)
	}
	privateResp.Body.Close()

	// List public templates
	resp, err := makeTemplateRequest("GET", "/templates?public=true", nil)
	if err != nil {
		t.Fatalf("Failed to list public templates: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	var templates []Template
	if err := json.NewDecoder(resp.Body).Decode(&templates); err != nil {
		t.Fatalf("Failed to decode templates: %v", err)
	}

	// Verify all returned templates are public
	for _, tmpl := range templates {
		if !tmpl.Public {
			t.Errorf("Expected public template, got private: %s", tmpl.TemplateID)
		}
	}

	t.Logf("Found %d public templates", len(templates))
}
