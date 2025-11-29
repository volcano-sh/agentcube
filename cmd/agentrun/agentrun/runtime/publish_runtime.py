"""
Publish runtime for AgentRun.

This module implements the publish command functionality, handling
the publishing of agent images to AgentCube.
"""

import logging
import time # New import
from pathlib import Path
from typing import Any, Dict

from agentrun.services.docker_service import DockerService
from agentrun.services.metadata_service import MetadataService
from agentrun.services.k8s_provider import KubernetesProvider
from agentrun.services.agentcube_provider import AgentCubeProvider # Use AgentCubeProvider for CRD


class PublishRuntime:
    """Runtime for the publish command."""

    def __init__(self, verbose: bool = False, provider: str = "agentcube") -> None:
        self.verbose = verbose
        self.provider = provider
        self.metadata_service = MetadataService(verbose=verbose)
        self.docker_service = DockerService(verbose=verbose)
        
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

    def publish(
        self,
        workspace_path: Path,
        **options: Any
    ) -> Dict[str, Any]:
        """
        Publish the agent to AgentCube or K8s cluster.

        Args:
            workspace_path: Path to the agent workspace directory
            **options: Additional publish options

        Returns:
            Dict containing publish results

        Raises:
            ValueError: If publish fails
        """
        if self.verbose:
            logger.info(f"Starting publish process for workspace: {workspace_path}")
            
        provider = options.get('provider', self.provider)

        if provider == "agentcube":
            return self._publish_crd_to_k8s(workspace_path, **options)
        elif provider == "k8s":
            return self._publish_k8s(workspace_path, **options)
        else:
            raise ValueError(f"Unsupported provider: {provider}. Supported providers are 'agentcube' and 'k8s'.")

    def _publish_crd_to_k8s( # Renamed from _publish_to_k8s
        self,
        workspace_path: Path,
        **options: Any
    ) -> Dict[str, Any]:
        """
        Publish the agent to Kubernetes cluster using AgentRuntime CR.

        Args:
            workspace_path: Path to the agent workspace directory
            **options: Additional publish options

        Returns:
            Dict containing publish results

        Raises:
            ValueError: If publish fails
        """
        if self.verbose:
            logger.info(f"Publishing to K8s cluster (AgentRuntime CR) for workspace: {workspace_path}")

        if not self.agentcube_provider: # Use the agentcube_provider
            raise RuntimeError(
                "AgentCube provider is not initialized. Ensure Kubernetes is configured."
            )

        # Step 1: Load metadata
        metadata = self.metadata_service.load_metadata(workspace_path)

        # Step 2: Get image information
        if not metadata.image:
            raise ValueError("Agent must be built before publishing. Run 'agentrun build' first.")

        image_url = metadata.image.get("repository_url")
        if not image_url:
            raise ValueError("No image found in metadata. Build the agent first.")

        # Step 3: Deploy AgentRuntime CR
        try:
            k8s_info = self.agentcube_provider.deploy_agent_runtime( # Call the new provider
                agent_name=metadata.agent_name,
                image_url=image_url,
                port=metadata.port,
                entrypoint=metadata.entrypoint,
                env_vars=options.get('env_vars', None)
            )

            # Step 4: Update metadata with initial K8s deployment information (before endpoint is available)
            updates = {
                "agent_id": k8s_info["deployment_name"],
                "k8s_deployment": {
                    **k8s_info,
                    "type": "AgentRuntime",
                    "status": "pending_endpoint" # Initial status
                }
            }
            self.metadata_service.update_metadata(workspace_path, updates)
            
            # --- POLLING FOR ENDPOINT ---
            agent_endpoint = None
            status = "pending_endpoint"
            timeout_seconds = 300 # 5 minutes timeout
            poll_interval = 5 # Poll every 5 seconds
            start_time = time.time()
            
            if self.verbose:
                logger.info(f"Polling AgentRuntime CR '{k8s_info['deployment_name']}' for endpoint. Timeout: {timeout_seconds}s")

            while time.time() - start_time < timeout_seconds:
                try:
                    cr = self.agentcube_provider.get_agent_runtime(
                        name=k8s_info["deployment_name"],
                        namespace=k8s_info["namespace"]
                    )
                    if cr and "status" in cr and "agentEndpoint" in cr["status"]:
                        agent_endpoint = cr["status"]["agentEndpoint"]
                        status = cr["status"].get("status", "deployed") # Get actual status from CR if available
                        if self.verbose:
                            logger.info(f"AgentRuntime CR '{k8s_info['deployment_name']}' endpoint found: {agent_endpoint}")
                        break
                except Exception as e:
                    logger.debug(f"Error while polling for AgentRuntime status: {e}")
                
                time.sleep(poll_interval)
            
            if not agent_endpoint:
                status = "endpoint_timeout"
                logger.warning(f"Timeout waiting for agentEndpoint from AgentRuntime CR '{k8s_info['deployment_name']}'. Please check CR status manually.")
                
            # Update metadata with final endpoint and status
            updates = {
                "agent_id": k8s_info["deployment_name"],
                "agent_endpoint": agent_endpoint, # This will be None if timeout
                "k8s_deployment": {
                    **k8s_info,
                    "type": "AgentRuntime",
                    "status": status,
                    "last_checked_at": time.time() # Optional: timestamp
                }
            }
            self.metadata_service.update_metadata(workspace_path, updates)

            result = {
                "agent_name": metadata.agent_name,
                "agent_id": k8s_info["deployment_name"],
                "deployment_name": k8s_info["deployment_name"],
                "namespace": k8s_info["namespace"],
                "status": status
            }
            if agent_endpoint:
                result["agent_endpoint"] = agent_endpoint
            
            if self.verbose:
                logger.info(f"K8s publish (AgentRuntime CR) completed: {result}")

            return result

        except Exception as e:
            raise RuntimeError(f"Failed to deploy AgentRuntime CR to K8s: {str(e)}")

    def _publish_k8s(
        self,
        workspace_path: Path,
        **options: Any
    ) -> Dict[str, Any]:
        """
        Publish the agent to local Kubernetes cluster using standard Deployment/Service.

        Args:
            workspace_path: Path to the agent workspace directory
            **options: Additional publish options

        Returns:
            Dict containing publish results

        Raises:
            ValueError: If publish fails
        """
        if self.verbose:
            logger.info(f"Publishing to K8s cluster (standard Deployment/Service) for workspace: {workspace_path}")

        if not self.k8s_provider:
            raise RuntimeError(
                "Standard K8s provider is not initialized. Ensure Kubernetes is configured."
            )

        # Step 1: Load metadata
        metadata = self._validate_publish_prerequisites(workspace_path)

        # Step 2: Get image information
        image_url = metadata.image.get("repository_url")
        if not image_url:
            raise ValueError("No image found in metadata. Build the agent first.")

        # Step 3: Deploy to K8s
        k8s_info = None
        try:
            k8s_info = self.k8s_provider.deploy_agent(
                agent_name=metadata.agent_name,
                image_url=image_url,
                port=metadata.port,
                entrypoint=metadata.entrypoint,
                replicas=options.get('replicas', 1),
                node_port=options.get('node_port', None),
                env_vars=options.get('env_vars', None)
            )

            # Step 4: Update metadata with K8s deployment information (after creation, before readiness check)
            # This ensures agent_id and agent_endpoint are saved even if readiness fails
            updates = {
                "agent_id": k8s_info["deployment_name"],
                "agent_endpoint": k8s_info["service_url"],
                "k8s_deployment": {
                    **k8s_info,
                    "status": "creating"
                }
            }
            self.metadata_service.update_metadata(workspace_path, updates)
            
            # --- Start readiness check ---
            if self.verbose:
                logger.info(f"Waiting for deployment '{k8s_info['deployment_name']}' to become ready...")
            
            final_status = "failed"
            try:
                self.k8s_provider._wait_for_deployment_ready(k8s_info['deployment_name'], timeout=120)
                final_status = "deployed"
            except Exception as e:
                error_message = str(e)
                logger.error(f"Deployment '{k8s_info['deployment_name']}' failed readiness check: {error_message}")
                updates = {
                    "k8s_deployment": {
                        **k8s_info,
                        "status": final_status,
                        "error": error_message
                    }
                }
                self.metadata_service.update_metadata(workspace_path, updates)
                raise RuntimeError(f"Failed to deploy to standard K8s: {error_message}")
            
            # If readiness succeeds, update metadata with success status
            updates = {
                "k8s_deployment": {
                    **k8s_info,
                    "status": final_status
                }
            }
            self.metadata_service.update_metadata(workspace_path, updates)

            result = {
                **k8s_info,
                "status": final_status
            }

            if self.verbose:
                logger.info(f"Standard K8s publish completed: {result}")

            return result

        except Exception as e:
            error_message = str(e)
            logger.error(f"Failed to create standard K8s resources for {metadata.agent_name}: {error_message}")
            if k8s_info:
                updates = {
                    "agent_id": k8s_info.get("deployment_name", metadata.agent_name),
                    "agent_endpoint": k8s_info.get("service_url", ""),
                    "k8s_deployment": {
                        **k8s_info,
                        "status": "failed",
                        "error": error_message
                    }
                }
                self.metadata_service.update_metadata(workspace_path, updates)
            raise RuntimeError(f"Failed to deploy to standard K8s: {error_message}")

    def _validate_publish_prerequisites(self, workspace_path: Path):
        """Validate that the workspace is ready for publishing."""
        # Load metadata
        metadata = self.metadata_service.load_metadata(workspace_path)

        # Check if agent has been built
        if not metadata.image:
            raise ValueError("Agent must be built before publishing. Run 'agentrun build' first.")

        if self.verbose:
            logger.debug(f"Publish prerequisites validated for: {workspace_path}")

        return metadata

    def _prepare_image_for_publishing(
        self,
        workspace_path: Path,
        metadata,
        options: Dict[str, Any]
    ) -> str:
        """Prepare the container image for publishing."""
        build_mode = options.get('build_mode', metadata.build_mode)

        if build_mode == 'local':
            return self._prepare_local_image(workspace_path, metadata, options)
        elif build_mode == 'cloud':
            return self._prepare_cloud_image(workspace_path, metadata, options)
        else:
            raise ValueError(f"Unsupported build mode for publishing: {build_mode}")

    def _prepare_local_image(
        self,
        workspace_path: Path,
        metadata,
        options: Dict[str, Any]
    ) -> str:
        """Prepare locally built image for publishing."""
        if self.verbose:
            logger.info("Preparing local image for publishing")

        # Get image information from metadata
        image_info = metadata.image
        local_image_name = image_info.get("repository_url")

        if not local_image_name:
            raise ValueError("No local image found. Build the agent first.")

        # Extract required image repository information
        image_url = options.get('image_url')
        username = options.get('image_username')
        password = options.get('image_password')

        if not image_url:
            raise ValueError(
                "Image repository URL is required for local build mode. "
                "Use --image-url option."
            )

        if not username or not password:
            raise ValueError(
                "Image repository credentials are required. "
                "Use --image-username and --image-password options."
            )

        # Tag and push the image
        try:
            if self.verbose:
                logger.info(f"Pushing image to repository: {image_url}")

            push_result = self.docker_service.push_image(
                image_name=local_image_name,
                registry_url=image_url,
                username=username,
                password=password
            )

            final_image_url = push_result["pushed_image"]

            if self.verbose:
                logger.info(f"Image pushed successfully: {final_image_url}")

            return final_image_url

        except Exception as e:
            raise RuntimeError(f"Failed to push image: {str(e)}")

    def _prepare_cloud_image(
        self,
        workspace_path: Path,
        metadata,
        options: Dict[str, Any]
    ) -> str:
        """Prepare cloud-built image for publishing."""
        if self.verbose:
            logger.info("Using cloud-built image")

        # For cloud build mode, the image should already be in a registry
        image_info = metadata.image
        cloud_image_url = image_info.get("repository_url")

        if not cloud_image_url:
            raise ValueError("No cloud image URL found in metadata")

        return cloud_image_url

    def _update_publish_metadata(self, workspace_path: Path, agent_info: Dict[str, Any]) -> None:
        """Update metadata with publish information."""
        updates = {
            "agent_id": agent_info["agent_id"],
            "agent_endpoint": agent_info["agent_endpoint"],
            "version": agent_info.get("version", "latest")
        }

        self.metadata_service.update_metadata(workspace_path, updates)

        if self.verbose:
            logger.debug(f"Updated metadata with publish info: {updates}")

logger = logging.getLogger(__name__)