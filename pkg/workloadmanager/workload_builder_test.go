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

	corev1 "k8s.io/api/core/v1"
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

// ---- tests: injectMTLSVolumes (spiffe-helper sidecar injection) ----

func TestInjectMTLSVolumes_InjectsSidecarAndVolumes(t *testing.T) {
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "code-interpreter",
				Image: "picod:latest",
				Args:  []string{"--port=8080"},
			},
		},
	}

	injectMTLSVolumes(&podSpec)

	// 3 volumes: spire-agent-socket, spiffe-helper-config, spire-certs
	if len(podSpec.Volumes) != 3 {
		t.Fatalf("expected 3 volumes, got %d", len(podSpec.Volumes))
	}
	if podSpec.Volumes[0].Name != spireAgentSocketVolumeName {
		t.Errorf("expected volume %q, got %q", spireAgentSocketVolumeName, podSpec.Volumes[0].Name)
	}
	if podSpec.Volumes[1].Name != spiffeHelperConfigVolumeName {
		t.Errorf("expected volume %q, got %q", spiffeHelperConfigVolumeName, podSpec.Volumes[1].Name)
	}
	if podSpec.Volumes[2].Name != spireCertVolumeName {
		t.Errorf("expected volume %q, got %q", spireCertVolumeName, podSpec.Volumes[2].Name)
	}

	// 2 containers: spiffe-helper sidecar (index 0) + original (index 1)
	if len(podSpec.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(podSpec.Containers))
	}
	if podSpec.Containers[0].Name != "spiffe-helper" {
		t.Errorf("expected sidecar name 'spiffe-helper', got %q", podSpec.Containers[0].Name)
	}
	if podSpec.Containers[1].Name != "code-interpreter" {
		t.Errorf("expected main container name 'code-interpreter', got %q", podSpec.Containers[1].Name)
	}

	// Sidecar has 3 volume mounts
	if len(podSpec.Containers[0].VolumeMounts) != 3 {
		t.Fatalf("expected 3 sidecar volume mounts, got %d", len(podSpec.Containers[0].VolumeMounts))
	}

	// Main container has spire-certs mount
	foundCertMount := false
	for _, vm := range podSpec.Containers[1].VolumeMounts {
		if vm.Name == spireCertVolumeName && vm.MountPath == spireCertMountPath {
			foundCertMount = true
		}
	}
	if !foundCertMount {
		t.Error("expected main container to have spire-certs volume mount")
	}

	// Main container args include mTLS flags
	args := podSpec.Containers[1].Args
	expectedFlags := []string{
		"--enable-mtls",
		"--mtls-cert-file=" + spireCertMountPath + "/" + svidCertFileName,
		"--mtls-key-file=" + spireCertMountPath + "/" + svidKeyFileName,
		"--mtls-ca-file=" + spireCertMountPath + "/" + svidBundleFileName,
	}
	for _, flag := range expectedFlags {
		found := false
		for _, arg := range args {
			if arg == flag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected flag %q in container args, got %v", flag, args)
		}
	}
}

func TestInjectMTLSVolumes_PreservesExistingArgs(t *testing.T) {
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name: "picod",
				Args: []string{"--port=8080", "--workspace=/tmp"},
			},
		},
	}

	injectMTLSVolumes(&podSpec)

	// Original args should still be present at the start (now on container[1])
	args := podSpec.Containers[1].Args
	if len(args) < 6 { // 2 original + 4 mTLS flags
		t.Fatalf("expected at least 6 args, got %d: %v", len(args), args)
	}
	if args[0] != "--port=8080" || args[1] != "--workspace=/tmp" {
		t.Errorf("original args should be preserved at the start: %v", args)
	}
}

func TestInjectMTLSVolumes_EmptyContainers(t *testing.T) {
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{},
	}

	// Should not panic with empty containers
	injectMTLSVolumes(&podSpec)

	// Volumes are still added
	if len(podSpec.Volumes) != 3 {
		t.Errorf("expected 3 volumes even with empty containers, got %d", len(podSpec.Volumes))
	}
	// Only the sidecar container exists
	if len(podSpec.Containers) != 1 {
		t.Errorf("expected 1 container (sidecar only), got %d", len(podSpec.Containers))
	}
}

