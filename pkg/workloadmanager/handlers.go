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
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/redis"
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
	userClient, err := s.k8sClient.GetOrCreateUserK8sClient(userToken, userNamespace, serviceAccountName)
	if err != nil {
		log.Printf("create user client failed: %v", err)
		respondError(c, http.StatusInternalServerError, "CLIENT_CREATION_FAILED", err.Error())
		return
	}

	// Store placeholder before creating, make sandbox/sandboxClaim GarbageCollection possible
	sandboxRedisPlaceHolder := buildSandboxRedisCachePlaceHolder(sandbox, externalInfo)
	if err = s.redisClient.StoreSandbox(c.Request.Context(), sandboxRedisPlaceHolder, RedisNoExpiredTTL); err != nil {
		errMessage := fmt.Sprintf("store sandbox place holder into redis failed: %v", err)
		log.Println(errMessage)
		respondError(c, http.StatusInternalServerError, "STORE_SANDBOX_FAILED", errMessage)
		return
	}

	if sandboxClaim != nil {
		err = userClient.CreateSandboxClaim(c.Request.Context(), sandboxClaim)
		if err != nil {
			log.Printf("create sandbox claim %s/%s failed: %v", sandboxClaim.Namespace, sandboxClaim.Name, err)
			respondError(c, http.StatusForbidden, "SANDBOX_CLAIM_CREATE_FAILED",
				fmt.Sprintf("Failed to create sandbox claim (service account: %s, namespace: %s): %v", serviceAccount, userNamespace, err))
			return
		}
	} else {
		_, err = userClient.CreateSandbox(c.Request.Context(), sandbox)
		if err != nil {
			log.Printf("create sandbox %s/%s failed: %v", sandbox.Namespace, sandbox.Name, err)
			respondError(c, http.StatusForbidden, "SANDBOX_CREATE_FAILED",
				fmt.Sprintf("Failed to create sandbox (service account: %s, namespace: %s): %v", serviceAccount, userNamespace, err))
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
		err := userClient.DeleteSandbox(ctxTimeout, namespace, sandboxName)
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
			log.Printf("sandbox rollback failed: %v", err)
		} else {
			log.Printf("sandbox %s/%s rollback succeeded", createdSandbox.Namespace, createdSandbox.Name)
		}
	}()

	podIP, err := s.k8sClient.GetSandboxPodIP(c.Request.Context(), namespace, sandboxName)
	if err != nil {
		log.Printf("failed to get sandbox %s/%s pod IP: %v", namespace, sandboxName, err)
		respondError(c, http.StatusInternalServerError, "SANDBOX_BUILD_FAILED", err.Error())
		return
	}

	redisCacheInfo := convertSandboxToRedisCache(createdSandbox, podIP, externalInfo)

	response := &types.CreateSandboxResponse{
		SessionID:   externalInfo.SessionID,
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

	// TODO: Code Interpreter sandbox created, init code interpreter
	return
}

// handleDeleteSandbox handles sandbox deletion requests
func (s *Server) handleDeleteSandbox(c *gin.Context) {
	sessionID := c.Param("sessionId")

	// Query sandbox from redis
	sandbox, err := s.redisClient.GetSandboxBySessionID(c.Request.Context(), sessionID)
	if err != nil {
		if errors.Is(err, redis.ErrNotFound) || errors.Is(err, redisv9.Nil) {
			respondError(c, http.StatusNotFound, "SESSION_NOT_FOUND", "Sandbox not found")
			return
		}
		respondError(c, http.StatusInternalServerError, "FIND_SESSION_FAILED", err.Error())
		return
	}

	// Extract user information from context
	userToken, userNamespace, serviceAccount, serviceAccountName := extractUserInfo(c)

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

	if sandbox.SandboxClaimName != "" {
		// SandboxClaimName is not empty, we should delete SandboxClaim
		err = userClient.DeleteSandboxClaim(c.Request.Context(), sandbox.SandboxNamespace, sandbox.SandboxClaimName)
		if err != nil {
			respondError(c, http.StatusForbidden, "SANDBOX_CLAIM_DELETE_FAILED",
				fmt.Sprintf("Failed to delete sandbox claim (service account: %s, namespace: %s): %v", serviceAccount, sandbox.SandboxNamespace, err))
			return
		}
	} else {
		// SandboxClaimName is empty, we should delete Sandbox directly
		err = userClient.DeleteSandbox(c.Request.Context(), sandbox.SandboxNamespace, sandbox.SandboxName)
		if err != nil {
			respondError(c, http.StatusForbidden, "SANDBOX_DELETE_FAILED",
				fmt.Sprintf("Failed to delete sandbox (service account: %s, namespace: %s): %v", serviceAccount, sandbox.SandboxNamespace, err))
			return
		}
	}

	// Delete sandbox from Redis
	err = s.redisClient.DeleteSandboxBySessionIDTx(c.Request.Context(), sessionID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "REDIS_SANDBOX_DELETE_FAILED", err.Error())
		return
	}
	respondJSON(c, http.StatusOK, map[string]string{
		"message": "Sandbox deleted successfully",
	})
}
