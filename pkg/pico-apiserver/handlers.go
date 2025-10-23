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

	// Generate session ID
	sessionID := uuid.New().String()

	// Create Kubernetes Sandbox CRD
	sandbox, err := s.k8sClient.CreateSandbox(r.Context(), sessionID, req.Image, req.Metadata)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "SANDBOX_CREATE_FAILED", err.Error())
		return
	}

	// Create session object
	now := time.Now()
	session := &Session{
		SessionID:      sessionID,
		Status:         "running",
		CreatedAt:      now,
		ExpiresAt:      now.Add(time.Duration(req.TTL) * time.Second),
		LastActivityAt: now,
		Metadata:       req.Metadata,
		SandboxName:    sandbox.Name,
	}

	// Store session
	s.sessionStore.Set(sessionID, session)

	// TODO: Start TTL expiration cleanup goroutine

	respondJSON(w, http.StatusOK, session)
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

	// Delete Kubernetes Sandbox CRD
	if err := s.k8sClient.DeleteSandbox(r.Context(), session.SandboxName); err != nil {
		respondError(w, http.StatusInternalServerError, "SANDBOX_DELETE_FAILED", err.Error())
		return
	}

	// Delete from store
	s.sessionStore.Delete(sessionID)

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Session deleted successfully",
	})
}

// handleExecuteCommand handles command execution requests (via SSH proxy)
func (s *Server) handleExecuteCommand(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	session := s.sessionStore.Get(sessionID)
	if session == nil {
		respondError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "Session not found or expired")
		return
	}

	var req CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	// TODO: Implement command execution via SSH
	// This requires:
	// 1. Get sandbox pod IP
	// 2. Establish SSH connection
	// 3. Execute command
	// 4. Return results

	respondError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "Command execution not yet implemented")
}

// handleExecuteCode handles code execution requests
func (s *Server) handleExecuteCode(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	session := s.sessionStore.Get(sessionID)
	if session == nil {
		respondError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "Session not found or expired")
		return
	}

	var req CodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	// TODO: Implement code execution
	respondError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "Code execution not yet implemented")
}

// handleUploadFile handles file upload requests
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	session := s.sessionStore.Get(sessionID)
	if session == nil {
		respondError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "Session not found or expired")
		return
	}

	// TODO: Implement file upload (via SFTP)
	respondError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "File upload not yet implemented")
}

// handleDownloadFile handles file download requests
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	session := s.sessionStore.Get(sessionID)
	if session == nil {
		respondError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "Session not found or expired")
		return
	}

	// TODO: Implement file download (via SFTP)
	respondError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "File download not yet implemented")
}
