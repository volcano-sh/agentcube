"""
Publish runtime for AgentRun.

This module implements the publish command functionality, handling
the publishing of agent images to AgentCube.
"""

import asyncio
import logging
from pathlib import Path
from typing import Any, Dict, Optional

from agentrun.services.agentcube_service import AgentCubeService
from agentrun.services.docker_service import DockerService
from agentrun.services.metadata_service import MetadataService
from agentrun.services.k8s_provider import KubernetesProvider


class PublishRuntime:
    """Runtime for the publish command."""

    def __init__(self, verbose: bool = False, use_k8s: bool = False) -> None:
        self.verbose = verbose
        self.use_k8s = use_k8s
        self.metadata_service = MetadataService(verbose=verbose)
        self.docker_service = DockerService(verbose=verbose)
        self.agentcube_service = AgentCubeService(verbose=verbose)
        self.k8s_provider = None

        if use_k8s:
            try:
                self.k8s_provider = KubernetesProvider(verbose=verbose)
            except Exception as e:
                logger.warning(f"Failed to initialize K8s provider: {e}")

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

        # Check if K8s deployment is requested
        use_k8s = options.get('use_k8s', self.use_k8s)

        if use_k8s:
            return self._publish_to_k8s(workspace_path, **options)
        else:
            return self._publish_to_agentcube(workspace_path, **options)

    def _publish_to_agentcube(
        self,
        workspace_path: Path,
        **options: Any
    ) -> Dict[str, Any]:
        """
        Publish the agent to AgentCube.

        Args:
            workspace_path: Path to the agent workspace directory
            **options: Additional publish options

        Returns:
            Dict containing publish results

        Raises:
            ValueError: If publish fails
        """
        if self.verbose:
            logger.info(f"Publishing to AgentCube for workspace: {workspace_path}")

        # Step 1: Validate workspace and metadata
        metadata = self._validate_publish_prerequisites(workspace_path)

        # Step 2: Prepare image for publishing
        # image_url = self._prepare_image_for_publishing(workspace_path, metadata, options)
        image_url = options.get('image_url')

        # Step 3: Register agent with AgentCube
        agent_info = asyncio.run(self._register_agent_with_agentcube(metadata, image_url, options))

        # Step 4: Update metadata with publish information
        self._update_publish_metadata(workspace_path, agent_info)

        result = {
            "agent_name": metadata.agent_name,
            "agent_id": agent_info["agent_id"],
            "agent_endpoint": agent_info["agent_endpoint"],
            "image_url": image_url,
            "version": options.get('version', 'latest')
        }

        if self.verbose:
            logger.info(f"Publish completed: {result}")

        return result

    def _publish_to_k8s(
        self,
        workspace_path: Path,
        **options: Any
    ) -> Dict[str, Any]:
        """
        Publish the agent to local Kubernetes cluster.

        Args:
            workspace_path: Path to the agent workspace directory
            **options: Additional publish options

        Returns:
            Dict containing publish results

        Raises:
            ValueError: If publish fails
        """
        if self.verbose:
            logger.info(f"Publishing to K8s cluster for workspace: {workspace_path}")

        if not self.k8s_provider:
            try:
                self.k8s_provider = KubernetesProvider(verbose=self.verbose)
            except Exception as e:
                raise RuntimeError(
                    f"Failed to initialize K8s provider: {str(e)}\n"
                    "Make sure you have kubernetes package installed: pip install kubernetes"
                )

        # Step 1: Load metadata
        metadata = self.metadata_service.load_metadata(workspace_path)

        # Step 2: Get image information
        if not metadata.image:
            raise ValueError("Agent must be built before publishing. Run 'agentrun build' first.")

        image_url = metadata.image.get("repository_url")
        if not image_url:
            raise ValueError("No image found in metadata. Build the agent first.")

        # Step 3: Deploy to K8s
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

            # Step 4: Update metadata with K8s deployment information
            updates = {
                "agent_id": k8s_info["deployment_name"],
                "agent_endpoint": k8s_info["service_url"],
                "k8s_deployment": {
                    "deployment_name": k8s_info["deployment_name"],
                    "service_name": k8s_info["service_name"],
                    "namespace": k8s_info["namespace"],
                    "node_port": k8s_info["node_port"],
                    "container_port": k8s_info["container_port"],
                    "replicas": k8s_info["replicas"],
                    "service_url": k8s_info["service_url"]
                }
            }
            self.metadata_service.update_metadata(workspace_path, updates)

            result = {
                "agent_name": metadata.agent_name,
                "agent_id": k8s_info["deployment_name"],
                "agent_endpoint": k8s_info["service_url"],
                "deployment_name": k8s_info["deployment_name"],
                "node_port": k8s_info["node_port"],
                "namespace": k8s_info["namespace"]
            }

            if self.verbose:
                logger.info(f"K8s publish completed: {result}")

            return result

        except Exception as e:
            raise RuntimeError(f"Failed to deploy to K8s: {str(e)}")

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

    async def _register_agent_with_agentcube(
        self,
        metadata,
        image_url: str,
        options: Dict[str, Any]
    ) -> Dict[str, Any]:
        """Register the agent with AgentCube."""
        if self.verbose:
            logger.info("Registering agent with AgentCube")

        # Prepare agent metadata for API
        agent_metadata = {
            "agent_name": metadata.agent_name,
            "description": options.get('description', metadata.description),
            "version": options.get('version', 'latest'),
            "language": metadata.language,
            "entrypoint": metadata.entrypoint,
            "port": metadata.port,
            "build_mode": metadata.build_mode,
            "region": options.get('region', metadata.region)
        }

        try:
            result = await self.agentcube_service.create_or_update_agent(
                agent_metadata=agent_metadata,
                image_url=image_url
            )

            return result

        except Exception as e:
            raise RuntimeError(f"Failed to register agent with AgentCube: {str(e)}")

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