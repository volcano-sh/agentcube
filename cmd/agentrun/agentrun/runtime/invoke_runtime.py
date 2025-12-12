"""
Invoke runtime for AgentRun.

This module implements the invoke command functionality, handling
the invocation of published agents via AgentCube.
"""

import asyncio
import logging
from pathlib import Path
from typing import Tuple, Any, Dict, Optional

from agentrun.services.metadata_service import MetadataService
from agentrun.services.k8s_provider import KubernetesProvider
from agentrun.services.agentcube_provider import AgentCubeProvider

logger = logging.getLogger(__name__)


class InvokeRuntime:
    """Runtime for the invoke command."""

    def __init__(self, verbose: bool = False, provider: str = "agentcube") -> None:
        self.verbose = verbose
        self.provider = provider
        self.metadata_service = MetadataService(verbose=verbose)
        
        # Providers for K8s deployments
        self.agentcube_provider = None         # For agentcube provider (CRD)
        self.k8s_provider = None    # For k8s provider (Deployment/Service)

        if provider == "agentcube":
            try:
                self.agentcube_provider = AgentCubeProvider(verbose=verbose)
            except Exception as e:
                logger.warning(f"Failed to initialize AgentCube provider for CRD: {e}")
        elif provider == "k8s":
            try:
                self.k8s_provider = KubernetesProvider(verbose=verbose)
            except Exception as e:
                logger.warning(f"Failed to initialize standard K8s provider: {e}")

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
        metadata, agent_id, base_endpoint = self._validate_invoke_prerequisites(workspace_path)

        # Determine final endpoint based on deployment type
        final_endpoint = base_endpoint
        if metadata.k8s_deployment and metadata.k8s_deployment.get("type") == "AgentRuntime":
            namespace = metadata.k8s_deployment.get("namespace", "default")
            agent_name = metadata.agent_name
            # Ensure base_endpoint doesn't have trailing slash
            base = base_endpoint.rstrip("/")
            final_endpoint = f"{base}/v1/namespaces/{namespace}/agent-runtimes/{agent_name}/invocations/"
            
            if self.verbose:
                logger.info(f"Constructed AgentRuntime invocation URL: {final_endpoint}")

        # Add session ID to headers if it exists
        if metadata.session_id:
            if headers is None:
                headers = {}
            headers["X-Agentcube-Session-Id"] = metadata.session_id

        # Step 2: Invoke the agent
        response = asyncio.run(self._invoke_agent_via_agentcube(
            agent_id=agent_id,
            payload=payload,
            headers=headers,
            endpoint=final_endpoint,
            workspace_path=workspace_path
        ))

        if self.verbose:
            logger.info(f"Agent invoked successfully: {agent_id}")

        return response

    def _validate_invoke_prerequisites(self, workspace_path: Path) -> Tuple[Any, str, str]:
        """Validate that the workspace is ready for invocation."""
        # Load metadata
        metadata = self.metadata_service.load_metadata(workspace_path)

        # Check if agent is published
        agent_id = metadata.agent_id
        if not agent_id:
            raise ValueError(
                "Agent is not published yet. Run 'agentrun publish' first."
            )

        endpoint = metadata.agent_endpoint
        
        if not endpoint:
            raise ValueError(
                "Agent endpoint is not available in metadata. "
                "Please publish with --endpoint or ensure router_url is set."
            )

        if self.verbose:
            logger.debug(f"Invocation prerequisites validated: agent_id={agent_id}, endpoint={endpoint}")

        return metadata, agent_id, endpoint

    async def _invoke_agent_via_agentcube(
        self,
        agent_id: str,
        payload: Dict[str, Any],
        headers: Optional[Dict[str, str]],
        endpoint: str,
        workspace_path: Path
    ) -> Any:
        """Invoke the agent via AgentCube API."""
        if self.verbose:
            logger.info(f"Invoking agent {agent_id} at {endpoint}")

        try:
            # Try direct HTTP invocation first (for local testing)
            if endpoint.startswith("http"):
                response = await self._direct_http_invocation(endpoint, payload, headers, workspace_path)

            return response

        except Exception as e:
            raise RuntimeError(f"Failed to invoke agent {agent_id}: {str(e)}")

    async def _direct_http_invocation(
        self,
        endpoint: str,
        payload: Dict[str, Any],
        headers: Optional[Dict[str, str]],
        workspace_path: Path
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

                # Save session ID from response header
                if "X-Agentcube-Session-Id" in response.headers:
                    session_id = response.headers["X-Agentcube-Session-Id"]
                    self.metadata_service.update_metadata(workspace_path, {"session_id": session_id})

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

        except httpx.ConnectError as e:
            if self.verbose:
                logger.error(f"Could not connect to {endpoint}. Please check if the agent is running and the endpoint is correct.")
            raise RuntimeError(f"Could not connect to agent at {endpoint}: {e}")

        except Exception as e:
            raise RuntimeError(f"HTTP invocation failed: {str(e)}")