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

package workloadmanager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	"sigs.k8s.io/agent-sandbox/controllers"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"

	"github.com/volcano-sh/agentcube/pkg/api"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// handleHealth handles health check requests
func (s *Server) handleHealth(c *gin.Context) {
	respondJSON(c, http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// handleAgentRuntimeCreate handles AgentRuntime sandbox creation requests.
func (s *Server) handleAgentRuntimeCreate(c *gin.Context) {
	s.handleSandboxCreate(c, types.AgentRuntimeKind)
}

// handleCodeInterpreterCreate handles CodeInterpreter sandbox creation requests.
func (s *Server) handleCodeInterpreterCreate(c *gin.Context) {
	s.handleSandboxCreate(c, types.CodeInterpreterKind)
}

// extractUserK8sClient extracts user information from the context and creates a user-specific Kubernetes client.
// It returns the dynamic client for the user and an error if authentication fails or client creation fails.
func (s *Server) extractUserK8sClient(c *gin.Context) (dynamic.Interface, error) {
	// Extract user information from context
	userToken, userNamespace, _, serviceAccountName := extractUserInfo(c)
	if userToken == "" || userNamespace == "" || serviceAccountName == "" {
		return nil, errors.New("unable to extract user credentials")
	}

	// Create sandbox using user's K8s client
	userClient, err := s.k8sClient.GetOrCreateUserK8sClient(userToken, userNamespace, serviceAccountName)
	if err != nil {
		klog.Infof("create user client failed: %v", err)
		return nil, fmt.Errorf("create user client failed: %w", err)
	}
	return userClient.dynamicClient, nil
}

// handleSandboxCreate handles sandbox creation given a specific kind.
func (s *Server) handleSandboxCreate(c *gin.Context, kind string) {
	sandboxReq := &types.CreateSandboxRequest{}
	if err := c.ShouldBindJSON(sandboxReq); err != nil {
		klog.Errorf("parse request body failed: %v", err)
		respondError(c, http.StatusBadRequest, "Invalid request body")
		return
	}

	sandboxReq.Kind = kind

	if err := sandboxReq.Validate(); err != nil {
		klog.Errorf("request body validation failed: %v", err)
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	var sandbox *sandboxv1alpha1.Sandbox
	var sandboxClaim *extensionsv1alpha1.SandboxClaim
	var sandboxEntry *sandboxEntry
	var err error
	switch sandboxReq.Kind {
	case types.AgentRuntimeKind:
		sandbox, sandboxEntry, err = buildSandboxByAgentRuntime(sandboxReq.Namespace, sandboxReq.Name, s.informers)
	case types.CodeInterpreterKind:
		sandbox, sandboxClaim, sandboxEntry, err = buildSandboxByCodeInterpreter(sandboxReq.Namespace, sandboxReq.Name, s.informers)
	}

	if err != nil {
		klog.Errorf("build sandbox failed %s/%s: %v", sandboxReq.Namespace, sandboxReq.Name, err)
		if errors.Is(err, api.ErrAgentRuntimeNotFound) || errors.Is(err, api.ErrCodeInterpreterNotFound) {
			respondError(c, http.StatusNotFound, err.Error())
		} else {
			respondError(c, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	// Calculate sandbox name and namespace before creating
	sandboxName := sandbox.Name
	namespace := sandbox.Namespace

	dynamicClient := s.k8sClient.dynamicClient
	if s.config.EnableAuth {
		userDynamicClient, errExtractClient := s.extractUserK8sClient(c)
		if errExtractClient != nil {
			klog.Infof("extract user k8s client failed: %v", errExtractClient)
			respondError(c, http.StatusUnauthorized, errExtractClient.Error())
			return
		}
		dynamicClient = userDynamicClient
	}

	// CRITICAL: Register watcher BEFORE creating sandbox
	// This ensures we don't miss the Running state notification
	resultChan := s.sandboxController.WatchSandboxOnce(c.Request.Context(), namespace, sandboxName)
	// Ensure cleanup is called when function returns to prevent memory leak
	defer s.sandboxController.UnWatchSandbox(namespace, sandboxName)

	response, err := s.createSandbox(c.Request.Context(), dynamicClient, sandbox, sandboxClaim, sandboxEntry, resultChan)
	if err != nil {
		klog.Errorf("create sandbox failed %s/%s: %v", sandbox.Namespace, sandbox.Name, err)
		respondError(c, http.StatusInternalServerError, "internal server error")
		return
	}

	respondJSON(c, http.StatusOK, response)
}

// createSandbox performs sandbox creation and returns the response payload or an error with an HTTP status code.
func (s *Server) createSandbox(ctx context.Context, dynamicClient dynamic.Interface, sandbox *sandboxv1alpha1.Sandbox, sandboxClaim *extensionsv1alpha1.SandboxClaim, sandboxEntry *sandboxEntry, resultChan <-chan SandboxStatusUpdate) (*types.CreateSandboxResponse, error) {
	// Store placeholder before creating, make sandbox/sandboxClaim GarbageCollection possible
	sandboxStorePlaceHolder := buildSandboxPlaceHolder(sandbox, sandboxEntry)
	if err := s.storeClient.StoreSandbox(ctx, sandboxStorePlaceHolder); err != nil {
		err = api.NewInternalError(fmt.Errorf("store sandbox placeholder failed: %v", err))
		return nil, err
	}

	if sandboxClaim != nil {
		if err := createSandboxClaim(ctx, dynamicClient, sandboxClaim); err != nil {
			err = api.NewInternalError(fmt.Errorf("create sandbox claim %s/%s failed: %v", sandboxClaim.Namespace, sandboxClaim.Name, err))
			return nil, err
		}
	} else {
		if _, err := createSandbox(ctx, dynamicClient, sandbox); err != nil {
			return nil, api.NewInternalError(fmt.Errorf("failed to create sandbox: %w", err))
		}
	}

	var createdSandbox *sandboxv1alpha1.Sandbox
	select {
	case result := <-resultChan:
		createdSandbox = result.Sandbox
		klog.V(2).Infof("sandbox %s/%s running", createdSandbox.Namespace, createdSandbox.Name)
	case <-time.After(2 * time.Minute): // consistent with router settings
		klog.Warningf("sandbox %s/%s create timed out", sandbox.Namespace, sandbox.Name)
		return nil, fmt.Errorf("sandbox creation timed out")
	}

	needRollbackSandbox := true
	sandboxRollbackFunc := func() {
		ctxTimeout, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		if sandboxClaim != nil {
			// Rollback SandboxClaim
			err = deleteSandboxClaim(ctxTimeout, dynamicClient, sandboxClaim.Namespace, sandboxClaim.Name)
			if err != nil {
				klog.Infof("sandbox claim %s/%s rollback failed: %v", sandboxClaim.Namespace, sandboxClaim.Name, err)
				return
			}
			klog.Infof("sandbox claim %s/%s rollback succeeded", sandboxClaim.Namespace, sandboxClaim.Name)
		} else {
			// Rollback Sandbox
			err = deleteSandbox(ctxTimeout, dynamicClient, sandbox.Namespace, sandbox.Name)
			if err != nil {
				klog.Infof("sandbox %s/%s rollback failed: %v", sandbox.Namespace, sandbox.Name, err)
				return
			}
			klog.Infof("sandbox %s/%s rollback succeeded", sandbox.Namespace, sandbox.Name)
		}
	}
	defer func() {
		if !needRollbackSandbox {
			return
		}
		sandboxRollbackFunc()
	}()

	sandboxPodName := ""
	if podName, exists := createdSandbox.Annotations[controllers.SanboxPodNameAnnotation]; exists {
		sandboxPodName = podName
	}

	podIP, err := s.k8sClient.GetSandboxPodIP(ctx, sandbox.Namespace, sandbox.Name, sandboxPodName)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox %s/%s pod IP: %v", sandbox.Namespace, sandbox.Name, err)
	}

	storeCacheInfo := buildSandboxInfo(createdSandbox, podIP, sandboxEntry)

	response := &types.CreateSandboxResponse{
		SessionID:   sandboxEntry.SessionID,
		SandboxID:   storeCacheInfo.SandboxID,
		SandboxName: sandbox.Name,
		EntryPoints: storeCacheInfo.EntryPoints,
	}

	if err := s.storeClient.UpdateSandbox(ctx, storeCacheInfo); err != nil {
		return nil, fmt.Errorf("update store cache failed: %v", err)
	}

	needRollbackSandbox = false
	klog.V(2).Infof("init sandbox %s/%s successfully, kind: %s, sessionID: %s", createdSandbox.Namespace,
		createdSandbox.Name, createdSandbox.Kind, sandboxEntry.SessionID)
	return response, nil
}

// handleDeleteSandbox handles sandbox deletion requests
func (s *Server) handleDeleteSandbox(c *gin.Context) {
	sessionID := c.Param("sessionId")
	// Query sandbox from store
	sandbox, err := s.storeClient.GetSandboxBySessionID(c.Request.Context(), sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			respondError(c, http.StatusNotFound, fmt.Sprintf("Session ID %s not found, maybe already deleted", sessionID))
			return
		}
		klog.Errorf("get sandbox from store by sessionID %s failed: %v", sessionID, err)
		respondError(c, http.StatusInternalServerError, "internal server error")
		return
	}

	dynamicClient := s.k8sClient.dynamicClient
	if s.config.EnableAuth {
		userDynamicClient, err := s.extractUserK8sClient(c)
		if err != nil {
			respondError(c, http.StatusUnauthorized, err.Error())
			return
		}
		dynamicClient = userDynamicClient
	}

	if sandbox.Kind == types.SandboxClaimsKind {
		err = deleteSandboxClaim(c.Request.Context(), dynamicClient, sandbox.SandboxNamespace, sandbox.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Already deleted, consider as success
				klog.Infof("sandbox claim %s/%s already deleted", sandbox.SandboxNamespace, sandbox.Name)
			} else {
				klog.Errorf("failed to delete sandbox claim %s/%s: %v", sandbox.SandboxNamespace, sandbox.Name, err)
				respondError(c, http.StatusInternalServerError, "internal server error")
				return
			}
		}
	} else {
		err = deleteSandbox(c.Request.Context(), dynamicClient, sandbox.SandboxNamespace, sandbox.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Already deleted, consider as success
				klog.Infof("sandbox %s/%s already deleted", sandbox.SandboxNamespace, sandbox.Name)
			} else {
				klog.Errorf("failed to delete sandbox %s/%s: %v", sandbox.SandboxNamespace, sandbox.Name, err)
				respondError(c, http.StatusInternalServerError, "internal server error")
				return
			}
		}
	}

	// Delete sandbox from store
	err = s.storeClient.DeleteSandboxBySessionID(c.Request.Context(), sessionID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "internal server error")
		return
	}

	klog.Infof("delete %s %s/%s successfully, sessionID: %v ", sandbox.Kind, sandbox.SandboxNamespace, sandbox.Name, sandbox.SessionID)
	respondJSON(c, http.StatusOK, map[string]string{
		"message": "Sandbox deleted successfully",
	})
}
