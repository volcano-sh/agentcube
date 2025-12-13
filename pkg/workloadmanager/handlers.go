package workloadmanager

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	redisv9 "github.com/redis/go-redis/v9"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/redis"
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
// nolint :gocyclo
func (s *Server) handleCreateSandbox(c *gin.Context) {
	createAgentRequest := &types.CreateSandboxRequest{}
	if err := c.ShouldBindJSON(createAgentRequest); err != nil {
		log.Printf("parse request body failed: %v", err)
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	reqPath := c.Request.URL.Path
	switch {
	case strings.HasSuffix(reqPath, "/agent-runtime"):
		createAgentRequest.Kind = types.AgentRuntimeKind
	case strings.HasSuffix(reqPath, "/code-interpreter"):
		createAgentRequest.Kind = types.CodeInterpreterKind
	default:
	}

	if err := createAgentRequest.Validate(); err != nil {
		log.Printf("request body validation failed: %v", err)
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
		log.Printf("invalid request kind: %v", createAgentRequest.Kind)
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("invalid request kind: %v", createAgentRequest.Kind))
		return
	}

	if err != nil {
		log.Printf("build sandbox failed: %v", err)
		respondError(c, http.StatusBadRequest, "SANDBOX_BUILD_FAILED", err.Error())
		return
	}

	// Calculate sandbox name and namespace before creating
	sandboxName := sandbox.Name
	namespace := sandbox.Namespace

	dynamicClient := s.k8sClient.dynamicClient
	if s.enableAuth {
		// Extract user information from context
		userToken, userNamespace, _, serviceAccountName := extractUserInfo(c)
		if userToken == "" || userNamespace == "" || serviceAccountName == "" {
			respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unable to extract user credentials")
			return
		}

		// Create sandbox using user's K8s client
		userClient, err := s.k8sClient.GetOrCreateUserK8sClient(userToken, userNamespace, serviceAccountName)
		if err != nil {
			log.Printf("create user client failed: %v", err)
			respondError(c, http.StatusInternalServerError, "CLIENT_CREATION_FAILED", err.Error())
			return
		}

		dynamicClient = userClient.dynamicClient
	}

	// CRITICAL: Register watcher BEFORE creating sandbox
	// This ensures we don't miss the Running state notification
	resultChan := s.sandboxController.WatchSandboxOnce(c.Request.Context(), namespace, sandboxName)

	// Store placeholder before creating, make sandbox/sandboxClaim GarbageCollection possible
	sandboxRedisPlaceHolder := buildSandboxRedisCachePlaceHolder(sandbox, externalInfo)
	if err = s.redisClient.StoreSandbox(c.Request.Context(), sandboxRedisPlaceHolder, RedisNoExpiredTTL); err != nil {
		errMessage := fmt.Sprintf("store sandbox place holder into redis failed: %v", err)
		log.Println(errMessage)
		respondError(c, http.StatusInternalServerError, "STORE_SANDBOX_FAILED", errMessage)
		return
	}

	if sandboxClaim != nil {
		err = createSandboxClaim(c.Request.Context(), dynamicClient, sandboxClaim)
		if err != nil {
			log.Printf("create sandbox claim %s/%s failed: %v", sandboxClaim.Namespace, sandboxClaim.Name, err)
			respondError(c, http.StatusForbidden, "SANDBOX_CLAIM_CREATE_FAILED",
				fmt.Sprintf("Failed to create sandbox claim: %v", err))
			return
		}
	} else {
		_, err = createSandbox(c.Request.Context(), dynamicClient, sandbox)
		if err != nil {
			log.Printf("create sandbox %s/%s failed: %v", sandbox.Namespace, sandbox.Name, err)
			respondError(c, http.StatusForbidden, "SANDBOX_CREATE_FAILED",
				fmt.Sprintf("Failed to create sandbox: %v", err))
			return
		}
	}

	var createdSandbox *sandboxv1alpha1.Sandbox
	select {
	case result := <-resultChan:
		// TODO: pendingRequests should remove manual if not receive result
		createdSandbox = result.Sandbox
		log.Printf("sandbox %s/%s running", createdSandbox.Namespace, createdSandbox.Name)
	case <-time.After(3 * time.Minute):
		// timeout, Sandbox/SandboxClaim maybe create successfully later,
		// it will be deleted in GarbageCollection
		log.Printf("sandbox %s/%s create timed out", sandbox.Namespace, sandbox.Name)
		respondError(c, http.StatusInternalServerError, "SANDBOX_TIMEOUT", "Sandbox creation timed out")
		return
	}

	needRollbackSandbox := true
	sandboxRollbackFunc := func() error {
		ctxTimeout, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := deleteSandbox(ctxTimeout, dynamicClient, namespace, sandboxName)
		if err != nil {
			return fmt.Errorf("failed to create sandbox: %v", err)
		}
		return nil
	}
	defer func() {
		if needRollbackSandbox == false {
			return
		}
		if err := sandboxRollbackFunc(); err != nil {
			log.Printf("sandbox rollback failed: %v", err)
		} else {
			log.Printf("sandbox %s/%s rollback succeeded", createdSandbox.Namespace, createdSandbox.Name)
		}
	}()

	sandboxPodName := ""
	if podName, exists := createdSandbox.Annotations["agents.x-k8s.io/pod-name"]; exists {
		sandboxPodName = podName
	}
	podIP, err := s.k8sClient.GetSandboxPodIP(c.Request.Context(), namespace, sandboxName, sandboxPodName)
	if err != nil {
		log.Printf("failed to get sandbox %s/%s pod IP: %v", namespace, sandboxName, err)
		respondError(c, http.StatusInternalServerError, "SANDBOX_BUILD_FAILED", err.Error())
		return
	}

	redisCacheInfo := convertSandboxToRedisCache(createdSandbox, podIP, externalInfo)

	response := &types.CreateSandboxResponse{
		SessionID:   externalInfo.SessionID,
		SandboxID:   redisCacheInfo.SandboxID,
		SandboxName: sandboxName,
		EntryPoints: redisCacheInfo.EntryPoints,
	}

	if createAgentRequest.Kind != types.CodeInterpreterKind {
		err = s.redisClient.UpdateSandbox(c.Request.Context(), redisCacheInfo, RedisNoExpiredTTL)
		if err != nil {
			log.Printf("update redis cache failed: %v", err)
			respondError(c, http.StatusInternalServerError, "SANDBOX_UPDATE_REDIS_FAILED", err.Error())
			return
		}
		needRollbackSandbox = false
		log.Printf("create sandbox %s/%s successfully, sesssionID: %s", createdSandbox.Namespace, createdSandbox.Name, externalInfo.SessionID)
		respondJSON(c, http.StatusOK, response)
		return
	}

	// Code Interpreter sandbox created, init code interpreter
	// Find the /init endpoint from entryPoints
	var initEndpoint string
	for _, access := range redisCacheInfo.EntryPoints {
		if access.Path == "/init" {
			initEndpoint = fmt.Sprintf("%s://%s", access.Protocol, access.Endpoint)
			break
		}
	}

	// If no /init path found, use the first entryPoint endpoint with /init appended
	if initEndpoint == "" {
		if len(redisCacheInfo.EntryPoints) > 0 {
			initEndpoint = fmt.Sprintf("%s://%s",
				redisCacheInfo.EntryPoints[0].Protocol,
				redisCacheInfo.EntryPoints[0].Endpoint)
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
		createAgentRequest.PublicKey,
		createAgentRequest.Metadata,
	)

	if err != nil {
		log.Printf("init sandbox %s/%s failed: %v", createdSandbox.Namespace, createdSandbox.Name, err)
		respondError(c, http.StatusInternalServerError, "SANDBOX_INIT_FAILED",
			fmt.Sprintf("Failed to initialize code interpreter: %v", err))
		return
	}

	err = s.redisClient.UpdateSandbox(c.Request.Context(), redisCacheInfo, RedisNoExpiredTTL)
	if err != nil {
		log.Printf("update redis cache failed: %v", err)
		respondError(c, http.StatusInternalServerError, "SANDBOX_UPDATE_REDIS_FAILED", err.Error())
		return
	}
	// init successful, no need to rollback
	needRollbackSandbox = false
	log.Printf("init sandbox %s/%s successfully, sessionID: %s", createdSandbox.Namespace, createdSandbox.Name, externalInfo.SessionID)
	respondJSON(c, http.StatusOK, response)
}

// handleDeleteSandbox handles sandbox deletion requests
func (s *Server) handleDeleteSandbox(c *gin.Context) {
	sessionID := c.Param("sessionId")

	// Query sandbox from redis
	sandbox, err := s.redisClient.GetSandboxBySessionID(c.Request.Context(), sessionID)
	if err != nil {
		if errors.Is(err, redis.ErrNotFound) || errors.Is(err, redisv9.Nil) {
			log.Printf("sessionID %s not found in redis", sessionID)
			respondError(c, http.StatusNotFound, "SESSION_NOT_FOUND", "Sandbox not found")
			return
		}
		log.Printf("get sandbox from redis by sessionID %s failed: %v", sessionID, err)
		respondError(c, http.StatusInternalServerError, "FIND_SESSION_FAILED", err.Error())
		return
	}

	dynamicClient := s.k8sClient.dynamicClient
	if s.enableAuth {
		// Extract user information from context
		userToken, userNamespace, _, serviceAccountName := extractUserInfo(c)

		if userToken == "" || userNamespace == "" || serviceAccountName == "" {
			respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unable to extract user credentials")
			return
		}

		// Delete sandbox using user's K8s client
		userClient, clientErr := s.k8sClient.GetOrCreateUserK8sClient(userToken, userNamespace, serviceAccountName)
		if clientErr != nil {
			respondError(c, http.StatusInternalServerError, "CLIENT_CREATION_FAILED", clientErr.Error())
			return
		}

		dynamicClient = userClient.dynamicClient
	}

	if sandbox.SandboxClaimName != "" {
		// SandboxClaimName is not empty, we should delete SandboxClaim
		err = deleteSandboxClaim(c.Request.Context(), dynamicClient, sandbox.SandboxNamespace, sandbox.SandboxClaimName)
		if err != nil {
			log.Printf("failed to delete sandbox claim %s/%s: %v", sandbox.SandboxNamespace, sandbox.SandboxClaimName, err)
			respondError(c, http.StatusForbidden, "SANDBOX_CLAIM_DELETE_FAILED",
				fmt.Sprintf("Failed to delete sandbox claim (namespace: %s): %v", sandbox.SandboxNamespace, err))
			return
		}
	} else {
		// SandboxClaimName is empty, we should delete Sandbox directly
		err = deleteSandbox(c.Request.Context(), dynamicClient, sandbox.SandboxNamespace, sandbox.SandboxName)
		if err != nil {
			log.Printf("failed to delete sandbox claim %s/%s: %v", sandbox.SandboxNamespace, sandbox.SandboxName, err)
			respondError(c, http.StatusForbidden, "SANDBOX_DELETE_FAILED",
				fmt.Sprintf("Failed to delete sandbox (namespace: %s): %v", sandbox.SandboxNamespace, err))
			return
		}
	}

	// Delete sandbox from Redis
	err = s.redisClient.DeleteSandboxBySessionIDTx(c.Request.Context(), sessionID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "REDIS_SANDBOX_DELETE_FAILED", err.Error())
		return
	}

	objectType := SandboxGVR.Resource
	objectName := sandbox.SandboxName
	if sandbox.SandboxClaimName != "" {
		objectName = sandbox.SandboxClaimName
	}
	log.Printf("delete %s %s/%s successfully, sessionID: %v ", objectType, sandbox.SandboxNamespace, objectName, sandbox.SessionID)
	respondJSON(c, http.StatusOK, map[string]string{
		"message": "Sandbox deleted successfully",
	})
}
