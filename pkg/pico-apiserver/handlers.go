package picoapiserver

import (
	"encoding/json"
	"fmt"
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

// handleCreateSession handles session creation requests
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
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

	// Extract user information from context
	userToken, _ := r.Context().Value(contextKeyUserToken).(string)
	userNamespace, _ := r.Context().Value(contextKeyNamespace).(string)
	serviceAccount, _ := r.Context().Value(contextKeyServiceAccount).(string)

	if userToken == "" || userNamespace == "" {
		respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unable to extract user credentials")
		return
	}

	// Create K8s client with user's token
	userClient, err := s.k8sClient.NewUserK8sClient(userToken, userNamespace)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "CLIENT_CREATION_FAILED", err.Error())
		return
	}

	// Generate session ID
	sessionID := uuid.New().String()

	// Calculate sandbox name and namespace before creating
	// This matches the naming in k8s_client.go CreateSandbox()
	sandboxName := "sandbox-" + sessionID[:8]

	// CRITICAL: Register watcher BEFORE creating sandbox
	// This ensures we don't miss the Running state notification
	resultChan := s.sandboxController.WatchSandboxOnce(r.Context(), userNamespace, sandboxName)

	// Now create Kubernetes Sandbox CRD using user's permissions
	_, err = userClient.CreateSandbox(r.Context(), sessionID, req.Image, req.SSHPublicKey, req.Metadata)
	if err != nil {
		respondError(w, http.StatusForbidden, "SANDBOX_CREATE_FAILED",
			fmt.Sprintf("Failed to create sandbox (service account: %s, namespace: %s): %v", serviceAccount, userNamespace, err))
		return
	}

	select {
	case result := <-resultChan:
		// Create session object
		now := time.Now()
		session := &Session{
			SessionID:      sessionID,
			Status:         result.Status,
			CreatedAt:      now,
			ExpiresAt:      now.Add(time.Duration(req.TTL) * time.Second),
			LastActivityAt: now,
			Metadata:       req.Metadata,
			SandboxName:    sandboxName,
		}

		// Store session
		s.sessionStore.Set(sessionID, session)
		respondJSON(w, http.StatusOK, session)
		return
	case <-time.After(time.Duration(req.TTL) * time.Second):
		respondError(w, http.StatusInternalServerError, "SANDBOX_TIMEOUT", "Sandbox creation timed out")
		return
	}
}

// handleListSessions handles listing all sessions requests
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	// Get limit and offset from query parameters
	limit := getIntQueryParam(r, "limit", 50)
	offset := getIntQueryParam(r, "offset", 0)

	if limit < 1 || limit > 100 {
		respondError(w, http.StatusBadRequest, "INVALID_LIMIT", "Limit must be between 1 and 100")
		return
	}

	// Get all sessions
	allSessions := s.sessionStore.List()
	total := len(allSessions)

	// Apply pagination
	start := offset
	end := offset + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	sessions := allSessions[start:end]

	response := map[string]interface{}{
		"sessions": sessions,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	}

	respondJSON(w, http.StatusOK, response)
}

// handleGetSession handles getting a single session request
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	session := s.sessionStore.Get(sessionID)
	if session == nil {
		respondError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "Session not found or expired")
		return
	}

	respondJSON(w, http.StatusOK, session)
}

// handleDeleteSession handles session deletion requests
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	session := s.sessionStore.Get(sessionID)
	if session == nil {
		respondError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "Session not found or expired")
		return
	}

	// Extract user information from context
	userToken, _ := r.Context().Value(contextKeyUserToken).(string)
	userNamespace, _ := r.Context().Value(contextKeyNamespace).(string)
	serviceAccount, _ := r.Context().Value(contextKeyServiceAccount).(string)

	if userToken == "" || userNamespace == "" {
		respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unable to extract user credentials")
		return
	}

	// Create K8s client with user's token
	userClient, err := s.k8sClient.NewUserK8sClient(userToken, userNamespace)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "CLIENT_CREATION_FAILED", err.Error())
		return
	}

	// Delete Kubernetes Sandbox CRD using user's permissions
	if err := userClient.DeleteSandbox(r.Context(), session.SandboxName); err != nil {
		respondError(w, http.StatusForbidden, "SANDBOX_DELETE_FAILED",
			fmt.Sprintf("Failed to delete sandbox (service account: %s, namespace: %s): %v", serviceAccount, userNamespace, err))
		return
	}

	// Delete from store
	s.sessionStore.Delete(sessionID)

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Session deleted successfully",
	})
}
