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

package mtls

// SPIFFE ID constants for AgentCube components.
// These follow the Istio-convention format: spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>
//
// NOTE(reviewer): These are currently hardcoded to match the identity assignments in
// docs/design/auth-proposal.md Section 1.3. If multi-cluster or configurable trust
// domains are needed in the future, consider exposing these via CLI flags.
const (
	// RouterSPIFFEID is the SPIFFE identity for the Router component.
	RouterSPIFFEID = "spiffe://cluster.local/ns/agentcube-system/sa/agentcube-router"

	// WorkloadManagerSPIFFEID is the SPIFFE identity for the WorkloadManager component.
	WorkloadManagerSPIFFEID = "spiffe://cluster.local/ns/agentcube-system/sa/workloadmanager"

	// SandboxSPIFFEID is the SPIFFE identity for PicoD sandbox pods.
	//
	// This intentionally omits the /ns/<namespace> segment because sandboxes can be created
	// in any user-requested namespace, not just the AgentCube system namespace. The
	// corresponding ClusterSPIFFEID registration in manifests/charts/base/templates/spire/
	// cluster-spiffe-ids.yaml uses the same namespace-agnostic template:
	//   spiffeIDTemplate: "spiffe://<trust-domain>/sa/{{ .PodSpec.ServiceAccountName }}"
	// If the ServiceAccount name changes, both this constant and the SPIRE registration
	// must be updated together.
	SandboxSPIFFEID = "spiffe://cluster.local/sa/agentcube-sandbox"
)
