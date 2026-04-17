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
