package workloadmanager

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

// mockIndexer is a mock implementation of cache.Indexer for testing
type mockIndexer struct {
	objects map[string]interface{}
}

func newMockIndexer() *mockIndexer {
	return &mockIndexer{
		objects: make(map[string]interface{}),
	}
}

func (m *mockIndexer) Add(obj interface{}) error {
	key, _ := cache.MetaNamespaceKeyFunc(obj)
	m.objects[key] = obj
	return nil
}

func (m *mockIndexer) Update(obj interface{}) error {
	key, _ := cache.MetaNamespaceKeyFunc(obj)
	m.objects[key] = obj
	return nil
}

func (m *mockIndexer) Delete(obj interface{}) error {
	key, _ := cache.MetaNamespaceKeyFunc(obj)
	delete(m.objects, key)
	return nil
}

func (m *mockIndexer) List() []interface{} {
	result := make([]interface{}, 0, len(m.objects))
	for _, obj := range m.objects {
		result = append(result, obj)
	}
	return result
}

func (m *mockIndexer) ListKeys() []string {
	keys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		keys = append(keys, k)
	}
	return keys
}

func (m *mockIndexer) Get(obj interface{}) (item interface{}, exists bool, err error) {
	key, _ := cache.MetaNamespaceKeyFunc(obj)
	item, exists = m.objects[key]
	return item, exists, nil
}

func (m *mockIndexer) GetByKey(key string) (item interface{}, exists bool, err error) {
	item, exists = m.objects[key]
	return item, exists, nil
}

func (m *mockIndexer) Replace(items []interface{}, _ string) error {
	m.objects = make(map[string]interface{})
	for _, item := range items {
		key, _ := cache.MetaNamespaceKeyFunc(item)
		m.objects[key] = item
	}
	return nil
}

func (m *mockIndexer) Resync() error {
	return nil
}

func (m *mockIndexer) ByIndex(indexName, indexKey string) ([]interface{}, error) {
	if indexName != cache.NamespaceIndex {
		return nil, nil
	}
	result := make([]interface{}, 0)
	for _, obj := range m.objects {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		if pod.Namespace == indexKey {
			result = append(result, obj)
		}
	}
	return result, nil
}

func (m *mockIndexer) IndexKeys(indexName, indexKey string) ([]string, error) {
	items, err := m.ByIndex(indexName, indexKey)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(items))
	for _, item := range items {
		key, _ := cache.MetaNamespaceKeyFunc(item)
		keys = append(keys, key)
	}
	return keys, nil
}

func (m *mockIndexer) GetIndexers() cache.Indexers {
	return cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	}
}

func (m *mockIndexer) AddIndexers(_ cache.Indexers) error {
	return nil
}

func (m *mockIndexer) Index(indexName string, obj interface{}) ([]interface{}, error) {
	// For namespace index, return all objects in the same namespace
	if indexName == cache.NamespaceIndex {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return nil, nil
		}
		return m.ByIndex(indexName, pod.Namespace)
	}
	return nil, nil
}

func (m *mockIndexer) ListIndexFuncValues(indexName string) []string {
	if indexName != cache.NamespaceIndex {
		return nil
	}
	namespaces := make(map[string]bool)
	for _, obj := range m.objects {
		pod, ok := obj.(*corev1.Pod)
		if ok {
			namespaces[pod.Namespace] = true
		}
	}
	result := make([]string, 0, len(namespaces))
	for ns := range namespaces {
		result = append(result, ns)
	}
	return result
}

// mockSharedIndexInformer is a mock implementation of cache.SharedIndexInformer
type mockSharedIndexInformer struct {
	indexer cache.Indexer
}

func newMockSharedIndexInformer(indexer cache.Indexer) *mockSharedIndexInformer {
	return &mockSharedIndexInformer{
		indexer: indexer,
	}
}

func (m *mockSharedIndexInformer) AddEventHandler(_ cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}

func (m *mockSharedIndexInformer) AddEventHandlerWithResyncPeriod(_ cache.ResourceEventHandler, _ time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}

func (m *mockSharedIndexInformer) AddEventHandlerWithOptions(_ cache.ResourceEventHandler, _ cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}

func (m *mockSharedIndexInformer) RemoveEventHandler(_ cache.ResourceEventHandlerRegistration) error {
	return nil
}

func (m *mockSharedIndexInformer) IsStopped() bool {
	return false
}

func (m *mockSharedIndexInformer) GetStore() cache.Store {
	return m.indexer
}

func (m *mockSharedIndexInformer) GetController() cache.Controller {
	return nil
}

func (m *mockSharedIndexInformer) Run(_ <-chan struct{}) {}

func (m *mockSharedIndexInformer) RunWithContext(_ context.Context) {}

func (m *mockSharedIndexInformer) HasSynced() bool {
	return true
}

func (m *mockSharedIndexInformer) LastSyncResourceVersion() string {
	return ""
}

func (m *mockSharedIndexInformer) SetWatchErrorHandler(_ cache.WatchErrorHandler) error {
	return nil
}

func (m *mockSharedIndexInformer) SetWatchErrorHandlerWithContext(_ cache.WatchErrorHandlerWithContext) error {
	return nil
}

func (m *mockSharedIndexInformer) SetTransform(_ cache.TransformFunc) error {
	return nil
}

func (m *mockSharedIndexInformer) GetIndexer() cache.Indexer {
	return m.indexer
}

func (m *mockSharedIndexInformer) AddIndexers(_ cache.Indexers) error {
	return nil
}

// TestGetSandboxPodIP_PodPresentInCache tests that GetSandboxPodIP returns IP when pod is present in cache
func TestGetSandboxPodIP_PodPresentInCache(t *testing.T) {
	// Setup: Create a mock indexer with a running pod
	indexer := newMockIndexer()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"sandbox-name": "test-sandbox",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
		},
	}
	_ = indexer.Add(pod)

	// Create mock informer
	mockInformer := newMockSharedIndexInformer(indexer)

	// Create K8sClient with mock informer
	client := &K8sClient{
		podInformer: mockInformer,
	}

	// Execute
	ip, err := client.GetSandboxPodIP(context.Background(), "test-namespace", "test-sandbox")

	// Verify
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1", ip)
}

// TestGetSandboxPodIP_PodNotPresentInCache tests that GetSandboxPodIP returns error when pod is not in cache
func TestGetSandboxPodIP_PodNotPresentInCache(t *testing.T) {
	// Setup: Create an empty mock indexer
	indexer := newMockIndexer()
	mockInformer := newMockSharedIndexInformer(indexer)

	// Create K8sClient with mock informer
	client := &K8sClient{
		podInformer: mockInformer,
	}

	// Execute
	ip, err := client.GetSandboxPodIP(context.Background(), "test-namespace", "test-sandbox")

	// Verify
	assert.Error(t, err)
	assert.Empty(t, ip)
	assert.Contains(t, err.Error(), "no pod found for sandbox test-sandbox")
}

// TestGetSandboxPodIP_PodNotRunning tests that GetSandboxPodIP returns validation error when pod is not running
func TestGetSandboxPodIP_PodNotRunning(t *testing.T) {
	// Setup: Create a mock indexer with a pending pod
	indexer := newMockIndexer()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"sandbox-name": "test-sandbox",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			PodIP: "10.0.0.1",
		},
	}
	_ = indexer.Add(pod)

	mockInformer := newMockSharedIndexInformer(indexer)

	// Create K8sClient with mock informer
	client := &K8sClient{
		podInformer: mockInformer,
	}

	// Execute
	ip, err := client.GetSandboxPodIP(context.Background(), "test-namespace", "test-sandbox")

	// Verify
	assert.Error(t, err)
	assert.Empty(t, ip)
	assert.Contains(t, err.Error(), "pod not running yet, status: Pending")
}

// TestGetSandboxPodIP_PodWithoutIP tests that GetSandboxPodIP returns validation error when pod has no IP
func TestGetSandboxPodIP_PodWithoutIP(t *testing.T) {
	// Setup: Create a mock indexer with a running pod but no IP
	indexer := newMockIndexer()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"sandbox-name": "test-sandbox",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "", // No IP assigned
		},
	}
	_ = indexer.Add(pod)

	mockInformer := newMockSharedIndexInformer(indexer)

	// Create K8sClient with mock informer
	client := &K8sClient{
		podInformer: mockInformer,
	}

	// Execute
	ip, err := client.GetSandboxPodIP(context.Background(), "test-namespace", "test-sandbox")

	// Verify
	assert.Error(t, err)
	assert.Empty(t, ip)
	assert.Contains(t, err.Error(), "pod IP not assigned yet")
}

// TestGetSandboxPodIP_PodWithWrongLabel tests that GetSandboxPodIP returns error when pod has wrong label
func TestGetSandboxPodIP_PodWithWrongLabel(t *testing.T) {
	// Setup: Create a mock indexer with a pod that has different sandbox-name label
	indexer := newMockIndexer()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"sandbox-name": "other-sandbox", // Different sandbox name
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
		},
	}
	_ = indexer.Add(pod)

	mockInformer := newMockSharedIndexInformer(indexer)

	// Create K8sClient with mock informer
	client := &K8sClient{
		podInformer: mockInformer,
	}

	// Execute
	ip, err := client.GetSandboxPodIP(context.Background(), "test-namespace", "test-sandbox")

	// Verify
	assert.Error(t, err)
	assert.Empty(t, ip)
	assert.Contains(t, err.Error(), "no pod found for sandbox test-sandbox")
}

// TestGetSandboxPodIP_MultiplePodsInNamespace tests that GetSandboxPodIP finds the correct pod when multiple pods exist
func TestGetSandboxPodIP_MultiplePodsInNamespace(t *testing.T) {
	// Setup: Create a mock indexer with multiple pods in the same namespace
	indexer := newMockIndexer()

	// Pod 1: Wrong sandbox name
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"sandbox-name": "other-sandbox",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
		},
	}
	_ = indexer.Add(pod1)

	// Pod 2: Correct sandbox name
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"sandbox-name": "test-sandbox",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.2",
		},
	}
	_ = indexer.Add(pod2)

	mockInformer := newMockSharedIndexInformer(indexer)

	// Create K8sClient with mock informer
	client := &K8sClient{
		podInformer: mockInformer,
	}

	// Execute
	ip, err := client.GetSandboxPodIP(context.Background(), "test-namespace", "test-sandbox")

	// Verify
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.2", ip)
}
