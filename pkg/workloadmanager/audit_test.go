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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
)

func TestSandboxAuditEvent_Created_Normal(t *testing.T) {
	fakeCS := kubernetesfake.NewSimpleClientset()
	ctx := context.Background()

	sandboxAuditEvent(ctx, fakeCS, "default", "sb-create", "abc123", AuditReasonCreated, "sandbox created")

	events, err := fakeCS.CoreV1().Events("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, events.Items, 1)

	ev := events.Items[0]
	assert.Equal(t, AuditReasonCreated, ev.Reason)
	assert.Equal(t, corev1.EventTypeNormal, ev.Type)
	assert.Equal(t, "sandbox created", ev.Message)
	assert.Equal(t, "abc123", ev.Annotations["agentcube.io/owner-hash"])
	assert.Equal(t, "agentcube-workloadmanager", ev.Source.Component)
}

func TestSandboxAuditEvent_CreateFailed_Warning(t *testing.T) {
	fakeCS := kubernetesfake.NewSimpleClientset()
	ctx := context.Background()

	sandboxAuditEvent(ctx, fakeCS, "default", "sb-fail", "abc123", AuditReasonCreateFailed, "image pull failed")

	events, err := fakeCS.CoreV1().Events("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, events.Items, 1)
	assert.Equal(t, corev1.EventTypeWarning, events.Items[0].Type)
}

func TestSandboxAuditEvent_Deleted(t *testing.T) {
	fakeCS := kubernetesfake.NewSimpleClientset()
	ctx := context.Background()

	sandboxAuditEvent(ctx, fakeCS, "ns-a", "sb-gc", "hash42", AuditReasonDeleted, "GC-deleted")

	events, err := fakeCS.CoreV1().Events("ns-a").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, events.Items, 1)
	ev := events.Items[0]
	assert.Equal(t, AuditReasonDeleted, ev.Reason)
	assert.Equal(t, corev1.EventTypeNormal, ev.Type)
	assert.Equal(t, "hash42", ev.Annotations["agentcube.io/owner-hash"])
}

func TestSandboxAuditEvent_EmptyOwnerHash_NoAnnotation(t *testing.T) {
	fakeCS := kubernetesfake.NewSimpleClientset()
	ctx := context.Background()

	sandboxAuditEvent(ctx, fakeCS, "default", "sb-anon", "", AuditReasonCreated, "anon sandbox")

	events, err := fakeCS.CoreV1().Events("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, events.Items, 1)
	assert.Empty(t, events.Items[0].Annotations["agentcube.io/owner-hash"])
}

func TestSandboxAuditEvent_NilClientset_NoPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		sandboxAuditEvent(context.Background(), nil, "default", "sb-nil", "", AuditReasonCreated, "nil clientset")
	})
}
