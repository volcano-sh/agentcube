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

package v1alpha1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateMultiAgentRuntime validates the MultiAgentRuntime and returns an ErrorList
func ValidateMultiAgentRuntime(mar *MultiAgentRuntime) field.ErrorList {
	allErrs := field.ErrorList{}
	allErrs = append(allErrs, ValidateMultiAgentRuntimeSpec(&mar.Spec, field.NewPath("spec"))...)
	return allErrs
}

// ValidateMultiAgentRuntimeSpec validates the MultiAgentRuntimeSpec
func ValidateMultiAgentRuntimeSpec(spec *MultiAgentRuntimeSpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(spec.Roles) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("roles"), "must provide at least one role"))
	}

	roleMap := make(map[string]bool)
	coordinatorCount := 0

	rolesPath := fldPath.Child("roles")
	for i, role := range spec.Roles {
		idxPath := rolesPath.Index(i)

		// Validate role name
		if len(role.Name) == 0 {
			allErrs = append(allErrs, field.Required(idxPath.Child("name"), "role name is required"))
		} else {
			for _, msg := range validation.IsDNS1123Label(role.Name) {
				allErrs = append(allErrs, field.Invalid(idxPath.Child("name"), role.Name, msg))
			}
		}

		// Check for duplicate role names
		if roleMap[role.Name] {
			allErrs = append(allErrs, field.Duplicate(idxPath.Child("name"), role.Name))
		} else {
			roleMap[role.Name] = true
		}

		if role.IsCoordinator {
			coordinatorCount++
		}

		// Validate runtime ref
		if len(role.RuntimeRef) == 0 {
			allErrs = append(allErrs, field.Required(idxPath.Child("runtimeRef"), "runtime reference is required"))
		}
	}

	if coordinatorCount != 1 {
		allErrs = append(allErrs, field.Invalid(rolesPath, spec.Roles, fmt.Sprintf("exactly one coordinator must be set, but found %d", coordinatorCount)))
	}

	// Validate dependencies exist
	for i, role := range spec.Roles {
		idxPath := rolesPath.Index(i)
		for j, dep := range role.Dependencies {
			if !roleMap[dep] {
				allErrs = append(allErrs, field.Invalid(idxPath.Child("dependencies").Index(j), dep, "dependency refers to a non-existent role"))
			}
		}
	}

	return allErrs
}
