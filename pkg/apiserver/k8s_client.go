package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	agentsv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

var (
	// Annotation key for last activity time
	LastActivityAnnotationKey = "last-activity-time"
	// Annotation key for creator service account
	CreatorServiceAccountAnnotationKey = "creator-service-account"
)

// K8sClient encapsulates the Kubernetes client
type K8sClient struct {
	clientset       *kubernetes.Clientset
	dynamicClient   dynamic.Interface
	namespace       string
	scheme          *runtime.Scheme
	baseConfig      *rest.Config // Store base config for creating user clients
	clientCache     *ClientCache // LRU cache for user clients
	dynamicInformer dynamicinformer.DynamicSharedInformerFactory
}

// Sandbox CRD GroupVersionResource
var SandboxGVR = schema.GroupVersionResource{
	Group:    "agents.x-k8s.io",
	Version:  "v1alpha1",
	Resource: "sandboxes",
}

// NewK8sClient creates a new Kubernetes client
func NewK8sClient(namespace string) (*K8sClient, error) {
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
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add agent-sandbox scheme: %w", err)
	}

	return &K8sClient{
		clientset:       clientset,
		dynamicClient:   dynamicClient,
		namespace:       namespace,
		scheme:          scheme,
		baseConfig:      config,
		clientCache:     NewClientCache(100), // Cache up to 100 clients
		dynamicInformer: dynamicinformer.NewFilteredDynamicSharedInformerFactory(dynamicClient, 0, namespace, nil),
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
	if cachedClient := c.clientCache.Get(cacheKey, userToken); cachedClient != nil {
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

// buildSandboxObject builds a Sandbox object from parameters
func buildSandboxObject(namespace, sandboxID, sandboxName, image, sshPublicKey, runtimeClassName string, ttl int, metadata map[string]interface{}, createdAt time.Time, creatorServiceAccount string) *agentsv1alpha1.Sandbox {
	// Use default sandbox image if not specified
	if image == "" {
		image = "sandbox:latest"
	}

	// Prepare container environment variables
	env := []corev1.EnvVar{}
	if sshPublicKey != "" {
		env = append(env, corev1.EnvVar{
			Name:  "SSH_PUBLIC_KEY",
			Value: sshPublicKey,
		})
	}

	// Create PodSpec
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:            "sandbox",
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Env:             env,
			},
		},
	}

	// Set RuntimeClassName only if it's not empty
	if runtimeClassName != "" {
		podSpec.RuntimeClassName = &runtimeClassName
	}

	// Create Sandbox object using agent-sandbox types
	sandbox := &agentsv1alpha1.Sandbox{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agents.x-k8s.io/v1alpha1",
			Kind:       "Sandbox",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: namespace,
			Labels: map[string]string{
				"sandbox-id":   sandboxID,
				"managed-by":   "agentcube-apiserver",
				"sandbox-name": sandboxName,
			},
			Annotations: convertToStringMap(metadata),
		},
		Spec: agentsv1alpha1.SandboxSpec{
			PodTemplate: agentsv1alpha1.PodTemplate{
				Spec: podSpec,
			},
		},
	}
	// Use the provided creation time as the initial active time to ensure consistency
	if !createdAt.IsZero() {
		sandbox.Annotations[LastActivityAnnotationKey] = createdAt.Format(time.RFC3339)
	}
	// Store TTL in annotations so informer can calculate expiresAt
	if ttl > 0 {
		sandbox.Annotations["ttl"] = fmt.Sprintf("%d", ttl)
	}
	// Store creator service account in annotations for authorization
	if creatorServiceAccount != "" {
		sandbox.Annotations[CreatorServiceAccountAnnotationKey] = creatorServiceAccount
	}

	return sandbox
}

// createSandbox creates a Sandbox using the provided dynamic client
func createSandbox(ctx context.Context, client dynamic.Interface, namespace string, sandbox *agentsv1alpha1.Sandbox) (*SandboxInfo, error) {
	// Convert to unstructured for dynamic client
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed to convert sandbox to unstructured: %w", err)
	}

	unstructuredSandbox := &unstructured.Unstructured{Object: unstructuredObj}

	// Create Sandbox
	created, err := client.Resource(SandboxGVR).Namespace(namespace).Create(
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

// CreateSandbox creates a new Sandbox using user's permissions
func (u *UserK8sClient) CreateSandbox(ctx context.Context, sandboxID, sandboxName, image, sshPublicKey, runtimeClassName string, ttl int, metadata map[string]interface{}, createdAt time.Time, creatorServiceAccount string) (*SandboxInfo, error) {
	sandbox := buildSandboxObject(u.namespace, sandboxID, sandboxName, image, sshPublicKey, runtimeClassName, ttl, metadata, createdAt, creatorServiceAccount)
	return createSandbox(ctx, u.dynamicClient, u.namespace, sandbox)
}

// DeleteSandbox deletes a Sandbox resource using user's permissions
func (u *UserK8sClient) DeleteSandbox(ctx context.Context, namespace, sandboxName string) error {
	return deleteSandbox(ctx, u.dynamicClient, namespace, sandboxName)
}

// GetSandboxPodIP gets the IP address of the pod corresponding to the Sandbox
func (c *K8sClient) GetSandboxPodIP(ctx context.Context, sandboxName string) (string, error) {
	// Try multiple methods to find the pod

	// Method 1: Try to find pod by exact name (agent-sandbox controller may create pod with same name)
	pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, sandboxName, metav1.GetOptions{})
	if err == nil {
		// Found pod by exact name
		return validateAndGetPodIP(pod)
	}

	// Method 2: Find pod through label selector (sandbox-name label we set)
	labelSelector := fmt.Sprintf("sandbox-name=%s", sandboxName)
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err == nil && len(pods.Items) > 0 {
		return validateAndGetPodIP(&pods.Items[0])
	}

	// Method 3: Find pod by agent-sandbox controller labels
	// The agent-sandbox controller typically adds labels like "sandbox.agents.x-k8s.io/name"
	labelSelector = fmt.Sprintf("sandbox.agents.x-k8s.io/name=%s", sandboxName)
	pods, err = c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err == nil && len(pods.Items) > 0 {
		return validateAndGetPodIP(&pods.Items[0])
	}

	// Method 4: Find pod by owner reference (more reliable)
	allPods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list pods: %w", err)
	}

	for _, pod := range allPods.Items {
		// Check if pod has owner reference to our sandbox
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == "Sandbox" && owner.Name == sandboxName {
				return validateAndGetPodIP(&pod)
			}
		}
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
func (c *K8sClient) WaitForSandboxReady(ctx context.Context, sandboxName string, timeout time.Duration) error {
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
			sandbox, err := c.dynamicClient.Resource(SandboxGVR).Namespace(c.namespace).Get(
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

func (c *K8sClient) UpdateSandboxLastActivityWithPatch(ctx context.Context, sandboxName string, timestamp time.Time) error {
	// Prepare the patch data
	patchData := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				LastActivityAnnotationKey: timestamp.Format(time.RFC3339),
			},
		},
	}

	// Convert to JSON
	patchBytes, err := json.Marshal(patchData)
	if err != nil {
		return fmt.Errorf("failed to marshal patch data: %w", err)
	}

	// Apply the patch using json merge patch
	_, err = c.dynamicClient.Resource(SandboxGVR).Namespace(c.namespace).Patch(
		ctx,
		sandboxName,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to patch sandbox %s: %w", sandboxName, err)
	}

	return nil
}

// convertToStringMap converts map[string]interface{} to map[string]string for annotations
func convertToStringMap(m map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for k, v := range m {
		if v != nil {
			result[k] = fmt.Sprintf("%v", v)
		}
	}
	return result
}

// GetSandboxInformer returns a shared informer for Sandbox CRD
func (c *K8sClient) GetSandboxInformer() cache.SharedInformer {
	return c.dynamicInformer.ForResource(SandboxGVR).Informer()
}
