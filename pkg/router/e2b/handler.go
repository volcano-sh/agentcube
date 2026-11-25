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

package e2b

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	cubeversioned "github.com/volcano-sh/agentcube/client-go/clientset/versioned"
	cubeinformers "github.com/volcano-sh/agentcube/client-go/informers/externalversions"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

const (
	defaultAPIKeySecret  = "e2b-api-keys" //nolint:gosec // Secret name, not a credential.
	defaultNamespaceName = "default"
	defaultSandboxTTLSec = 300
	defaultEnvdPort      = 49983
	defaultEnvdVersion   = "0.2.0"

	ttlModeSet            = "set"
	ttlModeExtend         = "extend"
	nextTokenHeader       = "X-Next-Token"
	defaultListLimit      = 100
	maxListLimit          = 1000
	accessTokenRandomByte = 16
)

// Config configures the embedded E2B module inside agentcube-router.
type Config struct {
	Domain                string
	APIKeySecret          string
	APIKeySecretNamespace string
	DefaultNamespace      string
	DefaultTTL            int
}

// DefaultConfig returns Config populated from environment variables.
func DefaultConfig() *Config {
	ns := envOrDefault("AGENTCUBE_NAMESPACE", defaultNamespaceName)
	return &Config{
		Domain:                strings.TrimPrefix(strings.TrimSpace(os.Getenv("E2B_DOMAIN")), "."),
		APIKeySecret:          envOrDefault("E2B_API_KEY_SECRET", defaultAPIKeySecret),
		APIKeySecretNamespace: envOrDefault("E2B_API_KEY_SECRET_NAMESPACE", ns),
		DefaultNamespace:      envOrDefault("E2B_DEFAULT_NAMESPACE", ns),
		DefaultTTL:            defaultSandboxTTLSec,
	}
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Handler serves both the E2B Platform REST API and the Host-based sandbox proxy inside agentcube-router.
type Handler struct {
	config          *Config
	engine          *gin.Engine
	storeClient     store.Store
	wmClient        WorkloadManagerClient
	authenticator   *Authenticator
	templates       *TemplateResolver
	proxyTransport  *http.Transport
	informerFactory cubeinformers.SharedInformerFactory
	codeInterpSync  cache.InformerSynced
}

// New creates an embedded E2B Handler reusing Router's shared Store, WM HTTP client, and IdentitySigner.
// Missing Kubernetes config, Secret, or CodeInterpreter RBAC degrade gracefully (fail closed on E2B routes).
func New(
	ctx context.Context,
	cfg *Config,
	storeClient store.Store,
	wmBaseURL string,
	wmHTTPClient *http.Client,
	signer IdentitySigner,
) (*Handler, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	wmClient, err := NewHTTPWorkloadManagerClient(wmBaseURL, wmHTTPClient, signer)
	if err != nil {
		return nil, fmt.Errorf("create E2B workloadmanager client: %w", err)
	}

	auth := NewAuthenticator(cfg.DefaultNamespace)
	templates := NewTemplateResolver(nil, defaultEnvdVersion)
	var informerFactory cubeinformers.SharedInformerFactory
	var ciSync cache.InformerSynced

	if kubeCfg, err := rest.InClusterConfig(); err != nil {
		klog.V(2).Infof("E2B module: in-cluster kubernetes config unavailable (%v); failing closed on Secret/template lookups", err)
	} else {
		if kubeCS, err := kubernetes.NewForConfig(kubeCfg); err != nil {
			klog.Warningf("E2B module: failed to create kubernetes clientset (%v); API key secret unavailable", err)
		} else if err := auth.LoadFromSecret(ctx, kubeCS, cfg.APIKeySecretNamespace, cfg.APIKeySecret); err != nil {
			klog.Warningf("E2B module: failed to load API key secret %s/%s (%v); failing closed", cfg.APIKeySecretNamespace, cfg.APIKeySecret, err)
		}
		if cubeCS, err := cubeversioned.NewForConfig(kubeCfg); err != nil {
			klog.Warningf("E2B module: failed to create agentcube clientset (%v); template lookup disabled", err)
		} else {
			informerFactory = cubeinformers.NewSharedInformerFactory(cubeCS, 0)
			ciInf := informerFactory.Runtime().V1alpha1().CodeInterpreters()
			templates = NewTemplateResolver(ciInf.Lister(), defaultEnvdVersion)
			ciSync = ciInf.Informer().HasSynced
		}
	}

	return NewWithDeps(cfg, storeClient, wmClient, auth, templates, informerFactory, ciSync)
}

// NewWithDeps builds an E2B Handler with explicit dependencies (primarily for unit tests).
func NewWithDeps(
	cfg *Config,
	storeClient store.Store,
	wmClient WorkloadManagerClient,
	auth *Authenticator,
	templates *TemplateResolver,
	informerFactory cubeinformers.SharedInformerFactory,
	ciSync cache.InformerSynced,
) (*Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if storeClient == nil || wmClient == nil {
		return nil, fmt.Errorf("storeClient and wmClient are required")
	}
	if auth == nil {
		auth = NewAuthenticator(cfg.DefaultNamespace)
	}
	if templates == nil {
		templates = NewTemplateResolver(nil, defaultEnvdVersion)
	}

	h := &Handler{
		config:        cfg,
		storeClient:   storeClient,
		wmClient:      wmClient,
		authenticator: auth,
		templates:     templates,
		proxyTransport: &http.Transport{
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true, // Preserve upstream ConnectRPC/streaming framing.
		},
		informerFactory: informerFactory,
		codeInterpSync:  ciSync,
	}
	h.setupRoutes()
	return h, nil
}

func (h *Handler) setupRoutes() {
	engine := gin.New()
	engine.Use(gin.Logger())
	engine.Use(gin.Recovery())

	api := engine.Group("")
	api.Use(h.authenticator.APIKeyMiddleware())
	{
		api.POST("/sandboxes", h.handleCreateSandbox)
		api.POST("/v2/sandboxes", h.handleCreateSandbox)
		api.GET("/sandboxes", h.handleListSandboxes)
		api.GET("/v2/sandboxes", h.handleListSandboxes)
		api.GET("/sandboxes/:id", h.handleGetSandbox)
		api.DELETE("/sandboxes/:id", h.handleDeleteSandbox)
		api.POST("/sandboxes/:id/timeout", h.handleSetTimeout)
		api.POST("/sandboxes/:id/connect", h.handleConnectSandbox)
		api.POST("/v2/sandboxes/:id/connect", h.handleConnectSandbox)
		api.GET("/templates", h.handleListTemplates)
		api.GET("/templates/:id", h.handleGetTemplate)

		// Unsupported operations return explicit 501.
		for _, p := range []string{"/sandboxes/:id/pause", "/sandboxes/:id/resume", "/sandboxes/:id/snapshots", "/templates", "/v3/templates"} {
			api.POST(p, h.handleNotImplemented)
		}
		api.PATCH("/templates/*path", h.handleNotImplemented)
		api.PATCH("/v2/templates/*path", h.handleNotImplemented)
		api.DELETE("/templates/*path", h.handleNotImplemented)
	}
	h.engine = engine
}

// StartInformers starts the read-only CodeInterpreter informer without blocking Router startup on RBAC errors.
func (h *Handler) StartInformers(ctx context.Context) {
	if h == nil || h.informerFactory == nil {
		return
	}
	h.informerFactory.Start(ctx.Done())
	if h.codeInterpSync == nil {
		return
	}
	go func() {
		syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if !cache.WaitForCacheSync(syncCtx.Done(), h.codeInterpSync) {
			klog.Warning("E2B module: CodeInterpreter informer cache did not sync (missing RBAC or CRD?); template lookups will fail closed")
		}
	}()
}

// WrapHandler wraps Router's fallback handler with Host-based E2B routing.
// When Domain is empty, returns fallback directly without intercepting any request.
func (h *Handler) WrapHandler(fallback http.Handler) http.Handler {
	if h == nil || strings.TrimSpace(h.config.Domain) == "" {
		return fallback
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, isProxy, err := extractHostTarget(r.Host, h.config.Domain)
		if isProxy {
			if err != nil {
				writeProxyJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			h.handleSandboxProxy(w, r, target)
			return
		}
		if isAPIHost(r.Host, h.config.Domain) {
			h.engine.ServeHTTP(w, r)
			return
		}
		if fallback != nil {
			fallback.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func respondError(c *gin.Context, code int, msg string) {
	c.JSON(code, ErrorResponse{Code: code, Message: msg})
}

func (h *Handler) handleCreateSandbox(c *gin.Context) {
	tenant, ok := tenantFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req CreateSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}
	if req.AutoPause {
		respondError(c, http.StatusNotImplemented, "autoPause is not supported")
		return
	}
	if (req.AllowInternetAccess != nil && !*req.AllowInternetAccess) ||
		(req.AllowInternetAccessSnake != nil && !*req.AllowInternetAccessSnake) ||
		hasNetworkRestrictions(req.Network) {
		respondError(c, http.StatusNotImplemented, "network restrictions are not supported")
		return
	}

	tpl, err := h.templates.Resolve(tenant.Namespace, req.TemplateID)
	if err != nil {
		switch {
		case errors.Is(err, errTemplateNotFound):
			respondError(c, http.StatusNotFound, fmt.Sprintf("template %q not found", req.TemplateID))
		case errors.Is(err, errTemplateAliasConflict):
			respondError(c, http.StatusConflict, err.Error())
		default:
			respondError(c, http.StatusInternalServerError, "failed to resolve template")
		}
		return
	}

	envdToken, err := generateRandomToken(accessTokenRandomByte)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to generate access token")
		return
	}
	ttl := int64(req.Timeout)
	if ttl <= 0 {
		ttl = int64(h.effectiveDefaultTTL())
	}

	wmResp, err := h.wmClient.CreateCodeInterpreter(c.Request.Context(), tenant.OwnerID, &types.CreateSandboxRequest{
		Kind:               types.CodeInterpreterKind,
		Name:               tpl.Name,
		Namespace:          tpl.Namespace,
		TTL:                &ttl,
		Metadata:           req.Metadata,
		EnvVars:            req.EnvVars,
		DisableIdleTimeout: true,
		InitEnvd:           true,
		EnvdPort:           defaultEnvdPort,
		EnvdAccessToken:    envdToken,
	})
	if err != nil {
		h.handleWMError(c, err)
		return
	}
	c.JSON(http.StatusCreated, h.buildSandboxModel(tpl.Name, tpl.PrimaryAlias, wmResp.SessionID, tenant.ClientID, wmResp.EnvdVersion, envdToken))
}

func (h *Handler) handleListSandboxes(c *gin.Context) {
	tenant, ok := tenantFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	opts, err := parseListOptions(c)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(opts.states) > 0 && !slices.Contains(opts.states, SandboxStateRunning) {
		c.JSON(http.StatusOK, []ListedSandbox{})
		return
	}

	sessions, err := h.wmClient.ListCodeInterpreterSessions(c.Request.Context(), tenant.OwnerID, tenant.Namespace)
	if err != nil {
		h.handleWMError(c, err)
		return
	}

	filtered := make([]*types.SandboxInfo, 0, len(sessions))
	for _, sb := range sessions {
		if isE2BSession(sb) && matchesMetadata(sb.Metadata, opts.metadata) {
			filtered = append(filtered, sb)
		}
	}
	slices.SortFunc(filtered, func(a, b *types.SandboxInfo) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			if a.CreatedAt.After(b.CreatedAt) {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.SessionID, b.SessionID)
	})

	page, nextToken := paginateSessions(filtered, opts.nextToken, opts.limit)
	if nextToken != "" {
		c.Header(nextTokenHeader, nextToken)
	}
	out := make([]ListedSandbox, 0, len(page))
	for _, sb := range page {
		out = append(out, h.toListedSandbox(sb, tenant))
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) handleGetSandbox(c *gin.Context) {
	tenant, ok := tenantFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	sb, ok := h.getOwnedE2BSession(c, tenant, c.Param("id"))
	if !ok {
		return
	}
	templateID, alias := h.templateInfo(sb)
	c.JSON(http.StatusOK, SandboxDetail{
		Sandbox:    h.buildSandboxModel(templateID, alias, sb.SessionID, tenant.ClientID, sb.EnvdVersion, sb.EnvdAccessToken),
		StartedAt:  sb.CreatedAt,
		EndAt:      sb.ExpiresAt,
		CPUCount:   defaultTemplateCPUCount,
		MemoryMB:   defaultTemplateMemoryMB,
		DiskSizeMB: defaultTemplateDiskSizeMB,
		State:      SandboxStateRunning,
		Metadata:   sb.Metadata,
	})
}

func (h *Handler) handleDeleteSandbox(c *gin.Context) {
	tenant, ok := tenantFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	sandboxID := c.Param("id")
	if _, ok := h.getOwnedE2BSession(c, tenant, sandboxID); !ok {
		return
	}
	if err := h.wmClient.DeleteCodeInterpreterSession(c.Request.Context(), tenant.OwnerID, tenant.Namespace, sandboxID); err != nil {
		h.handleWMError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) handleSetTimeout(c *gin.Context) {
	tenant, ok := tenantFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req TimeoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}
	if req.Timeout <= 0 {
		respondError(c, http.StatusBadRequest, "timeout must be > 0")
		return
	}
	sandboxID := c.Param("id")
	if _, ok := h.getOwnedE2BSession(c, tenant, sandboxID); !ok {
		return
	}
	if _, err := h.wmClient.UpdateCodeInterpreterSessionTTL(c.Request.Context(), tenant.OwnerID, tenant.Namespace, sandboxID, &types.UpdateSandboxTTLRequest{
		TTL:  int64(req.Timeout),
		Mode: ttlModeSet,
	}); err != nil {
		h.handleWMError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) handleConnectSandbox(c *gin.Context) {
	tenant, ok := tenantFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req ConnectSandboxRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return
		}
	}
	ttl := req.Timeout
	if ttl <= 0 {
		ttl = h.effectiveDefaultTTL()
	}

	ctx := c.Request.Context()
	sandboxID := c.Param("id")
	sb, ok := h.getOwnedE2BSession(c, tenant, sandboxID)
	if !ok {
		return
	}
	if _, err := h.wmClient.UpdateCodeInterpreterSessionTTL(ctx, tenant.OwnerID, tenant.Namespace, sandboxID, &types.UpdateSandboxTTLRequest{
		TTL:  int64(ttl),
		Mode: ttlModeExtend,
	}); err != nil {
		h.handleWMError(c, err)
		return
	}
	templateID, alias := h.templateInfo(sb)
	c.JSON(http.StatusCreated, h.buildSandboxModel(templateID, alias, sb.SessionID, tenant.ClientID, sb.EnvdVersion, sb.EnvdAccessToken))
}

func (h *Handler) handleListTemplates(c *gin.Context) {
	tenant, ok := tenantFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	list, err := h.templates.List(tenant.Namespace)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list templates")
		return
	}
	c.JSON(http.StatusOK, list)
}

func (h *Handler) handleGetTemplate(c *gin.Context) {
	tenant, ok := tenantFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	tpl, err := h.templates.Resolve(tenant.Namespace, c.Param("id"))
	if err != nil {
		if errors.Is(err, errTemplateNotFound) {
			respondError(c, http.StatusNotFound, "template not found")
			return
		}
		if errors.Is(err, errTemplateAliasConflict) {
			respondError(c, http.StatusConflict, err.Error())
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to resolve template")
		return
	}
	c.JSON(http.StatusOK, h.templates.toE2BTemplate(tpl.CRD))
}

func (h *Handler) handleNotImplemented(c *gin.Context) {
	respondError(c, http.StatusNotImplemented, fmt.Sprintf("%s %s is not supported in this version", c.Request.Method, c.Request.URL.Path))
}

func (h *Handler) handleWMError(c *gin.Context, err error) {
	var wmErr *WMStatusError
	if errors.As(err, &wmErr) {
		switch wmErr.StatusCode {
		case http.StatusBadRequest:
			respondError(c, http.StatusBadRequest, wmErr.Message)
		case http.StatusUnauthorized:
			respondError(c, http.StatusUnauthorized, wmErr.Message)
		case http.StatusForbidden, http.StatusNotFound:
			respondError(c, http.StatusNotFound, "sandbox not found")
		case http.StatusConflict:
			respondError(c, http.StatusConflict, wmErr.Message)
		default:
			respondError(c, http.StatusBadGateway, "upstream workload manager error")
		}
		return
	}
	klog.Errorf("E2B WorkloadManager call failed: %v", err)
	respondError(c, http.StatusBadGateway, "upstream workload manager error")
}

func (h *Handler) effectiveDefaultTTL() int {
	if h.config.DefaultTTL > 0 {
		return h.config.DefaultTTL
	}
	return defaultSandboxTTLSec
}

func (h *Handler) buildSandboxModel(templateID, alias, sandboxID, clientID, envdVersion, envdToken string) Sandbox {
	if envdVersion == "" {
		envdVersion = defaultEnvdVersion
	}
	sb := Sandbox{
		TemplateID:      templateID,
		SandboxID:       sandboxID,
		ClientID:        clientID,
		EnvdVersion:     envdVersion,
		Alias:           alias,
		EnvdAccessToken: envdToken,
	}
	if h.config.Domain != "" {
		domain := h.config.Domain
		sb.Domain = &domain
	}
	return sb
}

func (h *Handler) templateInfo(sb *types.SandboxInfo) (string, string) {
	templateID := sb.TemplateName
	if templateID == "" {
		templateID = sb.Name
	}
	if h.templates != nil && sb.TemplateName != "" {
		if resolved, err := h.templates.Resolve(sb.SandboxNamespace, sb.TemplateName); err == nil {
			return templateID, resolved.PrimaryAlias
		}
	}
	return templateID, ""
}

func (h *Handler) toListedSandbox(sb *types.SandboxInfo, tenant TenantIdentity) ListedSandbox {
	templateID, alias := h.templateInfo(sb)
	ver := sb.EnvdVersion
	if ver == "" {
		ver = defaultEnvdVersion
	}
	return ListedSandbox{
		TemplateID:  templateID,
		SandboxID:   sb.SessionID,
		ClientID:    tenant.ClientID,
		StartedAt:   sb.CreatedAt,
		EndAt:       sb.ExpiresAt,
		CPUCount:    defaultTemplateCPUCount,
		MemoryMB:    defaultTemplateMemoryMB,
		DiskSizeMB:  defaultTemplateDiskSizeMB,
		State:       SandboxStateRunning,
		EnvdVersion: ver,
		Alias:       alias,
		Metadata:    sb.Metadata,
	}
}

func isE2BSession(sb *types.SandboxInfo) bool {
	return sb != nil && sb.EnvdVersion != ""
}

func (h *Handler) getOwnedE2BSession(c *gin.Context, tenant TenantIdentity, sandboxID string) (*types.SandboxInfo, bool) {
	sb, err := h.wmClient.GetCodeInterpreterSession(c.Request.Context(), tenant.OwnerID, tenant.Namespace, sandboxID)
	if err != nil {
		h.handleWMError(c, err)
		return nil, false
	}
	if !isE2BSession(sb) {
		respondError(c, http.StatusNotFound, "sandbox not found")
		return nil, false
	}
	return sb, true
}

func hasNetworkRestrictions(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
}

type listOptions struct {
	states    []string
	metadata  map[string]string
	limit     int
	nextToken string
}

func parseListOptions(c *gin.Context) (*listOptions, error) {
	opts := &listOptions{
		limit:     defaultListLimit,
		nextToken: strings.TrimSpace(c.Query("nextToken")),
	}
	for _, item := range c.QueryArray("state") {
		for _, part := range strings.Split(item, ",") {
			if st := strings.TrimSpace(part); st != "" {
				opts.states = append(opts.states, st)
			}
		}
	}
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxListLimit {
			return nil, fmt.Errorf("limit must be between 1 and %d", maxListLimit)
		}
		opts.limit = limit
	}
	if rawMeta := strings.TrimSpace(c.Query("metadata")); rawMeta != "" {
		parsed, err := parseMetadataFilter(rawMeta)
		if err != nil {
			return nil, fmt.Errorf("invalid metadata query: %w", err)
		}
		opts.metadata = parsed
	}
	return opts, nil
}

func parseMetadataFilter(raw string) (map[string]string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(values))
	for k, vals := range values {
		if len(vals) > 0 {
			out[k] = vals[len(vals)-1]
		}
	}
	return out, nil
}

func matchesMetadata(actual, required map[string]string) bool {
	for k, v := range required {
		if actual[k] != v {
			return false
		}
	}
	return true
}

func paginateSessions(sessions []*types.SandboxInfo, nextToken string, limit int) ([]*types.SandboxInfo, string) {
	startIdx := 0
	if ts, id, ok := decodeCursor(nextToken); ok {
		startIdx = len(sessions)
		for i, sb := range sessions {
			sbTs := sb.CreatedAt.UTC().UnixNano()
			if sbTs < ts || (sbTs == ts && sb.SessionID > id) {
				startIdx = i
				break
			}
		}
	}
	if startIdx >= len(sessions) {
		return nil, ""
	}
	endIdx := startIdx + limit
	var outCursor string
	if endIdx < len(sessions) {
		raw := fmt.Sprintf("%d|%s", sessions[endIdx-1].CreatedAt.UTC().UnixNano(), sessions[endIdx-1].SessionID)
		outCursor = base64.RawURLEncoding.EncodeToString([]byte(raw))
	} else {
		endIdx = len(sessions)
	}
	return sessions[startIdx:endIdx], outCursor
}

func decodeCursor(token string) (int64, string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || token == "" {
		return 0, "", false
	}
	tsStr, id, ok := strings.Cut(string(b), "|")
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if !ok || err != nil || id == "" {
		return 0, "", false
	}
	return ts, id, true
}
