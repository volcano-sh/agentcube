"""
Status runtime for AgentRun.

This module implements the status command functionality, handling
the status checking of published agents via AgentCube or Kubernetes.
"""

import asyncio
import logging
from pathlib import Path
from typing import Any, Dict, Optional

from agentrun.services.agentcube_service import AgentCubeService
from agentrun.services.metadata_service import MetadataService
from agentrun.services.k8s_provider import KubernetesProvider


class StatusRuntime:
    """Runtime for the status command."""

    def __init__(self, verbose: bool = False, use_k8s: bool = False) -> None:
        self.verbose = verbose
        self.use_k8s = use_k8s
        self.metadata_service = MetadataService(verbose=verbose)
        self.agentcube_service = AgentCubeService(verbose=verbose)
        self.k8s_provider = None

        if use_k8s:
            try:
                self.k8s_provider = KubernetesProvider(verbose=verbose)
            except Exception as e:
                logger.warning(f"Failed to initialize K8s provider: {e}")

        if verbose:
            logging.basicConfig(level=logging.DEBUG)

    def get_status(self, workspace_path: Path, use_k8s: Optional[bool] = None) -> Dict[str, Any]:
        """
        Get the status of a published agent.

        Args:
            workspace_path: Path to the agent workspace directory
            use_k8s: Override to check K8s status (defaults to self.use_k8s)

        Returns:
            Dict containing agent status information

        Raises:
            ValueError: If status check fails
        """
        if self.verbose:
            logger.info(f"Checking agent status for workspace: {workspace_path}")

        try:
            metadata = self.metadata_service.load_metadata(workspace_path)

            if not metadata.agent_id:
                return {
                    "status": "not_published",
                    "message": "Agent has not been published yet"
                }

            # Determine whether to check K8s or AgentCube
            check_k8s = use_k8s if use_k8s is not None else (
                self.use_k8s or metadata.k8s_deployment is not None
            )

            if check_k8s:
                # Get status from K8s cluster
                return self._get_k8s_status(metadata)
            else:
                # Get status from AgentCube
                return self._get_agentcube_status(metadata)

        except Exception as e:
            logger.error(f"Error getting agent status: {e}")
            return {
                "status": "error",
                "error": str(e)
            }

    def _get_agentcube_status(self, metadata) -> Dict[str, Any]:
        """Get status from AgentCube API."""
        if self.verbose:
            logger.info(f"Querying AgentCube for agent status: {metadata.agent_id}")

        try:
            # Try to get status from AgentCube API
            status_info = asyncio.run(
                self.agentcube_service.get_agent_status(metadata.agent_id)
            )

            # Merge with metadata information
            result = {
                "agent_id": metadata.agent_id,
                "agent_name": metadata.agent_name,
                "agent_endpoint": metadata.agent_endpoint,
                "status": status_info.get("status", "active"),
                "version": metadata.version or "latest",
                "language": metadata.language,
                "build_mode": metadata.build_mode,
                "last_activity": status_info.get("last_activity"),
            }

            if self.verbose:
                logger.info(f"AgentCube status retrieved: {result}")

            return result

        except Exception as e:
            # Fallback to metadata-only status if API call fails
            logger.warning(f"Failed to get status from AgentCube API, using metadata: {e}")
            return {
                "agent_id": metadata.agent_id,
                "agent_name": metadata.agent_name,
                "agent_endpoint": metadata.agent_endpoint,
                "status": "published",
                "version": metadata.version or "latest",
                "language": metadata.language,
                "build_mode": metadata.build_mode,
                "note": "Status from metadata (AgentCube API unavailable)"
            }

    def _get_k8s_status(self, metadata) -> Dict[str, Any]:
        """Get status from K8s cluster."""
        if self.verbose:
            logger.info(f"Querying Kubernetes for agent status: {metadata.agent_name}")

        if not self.k8s_provider:
            try:
                self.k8s_provider = KubernetesProvider(verbose=self.verbose)
            except Exception as e:
                return {
                    "status": "error",
                    "error": f"Failed to initialize K8s provider: {str(e)}"
                }

        try:
            k8s_status = self.k8s_provider.get_agent_status(metadata.agent_name)

            # Merge with metadata information
            result = {
                "agent_id": metadata.agent_id,
                "agent_name": metadata.agent_name,
                "agent_endpoint": metadata.agent_endpoint,
                "status": k8s_status.get("status"),
                "version": metadata.version or "N/A",
                "language": metadata.language,
                "build_mode": metadata.build_mode,
                "k8s_deployment": k8s_status
            }

            if self.verbose:
                logger.info(f"Kubernetes status retrieved: {result}")

            return result

        except Exception as e:
            logger.error(f"Failed to get K8s status: {e}")
            return {
                "status": "error",
                "error": str(e)
            }


logger = logging.getLogger(__name__)