package apiserver

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// handleHealth handles health check requests
func (s *Server) handleHealth(c *gin.Context) {
	respondJSON(c, http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// handleCreateSandbox handles sandbox creation requests
func (s *Server) handleCreateSandbox(c *gin.Context) {
	var req CreateSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	// Set default values
	if req.TTL == 0 {
		req.TTL = 3600
	}
	if req.TTL < 60 || req.TTL > 28800 {
		respondError(c, http.StatusBadRequest, "INVALID_TTL", "TTL must be between 60 and 28800 seconds")
		return
	}

	// Extract user information from context
	userToken, userNamespace, serviceAccount, serviceAccountName := extractUserInfo(c)

	if userToken == "" || userNamespace == "" || serviceAccountName == "" {
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unable to extract user credentials")
		return
	}

	// Generate sandbox ID
	sandboxID := uuid.New().String()

	// Calculate sandbox name and namespace before creating
	sandboxName := "sandbox-" + sandboxID[:8]
	namespace := userNamespace

	// CRITICAL: Register watcher BEFORE creating sandbox
	// This ensures we don't miss the Running state notification
	resultChan := s.sandboxController.WatchSandboxOnce(c.Request.Context(), namespace, sandboxName)

	// Get creation time BEFORE creating sandbox to ensure consistency
	now := time.Now()

	// Create sandbox using user's K8s client
	userClient, clientErr := s.k8sClient.GetOrCreateUserK8sClient(userToken, userNamespace, serviceAccountName)
	if clientErr != nil {
		respondError(c, http.StatusInternalServerError, "CLIENT_CREATION_FAILED", clientErr.Error())
		return
	}
	_, err := userClient.CreateSandbox(c.Request.Context(), sandboxID, sandboxName, req.Image, req.SSHPublicKey, s.config.RuntimeClassName, req.TTL, req.Metadata, now, serviceAccountName)
	if err != nil {
		respondError(c, http.StatusForbidden, "SANDBOX_CREATE_FAILED",
			fmt.Sprintf("Failed to create sandbox (service account: %s, namespace: %s): %v", serviceAccount, userNamespace, err))
		return
	}

	select {
	case result := <-resultChan:
		// Convert the raw sandbox CRD to internal Sandbox structure
		sandbox, err := convertTypedSandboxToSandbox(result.Sandbox)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "CONVERSION_FAILED",
				fmt.Sprintf("Failed to convert sandbox: %v", err))
			return
		}

		// The store will be updated by the informer when the CRD is created
		// but we set it here for immediate response consistency.
		s.sandboxStore.Set(sandboxID, sandbox)
		respondJSON(c, http.StatusOK, sandbox)
		return
	case <-time.After(time.Duration(req.TTL) * time.Second):
		respondError(c, http.StatusInternalServerError, "SANDBOX_TIMEOUT", "Sandbox creation timed out")
		return
	}
}

// handleListSandboxes handles listing all sandboxes requests
func (s *Server) handleListSandboxes(c *gin.Context) {
	// Extract user information from context
	_, _, _, serviceAccountName := extractUserInfo(c)

	if serviceAccountName == "" {
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unable to extract user credentials")
		return
	}

	// Get limit and offset from query parameters
	limit := getIntQueryParam(c, "limit", 50)
	offset := getIntQueryParam(c, "offset", 0)

	if limit < 1 || limit > 100 {
		respondError(c, http.StatusBadRequest, "INVALID_LIMIT", "Limit must be between 1 and 100")
		return
	}

	// Get all sandboxes from store
	allSandboxes := s.sandboxStore.List()

	// Filter sandboxes: users can only see their own sandboxes
	var filteredSandboxes []*Sandbox
	for _, sandbox := range allSandboxes {
		if sandbox.CreatorServiceAccount == serviceAccountName {
			filteredSandboxes = append(filteredSandboxes, sandbox)
		}
	}

	total := len(filteredSandboxes)

	// Apply pagination
	start := offset
	end := offset + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	sandboxes := filteredSandboxes[start:end]

	response := map[string]interface{}{
		"sandboxes": sandboxes,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	}

	respondJSON(c, http.StatusOK, response)
}

// handleGetSandbox handles getting a single sandbox request
func (s *Server) handleGetSandbox(c *gin.Context) {
	sandboxID := c.Param("sandboxId")

	sandbox := s.sandboxStore.Get(sandboxID)
	if sandbox == nil {
		respondError(c, http.StatusNotFound, "SANDBOX_NOT_FOUND", "Sandbox not found or expired")
		return
	}

	// Extract user information from context
	_, _, _, serviceAccountName := extractUserInfo(c)

	if serviceAccountName == "" {
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unable to extract user credentials")
		return
	}

	// Check if user has access to this sandbox
	if !s.checkSandboxAccess(sandbox, serviceAccountName) {
		respondError(c, http.StatusForbidden, "FORBIDDEN", "You don't have permission to access this sandbox")
		return
	}

	respondJSON(c, http.StatusOK, sandbox)
}

// handleDeleteSandbox handles sandbox deletion requests
func (s *Server) handleDeleteSandbox(c *gin.Context) {
	sandboxID := c.Param("sandboxId")

	sandbox := s.sandboxStore.Get(sandboxID)
	if sandbox == nil {
		respondError(c, http.StatusNotFound, "SANDBOX_NOT_FOUND", "Sandbox not found or expired")
		return
	}

	// Extract user information from context
	userToken, userNamespace, serviceAccount, serviceAccountName := extractUserInfo(c)

	if userToken == "" || userNamespace == "" || serviceAccountName == "" {
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unable to extract user credentials")
		return
	}

	// Check if user has access to this sandbox
	if !s.checkSandboxAccess(sandbox, serviceAccountName) {
		respondError(c, http.StatusForbidden, "FORBIDDEN", "You don't have permission to delete this sandbox")
		return
	}

	// Delete sandbox using user's K8s client
	// The informer will automatically delete it from the store when the CRD is deleted
	userClient, clientErr := s.k8sClient.GetOrCreateUserK8sClient(userToken, userNamespace, serviceAccountName)
	if clientErr != nil {
		respondError(c, http.StatusInternalServerError, "CLIENT_CREATION_FAILED", clientErr.Error())
		return
	}
	err := userClient.DeleteSandbox(c.Request.Context(), sandbox.Namespace, sandbox.SandboxName)

	if err != nil {
		respondError(c, http.StatusForbidden, "SANDBOX_DELETE_FAILED",
			fmt.Sprintf("Failed to delete sandbox (service account: %s, namespace: %s): %v", serviceAccount, sandbox.Namespace, err))
		return
	}

	// Note: Don't manually delete from store - informer will handle it
	respondJSON(c, http.StatusOK, map[string]string{
		"message": "Sandbox deleted successfully",
	})
}
