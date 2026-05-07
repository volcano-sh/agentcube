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

package e2b

import (
	"fmt"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

// ResolveTemplate resolves templateID and metadata to namespace, name, and kind.
// In the E2B compatibility layer, namespace is resolved from the API Key mapping
// before calling this function; templateID is guaranteed to be a plain name without
// namespace prefix.
func ResolveTemplate(templateID string, metadata map[string]interface{}) (namespace, name, kind string, err error) {
	if templateID == "" {
		return "", "", "", fmt.Errorf("template id is required")
	}
	name = templateID

	// Default kind is CodeInterpreter
	kind = types.CodeInterpreterKind

	if metadata != nil {
		if v, ok := metadata["agentcube.kind"]; ok {
			if str, ok := v.(string); ok && str != "" {
				switch str {
				case types.CodeInterpreterKind:
					kind = types.CodeInterpreterKind
				case types.AgentRuntimeKind:
					kind = types.AgentRuntimeKind
				default:
					return "", "", "", fmt.Errorf("INVALID_KIND: supported values are %q and %q",
						types.CodeInterpreterKind, types.AgentRuntimeKind)
				}
			}
		}
	}

	return namespace, name, kind, nil
}
