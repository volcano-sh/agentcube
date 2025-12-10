"""
Publish runtime for AgentRun.

This module implements the publish command functionality, handling
the publishing of agent images to AgentCube.
"""

import logging
from pathlib import Path
from typing import Any, Dict

from agentrun.services.docker_service import DockerService
from agentrun.services.metadata_service import MetadataService
from agentrun.services.k8s_provider import KubernetesProvider
from agentrun.services.agentcube_provider import AgentCubeProvider

logger = logging.getLogger(__name__)


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

        if self.verbose:
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
        namespace = str(options.get('namespace', 'default'))

        # Load metadata early to prepare image
        metadata = self.metadata_service.load_metadata(workspace_path)

        # Delete session_id from metadata if it exists
        if metadata.session_id:
            if self.verbose:
                logger.info(f"Removing session_id: {metadata.session_id} from metadata")
            metadata.session_id = None
            self.metadata_service.save_metadata(workspace_path, metadata)
            metadata = self.metadata_service.load_metadata(workspace_path)

        if provider == "agentcube":
            if not metadata.router_url or not metadata.workload_manager_url:
                 raise ValueError(
                    "Missing required configuration for AgentCube provider. "
                    "Please ensure 'router_url' and 'workload_manager_url' are set in agent_metadata.yaml."
                )
        
        # Determine the final image URL for deployment, potentially pushing it
        # This will handle the logic of using metadata registry or explicit --image-url
        final_image_url = self._prepare_image_for_publishing(workspace_path, metadata, options)
        
        # Override options['image_url'] with the resolved final_image_url
        options['image_url'] = final_image_url

        if provider == "agentcube":
            try:
                self.agentcube_provider = AgentCubeProvider(verbose=self.verbose, namespace=namespace)
            except Exception as e:
                logger.warning(f"Failed to initialize AgentCube provider for CRD: {e}")
            return self._publish_cr_to_k8s(workspace_path, metadata, **options)
        elif provider == "k8s":
            try:
                self.k8s_provider = KubernetesProvider(verbose=self.verbose, namespace=namespace)
            except Exception as e:
                logger.warning(f"Failed to initialize standard K8s provider: {e}")
            return self._publish_k8s(workspace_path, metadata, **options)
        else:
            raise ValueError(f"Unsupported provider: {provider}. Supported providers are 'agentcube' and 'k8s'.")

    def _publish_cr_to_k8s(
        self,
        workspace_path: Path,
        metadata,
        **options: Any
    ) -> Dict[str, Any]:
        """
        Publish the agent to Kubernetes cluster using AgentRuntime CR.

        Args:
            workspace_path: Path to the agent workspace directory
            metadata: AgentMetadata object
            **options: Additional publish options

        Returns:
            Dict containing publish results

        Raises:
            ValueError: If publish fails
        """
        if self.verbose:
            logger.info(f"Publishing to K8s cluster (AgentRuntime CR) for workspace: {workspace_path}")

        if not self.agentcube_provider:
            raise RuntimeError(
                "AgentCube provider is not initialized. Ensure Kubernetes is configured."
            )

        # Image URL is already resolved in publish()
        image_url = options.get('image_url')
        if not image_url:
            raise ValueError("Image URL must be provided or configured in metadata.")

        if not metadata.readiness_probe_path or not metadata.readiness_probe_port:
            raise ValueError(
                "Missing required configuration for readiness probe. "
                "Please ensure 'readiness_probe_path' and 'readiness_probe_port' are set in agent_metadata.yaml."
            )

        # Step 3: Deploy AgentRuntime CR
        try:
            k8s_info = self.agentcube_provider.deploy_agent_runtime(
                agent_name=metadata.agent_name,
                image_url=image_url,
                port=metadata.port,
                entrypoint=metadata.entrypoint,
                env_vars=options.get('env_vars', None),
                workload_manager_url=metadata.workload_manager_url,
                router_url=metadata.router_url,
                readiness_probe_path=metadata.readiness_probe_path,
                readiness_probe_port=metadata.readiness_probe_port
            )
        except Exception as e:
            raise RuntimeError(f"Failed to deploy AgentRuntime CR to K8s: {str(e)}")

        # Step 4: Update metadata with K8s deployment information
        updates = {
            "agent_id": k8s_info["deployment_name"],
            "k8s_deployment": {
                **k8s_info,
                "type": "AgentRuntime",
            }
        }

        # Use provided endpoint or fall back to router_url from metadata
        endpoint = options.get('agent_endpoint') or metadata.agent_endpoint
        if not endpoint:
            raise ValueError("Please enter the endpoint for the agent")

        self.metadata_service.update_metadata(workspace_path, updates)

        result = {
            "agent_name": metadata.agent_name,
            "agent_id": k8s_info["deployment_name"],
            "deployment_name": k8s_info["deployment_name"],
            "namespace": k8s_info["namespace"],
            "status": "deployed",
            "agent_endpoint": endpoint,
        }
        
        if self.verbose:
            logger.info(f"K8s publish (AgentRuntime CR) completed: {result}")

        return result

    def _publish_k8s(
        self,
        workspace_path: Path,
        metadata,
        **options: Any
    ) -> Dict[str, Any]:
        """
        Publish the agent to local Kubernetes cluster using standard Deployment/Service.

        Args:
            workspace_path: Path to the agent workspace directory
            metadata: AgentMetadata object
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

        # Image URL is already resolved in publish()
        image_url = options.get('image_url')
        if not image_url:
            raise ValueError("Image URL must be provided or configured in metadata.")

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
                self.k8s_provider.wait_for_deployment_ready(k8s_info['deployment_name'], timeout=120)
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

        # Determine registry and credentials
        # CLI options take precedence over metadata
        target_registry_url = options.get('image_url') or metadata.registry_url
        target_username = options.get('image_username') or metadata.registry_username
        target_password = options.get('image_password') or metadata.registry_password

        final_image_to_deploy = ""

        if target_registry_url:
            # We have a registry to push to
            if self.verbose:
                logger.info(f"Pushing image to repository: {target_registry_url}")

            push_result = self.docker_service.push_image(
                image_name=local_image_name,
                registry_url=target_registry_url,
                username=target_username,
                password=target_password
            )
            final_image_to_deploy = push_result["pushed_image"]

            if self.verbose:
                logger.info(f"Image pushed successfully: {final_image_to_deploy}")
        else:
            # No registry specified, expect a pre-built image URL for deployment
            # Check if options explicitly provided an image_url which is to be used directly
            final_image_to_deploy = options.get('image_url')

            if not final_image_to_deploy:
                raise ValueError(
                    "No registry URL provided in metadata or via --image-url option. "
                    "Please provide an image URL via --image-url for direct deployment."
                )
            if self.verbose:
                logger.info(f"Using pre-existing image for deployment: {final_image_to_deploy}")


        return final_image_to_deploy

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