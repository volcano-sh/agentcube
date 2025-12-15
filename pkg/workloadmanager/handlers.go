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
	"k8s.io/client-go/dynamic"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	"sigs.k8s.io/agent-sandbox/controllers"
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
		log.Printf("create user client failed: %v", err)
		return nil, fmt.Errorf("create user client failed: %w", err)
	}
	return userClient.dynamicClient, nil
}

func (s *Server) handleCreateSandbox(c *gin.Context) {
	sandboxReq := &types.CreateSandboxRequest{}
	if err := c.ShouldBindJSON(sandboxReq); err != nil {
		log.Printf("parse request body failed: %v", err)
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	reqPath := c.Request.URL.Path
	switch {
	case strings.HasSuffix(reqPath, "/agent-runtime"):
		sandboxReq.Kind = types.AgentRuntimeKind
	case strings.HasSuffix(reqPath, "/code-interpreter"):
		sandboxReq.Kind = types.CodeInterpreterKind
	default:
	}

	if err := sandboxReq.Validate(); err != nil {
		log.Printf("request body validation failed: %v", err)
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	var sandbox *sandboxv1alpha1.Sandbox
	var sandboxClaim *extensionsv1alpha1.SandboxClaim
	var externalInfo *sandboxExternalInfo
	var err error
	switch sandboxReq.Kind {
	case types.AgentRuntimeKind:
		sandbox, externalInfo, err = buildSandboxByAgentRuntime(sandboxReq.Namespace, sandboxReq.Name, s.informers)
	case types.CodeInterpreterKind:
		sandbox, sandboxClaim, externalInfo, err = buildSandboxByCodeInterpreter(sandboxReq.Namespace, sandboxReq.Name, s.informers)
	default:
		log.Printf("invalid request kind: %v", sandboxReq.Kind)
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("invalid request kind: %v", sandboxReq.Kind))
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
	if s.config.EnableAuth {
		userDynamicClient, err := s.extractUserK8sClient(c)
		if err != nil {
			respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
			return
		}
		dynamicClient = userDynamicClient
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
	if podName, exists := createdSandbox.Annotations[controllers.SanboxPodNameAnnotation]; exists {
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

	if sandboxReq.Kind != types.CodeInterpreterKind {
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

	if len(redisCacheInfo.EntryPoints) == 0 {
		// Fallback to default http://ip:8080
		defaultEntryPoint := types.SandboxEntryPoints{
			Path:     "/",
			Protocol: "http",
			Endpoint: fmt.Sprintf("%s:8080", podIP),
		}
		redisCacheInfo.EntryPoints = []types.SandboxEntryPoints{defaultEntryPoint}
		response.EntryPoints = redisCacheInfo.EntryPoints
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

	// If no /init path found, use the first entryPoint endpoint fallback
	if initEndpoint == "" {
		initEndpoint = fmt.Sprintf("%s://%s", redisCacheInfo.EntryPoints[0].Protocol,
			redisCacheInfo.EntryPoints[0].Endpoint)
	}

	// Call sandbox init endpoint with JWT-signed request
	err = s.InitCodeInterpreterSandbox(
		c.Request.Context(),
		initEndpoint,
		externalInfo.SessionID,
		sandboxReq.PublicKey,
		sandboxReq.Metadata,
		sandboxReq.InitTimeoutSeconds,
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
	if s.config.EnableAuth {
		userDynamicClient, err := s.extractUserK8sClient(c)
		if err != nil {
			respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
			return
		}
		dynamicClient = userDynamicClient
	}

	if sandbox.Kind == types.SandboxClaimsKind {
		err = deleteSandboxClaim(c.Request.Context(), dynamicClient, sandbox.SandboxNamespace, sandbox.Name)
		if err != nil {
			log.Printf("failed to delete sandbox claim %s/%s: %v", sandbox.SandboxNamespace, sandbox.Name, err)
			respondError(c, http.StatusForbidden, "SANDBOX_CLAIM_DELETE_FAILED",
				fmt.Sprintf("Failed to delete sandbox claim (namespace: %s): %v", sandbox.SandboxNamespace, err))
			return
		}
	} else {
		err = deleteSandbox(c.Request.Context(), dynamicClient, sandbox.SandboxNamespace, sandbox.Name)
		if err != nil {
			log.Printf("failed to delete sandbox %s/%s: %v", sandbox.SandboxNamespace, sandbox.Name, err)
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

	log.Printf("delete %s %s/%s successfully, sessionID: %v ", sandbox.Kind, sandbox.SandboxNamespace, sandbox.Name, sandbox.SessionID)
	respondJSON(c, http.StatusOK, map[string]string{
		"message": "Sandbox deleted successfully",
	})
}
