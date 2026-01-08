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

import "errors"

var (
	// ErrAgentRuntimeNotFound indicates that the requested AgentRuntime does not exist.
	ErrAgentRuntimeNotFound = errors.New("agent runtime not found")

	// ErrCodeInterpreterNotFound indicates that the requested CodeInterpreter does not exist.
	ErrCodeInterpreterNotFound = errors.New("code interpreter not found")

	// ErrTemplateMissing indicates that the resource exists but has no pod template.
	ErrTemplateMissing = errors.New("resource has no pod template")

	// ErrPublicKeyMissing indicates that the Router public key is not yet available.
	ErrPublicKeyMissing = errors.New("public key not yet cached from Router Secret")
)
