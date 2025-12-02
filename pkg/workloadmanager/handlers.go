package workloadmanager

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

// handleHealth handles health check requests
func (s *Server) handleHealth(c *gin.Context) {
	respondJSON(c, http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// handleCreateSandbox handles sandbox creation requests
func (s *Server) handleCreateSandbox(c *gin.Context) {
	createAgentRequest := &types.CreateSandboxRequest{}
	if err := c.ShouldBindJSON(createAgentRequest); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	if err := createAgentRequest.Validate(); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	var sandbox *sandboxv1alpha1.Sandbox
	var sandboxClaim *extensionsv1alpha1.SandboxClaim
	var externalInfo *sandboxExternalInfo
	var err error
	switch createAgentRequest.Kind {
	case types.AgentRuntimeKind:
		sandbox, externalInfo, err = buildSandboxByAgentRuntime(createAgentRequest.Namespace, createAgentRequest.Name, s.informers)
	case types.CodeInterpreterKind:
		sandbox, sandboxClaim, externalInfo, err = buildSandboxByCodeInterpreter(createAgentRequest.Namespace, createAgentRequest.Name, s.informers)
	default:
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request kind")
		return
	}

	if err != nil {
		respondError(c, http.StatusBadRequest, "SANDBOX_BUILD_FAILED", err.Error())
		return
	}

	// Extract user information from context
	userToken, userNamespace, serviceAccount, serviceAccountName := extractUserInfo(c)

	if userToken == "" || userNamespace == "" || serviceAccountName == "" {
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unable to extract user credentials")
		return
	}

	// Calculate sandbox name and namespace before creating
	sandboxName := sandbox.Name
	namespace := sandbox.Namespace

	// CRITICAL: Register watcher BEFORE creating sandbox
	// This ensures we don't miss the Running state notification
	resultChan := s.sandboxController.WatchSandboxOnce(c.Request.Context(), namespace, sandboxName)

	// Create sandbox using user's K8s client
	userClient, clientErr := s.k8sClient.GetOrCreateUserK8sClient(userToken, userNamespace, serviceAccountName)
	if clientErr != nil {
		respondError(c, http.StatusInternalServerError, "CLIENT_CREATION_FAILED", clientErr.Error())
		return
	}
	if sandboxClaim != nil {
		err = userClient.CreateSandboxClaim(c.Request.Context(), sandboxClaim)
		if err != nil {
			respondError(c, http.StatusForbidden, "SANDBOX_CLAIM_CREATE_FAILED",
				fmt.Sprintf("Failed to create sandbox claim (service account: %s, namespace: %s): %v", serviceAccount, userNamespace, err))
			return
		}
	} else {
		_, err = userClient.CreateSandbox(c.Request.Context(), sandbox)
		if err != nil {
			respondError(c, http.StatusForbidden, "SANDBOX_CREATE_FAILED",
				fmt.Sprintf("Failed to create sandbox (service account: %s, namespace: %s): %v", serviceAccount, userNamespace, err))
			return
		}
	}

	var createdSandbox *sandboxv1alpha1.Sandbox
	select {
	case result := <-resultChan:
		createdSandbox = result.Sandbox
	case <-time.After(3 * time.Minute):
		respondError(c, http.StatusInternalServerError, "SANDBOX_TIMEOUT", "Sandbox creation timed out")
		return
	}

	needRollbackSandbox := true
	sandboxRollbackFunc := func() error {
		err := userClient.DeleteSandbox(c.Request.Context(), namespace, sandboxName)
		if err != nil {
			return fmt.Errorf("failed to create sandbox (service account: %s, namespace: %s): %v", serviceAccount, userNamespace, err)
		}
		return nil
	}
	defer func() {
		if needRollbackSandbox == false {
			return
		}
		if err := sandboxRollbackFunc(); err != nil {
			log.Printf("sandbox rollback failed: %v\n", err)
		}
	}()

	podIP, err := s.k8sClient.GetSandboxPodIP(c.Request.Context(), namespace, sandboxName)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "SANDBOX_BUILD_FAILED", err.Error())
		return
	}

	redisCacheInfo, err := convertSandboxToRedisCache(createdSandbox, podIP, externalInfo)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "SANDBOX_BUILD_FAILED", err.Error())
		return
	}

	response := &types.CreateSandboxResponse{
		SessionID: sandbox.Labels[SessionIdLabelKey],
		EntryPoints: []types.SandboxEntryPoints{
			{
				Protocol: redisCacheInfo.EntryPoints[0].Protocol,
				Endpoint: redisCacheInfo.EntryPoints[0].Endpoint,
			},
		},
	}

	if createAgentRequest.Kind != types.CodeInterpreterKind {
		err = s.redisClient.StoreSandbox(c.Request.Context(), redisCacheInfo, DefaultRedisTTL)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "SANDBOX_STORE_REDIS_FAILED", err.Error())
			return
		}
		needRollbackSandbox = false
		respondJSON(c, http.StatusOK, response)
		return
	}

	// Code Interpreter sandbox created, init code interpreter
	// Find the /init endpoint from accesses
	var initEndpoint string
	for _, access := range redisCacheInfo.Accesses {
		if access.Path == "/init" {
			initEndpoint = fmt.Sprintf("%s://%s", access.Protocol, access.Endpoint)
			break
		}
	}

	// If no /init path found, use the first access endpoint with /init appended
	if initEndpoint == "" {
		if len(redisCacheInfo.Accesses) > 0 {
			initEndpoint = fmt.Sprintf("%s://%s/init",
				redisCacheInfo.Accesses[0].Protocol,
				redisCacheInfo.Accesses[0].Endpoint)
		} else {
			respondError(c, http.StatusInternalServerError, "SANDBOX_INIT_FAILED",
				"No access endpoint found for sandbox initialization")
			return
		}
	}

	// Call sandbox init endpoint with JWT-signed request
	err = s.InitCodeInterpreterSandbox(
		c.Request.Context(),
		initEndpoint,
		sandbox.Labels[SessionIdLabelKey],
		createAgentRequest.Metadata,
	)

	if err != nil {
		respondError(c, http.StatusInternalServerError, "SANDBOX_INIT_FAILED",
			fmt.Sprintf("Failed to initialize code interpreter: %v", err))
		return
	}

	// Init successful, no need to rollback
	needRollbackSandbox = false
	respondJSON(c, http.StatusOK, response)
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
