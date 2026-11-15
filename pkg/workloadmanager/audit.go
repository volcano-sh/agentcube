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

package workloadmanager

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	// AuditReasonCreated is emitted when a sandbox is successfully created.
	AuditReasonCreated = "SandboxCreated"
	// AuditReasonCreateFailed is emitted when sandbox creation fails.
	AuditReasonCreateFailed = "SandboxCreateFailed"
	// AuditReasonDeleted is emitted when a sandbox is successfully deleted.
	AuditReasonDeleted = "SandboxDeleted"
)

// sandboxAuditEvent emits a Kubernetes Event in the sandbox's namespace so
// that sandbox lifecycle transitions appear in `kubectl get events` and feed
// into standard CNCF observability tooling (Loki, Falco, the K8s audit sink).
//
// ownerHash is a SHA-256-shortened owner identity label (never the raw sub
// claim) so operators can correlate events without storing PII in the event log.
//
// Audit failures are non-fatal: errors are logged at V(2) and the caller's
// control path continues. A nil or panicking clientset (common in unit tests
// that do not set up a full K8sClient) is handled gracefully.
func sandboxAuditEvent(ctx context.Context, clientset kubernetes.Interface, namespace, sandboxName, ownerHash, reason, message string) {
	defer func() {
		if r := recover(); r != nil {
			klog.V(4).Infof("audit: suppressed panic emitting event %s for %s/%s: %v", reason, namespace, sandboxName, r)
		}
	}()
	if clientset == nil {
		return
	}
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("agentcube-%s-", sandboxName),
			Namespace:    namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Sandbox",
			Namespace: namespace,
			Name:      sandboxName,
		},
		Reason:         reason,
		Message:        message,
		Source:         corev1.EventSource{Component: "agentcube-workloadmanager"},
		Type:           corev1.EventTypeNormal,
		FirstTimestamp: metav1.Now(),
		LastTimestamp:  metav1.Now(),
		Count:          1,
	}
	if reason == AuditReasonCreateFailed {
		event.Type = corev1.EventTypeWarning
	}
	if ownerHash != "" {
		if event.Annotations == nil {
			event.Annotations = make(map[string]string)
		}
		event.Annotations["agentcube.io/owner-hash"] = ownerHash
	}
	if _, err := clientset.CoreV1().Events(namespace).Create(ctx, event, metav1.CreateOptions{}); err != nil {
		klog.V(2).Infof("audit: failed to emit event %s for sandbox %s/%s: %v", reason, namespace, sandboxName, err)
	}
}
