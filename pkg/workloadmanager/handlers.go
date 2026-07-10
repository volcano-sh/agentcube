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
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"

	"github.com/volcano-sh/agentcube/pkg/api"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

var (
	// errSandboxCreationTimeout is returned when the internal sandbox-ready wait exceeds the 2-minute deadline.
	errSandboxCreationTimeout = errors.New("sandbox creation timed out")

	// errSandboxReadyWatcherNotRegistered is returned when direct sandbox creation starts without a readiness watcher.
	errSandboxReadyWatcherNotRegistered = errors.New("sandbox ready watcher not registered")

	// errSandboxReadyWatcherClosed is returned when the readiness watcher closes before a sandbox becomes ready.
	errSandboxReadyWatcherClosed = errors.New("sandbox ready watcher closed before sandbox was ready")

	// errSandboxReadyWatcherMissingSandbox is returned when the readiness watcher reports readiness without a sandbox object.
	errSandboxReadyWatcherMissingSandbox = errors.New("sandbox ready watcher returned empty sandbox")
)

const (
	// sandboxCreationTimeout matches the router's sandbox creation timeout.
	sandboxCreationTimeout = 2 * time.Minute
	// storeCleanupTimeout is the maximum duration allowed to clean up a store placeholder.
	storeCleanupTimeout = 30 * time.Second
)

// isContextError reports whether err is a context cancellation or deadline error.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func sandboxReadyWaitContextError(parentCtx, waitCtx context.Context) error {
	if waitCtx.Err() == nil {
		return nil
	}
	if context.Cause(waitCtx) == errSandboxCreationTimeout {
		return errSandboxCreationTimeout
	}
	if err := parentCtx.Err(); err != nil {
		return err
	}
	return waitCtx.Err()
}

func sandboxReadyWaitEnded(parentCtx, waitCtx context.Context, readErr error) (bool, error) {
	if waitErr := sandboxReadyWaitContextError(parentCtx, waitCtx); waitErr != nil {
		return true, waitErr
	}
	if isContextError(readErr) {
		return true, readErr
	}
	return false, nil
}

func isRetryableSandboxReadError(err error) bool {
	if isContextError(err) {
		return false
	}
	return apierrors.IsNotFound(err) ||
		apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err) ||
		apierrors.IsUnexpectedServerError(err) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		utilnet.IsTimeout(err) ||
		utilnet.IsProbableEOF(err) ||
		utilnet.IsConnectionReset(err) ||
		utilnet.IsHTTP2ConnectionLost(err) ||
		utilnet.IsConnectionRefused(err)
}

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

	ownerID, statusCode, errMsg := resolveSandboxOwnerID(c.Request)
	if statusCode != 0 {
		respondError(c, statusCode, errMsg)
		return
	}

	switch sandboxReq.Kind {
	case types.AgentRuntimeKind:
		sandbox, sandboxEntry, err = buildSandboxByAgentRuntime(sandboxReq.Namespace, sandboxReq.Name, ownerID, s.informers)
	case types.CodeInterpreterKind:
		sandbox, sandboxClaim, sandboxEntry, err = buildSandboxByCodeInterpreter(sandboxReq.Namespace, sandboxReq.Name, ownerID, s.informers)
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

	// Set ownership on the store entry as well
	if ownerID != "" {
		sandboxEntry.OwnerID = ownerID
	}

	// Calculate sandbox name and namespace before creating.
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

	var resultChan <-chan SandboxStatusUpdate
	if sandboxClaim == nil {
		// CRITICAL: Register watcher BEFORE creating sandbox.
		// This ensures we don't miss the Ready state notification for directly created sandboxes.
		resultChan = s.sandboxController.WatchSandboxOnce(c.Request.Context(), namespace, sandboxName)
		// Ensure cleanup is called when function returns to prevent memory leak.
		defer s.sandboxController.UnWatchSandbox(namespace, sandboxName)
	}

	response, err := s.createSandbox(c.Request.Context(), dynamicClient, sandbox, sandboxClaim, sandboxEntry, resultChan)
	if err != nil {
		s.respondSandboxCreateError(c, sandbox, err)
		return
	}

	respondJSON(c, http.StatusOK, response)
}

func resolveSandboxOwnerID(r *http.Request) (string, int, string) {
	ownerID, err := extractOwnerID(r)
	if err == nil {
		return ownerID, 0, ""
	}
	if errors.Is(err, ErrNoIdentityHeader) {
		return "", 0, ""
	}

	klog.Errorf("Failed to extract owner ID: %v", err)
	if errors.Is(err, ErrPublicKeyNotCached) {
		return "", http.StatusServiceUnavailable, "identity verifier not ready"
	}
	return "", http.StatusUnauthorized, "invalid identity token"
}

func (s *Server) respondSandboxCreateError(c *gin.Context, sandbox *sandboxv1alpha1.Sandbox, err error) {
	if errors.Is(err, context.Canceled) {
		klog.Warningf("create sandbox aborted %s/%s: client disconnected", sandbox.Namespace, sandbox.Name)
		c.AbortWithStatus(499)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		klog.Warningf("create sandbox timed out %s/%s: request deadline exceeded", sandbox.Namespace, sandbox.Name)
		respondError(c, http.StatusGatewayTimeout, "request timed out")
		return
	}
	if errors.Is(err, errSandboxCreationTimeout) {
		klog.Warningf("create sandbox timed out %s/%s: sandbox did not become ready within deadline", sandbox.Namespace, sandbox.Name)
		respondError(c, http.StatusGatewayTimeout, err.Error())
		return
	}

	klog.Errorf("create sandbox failed %s/%s: %v", sandbox.Namespace, sandbox.Name, err)
	msg := err.Error()
	if isInternalSandboxCreateError(err) {
		msg = "internal server error"
	}
	respondError(c, http.StatusInternalServerError, msg)
}

func isInternalSandboxCreateError(err error) bool {
	return apierrors.IsInternalError(err) ||
		errors.Is(err, errSandboxReadyWatcherNotRegistered) ||
		errors.Is(err, errSandboxReadyWatcherClosed) ||
		errors.Is(err, errSandboxReadyWatcherMissingSandbox)
}

// createK8sResources creates the K8s sandbox or sandbox claim resource.
func (s *Server) createK8sResources(ctx context.Context, dynamicClient dynamic.Interface, sandbox *sandboxv1alpha1.Sandbox, sandboxClaim *extensionsv1alpha1.SandboxClaim) error {
	if sandboxClaim != nil {
		if err := createSandboxClaim(ctx, dynamicClient, sandboxClaim); err != nil {
			if isContextError(err) {
				return err
			}
			return api.NewInternalError(fmt.Errorf("create sandbox claim %s/%s failed: %w", sandboxClaim.Namespace, sandboxClaim.Name, err))
		}
	} else {
		if _, err := createSandbox(ctx, dynamicClient, sandbox); err != nil {
			if isContextError(err) {
				return err
			}
			return api.NewInternalError(fmt.Errorf("failed to create sandbox: %w", err))
		}
	}
	return nil
}

func (s *Server) waitForDirectSandboxReady(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox, resultChan <-chan SandboxStatusUpdate) (*sandboxv1alpha1.Sandbox, error) {
	if resultChan == nil {
		return nil, errSandboxReadyWatcherNotRegistered
	}

	// Use NewTimer so we can stop it explicitly when another branch wins,
	// preventing the runtime from retaining the timer until it fires.
	timer := time.NewTimer(sandboxCreationTimeout)
	defer timer.Stop()

	select {
	case result, ok := <-resultChan:
		if !ok {
			klog.Warningf("sandbox %s/%s ready watcher closed before ready", sandbox.Namespace, sandbox.Name)
			return nil, errSandboxReadyWatcherClosed
		}
		createdSandbox := result.Sandbox
		if createdSandbox == nil {
			klog.Warningf("sandbox %s/%s ready watcher returned empty sandbox", sandbox.Namespace, sandbox.Name)
			return nil, errSandboxReadyWatcherMissingSandbox
		}
		klog.V(2).Infof("sandbox %s/%s reported ready, verifying entrypoints", createdSandbox.Namespace, createdSandbox.Name)
		return createdSandbox, nil
	case <-ctx.Done():
		klog.Warningf("sandbox %s/%s wait canceled: %v", sandbox.Namespace, sandbox.Name, ctx.Err())
		return nil, ctx.Err()
	case <-timer.C:
		klog.Warningf("sandbox %s/%s create timed out", sandbox.Namespace, sandbox.Name)
		return nil, errSandboxCreationTimeout
	}
}

func (s *Server) waitForClaimSandboxReady(ctx context.Context, dynamicClient dynamic.Interface, sandboxClaim *extensionsv1alpha1.SandboxClaim) (*sandboxv1alpha1.Sandbox, error) {
	return s.waitForClaimSandboxReadyWithTimeout(ctx, dynamicClient, sandboxClaim, sandboxCreationTimeout)
}

func (s *Server) waitForClaimSandboxReadyWithTimeout(ctx context.Context, dynamicClient dynamic.Interface, sandboxClaim *extensionsv1alpha1.SandboxClaim, timeout time.Duration) (*sandboxv1alpha1.Sandbox, error) {
	waitCtx, cancel := context.WithTimeoutCause(ctx, timeout, errSandboxCreationTimeout)
	defer cancel()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		claim, err := getSandboxClaim(waitCtx, dynamicClient, sandboxClaim.Namespace, sandboxClaim.Name)
		if ended, waitErr := sandboxReadyWaitEnded(ctx, waitCtx, err); ended {
			return nil, waitErr
		}
		if err != nil {
			if !isRetryableSandboxReadError(err) {
				return nil, api.NewInternalError(err)
			}
			klog.V(2).Infof("waiting for sandbox claim %s/%s status: %v", sandboxClaim.Namespace, sandboxClaim.Name, err)
		}
		if err == nil {
			if sandboxName := claim.Status.SandboxStatus.Name; sandboxName != "" {
				createdSandbox, err := getSandbox(waitCtx, dynamicClient, sandboxClaim.Namespace, sandboxName)
				if ended, waitErr := sandboxReadyWaitEnded(ctx, waitCtx, err); ended {
					return nil, waitErr
				}
				if err != nil {
					if !isRetryableSandboxReadError(err) {
						return nil, api.NewInternalError(err)
					}
					klog.V(2).Infof("waiting for adopted sandbox %s/%s: %v", sandboxClaim.Namespace, sandboxName, err)
				} else if getSandboxStatus(createdSandbox) == sandboxStatusReady {
					if waitErr := sandboxReadyWaitContextError(ctx, waitCtx); waitErr != nil {
						return nil, waitErr
					}
					klog.V(2).Infof("sandbox claim %s/%s adopted ready sandbox %s/%s", sandboxClaim.Namespace, sandboxClaim.Name, createdSandbox.Namespace, createdSandbox.Name)
					return createdSandbox, nil
				}
			}
		}

		select {
		case <-waitCtx.Done():
			waitErr := sandboxReadyWaitContextError(ctx, waitCtx)
			if errors.Is(waitErr, errSandboxCreationTimeout) {
				klog.Warningf("sandbox claim %s/%s create timed out", sandboxClaim.Namespace, sandboxClaim.Name)
			} else {
				klog.Warningf("sandbox claim %s/%s wait canceled: %v", sandboxClaim.Namespace, sandboxClaim.Name, waitErr)
			}
			return nil, waitErr
		case <-ticker.C:
		}
	}
}

func (s *Server) waitForCreatedSandbox(ctx context.Context, dynamicClient dynamic.Interface, sandbox *sandboxv1alpha1.Sandbox, sandboxClaim *extensionsv1alpha1.SandboxClaim, resultChan <-chan SandboxStatusUpdate) (*sandboxv1alpha1.Sandbox, error) {
	if sandboxClaim != nil {
		return s.waitForClaimSandboxReady(ctx, dynamicClient, sandboxClaim)
	}
	return s.waitForDirectSandboxReady(ctx, sandbox, resultChan)
}

// createSandbox performs sandbox creation and returns the response payload or an error with an HTTP status code.
func (s *Server) createSandbox(ctx context.Context, dynamicClient dynamic.Interface, sandbox *sandboxv1alpha1.Sandbox, sandboxClaim *extensionsv1alpha1.SandboxClaim, sandboxEntry *sandboxEntry, resultChan <-chan SandboxStatusUpdate) (*types.CreateSandboxResponse, error) {
	placeholder := buildSandboxPlaceHolder(sandbox, sandboxEntry)
	if err := s.storeClient.StoreSandbox(ctx, placeholder); err != nil {
		if isContextError(err) {
			return nil, err
		}
		return nil, api.NewInternalError(fmt.Errorf("store sandbox placeholder failed: %w", err))
	}

	// Register rollback right after the placeholder is stored so that a K8s
	// creation failure does not leave an orphaned store entry.
	needRollbackSandbox := true
	defer func() {
		if !needRollbackSandbox {
			return
		}
		s.rollbackSandboxCreation(dynamicClient, sandbox, sandboxClaim, sandboxEntry.SessionID)
	}()

	if err := s.createK8sResources(ctx, dynamicClient, sandbox, sandboxClaim); err != nil {
		return nil, err
	}

	createdSandbox, err := s.waitForCreatedSandbox(ctx, dynamicClient, sandbox, sandboxClaim, resultChan)
	if err != nil {
		return nil, err
	}

	// agent-sandbox creates a Pod with the same name as the Sandbox if no warm pool is used.
	// If a warm pool is used, the adopted Pod name is stored in
	// sandboxv1alpha1.SandboxPodNameAnnotation.
	sandboxNameForPod := createdSandbox.Name
	sandboxPodName := createdSandbox.Name
	if podName, exists := createdSandbox.Annotations[sandboxv1alpha1.SandboxPodNameAnnotation]; exists {
		sandboxPodName = podName
	}

	podIP, nodeName, err := s.k8sClient.GetSandboxPodInfo(ctx, createdSandbox.Namespace, sandboxNameForPod, sandboxPodName)
	if err != nil {
		if isContextError(err) {
			return nil, err
		}
		return nil, api.NewInternalError(fmt.Errorf("failed to get sandbox %s/%s pod info: %w", createdSandbox.Namespace, sandboxNameForPod, err))
	}
	if err := s.waitForSandboxEntryPointsReady(ctx, podIP, sandboxEntry); err != nil {
		if isContextError(err) {
			return nil, err
		}
		return nil, api.NewInternalError(fmt.Errorf("failed to verify sandbox %s/%s entrypoints: %w", createdSandbox.Namespace, sandboxNameForPod, err))
	}

	storeCacheInfo := buildSandboxInfo(createdSandbox, podIP, sandboxEntry)
	if sandboxClaim != nil {
		storeCacheInfo.Name = sandboxClaim.Name
		storeCacheInfo.SandboxNamespace = sandboxClaim.Namespace
		storeCacheInfo.ExpiresAt = placeholder.ExpiresAt
		storeCacheInfo.CreatedAt = placeholder.CreatedAt
	}

	response := &types.CreateSandboxResponse{
		Kind:        storeCacheInfo.Kind,
		SessionID:   sandboxEntry.SessionID,
		SandboxID:   storeCacheInfo.SandboxID,
		SandboxName: storeCacheInfo.Name,
		EntryPoints: storeCacheInfo.EntryPoints,
		OwnerID:     sandboxEntry.OwnerID,
	}

	if err := s.storeClient.UpdateSandbox(ctx, storeCacheInfo); err != nil {
		if isContextError(err) {
			return nil, err
		}
		return nil, api.NewInternalError(fmt.Errorf("update store cache failed: %w", err))
	}

	needRollbackSandbox = false

	// Only record the node after the store update succeeds, so that a rollback
	// (which deletes the sandbox) does not race with a goroutine that has already
	// recorded a node for a session that was never fully established.
	s.recordStickyNode(createdSandbox.Namespace, sandboxEntry, nodeName)
	klog.V(2).Infof("init sandbox %s/%s successfully, kind: %s, sessionID: %s", createdSandbox.Namespace,
		createdSandbox.Name, createdSandbox.Kind, sandboxEntry.SessionID)
	return response, nil
}

// recordStickyNode records the scheduled node on the owning workload so the next
// session prefers the same node. Best-effort: a failure here must not fail the
// create, since the session is already usable. The patch runs in a detached
// goroutine with its own short timeout so that a slow API server or a client
// disconnect does not delay the sandbox creation response.
func (s *Server) recordStickyNode(namespace string, sandboxEntry *sandboxEntry, nodeName string) {
	if sandboxEntry.StickyWorkloadName == "" || nodeName == "" {
		return
	}
	kind := sandboxEntry.StickyWorkloadKind
	name := sandboxEntry.StickyWorkloadName

	// Hold a read lock while checking the flag and adding to the wait group.
	// Concurrent recordStickyNode calls do not block each other (read lock),
	// while Shutdown() takes the write lock so that no recordStickyNode can
	// observe shuttingDown=false and then call wg.Add after wg.Wait has begun.
	s.shutdownMu.RLock()
	if s.shuttingDown {
		s.shutdownMu.RUnlock()
		return
	}
	s.wg.Add(1)
	s.shutdownMu.RUnlock()

	go func() {
		defer s.wg.Done()
		patchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.k8sClient.PatchWorkloadLastNode(patchCtx, namespace, kind, name, nodeName); err != nil {
			klog.Warningf("failed to patch %s %s/%s last-node=%s: %v", kind, namespace, name, nodeName, err)
		}
	}()
}

// rollbackSandboxCreation deletes the sandbox (or sandbox claim) and its store
// placeholder when creation fails. It runs in a fresh context so that a
// canceled request context does not prevent cleanup.
func (s *Server) rollbackSandboxCreation(dynamicClient dynamic.Interface, sandbox *sandboxv1alpha1.Sandbox, sandboxClaim *extensionsv1alpha1.SandboxClaim, sessionID string) {
	ctxTimeout, cancel := context.WithTimeout(context.Background(), storeCleanupTimeout)
	defer cancel()
	if sandboxClaim != nil {
		if err := deleteSandboxClaim(ctxTimeout, dynamicClient, sandboxClaim.Namespace, sandboxClaim.Name); err != nil {
			klog.Infof("sandbox claim %s/%s rollback failed: %v", sandboxClaim.Namespace, sandboxClaim.Name, err)
		} else {
			klog.Infof("sandbox claim %s/%s rollback succeeded", sandboxClaim.Namespace, sandboxClaim.Name)
		}
	} else {
		if err := deleteSandbox(ctxTimeout, dynamicClient, sandbox.Namespace, sandbox.Name); err != nil {
			klog.Infof("sandbox %s/%s rollback failed: %v", sandbox.Namespace, sandbox.Name, err)
		} else {
			klog.Infof("sandbox %s/%s rollback succeeded", sandbox.Namespace, sandbox.Name)
		}
	}
	if delErr := s.storeClient.DeleteSandboxBySessionID(ctxTimeout, sessionID); delErr != nil {
		klog.Infof("sandbox %s/%s store placeholder cleanup failed: %v", sandbox.Namespace, sandbox.Name, delErr)
	}
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

	// Use a detached context for the store delete so a client disconnect
	// after K8s deletion doesn't orphan the store entry.
	deleteCtx, cancel := context.WithTimeout(context.Background(), storeCleanupTimeout)
	defer cancel()

	// Delete sandbox from store
	err = s.storeClient.DeleteSandboxBySessionID(deleteCtx, sessionID)
	if err != nil {
		klog.Errorf("delete %s %s/%s from store by sessionID %s failed: %v", sandbox.Kind, sandbox.SandboxNamespace, sandbox.Name, sessionID, err)
		respondError(c, http.StatusInternalServerError, "internal server error")
		return
	}

	klog.Infof("delete %s %s/%s successfully, sessionID: %v ", sandbox.Kind, sandbox.SandboxNamespace, sandbox.Name, sandbox.SessionID)
	respondJSON(c, http.StatusOK, map[string]string{
		"message": "Sandbox deleted successfully",
	})
}
