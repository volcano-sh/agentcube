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

			result := reconciler.convertToPodTemplate(template, nil)

			assert.Equal(t, tt.expected, result.Spec.RuntimeClassName, tt.description)
		})
	}
}
