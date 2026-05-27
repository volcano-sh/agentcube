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
	"strings"
	"testing"
)

func TestValidateMultiAgentRuntimeSpec(t *testing.T) {
	testCases := []struct {
		name          string
		spec          MultiAgentRuntimeSpec
		expectedError string
	}{
		{
			name: "valid configuration",
			spec: MultiAgentRuntimeSpec{
				Roles: []RoleSpec{
					{
						Name:          "planner",
						RuntimeRef:    "planner-runtime",
						IsCoordinator: true,
					},
					{
						Name:         "worker",
						RuntimeRef:   "worker-runtime",
						Dependencies: []string{"planner"},
					},
				},
			},
			expectedError: "",
		},
		{
			name: "missing roles",
			spec: MultiAgentRuntimeSpec{
				Roles: []RoleSpec{},
			},
			expectedError: "must provide at least one role",
		},
		{
			name: "missing coordinator",
			spec: MultiAgentRuntimeSpec{
				Roles: []RoleSpec{
					{
						Name:       "worker1",
						RuntimeRef: "runtime1",
					},
				},
			},
			expectedError: "exactly one coordinator must be set, but found 0",
		},
		{
			name: "multiple coordinators",
			spec: MultiAgentRuntimeSpec{
				Roles: []RoleSpec{
					{
						Name:          "planner1",
						RuntimeRef:    "runtime1",
						IsCoordinator: true,
					},
					{
						Name:          "planner2",
						RuntimeRef:    "runtime2",
						IsCoordinator: true,
					},
				},
			},
			expectedError: "exactly one coordinator must be set, but found 2",
		},
		{
			name: "duplicate role names",
			spec: MultiAgentRuntimeSpec{
				Roles: []RoleSpec{
					{
						Name:          "planner",
						RuntimeRef:    "runtime1",
						IsCoordinator: true,
					},
					{
						Name:       "planner",
						RuntimeRef: "runtime2",
					},
				},
			},
			expectedError: "Duplicate value",
		},
		{
			name: "invalid role name",
			spec: MultiAgentRuntimeSpec{
				Roles: []RoleSpec{
					{
						Name:          "invalid_name!",
						RuntimeRef:    "runtime1",
						IsCoordinator: true,
					},
				},
			},
			expectedError: "a lowercase RFC 1123 label must consist of lower case alphanumeric characters",
		},
		{
			name: "non-existent dependency",
			spec: MultiAgentRuntimeSpec{
				Roles: []RoleSpec{
					{
						Name:          "planner",
						RuntimeRef:    "runtime1",
						IsCoordinator: true,
					},
					{
						Name:         "worker",
						RuntimeRef:   "runtime2",
						Dependencies: []string{"missing-role"},
					},
				},
			},
			expectedError: "dependency refers to a non-existent role",
		},
		{
			name: "missing runtime ref",
			spec: MultiAgentRuntimeSpec{
				Roles: []RoleSpec{
					{
						Name:          "planner",
						IsCoordinator: true,
					},
				},
			},
			expectedError: "runtime reference is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateMultiAgentRuntime(&MultiAgentRuntime{Spec: tc.spec})
			if tc.expectedError == "" {
				if len(errs) != 0 {
					t.Errorf("Expected valid spec, got errors: %v", errs)
				}
			} else {
				if len(errs) == 0 {
					t.Errorf("Expected error containing %q, but got valid", tc.expectedError)
					return
				}

				found := false
				for _, err := range errs {
					if err.Error() != "" {
						if strings.Contains(err.Error(), tc.expectedError) {
							found = true
							break
						}
					}
				}
				if !found {
					t.Errorf("Expected error containing %q, got: %v", tc.expectedError, errs)
				}
			}
		})
	}
}
