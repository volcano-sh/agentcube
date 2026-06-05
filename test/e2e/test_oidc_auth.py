#!/usr/bin/env python3
# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""E2E tests for OIDC authentication using the Python SDK."""

import os
import sys
import unittest


def skip_if_no_oidc():
    """Skip test if OIDC is not enabled."""
    if os.getenv("OIDC_ENABLED") != "true":
        raise unittest.SkipTest("OIDC_ENABLED not set (Keycloak not deployed)")


class TestOIDCSDKAuth(unittest.TestCase):
    """Tests that the Python SDK works with ServiceAccountAuth."""

    def test_service_account_auth_agent_runtime(self):
        """Full flow: ServiceAccountAuth -> AgentRuntime session -> execute code."""
        skip_if_no_oidc()

        from agentcube import AgentRuntimeClient, ServiceAccountAuth

        keycloak_url = os.getenv(
            "KEYCLOAK_TOKEN_URL",
            "http://localhost:8082/realms/agentcube/protocol/openid-connect/token",
        )

        system_namespace = os.getenv("AGENTCUBE_SYSTEM_NAMESPACE", "agentcube-system")
        auth = ServiceAccountAuth(
            token_url=keycloak_url,
            client_id="agentcube-service",
            client_secret="e2e-service-secret",
            headers={"Host": f"keycloak.{system_namespace}.svc.cluster.local:8080"}
        )

        namespace = os.getenv("AGENTCUBE_NAMESPACE", "agentcube")
        router_url = os.getenv("ROUTER_URL", "http://localhost:8081")

        with AgentRuntimeClient(
            agent_name="echo-agent",
            namespace=namespace,
            router_url=router_url,
            auth=auth,
        ) as client:
            result = client.invoke({"input": "Hello OIDC"}, path="echo")
            output = result.get("output", "") if isinstance(result, dict) else str(result)
            self.assertIn("Hello OIDC", output)


if __name__ == "__main__":
    result = unittest.main(verbosity=2, exit=False)
    sys.exit(0 if result.result.wasSuccessful() else 1)
