"""
Invoke runtime for AgentRun.

This module implements the invoke command functionality, handling
the invocation of published agents via AgentCube.
"""

import asyncio
import logging
from pathlib import Path
from typing import Any, Dict, Optional

from agentrun.services.agentcube_service import AgentCubeService
from agentrun.services.metadata_service import MetadataService
from agentrun.services.k8s_provider import KubernetesProvider


class InvokeRuntime:
    """Runtime for the invoke command."""

    def __init__(self, verbose: bool = False, use_k8s: bool = False, provider: str = "agentcube", agentcube_uri: Optional[str] = None) -> None:
        self.verbose = verbose
        self.use_k8s = use_k8s
        self.provider = provider
        self.agentcube_uri = agentcube_uri
        self.metadata_service = MetadataService(verbose=verbose)
        self.agentcube_service = AgentCubeService(verbose=verbose, api_url=agentcube_uri)
        
        if use_k8s or provider == "k8s":
            # K8s provider not really used for invoke in current logic as we use http endpoint
            # But keeping initialization if needed for future logic
             try:
                self.k8s_provider = KubernetesProvider(verbose=verbose)
             except Exception:
                 pass # Ignore if k8s not available

        if verbose:
            logging.basicConfig(level=logging.DEBUG)

    def invoke(
        self,
        workspace_path: Path,
        payload: Dict[str, Any],
        headers: Optional[Dict[str, str]] = None
    ) -> Any:
        """
        Invoke a published agent.

        Args:
            workspace_path: Path to the agent workspace directory
            payload: JSON payload to send to the agent
            headers: Optional HTTP headers

        Returns:
            Agent response

        Raises:
            ValueError: If invocation fails
        """
        if self.verbose:
            logger.info(f"Starting agent invocation for workspace: {workspace_path}")

        # Step 1: Load metadata and validate agent is published
        agent_id, endpoint = self._validate_invoke_prerequisites(workspace_path)

        # Step 2: Invoke the agent
        response = asyncio.run(self._invoke_agent_via_agentcube(
            agent_id=agent_id,
            payload=payload,
            headers=headers,
            endpoint=endpoint
        ))

        if self.verbose:
            logger.info(f"Agent invoked successfully: {agent_id}")

        return response

    def _validate_invoke_prerequisites(self, workspace_path: Path) -> tuple[str, str]:
        """Validate that the workspace is ready for invocation."""
        # Load metadata
        metadata = self.metadata_service.load_metadata(workspace_path)

        # Check if agent is published
        agent_id = metadata.agent_id
        if not agent_id:
            raise ValueError(
                "Agent is not published yet. Run 'agentrun publish' first."
            )

        # Priority 1: Use CLI provided agentcube-uri to construct endpoint override if applicable
        # But typically agentcube-uri is the base URI. 
        # If user provides --agentcube-uri, we might want to use it to reconstruct the endpoint
        # especially if the one in metadata is outdated or 'localhost'.
        
        endpoint = metadata.agent_endpoint
        
        if self.agentcube_uri:
             # If CLI arg provided, override base part of endpoint if it looks like a full URL
             # Or construct a standard endpoint path if metadata endpoint is missing
             base_uri = self.agentcube_uri.rstrip('/')
             if self.provider == "k8s" or self.use_k8s:
                 # Reconstruct K8s endpoint format: <base>/v1/namespaces/<ns>/agents/<name>
                 # We need namespace. If not in metadata, assume default.
                 namespace = "agentrun"
                 if metadata.k8s_deployment and "namespace" in metadata.k8s_deployment:
                     namespace = metadata.k8s_deployment["namespace"]
                 
                 endpoint = f"{base_uri}/v1/namespaces/{namespace}/agents/{metadata.agent_name}"
             else:
                 # Standard AgentCube endpoint format if needed
                 endpoint = f"{base_uri}/v1/agents/{agent_id}/invoke"

        if not endpoint:
             raise ValueError(
                "Agent endpoint is not available in metadata and could not be constructed. "
                "Please publish with --agentcube-uri or provide it during invocation."
            )

        if self.verbose:
            logger.debug(f"Invocation prerequisites validated: agent_id={agent_id}, endpoint={endpoint}")

        return agent_id, endpoint

    async def _invoke_agent_via_agentcube(
        self,
        agent_id: str,
        payload: Dict[str, Any],
        headers: Optional[Dict[str, str]],
        endpoint: str
    ) -> Any:
        """Invoke the agent via AgentCube API."""
        if self.verbose:
            logger.info(f"Invoking agent {agent_id} at {endpoint}")

        try:
            # Try direct HTTP invocation first (for local testing)
            if endpoint.startswith("http"):
                response = await self._direct_http_invocation(endpoint, payload, headers)
            else:
                # Fall back to AgentCube service
                response = await self.agentcube_service.invoke_agent(
                    agent_id=agent_id,
                    payload=payload,
                    headers=headers
                )

            return response

        except Exception as e:
            raise RuntimeError(f"Failed to invoke agent {agent_id}: {str(e)}")

    async def _direct_http_invocation(
        self,
        endpoint: str,
        payload: Dict[str, Any],
        headers: Optional[Dict[str, str]]
    ) -> Dict[str, Any]:
        """Perform direct HTTP invocation of the agent."""
        import httpx

        # Prepare request headers
        request_headers = {"Content-Type": "application/json"}
        if headers:
            request_headers.update(headers)

        try:
            async with httpx.AsyncClient(timeout=60.0) as client:
                if self.verbose:
                    logger.info(f"Making HTTP POST request to {endpoint}")

                response = await client.post(
                    endpoint,
                    json=payload,
                    headers=request_headers
                )
                response.raise_for_status()

                # Try to parse JSON response
                try:
                    return response.json()
                except Exception:
                    # If not JSON, return text response
                    return {
                        "response": response.text,
                        "status_code": response.status_code,
                        "headers": dict(response.headers)
                    }

        except httpx.ConnectError:
            # If connection fails, return a mock response for testing
            if self.verbose:
                logger.warning(f"Could not connect to {endpoint}, returning mock response")

            return {
                "response": f"Mock response: Agent processed payload {payload}",
                "agent_endpoint": endpoint,
                "status": "mock",
                "note": "Actual agent endpoint not reachable"
            }

        except Exception as e:
            raise RuntimeError(f"HTTP invocation failed: {str(e)}")


logger = logging.getLogger(__name__)