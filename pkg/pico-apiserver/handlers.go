package picoapiserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// handleCreateSandbox handles sandbox creation requests
func (s *Server) handleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	var req CreateSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	// Set default values
	if req.TTL == 0 {
		req.TTL = 3600
	}
	if req.TTL < 60 || req.TTL > 28800 {
		respondError(w, http.StatusBadRequest, "INVALID_TTL", "TTL must be between 60 and 28800 seconds")
		return
	}

	// Generate sandbox ID
	sandboxID := uuid.New().String()

	// Calculate sandbox name and namespace before creating
	sandboxName := "sandbox-" + sandboxID[:8]
	namespace := s.config.Namespace

	// CRITICAL: Register watcher BEFORE creating sandbox
	// This ensures we don't miss the Running state notification
	resultChan := s.sandboxController.WatchSandboxOnce(r.Context(), namespace, sandboxName)

	// Now create Kubernetes Sandbox CRD
	_, err := s.k8sClient.CreateSandbox(r.Context(), sandboxName, sandboxID, req.Image, req.SSHPublicKey, s.config.RuntimeClassName, req.Metadata)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "SANDBOX_CREATE_FAILED", err.Error())
		return
	}

	select {
	case result := <-resultChan:
		// Create sandbox object
		now := time.Now()
		sandbox := &Sandbox{
			SandboxID:      sandboxID,
			Status:         result.Status,
			CreatedAt:      now,
			ExpiresAt:      now.Add(time.Duration(req.TTL) * time.Second),
			LastActivityAt: now,
			Metadata:       req.Metadata,
			SandboxName:    sandboxName,
		}

		// Store sandbox
		s.sandboxStore.Set(sandboxID, sandbox)
		respondJSON(w, http.StatusOK, sandbox)
		return
	case <-time.After(time.Duration(req.TTL) * time.Second):
		respondError(w, http.StatusInternalServerError, "SANDBOX_TIMEOUT", "Sandbox creation timed out")
		return
	}
}

// handleListSandboxes handles listing all sandboxes requests
func (s *Server) handleListSandboxes(w http.ResponseWriter, r *http.Request) {
	// Get limit and offset from query parameters
	limit := getIntQueryParam(r, "limit", 50)
	offset := getIntQueryParam(r, "offset", 0)

	if limit < 1 || limit > 100 {
		respondError(w, http.StatusBadRequest, "INVALID_LIMIT", "Limit must be between 1 and 100")
		return
	}

	// Get all sandboxes
	allSandboxes := s.sandboxStore.List()
	total := len(allSandboxes)

	// Apply pagination
	start := offset
	end := offset + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	sandboxes := allSandboxes[start:end]

	response := map[string]interface{}{
		"sandboxes": sandboxes,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	}

	respondJSON(w, http.StatusOK, response)
}

// handleGetSandbox handles getting a single sandbox request
func (s *Server) handleGetSandbox(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sandboxID := vars["sandboxId"]

	sandbox := s.sandboxStore.Get(sandboxID)
	if sandbox == nil {
		respondError(w, http.StatusNotFound, "SANDBOX_NOT_FOUND", "Sandbox not found or expired")
		return
	}

	respondJSON(w, http.StatusOK, sandbox)
}

// handleDeleteSandbox handles sandbox deletion requests
func (s *Server) handleDeleteSandbox(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sandboxID := vars["sandboxId"]

	sandbox := s.sandboxStore.Get(sandboxID)
	if sandbox == nil {
		respondError(w, http.StatusNotFound, "SANDBOX_NOT_FOUND", "Sandbox not found or expired")
		return
	}

	// Delete Kubernetes Sandbox CRD
	if err := s.k8sClient.DeleteSandbox(r.Context(), sandbox.SandboxName); err != nil {
		respondError(w, http.StatusInternalServerError, "SANDBOX_DELETE_FAILED", err.Error())
		return
	}

	// Delete from store
	s.sandboxStore.Delete(sandboxID)

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Sandbox deleted successfully",
	})
}
