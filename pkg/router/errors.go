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

package router

import (
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

const (
	resourceGroup               = "agentcube.volcano.sh"
	sessionResourceName         = "sessions"
	agentRuntimeResourceName    = "agentruntimes"
	codeInterpreterResourceName = "codeinterpreters"
)

var (
	sessionResource         = schema.GroupResource{Group: resourceGroup, Resource: sessionResourceName}
	agentRuntimeResource    = schema.GroupResource{Group: resourceGroup, Resource: agentRuntimeResourceName}
	codeInterpreterResource = schema.GroupResource{Group: resourceGroup, Resource: codeInterpreterResourceName}

	// ErrSessionNotFound indicates that the session does not exist in store.
	ErrSessionNotFound = apierrors.NewNotFound(sessionResource, "")

	// ErrUpstreamUnavailable indicates that the workload manager is unavailable.
	ErrUpstreamUnavailable = apierrors.NewServiceUnavailable("sessionmgr: workload manager unavailable")

	// ErrCreateSandboxFailed indicates that the workload manager returned an error.
	ErrCreateSandboxFailed = apierrors.NewInternalError(fmt.Errorf("sessionmgr: create sandbox failed"))

	// ErrAgentRuntimeNotFound indicates that the AgentRuntime does not exist.
	ErrAgentRuntimeNotFound = apierrors.NewNotFound(agentRuntimeResource, "")
)

func sessionNotFoundError(sessionID string) error {
	return errors.Join(ErrSessionNotFound, apierrors.NewNotFound(sessionResource, sessionID))
}

func workloadResource(kind string) schema.GroupResource {
	switch kind {
	case types.CodeInterpreterKind:
		return codeInterpreterResource
	default:
		return agentRuntimeResource
	}
}

func sandboxNotFoundError(namespace, name, kind string) error {
	gr := workloadResource(kind)
	return errors.Join(ErrAgentRuntimeNotFound, apierrors.NewNotFound(gr, fmt.Sprintf("%s/%s", namespace, name)))
}

func upstreamUnavailableError(err error) error {
	return errors.Join(ErrUpstreamUnavailable, apierrors.NewServiceUnavailable(err.Error()))
}

func createSandboxFailedStatusError(statusCode int, respBody []byte) error {
	return errors.Join(ErrCreateSandboxFailed, apierrors.NewInternalError(fmt.Errorf("status code %d, body: %s", statusCode, string(respBody))))
}

func createSandboxFailedError(err error) error {
	return errors.Join(ErrCreateSandboxFailed, apierrors.NewInternalError(err))
}
