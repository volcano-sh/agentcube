package apiserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	agentsv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

// Sandbox represents a sandbox instance
type Sandbox struct {
	SandboxID      string                 `json:"sandboxId"`
	Status         string                 `json:"status"` // "running" or "paused"
	CreatedAt      time.Time              `json:"createdAt"`
	ExpiresAt      time.Time              `json:"expiresAt"`
	LastActivityAt time.Time              `json:"lastActivityAt,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	SandboxName    string                 `json:"-"` // Kubernetes Sandbox CRD name
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

// CleanExpired cleans up expired sandboxes
func (s *SandboxStore) CleanExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	count := 0
	for sandboxID, sandbox := range s.sandboxes {
		if now.After(sandbox.ExpiresAt) {
			delete(s.sandboxes, sandboxID)
			count++
		}
	}
	return count
}

// InitializeWithInformer initializes the store with an informer and performs initial sync
func (s *SandboxStore) InitializeWithInformer(ctx context.Context, informer cache.SharedInformer, k8sClient *K8sClient, namespace string) error {
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

	// Perform initial sync: list all sandboxes from Kubernetes
	if err := s.initializeFromCluster(ctx, k8sClient, namespace); err != nil {
		return fmt.Errorf("failed to initialize from cluster: %w", err)
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

// initializeFromCluster lists all sandboxes from Kubernetes and populates the store
func (s *SandboxStore) initializeFromCluster(ctx context.Context, k8sClient *K8sClient, namespace string) error {
	// Since we're using an informer, we can get all objects from the informer's cache
	// after it has synced. This avoids a separate list call.
	store := s.informer.GetStore()
	if store == nil {
		return fmt.Errorf("informer store is not available")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Get all objects from the informer cache
	list := store.List()
	for _, obj := range list {
		unstructuredObj, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}

		sandbox, err := convertK8sSandboxToSandbox(unstructuredObj)
		if err != nil {
			// Log error but continue processing other sandboxes
			continue
		}
		if sandbox != nil && sandbox.SandboxID != "" {
			s.sandboxes[sandbox.SandboxID] = sandbox
		}
	}

	return nil
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
	sandboxID := labels["sandbox-id"]
	if sandboxID == "" {
		return
	}

	s.mu.Lock()
	delete(s.sandboxes, sandboxID)
	s.mu.Unlock()
}

// convertK8sSandboxToSandbox converts a Kubernetes Sandbox CRD to internal Sandbox structure
func convertK8sSandboxToSandbox(unstructuredObj *unstructured.Unstructured) (*Sandbox, error) {
	// Convert unstructured to typed Sandbox
	var sandboxCRD agentsv1alpha1.Sandbox
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &sandboxCRD); err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to Sandbox: %w", err)
	}

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
	status := getSandboxStatus(&sandboxCRD)

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
		SandboxID:      sandboxID,
		Status:         status,
		CreatedAt:      createdAt,
		ExpiresAt:      expiresAt,
		LastActivityAt: lastActivityAt,
		Metadata:       metadata,
		SandboxName:    sandboxCRD.GetName(),
	}

	return sandbox, nil
}

// getSandboxStatus extracts status from Sandbox CRD conditions
func getSandboxStatus(sandbox *agentsv1alpha1.Sandbox) string {
	// Check conditions for Ready status
	for _, condition := range sandbox.Status.Conditions {
		if condition.Type == string(agentsv1alpha1.SandboxConditionReady) && condition.Status == metav1.ConditionTrue {
			return "running"
		}
	}
	return "paused"
}

// CreateSandboxRequest represents the request structure for creating a sandbox
type CreateSandboxRequest struct {
	TTL          int                    `json:"ttl,omitempty"`
	Image        string                 `json:"image,omitempty"`
	SSHPublicKey string                 `json:"sshPublicKey,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}
