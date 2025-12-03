package workloadmanager

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

// Sandbox represents a sandbox instance
type Sandbox struct {
	SandboxID             string                 `json:"sandboxId"`
	Status                string                 `json:"status"` // "running" or "paused"
	CreatedAt             time.Time              `json:"createdAt"`
	ExpiresAt             time.Time              `json:"expiresAt"`
	LastActivityAt        time.Time              `json:"lastActivityAt,omitempty"`
	Metadata              map[string]interface{} `json:"metadata,omitempty"`
	SandboxName           string                 `json:"-"` // Kubernetes Sandbox CRD name
	Namespace             string                 `json:"-"` // Kubernetes namespace where sandbox is created
	CreatorServiceAccount string                 `json:"-"` // Service account that created this sandbox
}

// SandboxStore manages in-memory sandbox storage synchronized with Kubernetes
type SandboxStore struct {
	mu        sync.RWMutex
	sandboxes map[string]*Sandbox
	informer  cache.SharedInformer
	stopCh    chan struct{}
	stopped   bool
}

// NewSandboxStore creates a new sandbox store
func NewSandboxStore() *SandboxStore {
	return &SandboxStore{
		sandboxes: make(map[string]*Sandbox),
		stopCh:    make(chan struct{}),
	}
}

// Set sets or updates a sandbox
func (s *SandboxStore) Set(sandboxID string, sandbox *Sandbox) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sandboxes[sandboxID] = sandbox
}

// Get gets a sandbox
func (s *SandboxStore) Get(sandboxID string) *Sandbox {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sandbox, exists := s.sandboxes[sandboxID]
	if !exists {
		return nil
	}

	// Check if expired
	if time.Now().After(sandbox.ExpiresAt) {
		return nil
	}

	return sandbox
}

// Delete deletes a sandbox
func (s *SandboxStore) Delete(sandboxID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sandboxes, sandboxID)
}

// List lists all non-expired sandboxes
func (s *SandboxStore) List() []*Sandbox {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var sandboxes []*Sandbox
	for _, sandbox := range s.sandboxes {
		if now.Before(sandbox.ExpiresAt) {
			sandboxes = append(sandboxes, sandbox)
		}
	}
	return sandboxes
}

// InitializeWithInformer initializes the store with an informer and performs initial sync
func (s *SandboxStore) InitializeWithInformer(ctx context.Context, informer cache.SharedInformer, k8sClient *K8sClient) error {
	s.mu.Lock()
	s.informer = informer
	s.mu.Unlock()

	// Set up event handlers
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			s.onSandboxAdd(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			s.onSandboxUpdate(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			s.onSandboxDelete(obj)
		},
	})

	// Start the informer
	go informer.Run(s.stopCh)

	// Wait for cache to sync
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}

// Stop stops the informer
func (s *SandboxStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		close(s.stopCh)
		s.stopped = true
	}
}

// onSandboxAdd handles sandbox add events
func (s *SandboxStore) onSandboxAdd(obj interface{}) {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	sandbox, err := convertK8sSandboxToSandbox(unstructuredObj)
	if err != nil {
		return
	}
	if sandbox != nil && sandbox.SandboxID != "" {
		s.mu.Lock()
		s.sandboxes[sandbox.SandboxID] = sandbox
		s.mu.Unlock()
	}
}

// onSandboxUpdate handles sandbox update events
func (s *SandboxStore) onSandboxUpdate(newObj interface{}) {
	unstructuredObj, ok := newObj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	sandbox, err := convertK8sSandboxToSandbox(unstructuredObj)
	if err != nil {
		return
	}
	if sandbox != nil && sandbox.SandboxID != "" {
		s.mu.Lock()
		s.sandboxes[sandbox.SandboxID] = sandbox
		s.mu.Unlock()
	}
}

// onSandboxDelete handles sandbox delete events
func (s *SandboxStore) onSandboxDelete(obj interface{}) {
	var unstructuredObj *unstructured.Unstructured

	switch obj := obj.(type) {
	case *unstructured.Unstructured:
		unstructuredObj = obj
	case cache.DeletedFinalStateUnknown:
		unstructuredObj, _ = obj.Obj.(*unstructured.Unstructured)
	default:
		return
	}

	if unstructuredObj == nil {
		return
	}

	// Get sandbox ID from labels
	labels := unstructuredObj.GetLabels()
	sessionID := labels[SessionIdLabelKey]
	if sessionID == "" {
		return
	}

	s.mu.Lock()
	delete(s.sandboxes, sessionID)
	s.mu.Unlock()
}

// convertK8sSandboxToSandbox converts a Kubernetes Sandbox CRD to internal Sandbox structure
func convertK8sSandboxToSandbox(unstructuredObj *unstructured.Unstructured) (*Sandbox, error) {
	// Convert unstructured to typed Sandbox
	var sandboxCRD sandboxv1alpha1.Sandbox
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &sandboxCRD); err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to Sandbox: %w", err)
	}

	return convertTypedSandboxToSandbox(&sandboxCRD)
}

// convertTypedSandboxToSandbox converts a typed Kubernetes Sandbox CRD to internal Sandbox structure
func convertTypedSandboxToSandbox(sandboxCRD *sandboxv1alpha1.Sandbox) (*Sandbox, error) {
	// Get sandbox ID from labels (UUID)
	labels := sandboxCRD.GetLabels()
	sandboxID := labels["sandbox-id"]
	if sandboxID == "" {
		// If sandbox-id label doesn't exist, try to use the UUID from metadata
		sandboxID = string(sandboxCRD.GetUID())
		if sandboxID == "" {
			return nil, fmt.Errorf("sandbox ID not found in labels or UID")
		}
	}

	// Get status from conditions
	status := getSandboxStatus(sandboxCRD)

	// Get creation time
	createdAt := sandboxCRD.GetCreationTimestamp().Time

	// Get last activity time from annotations
	var lastActivityAt time.Time
	if lastActivityStr, ok := sandboxCRD.GetAnnotations()[LastActivityAnnotationKey]; ok {
		if parsed, err := time.Parse(time.RFC3339, lastActivityStr); err == nil {
			lastActivityAt = parsed
		}
	}

	// Get metadata from annotations (excluding internal annotations)
	metadata := make(map[string]interface{})
	for k, v := range sandboxCRD.GetAnnotations() {
		if k != LastActivityAnnotationKey {
			metadata[k] = v
		}
	}

	// Calculate expiresAt based on TTL or use a default
	// For now, we'll use creation time + default TTL (3600 seconds)
	// In a real scenario, TTL might be stored in annotations or spec
	expiresAt := createdAt.Add(3600 * time.Second)
	if ttlStr, ok := sandboxCRD.GetAnnotations()["ttl"]; ok {
		if ttl, err := time.ParseDuration(ttlStr + "s"); err == nil {
			expiresAt = createdAt.Add(ttl)
		}
	}

	sandbox := &Sandbox{
		SandboxID:             sandboxID,
		Status:                status,
		CreatedAt:             createdAt,
		ExpiresAt:             expiresAt,
		LastActivityAt:        lastActivityAt,
		Metadata:              metadata,
		SandboxName:           sandboxCRD.GetName(),
		Namespace:             sandboxCRD.GetNamespace(),
		CreatorServiceAccount: "", // Will be set from annotations if available
	}

	// Try to get creator service account from annotations
	if creatorSA, ok := sandboxCRD.GetAnnotations()[CreatorServiceAccountAnnotationKey]; ok {
		sandbox.CreatorServiceAccount = creatorSA
	}

	return sandbox, nil
}

func buildSandboxRedisCachePlaceHolder(sandboxCRD *sandboxv1alpha1.Sandbox, externalInfo *sandboxExternalInfo) *types.SandboxRedis {
	sandboxRedis := &types.SandboxRedis{
		SessionID:        externalInfo.SessionID,
		SandboxNamespace: sandboxCRD.GetNamespace(),
		ExpiresAt:        time.Now().Add(DefaultSandboxTTL),
		Status:           "creating",
	}
	if externalInfo.SandboxClaimName != "" {
		sandboxRedis.SandboxClaimName = externalInfo.SandboxClaimName
	} else {
		sandboxRedis.SandboxName = sandboxCRD.GetName()
	}
	return sandboxRedis
}

func convertSandboxToRedisCache(sandboxCRD *sandboxv1alpha1.Sandbox, podIP string, externalInfo *sandboxExternalInfo) (*types.SandboxRedis, error) {
	createdAt := sandboxCRD.GetCreationTimestamp().Time
	expiresAt := createdAt.Add(DefaultSandboxTTL)
	if sandboxCRD.Spec.ShutdownTime != nil {
		expiresAt = sandboxCRD.Spec.ShutdownTime.Time
	}
	accesses := make([]types.SandboxEntryPoints, 0, len(externalInfo.Ports))
	for _, port := range externalInfo.Ports {
		accesses = append(accesses, types.SandboxEntryPoints{
			Path:     port.PathPrefix,
			Protocol: string(port.Protocol),
			Endpoint: net.JoinHostPort(podIP, strconv.Itoa(int(port.Port))),
		})
	}
	sandboxRedis := &types.SandboxRedis{
		SandboxID:        string(sandboxCRD.GetUID()),
		SandboxName:      sandboxCRD.GetName(),
		SandboxNamespace: sandboxCRD.GetNamespace(),
		EntryPoints:      accesses,
		SessionID:        externalInfo.SessionID,
		CreatedAt:        createdAt,
		ExpiresAt:        expiresAt,
		Status:           getSandboxStatus(sandboxCRD),
	}
	if externalInfo.SandboxClaimName != "" {
		sandboxRedis.SandboxClaimName = externalInfo.SandboxClaimName
	}
	return sandboxRedis, nil
}

// getSandboxStatus extracts status from Sandbox CRD conditions
func getSandboxStatus(sandbox *sandboxv1alpha1.Sandbox) string {
	// Check conditions for Ready status
	for _, condition := range sandbox.Status.Conditions {
		if condition.Type == string(sandboxv1alpha1.SandboxConditionReady) && condition.Status == metav1.ConditionTrue {
			return "running"
		}
	}
	return "paused"
}
