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

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"

	"github.com/volcano-sh/agentcube/pkg/agentd"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSchemeBuilder(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, sandboxv1alpha1.AddToScheme(s))

	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &agentd.Reconciler{Client: cl, Scheme: s}
	require.NotNil(t, r)
}