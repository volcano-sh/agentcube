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

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultTrustDomain = "cluster.local"
	trustDomainEnvVar  = "AGENTCUBE_SPIFFE_TRUST_DOMAIN"
	defaultNamespace   = "agentcube"
	namespaceEnvVar    = "AGENTCUBE_NAMESPACE"
)

// SPIFFE IDs for AgentCube components.
// These follow the Istio-convention format: spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>.
// The trust domain defaults to cluster.local and can be overridden with AGENTCUBE_SPIFFE_TRUST_DOMAIN
// to match the SPIRE trust domain configured by deployment tooling.
// The namespace defaults to agentcube and can be overridden with AGENTCUBE_NAMESPACE.
var (
	// RouterSPIFFEID is the SPIFFE identity for the Router component.
	RouterSPIFFEID = componentSPIFFEID(configuredTrustDomain(), configuredNamespace(), "agentcube-router")

	// WorkloadManagerSPIFFEID is the SPIFFE identity for the WorkloadManager component.
	WorkloadManagerSPIFFEID = componentSPIFFEID(configuredTrustDomain(), configuredNamespace(), "workloadmanager")
)

func configuredTrustDomain() string {
	trustDomain := strings.TrimSpace(os.Getenv(trustDomainEnvVar))
	if trustDomain == "" {
		return defaultTrustDomain
	}
	return trustDomain
}

func configuredNamespace() string {
	namespace := strings.TrimSpace(os.Getenv(namespaceEnvVar))
	if namespace == "" {
		return defaultNamespace
	}
	return namespace
}

func componentSPIFFEID(trustDomain, namespace, serviceAccount string) string {
	return fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", trustDomain, namespace, serviceAccount)
}
