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

	"github.com/stretchr/testify/assert"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"k8s.io/utils/ptr"
)

func TestConvertToPodTemplate_RuntimeClassName(t *testing.T) {
	reconciler := &CodeInterpreterReconciler{}

	tests := []struct {
		name             string
		runtimeClassName *string
		expected         *string
		description      string
	}{
		{
			name:             "empty string should be normalized to nil",
			runtimeClassName: ptr.To(""),
			expected:         nil,
			description:      "When RuntimeClassName is empty string, it should be set to nil",
		},
		{
			name:             "nil should remain nil",
			runtimeClassName: nil,
			expected:         nil,
			description:      "When RuntimeClassName is nil, it should remain nil",
		},
		{
			name:             "valid value should remain unchanged",
			runtimeClassName: ptr.To("kuasar-vmm"),
			expected:         ptr.To("kuasar-vmm"),
			description:      "When RuntimeClassName has a valid value, it should remain unchanged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image:            "test-image:latest",
				RuntimeClassName: tt.runtimeClassName,
			}

			ci := &runtimev1alpha1.CodeInterpreter{
				Spec: runtimev1alpha1.CodeInterpreterSpec{
					AuthMode: runtimev1alpha1.AuthModeNone, // Use none to skip public key injection
				},
			}

			result := reconciler.convertToPodTemplate(template, ci)

			assert.Equal(t, tt.expected, result.Spec.RuntimeClassName, tt.description)
		})
	}
}
