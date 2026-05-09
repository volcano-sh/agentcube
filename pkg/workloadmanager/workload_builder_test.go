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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestBuildSandboxObject_DoesNotMutateCallerLabels verifies that buildSandboxObject
// does not write session-specific labels back into the caller's map, which would
// corrupt the informer-cached CRD object shared across goroutines.
func TestBuildSandboxObject_DoesNotMutateCallerLabels(t *testing.T) {
	original := map[string]string{
		"app": "my-app",
		"env": "test",
	}
	// Take a snapshot before the call.
	before := make(map[string]string, len(original))
	for k, v := range original {
		before[k] = v
	}

	params := &buildSandboxParams{
		namespace:   "default",
		sandboxName: "sandbox-abc",
		sessionID:   "session-123",
		ttl:         DefaultSandboxTTL,
		idleTimeout: DefaultSandboxIdleTimeout,
		podLabels:   original,
	}

	sandbox := buildSandboxObject(params)

	// The caller's map must be unchanged.
	if len(original) != len(before) {
		t.Fatalf("caller labels map was mutated: before=%v after=%v", before, original)
	}
	for k, v := range before {
		if original[k] != v {
			t.Fatalf("caller labels map was mutated: key %q changed from %q to %q", k, v, original[k])
		}
	}

	// The sandbox pod-template labels must contain both original and injected keys.
	podLabels := sandbox.Spec.PodTemplate.ObjectMeta.Labels
	if podLabels["app"] != "my-app" {
		t.Errorf("expected pod label app=my-app, got %q", podLabels["app"])
	}
	if podLabels[SessionIdLabelKey] != "session-123" {
		t.Errorf("expected pod label %s=session-123, got %q", SessionIdLabelKey, podLabels[SessionIdLabelKey])
	}
	if podLabels[SandboxNameLabelKey] != "sandbox-abc" {
		t.Errorf("expected pod label %s=sandbox-abc, got %q", SandboxNameLabelKey, podLabels[SandboxNameLabelKey])
	}

	// Mutating the returned sandbox labels must not affect the caller's map.
	podLabels["app"] = "mutated-app"
	delete(podLabels, "env")

	if original["app"] != before["app"] {
		t.Fatalf("caller labels map was aliased through sandbox labels: key %q changed from %q to %q", "app", before["app"], original["app"])
	}
	if original["env"] != before["env"] {
		t.Fatalf("caller labels map was aliased through sandbox labels: key %q changed from %q to %q", "env", before["env"], original["env"])
	}
}

// TestBuildSandboxObject_NilLabels verifies that a nil podLabels input still
// produces a sandbox with the injected session labels.
func TestBuildSandboxObject_NilLabels(t *testing.T) {
	params := &buildSandboxParams{
		namespace:   "default",
		sandboxName: "sandbox-xyz",
		sessionID:   "session-456",
		ttl:         time.Hour,
		idleTimeout: 15 * time.Minute,
		podLabels:   nil,
	}

	sandbox := buildSandboxObject(params)

	podLabels := sandbox.Spec.PodTemplate.ObjectMeta.Labels
	if podLabels[SessionIdLabelKey] != "session-456" {
		t.Errorf("expected %s=session-456, got %q", SessionIdLabelKey, podLabels[SessionIdLabelKey])
	}
	if podLabels[SandboxNameLabelKey] != "sandbox-xyz" {
		t.Errorf("expected %s=sandbox-xyz, got %q", SandboxNameLabelKey, podLabels[SandboxNameLabelKey])
	}
}

// TestBuildSandboxObject_WorkloadNameLabel verifies that the WorkloadNameLabelKey
// label is correctly set on the Sandbox object metadata from the workloadName param.
// This covers the bug where CodeInterpreter sandboxes were missing this label.
func TestBuildSandboxObject_WorkloadNameLabel(t *testing.T) {
	tests := []struct {
		name         string
		workloadName string
		wantLabel    string
	}{
		{
			name:         "workloadName is set",
			workloadName: "my-code-interpreter",
			wantLabel:    "my-code-interpreter",
		},
		{
			name:         "workloadName is empty",
			workloadName: "",
			wantLabel:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := &buildSandboxParams{
				namespace:    "default",
				workloadName: tt.workloadName,
				sandboxName:  "sandbox-wl-test",
				sessionID:    "session-wl-test",
				ttl:          time.Hour,
				idleTimeout:  15 * time.Minute,
			}
			sandbox := buildSandboxObject(params)

			got := sandbox.ObjectMeta.Labels[WorkloadNameLabelKey]
			if got != tt.wantLabel {
				t.Errorf("expected %s=%q, got %q", WorkloadNameLabelKey, tt.wantLabel, got)
			}
		})
	}
}

// TestBuildSandboxClaimObject verifies that buildSandboxClaimObject correctly
// populates all fields including labels, annotations, and owner references.
func TestBuildSandboxClaimObject(t *testing.T) {
	t.Run("with owner reference", func(t *testing.T) {
		ownerRef := &metav1.OwnerReference{
			APIVersion: "runtime.agentcube.volcano.sh/v1alpha1",
			Kind:       "CodeInterpreter",
			Name:       "my-ci",
			UID:        "test-uid-123",
		}
		params := &buildSandboxClaimParams{
			namespace:           "test-ns",
			name:                "claim-abc",
			sandboxTemplateName: "my-ci",
			sessionID:           "session-claim-test",
			idleTimeout:         10 * time.Minute,
			ownerReference:      ownerRef,
		}
		claim := buildSandboxClaimObject(params)

		if claim.Namespace != "test-ns" {
			t.Errorf("expected namespace test-ns, got %q", claim.Namespace)
		}
		if claim.Name != "claim-abc" {
			t.Errorf("expected name claim-abc, got %q", claim.Name)
		}
		if claim.Spec.TemplateRef.Name != "my-ci" {
			t.Errorf("expected templateRef name my-ci, got %q", claim.Spec.TemplateRef.Name)
		}
		if claim.Labels[SessionIdLabelKey] != "session-claim-test" {
			t.Errorf("expected label %s=session-claim-test, got %q", SessionIdLabelKey, claim.Labels[SessionIdLabelKey])
		}
		if claim.Annotations[IdleTimeoutAnnotationKey] != "10m0s" {
			t.Errorf("expected annotation %s=10m0s, got %q", IdleTimeoutAnnotationKey, claim.Annotations[IdleTimeoutAnnotationKey])
		}
		if len(claim.OwnerReferences) != 1 {
			t.Fatalf("expected 1 owner reference, got %d", len(claim.OwnerReferences))
		}
		if claim.OwnerReferences[0].Name != "my-ci" {
			t.Errorf("expected owner ref name my-ci, got %q", claim.OwnerReferences[0].Name)
		}
	})

	t.Run("without owner reference", func(t *testing.T) {
		params := &buildSandboxClaimParams{
			namespace:           "default",
			name:                "claim-no-owner",
			sandboxTemplateName: "template-1",
			sessionID:           "session-no-owner",
			idleTimeout:         0,
		}
		claim := buildSandboxClaimObject(params)

		if len(claim.OwnerReferences) != 0 {
			t.Errorf("expected 0 owner references, got %d", len(claim.OwnerReferences))
		}
		if claim.Annotations[IdleTimeoutAnnotationKey] != DefaultSandboxIdleTimeout.String() {
			t.Errorf("expected default idle timeout annotation %s, got %q",
				DefaultSandboxIdleTimeout.String(), claim.Annotations[IdleTimeoutAnnotationKey])
		}
	})
}
