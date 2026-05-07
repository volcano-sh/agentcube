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

package e2b

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/volcano-sh/agentcube/pkg/store"
	"k8s.io/klog/v2"
)

// handleCreateSandbox handles POST /sandboxes - Create a new sandbox
func (s *Server) handleCreateSandbox(c *gin.Context) {
	var req NewSandbox
	if err := c.ShouldBindJSON(&req); err != nil {
		klog.Errorf("failed to bind request body: %v", err)
		respondWithError(c, ErrInvalidRequest, "invalid request body")
		return
	}

	// Validate unsupported features for Phase 1
	if req.AutoPause {
		respondWithError(c, ErrInvalidRequest, "auto_pause not supported")
		return
	}

	// Get namespace and api key hash from auth context
	namespace := c.GetString("namespace")
	if namespace == "" {
		namespace = s.config.E2BDefaultNamespace
	}
	apiKeyHash := c.GetString("api_key_hash")

	// Resolve template to name and kind
	_, name, kind, err := ResolveTemplate(req.TemplateID, req.Metadata)
	if err != nil {
		respondWithError(c, ErrInvalidRequest, err.Error())
		return
	}

	klog.Infof("creating sandbox: template=%s, namespace=%s, kind=%s, timeout=%d",
		req.TemplateID, namespace, kind, req.Timeout)

	ctx := c.Request.Context()

	// Call session manager to create/get sandbox
	sandbox, err := s.sessionManager.GetSandboxBySession(ctx, "", namespace, name, kind, req.EnvVars)
	if err != nil {
		klog.Errorf("failed to create sandbox: %v", err)
		code, msg := mapError(err)
		respondWithError(c, code, msg)
		return
	}

	// Generate E2B sandbox ID
	e2bID, err := s.idGenerator.Generate(ctx)
	if err != nil {
		klog.Errorf("failed to generate e2b sandbox id: %v", err)
		respondWithError(c, ErrInternal, "failed to generate sandbox id")
		return
	}

	sandbox.E2BSandboxID = e2bID
	sandbox.APIKeyHash = apiKeyHash
	sandbox.TemplateID = req.TemplateID

	// Ensure ExpiresAt is set for StoreSandbox validation
	if sandbox.ExpiresAt.IsZero() {
		sandbox.ExpiresAt = time.Now().Add(time.Duration(s.config.E2BDefaultTTL) * time.Second)
	}

	// Persist to store - try UpdateSandbox first, fall back to StoreSandbox
	if err := s.storeClient.UpdateSandbox(ctx, sandbox); err != nil {
		if err := s.storeClient.StoreSandbox(ctx, sandbox); err != nil {
			if errors.Is(err, store.ErrIDConflict) {
				// Retry once with a new e2bID on ID conflict
				klog.Warningf("e2b sandbox id conflict, retrying: e2bSandboxID=%s", e2bID)
				newE2bID, genErr := s.idGenerator.Generate(ctx)
				if genErr != nil {
					klog.Errorf("failed to regenerate e2b sandbox id: %v", genErr)
					respondWithError(c, ErrInternal, "failed to generate sandbox id")
					return
				}
				sandbox.E2BSandboxID = newE2bID
				e2bID = newE2bID
				if retryErr := s.storeClient.StoreSandbox(ctx, sandbox); retryErr != nil {
					klog.Errorf("failed to persist sandbox after retry: %v", retryErr)
					respondWithError(c, ErrInternal, "failed to persist sandbox")
					return
				}
			} else {
				klog.Errorf("failed to persist sandbox: %v", err)
				respondWithError(c, ErrInternal, "failed to persist sandbox")
				return
			}
		}
	}

	// Set timeout if specified
	if req.Timeout > 0 {
		expiresAt := CalculateExpiry(req.Timeout)
		if err := s.storeClient.UpdateSandboxTTL(ctx, sandbox.SessionID, expiresAt); err != nil {
			klog.Warningf("failed to update sandbox ttl: %v", err)
		}
	}

	// Convert to E2B response
	response := s.mapper.ToE2BSandbox(sandbox, apiKeyHash, s.config.E2BSandboxDomain)

	klog.Infof("sandbox created successfully: e2bSandboxID=%s", e2bID)
	c.JSON(http.StatusCreated, response)
}

// handleListSandboxes handles GET /sandboxes - List all sandboxes
func (s *Server) handleListSandboxes(c *gin.Context) {
	// Get api key hash from auth context
	apiKeyHash := c.GetString("api_key_hash")

	ctx := c.Request.Context()

	// List sandboxes scoped to the API key
	sandboxes, err := s.storeClient.ListSandboxesByAPIKeyHash(ctx, apiKeyHash)
	if err != nil {
		klog.Errorf("failed to list sandboxes: %v", err)
		respondWithError(c, ErrInternal, "failed to list sandboxes")
		return
	}

	// Convert to E2B response
	response := make([]ListedSandbox, 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		response = append(response, *s.mapper.ToE2BListedSandbox(sandbox, apiKeyHash))
	}

	klog.V(4).Infof("listed %d sandboxes", len(response))
	c.JSON(http.StatusOK, response)
}

// handleGetSandbox handles GET /sandboxes/{id} - Get sandbox details
func (s *Server) handleGetSandbox(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		respondWithError(c, ErrInvalidRequest, "sandbox id is required")
		return
	}

	// Get api key hash from auth context
	apiKeyHash := c.GetString("api_key_hash")

	ctx := c.Request.Context()

	// Get sandbox by E2B sandbox ID
	sandbox, err := s.storeClient.GetSandboxByE2BSandboxID(ctx, sandboxID)
	if err != nil {
		handleStoreError(c, err)
		return
	}

	// Verify ownership
	if sandbox.APIKeyHash != apiKeyHash {
		respondWithError(c, ErrNotFound, "sandbox not found")
		return
	}

	// Convert to E2B response
	response := s.mapper.ToE2BSandboxDetail(sandbox, apiKeyHash, s.config.E2BSandboxDomain)

	klog.V(4).Infof("retrieved sandbox: e2bSandboxID=%s", sandboxID)
	c.JSON(http.StatusOK, response)
}

// handleDeleteSandbox handles DELETE /sandboxes/{id} - Delete a sandbox
func (s *Server) handleDeleteSandbox(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		respondWithError(c, ErrInvalidRequest, "sandbox id is required")
		return
	}

	apiKeyHash := c.GetString("api_key_hash")
	ctx := c.Request.Context()

	// Get sandbox by E2B sandbox ID
	sandbox, err := s.storeClient.GetSandboxByE2BSandboxID(ctx, sandboxID)
	if err != nil {
		handleStoreError(c, err)
		return
	}

	// Verify ownership
	if sandbox.APIKeyHash != apiKeyHash {
		respondWithError(c, ErrNotFound, "sandbox not found")
		return
	}

	// Delete sandbox by session ID
	if err := s.storeClient.DeleteSandboxBySessionID(ctx, sandbox.SessionID); err != nil {
		klog.Errorf("failed to delete sandbox: %v", err)
		respondWithError(c, ErrInternal, "failed to delete sandbox")
		return
	}

	klog.Infof("sandbox deleted successfully: e2bSandboxID=%s, sessionID=%s", sandboxID, sandbox.SessionID)
	c.Status(http.StatusNoContent)
}

// handleSetTimeout handles POST /sandboxes/{id}/timeout - Set sandbox timeout
func (s *Server) handleSetTimeout(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		respondWithError(c, ErrInvalidRequest, "sandbox id is required")
		return
	}

	var req TimeoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		klog.Errorf("failed to bind request body: %v", err)
		respondWithError(c, ErrInvalidRequest, "invalid request body")
		return
	}

	// Validate timeout
	if req.Timeout <= 0 {
		respondWithError(c, ErrInvalidRequest, "timeout must be greater than 0")
		return
	}

	apiKeyHash := c.GetString("api_key_hash")
	ctx := c.Request.Context()

	// Get sandbox by E2B sandbox ID
	sandbox, err := s.storeClient.GetSandboxByE2BSandboxID(ctx, sandboxID)
	if err != nil {
		handleStoreError(c, err)
		return
	}

	// Verify ownership
	if sandbox.APIKeyHash != apiKeyHash {
		respondWithError(c, ErrNotFound, "sandbox not found")
		return
	}

	// Calculate new expiration time from now
	expiresAt := time.Now().Add(time.Duration(req.Timeout) * time.Second)

	// Update sandbox TTL atomically
	if err := s.storeClient.UpdateSandboxTTL(ctx, sandbox.SessionID, expiresAt); err != nil {
		klog.Errorf("failed to update sandbox timeout: %v", err)
		respondWithError(c, ErrInternal, "failed to set timeout")
		return
	}

	klog.Infof("sandbox timeout updated: e2bSandboxID=%s, timeout=%d, expiresAt=%v",
		sandboxID, req.Timeout, expiresAt)
	c.Status(http.StatusNoContent)
}

// handleRefreshSandbox handles POST /sandboxes/{id}/refreshes - Refresh sandbox keepalive
func (s *Server) handleRefreshSandbox(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		respondWithError(c, ErrInvalidRequest, "sandbox id is required")
		return
	}

	var req RefreshRequest
	// Bind JSON but allow empty body
	if err := c.ShouldBindJSON(&req); err != nil {
		// If binding fails, continue with default empty request
		klog.V(4).Infof("refresh request without body or with empty body: e2bSandboxID=%s", sandboxID)
	}

	apiKeyHash := c.GetString("api_key_hash")
	ctx := c.Request.Context()

	// Get sandbox by E2B sandbox ID
	sandbox, err := s.storeClient.GetSandboxByE2BSandboxID(ctx, sandboxID)
	if err != nil {
		handleStoreError(c, err)
		return
	}

	// Verify ownership
	if sandbox.APIKeyHash != apiKeyHash {
		respondWithError(c, ErrNotFound, "sandbox not found")
		return
	}

	// If timeout is provided, extend expiration time
	if req.Timeout > 0 {
		expiresAt := time.Now().Add(time.Duration(req.Timeout) * time.Second)
		if err := s.storeClient.UpdateSandboxTTL(ctx, sandbox.SessionID, expiresAt); err != nil {
			klog.Errorf("failed to update sandbox on refresh: %v", err)
			respondWithError(c, ErrInternal, "failed to refresh sandbox")
			return
		}
		klog.Infof("sandbox refreshed with timeout: e2bSandboxID=%s, timeout=%d", sandboxID, req.Timeout)
	} else {
		klog.V(4).Infof("sandbox refreshed (activity updated): e2bSandboxID=%s", sandboxID)
	}

	// Update last activity time
	if err := s.storeClient.UpdateSessionLastActivity(ctx, sandbox.SessionID, time.Now()); err != nil {
		klog.Warningf("failed to update session last activity: %v", err)
		// Don't fail the request if activity update fails
	}

	c.Status(http.StatusNoContent)
}
