package picoapiserver

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// K8sClient encapsulates the Kubernetes client
type K8sClient struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	namespace     string
}

// Sandbox CRD GroupVersionResource
var sandboxGVR = schema.GroupVersionResource{
	Group:    "agent-sandbox.k8s.io",
	Version:  "v1alpha1",
	Resource: "sandboxes",
}

// NewK8sClient creates a new Kubernetes client
func NewK8sClient(kubeconfig, namespace string) (*K8sClient, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		// Use provided kubeconfig file
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
	} else {
		// Use in-cluster configuration
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
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

	return &K8sClient{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     namespace,
	}, nil
}

// SandboxInfo contains information about the created Sandbox
type SandboxInfo struct {
	Name      string
	Namespace string
}

// CreateSandbox creates a new Sandbox CRD resource
func (c *K8sClient) CreateSandbox(ctx context.Context, sessionID, image string, metadata map[string]interface{}) (*SandboxInfo, error) {
	// Construct Sandbox CRD object
	// Note: This structure needs to be adjusted according to the actual agent-sandbox CRD specification
	sandboxName := fmt.Sprintf("sandbox-%s", sessionID[:8]) // Use first 8 characters of session ID

	sandbox := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "agent-sandbox.k8s.io/v1alpha1",
			"kind":       "Sandbox",
			"metadata": map[string]interface{}{
				"name":      sandboxName,
				"namespace": c.namespace,
				"labels": map[string]interface{}{
					"session-id": sessionID,
					"managed-by": "pico-apiserver",
				},
				"annotations": metadata,
			},
			"spec": map[string]interface{}{
				"image": image,
				// TODO: Add more fields according to actual CRD specification
				// e.g.: resources, volumes, network, etc.
			},
		},
	}

	// Create Sandbox CRD
	created, err := c.dynamicClient.Resource(sandboxGVR).Namespace(c.namespace).Create(
		ctx,
		sandbox,
		metav1.CreateOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox CRD: %w", err)
	}

	return &SandboxInfo{
		Name:      created.GetName(),
		Namespace: created.GetNamespace(),
	}, nil
}

// DeleteSandbox deletes a Sandbox CRD resource
func (c *K8sClient) DeleteSandbox(ctx context.Context, sandboxName string) error {
	err := c.dynamicClient.Resource(sandboxGVR).Namespace(c.namespace).Delete(
		ctx,
		sandboxName,
		metav1.DeleteOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox CRD: %w", err)
	}
	return nil
}

// GetSandboxPodIP gets the IP address of the pod corresponding to the Sandbox
func (c *K8sClient) GetSandboxPodIP(ctx context.Context, sandboxName string) (string, error) {
	// Method 1: Find pod through label selector
	// Assume the Sandbox controller creates a Pod with the same name as the Sandbox, or with specific labels
	labelSelector := fmt.Sprintf("sandbox=%s", sandboxName)

	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pod found for sandbox %s", sandboxName)
	}

	pod := pods.Items[0]

	// Wait for Pod to be ready and get IP
	if pod.Status.Phase != "Running" {
		return "", fmt.Errorf("pod not running yet, status: %s", pod.Status.Phase)
	}

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
			sandbox, err := c.dynamicClient.Resource(sandboxGVR).Namespace(c.namespace).Get(
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
