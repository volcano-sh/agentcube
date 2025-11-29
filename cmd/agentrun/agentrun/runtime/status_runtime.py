"""
Status runtime for AgentRun.

This module implements the status command functionality, handling
the status checking of published agents via AgentCube or Kubernetes.
"""

import logging
from pathlib import Path
from typing import Any, Dict, Optional

from agentrun.services.metadata_service import MetadataService
from agentrun.services.k8s_provider import KubernetesProvider
from agentrun.services.agentcube_provider import AgentCubeProvider # New import


class StatusRuntime:
    """Runtime for the status command."""

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

    def get_status(self, workspace_path: Path, provider: Optional[str] = None) -> Dict[str, Any]:
        """
        Get the status of a published agent.

        Args:
            workspace_path: Path to the agent workspace directory
            provider: Override provider

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

            effective_provider = provider if provider is not None else self.provider
            
            if effective_provider == "agentcube":
                # Get status from K8s cluster (AgentRuntime CR)
                return self._get_crd_k8s_status(metadata)
            else:
                # Get status from K8s cluster (standard Deployment/Service)
                return self._get_k8s_status(metadata)

        except Exception as e:
            logger.error(f"Error getting agent status: {e}")
            return {
                "status": "error",
                "error": str(e)
            }

    def _get_k8s_status(self, metadata) -> Dict[str, Any]: # Renamed
        """Get status from K8s cluster (standard Deployment/Service)."""
        if self.verbose:
            logger.info(f"Querying Kubernetes for agent status (standard Deployment/Service): {metadata.agent_name}")

        if not self.k8s_provider: # Use the standard K8s provider
            raise RuntimeError(
                "Standard K8s provider is not initialized. Ensure Kubernetes is configured."
            )

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
    
    def _get_crd_k8s_status(self, metadata) -> Dict[str, Any]:
        """Get status from K8s cluster (AgentRuntime CR)."""
        if self.verbose:
            logger.info(f"Querying Kubernetes for AgentRuntime CR status: {metadata.agent_name}")

        if not self.agentcube_provider:
            raise RuntimeError(
                "AgentCube provider is not initialized. Ensure Kubernetes is configured."
            )

        try:
            crd_name = metadata.agent_id
            cr_namespace = self.agentcube_provider.namespace # Use the provider's namespace
            
            cr_object = self.agentcube_provider.get_agent_runtime(crd_name, cr_namespace)
            
            agent_status = "unknown"
            agent_endpoint = None
            k8s_deployment_details = {
                "type": "AgentRuntime",
                "namespace": cr_namespace,
                "deployment_name": crd_name
            }

            if cr_object:
                if "status" in cr_object:
                    agent_status = cr_object["status"].get("status", "pending")
                    agent_endpoint = cr_object["status"].get("agentEndpoint")
                    
                    # Merge full status into k8s_deployment_details for display
                    k8s_deployment_details.update(cr_object["status"])
                else:
                    agent_status = "created_no_status"
                
                # Use metadata.agent_endpoint if available from CR status, else fallback to metadata
                if not agent_endpoint and metadata.agent_endpoint:
                    agent_endpoint = metadata.agent_endpoint
            else:
                agent_status = "not_found_in_k8s"
                logger.warning(f"AgentRuntime CR '{crd_name}' not found in K8s, relying on metadata.")
            
            result = {
                "agent_id": metadata.agent_id,
                "agent_name": metadata.agent_name,
                "agent_endpoint": agent_endpoint,
                "status": agent_status,
                "version": metadata.version or "N/A",
                "language": metadata.language,
                "build_mode": metadata.build_mode,
                "k8s_deployment": k8s_deployment_details
            }

            if self.verbose:
                logger.info(f"AgentRuntime CR status retrieved: {result}")

            return result

        except Exception as e:
            logger.error(f"Failed to get AgentRuntime CR status: {e}")
            return {
                "status": "error",
                "error": str(e)
            }
        
logger = logging.getLogger(__name__)