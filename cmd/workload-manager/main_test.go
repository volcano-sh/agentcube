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

package main

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	extensionsv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

func TestSchemeRegistration(t *testing.T) {
	// schemeBuilder is populated in init()

	expectedKinds := []schema.GroupVersionKind{
		sandboxv1beta1.SchemeGroupVersion.WithKind("Sandbox"),
		extensionsv1beta1.SchemeGroupVersion.WithKind("SandboxTemplate"),
		extensionsv1beta1.SchemeGroupVersion.WithKind("SandboxWarmPool"),
		runtimev1alpha1.SchemeGroupVersion.WithKind("CodeInterpreter"),
		runtimev1alpha1.SchemeGroupVersion.WithKind("AgentRuntime"),
	}

	for _, gvk := range expectedKinds {
		if !schemeBuilder.Recognizes(gvk) {
			t.Errorf("Scheme does not recognize %v", gvk)
		}
	}
}
