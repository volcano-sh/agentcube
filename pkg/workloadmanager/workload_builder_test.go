/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at

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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestPublicKeyCache(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	
	// Set initial state
	publicKeyCacheMutex.Lock()
	cachedPublicKey = ""
	publicKeyCacheMutex.Unlock()
	
	assert.False(t, IsPublicKeyCached())
	
	// Create secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      IdentitySecretName,
			Namespace: IdentitySecretNamespace,
		},
		Data: map[string][]byte{
			PublicKeyDataKey: []byte("test-pub-key"),
		},
	}
	_, _ = fakeClient.CoreV1().Secrets(IdentitySecretNamespace).Create(context.Background(), secret, metav1.CreateOptions{})
	
	err := loadPublicKeyFromSecret(fakeClient)
	require.NoError(t, err)
	assert.True(t, IsPublicKeyCached())
	assert.Equal(t, "test-pub-key", GetCachedPublicKey())
}

func TestBuildSandboxObject(t *testing.T) {
	params := &buildSandboxParams{
		namespace:    "default",
		workloadName: "test-workload",
		sandboxName:  "test-sandbox",
		sessionID:    "sess-123",
		ttl:          time.Hour,
		idleTimeout:  time.Minute,
		podSpec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: "busybox"}},
		},
	}
	
	sb := buildSandboxObject(params)
	assert.Equal(t, "test-sandbox", sb.Name)
	assert.Equal(t, "default", sb.Namespace)
	assert.Equal(t, "sess-123", sb.Labels[SessionIdLabelKey])
	assert.Equal(t, "1m0s", sb.Annotations[IdleTimeoutAnnotationKey])
	assert.Equal(t, "busybox", sb.Spec.PodTemplate.Spec.Containers[0].Image)
}

func TestBuildSandboxByAgentRuntime(t *testing.T) {
	ifm := &Informers{
		AgentRuntimeInformer: fakeDynamicInformer(),
	}

	ar := &runtimev1alpha1.AgentRuntime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.io/v1alpha1",
			Kind:       "AgentRuntime",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-agent",
		},
		Spec: runtimev1alpha1.AgentRuntimeSpec{
			Template: &runtimev1alpha1.SandboxTemplate{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Image: "agent-image"}},
				},
			},
		},
	}

	unstructuredAR, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(ar)
	_ = ifm.AgentRuntimeInformer.GetStore().Add(&unstructured.Unstructured{Object: unstructuredAR})

	sandbox, entry, err := buildSandboxByAgentRuntime("default", "test-agent", ifm)
	require.NoError(t, err)
	assert.NotNil(t, sandbox)
	assert.Equal(t, types.SandboxKind, entry.Kind)
	assert.Equal(t, "agent-image", sandbox.Spec.PodTemplate.Spec.Containers[0].Image)
}

func fakeDynamicInformer() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(nil, nil, 0, nil)
}

func TestBuildSandboxClaimObject(t *testing.T) {
	params := &buildSandboxClaimParams{
		namespace:           "default",
		name:                "claim-1",
		sandboxTemplateName: "tmpl-1",
		sessionID:           "sess-1",
	}
	
	claim := buildSandboxClaimObject(params)
	assert.Equal(t, "claim-1", claim.Name)
	assert.Equal(t, "tmpl-1", claim.Spec.TemplateRef.Name)
	assert.Equal(t, "sess-1", claim.Labels[SessionIdLabelKey])
}

func TestBuildSandboxByCodeInterpreter(t *testing.T) {
	ifm := &Informers{
		CodeInterpreterInformer: fakeDynamicInformer(),
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.io/v1alpha1",
			Kind:       "CodeInterpreter",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-ci",
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModeNone,
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image: "ci-image",
			},
		},
	}

	unstructuredCI, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(ci)
	_ = ifm.CodeInterpreterInformer.GetStore().Add(&unstructured.Unstructured{Object: unstructuredCI})

	// 1. WarmPoolSize = 0 (Regular Sandbox)
	sandbox, claim, entry, err := buildSandboxByCodeInterpreter("default", "test-ci", ifm)
	require.NoError(t, err)
	assert.NotNil(t, sandbox)
	assert.Nil(t, claim)
	assert.Equal(t, types.SandboxKind, entry.Kind)
	assert.Equal(t, "ci-image", sandbox.Spec.PodTemplate.Spec.Containers[0].Image)

	// 2. WarmPoolSize > 0 (SandboxClaim)
	poolSize := int32(5)
	ci.Spec.WarmPoolSize = &poolSize
	unstructuredCI, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(ci)
	_ = ifm.CodeInterpreterInformer.GetStore().Update(&unstructured.Unstructured{Object: unstructuredCI})

	sandbox, claim, entry, err = buildSandboxByCodeInterpreter("default", "test-ci", ifm)
	require.NoError(t, err)
	assert.NotNil(t, sandbox)
	assert.NotNil(t, claim)
	assert.Equal(t, types.SandboxClaimsKind, entry.Kind)
}
