package workloadmanager

import (
	"context"
	"fmt"
	"log"
	"time"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

const (
	DefaultSandboxTTL         = 8 * time.Hour
	DefaultSandboxIdleTimeout = 15 * time.Minute
	RedisNoExpiredTTL         = 0
)

var (
	// SessionIdLabelKey labels key for session id
	SessionIdLabelKey = "runtime.agentcube.io/session-id" // revive:disable-line:var-naming - keep label backward compatible
	// WorkloadNameLabelKey labels key for workload name
	WorkloadNameLabelKey = "runtime.agentcube.io/workload-name"
	// Annotation key for last activity time
	LastActivityAnnotationKey = "last-activity-time"
	// IdleTimeoutAnnotationKey key for idle timeout
	IdleTimeoutAnnotationKey = "runtime.agentcube.io/idle-timeout"
	// Annotation key for creator service account
	CreatorServiceAccountAnnotationKey = "creator-service-account"
)

// K8sClient encapsulates the Kubernetes client
type K8sClient struct {
	clientset       *kubernetes.Clientset
	dynamicClient   dynamic.Interface
	scheme          *runtime.Scheme
	baseConfig      *rest.Config // Store base config for creating user clients
	clientCache     *ClientCache // LRU cache for user clients
	dynamicInformer dynamicinformer.DynamicSharedInformerFactory
}

type sandboxExternalInfo struct {
	SandboxClaimName string
	SessionID        string
	Ports            []runtimev1alpha1.TargetPort
}

// NewK8sClient creates a new Kubernetes client
func NewK8sClient() (*K8sClient, error) {
	var config *rest.Config
	var err error

	// Try in-cluster config first
	config, err = rest.InClusterConfig()
	if err != nil {
		// If not in cluster, use default kubeconfig loading rules
		// This will check KUBECONFIG env var, then ~/.kube/config
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		config, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
		}
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	// Create dynamic client (for CRD operations)
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create scheme and register agent-sandbox types
	scheme := runtime.NewScheme()
	if err := sandboxv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add agent-sandbox scheme: %w", err)
	}

	return &K8sClient{
		clientset:       clientset,
		dynamicClient:   dynamicClient,
		scheme:          scheme,
		baseConfig:      config,
		clientCache:     NewClientCache(100), // Cache up to 100 clients
		dynamicInformer: dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0),
	}, nil
}

// SandboxInfo contains information about the created Sandbox
type SandboxInfo struct {
	Name      string
	Namespace string
}

// UserK8sClient creates a temporary Kubernetes client using user's token
type UserK8sClient struct {
	dynamicClient dynamic.Interface
	namespace     string
}

// NewUserK8sClient creates a K8s client using the provided user token
// This method creates a new client without using cache (for direct creation)
func (c *K8sClient) NewUserK8sClient(userToken, namespace string) (*UserK8sClient, error) {
	// Create a new config based on base config but with user's token
	config := rest.CopyConfig(c.baseConfig)
	config.BearerToken = userToken
	config.BearerTokenFile = "" // Clear token file if any

	// Create dynamic client with user's token
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client with user token: %w", err)
	}

	return &UserK8sClient{
		dynamicClient: dynamicClient,
		namespace:     namespace,
	}, nil
}

// GetOrCreateUserK8sClient gets a cached client or creates a new one if not found
// Uses service account name and namespace as cache key
// If token doesn't match cached entry, Set will overwrite it
func (c *K8sClient) GetOrCreateUserK8sClient(userToken, namespace, serviceAccountName string) (*UserK8sClient, error) {
	// Create cache key
	cacheKey := makeCacheKey(namespace, serviceAccountName)

	// Try to get from cache
	if cachedClient := c.clientCache.Get(cacheKey); cachedClient != nil {
		return cachedClient, nil
	}

	// Create new client
	client, err := c.NewUserK8sClient(userToken, namespace)
	if err != nil {
		return nil, err
	}

	// Store in cache (will overwrite if key exists)
	c.clientCache.Set(cacheKey, userToken, client)

	return client, nil
}

// createSandbox creates a Sandbox using the provided dynamic client
func createSandbox(ctx context.Context, client dynamic.Interface, sandbox *sandboxv1alpha1.Sandbox) (*SandboxInfo, error) {
	// Convert to unstructured for dynamic client
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed to convert sandbox to unstructured: %w", err)
	}

	unstructuredSandbox := &unstructured.Unstructured{Object: unstructuredObj}

	// Create Sandbox
	created, err := client.Resource(SandboxGVR).Namespace(sandbox.Namespace).Create(
		ctx,
		unstructuredSandbox,
		metav1.CreateOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox: %w", err)
	}

	return &SandboxInfo{
		Name:      created.GetName(),
		Namespace: created.GetNamespace(),
	}, nil
}

// createSandboxClaim creates a SandboxClaim using the provided dynamic client
func createSandboxClaim(ctx context.Context, client dynamic.Interface, sandboxClaim *extensionsv1alpha1.SandboxClaim) error {
	// Convert to unstructured for dynamic client
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sandboxClaim)
	if err != nil {
		return fmt.Errorf("failed to convert sandbox claim to unstructured: %w", err)
	}

	unstructuredSandbox := &unstructured.Unstructured{Object: unstructuredObj}

	// Create SandboxClaim
	_, err = client.Resource(SandboxClaimGVR).Namespace(sandboxClaim.Namespace).Create(
		ctx,
		unstructuredSandbox,
		metav1.CreateOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to create sandbox claim: %w", err)
	}

	return nil
}

// deleteSandbox deletes a Sandbox using the provided dynamic client
func deleteSandbox(ctx context.Context, client dynamic.Interface, namespace, sandboxName string) error {
	err := client.Resource(SandboxGVR).Namespace(namespace).Delete(
		ctx,
		sandboxName,
		metav1.DeleteOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox: %w", err)
	}
	return nil
}

// deleteSandboxClaim deletes a SandboxClaim using the provided dynamic client
func deleteSandboxClaim(ctx context.Context, client dynamic.Interface, namespace, sandboxClaimName string) error {
	err := client.Resource(SandboxClaimGVR).Namespace(namespace).Delete(
		ctx,
		sandboxClaimName,
		metav1.DeleteOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox claim: %w", err)
	}
	return nil
}

// CreateSandboxClaim creates a new SandboxClaim using user's permissions
func (u *UserK8sClient) CreateSandboxClaim(ctx context.Context, sandboxClaim *extensionsv1alpha1.SandboxClaim) error {
	return createSandboxClaim(ctx, u.dynamicClient, sandboxClaim)
}

// CreateSandbox creates a new Sandbox using user's permissions
func (u *UserK8sClient) CreateSandbox(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox) (*SandboxInfo, error) {
	return createSandbox(ctx, u.dynamicClient, sandbox)
}

// DeleteSandbox deletes a Sandbox resource using user's permissions
func (u *UserK8sClient) DeleteSandbox(ctx context.Context, namespace, sandboxName string) error {
	return deleteSandbox(ctx, u.dynamicClient, namespace, sandboxName)
}

// DeleteSandboxClaim deletes a SandboxClaim resource using user's permissions
func (u *UserK8sClient) DeleteSandboxClaim(ctx context.Context, namespace, sandboxClaimName string) error {
	return deleteSandboxClaim(ctx, u.dynamicClient, namespace, sandboxClaimName)
}

// GetSandboxPodIP gets the IP address of the pod corresponding to the Sandbox
func (c *K8sClient) GetSandboxPodIP(ctx context.Context, namespace, sandboxName, podName string) (string, error) {
	if podName != "" {
		pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err == nil && pod != nil {
			return validateAndGetPodIP(pod)
		}
		log.Printf("failed to get sandbox pod %s/%s, try get pod by sandbox-name label", namespace, podName)
	}

	// Find pod through label selector (sandbox-name label we set)
	labelSelector := fmt.Sprintf("sandbox-name=%s", sandboxName)
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err == nil && pods != nil && len(pods.Items) > 0 {
		return validateAndGetPodIP(&pods.Items[0])
	}

	return "", fmt.Errorf("no pod found for sandbox %s", sandboxName)
}

// validateAndGetPodIP validates pod status and returns IP
func validateAndGetPodIP(pod *corev1.Pod) (string, error) {
	// Check if Pod is running
	if pod.Status.Phase != corev1.PodRunning {
		return "", fmt.Errorf("pod not running yet, status: %s", pod.Status.Phase)
	}

	// Check if Pod IP is assigned
	if pod.Status.PodIP == "" {
		return "", fmt.Errorf("pod IP not assigned yet")
	}

	return pod.Status.PodIP, nil
}

// WaitForSandboxReady waits for the Sandbox to be ready
func (c *K8sClient) WaitForSandboxReady(ctx context.Context, namespace, sandboxName string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for sandbox to be ready")
		case <-ticker.C:
			// Check Sandbox status
			sandbox, err := c.dynamicClient.Resource(SandboxGVR).Namespace(namespace).Get(
				ctx,
				sandboxName,
				metav1.GetOptions{},
			)
			if err != nil {
				continue
			}

			// Check status.phase or status.ready
			status, found, err := unstructured.NestedMap(sandbox.Object, "status")
			if err != nil || !found {
				continue
			}

			phase, found, err := unstructured.NestedString(status, "phase")
			if err != nil || !found {
				continue
			}

			if phase == "Running" || phase == "Ready" {
				return nil
			}
		}
	}
}

// GetSandboxInformer returns a shared informer for Sandbox CRD
func (c *K8sClient) GetSandboxInformer() cache.SharedInformer {
	return c.dynamicInformer.ForResource(SandboxGVR).Informer()
}
