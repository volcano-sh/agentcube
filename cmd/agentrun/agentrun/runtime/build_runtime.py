"""
Build runtime for AgentRun.

This module implements the build command functionality, handling
the building of agent images from packaged workspaces.
"""

import logging
from pathlib import Path
from typing import Any, Dict

from agentrun.services.docker_service import DockerService
from agentrun.services.metadata_service import MetadataService


class BuildRuntime:
    """Runtime for the build command."""

    def __init__(self, verbose: bool = False) -> None:
        self.verbose = verbose
        self.metadata_service = MetadataService(verbose=verbose)
        self.docker_service = DockerService(verbose=verbose)

        if verbose:
            logging.basicConfig(level=logging.DEBUG)

    def build(
        self,
        workspace_path: Path,
        **options: Any
    ) -> Dict[str, Any]:
        """
        Build the agent image from the packaged workspace.

        Args:
            workspace_path: Path to the agent workspace directory
            **options: Additional build options (proxy, cloud_provider, output)

        Returns:
            Dict containing build results

        Raises:
            ValueError: If build fails
            FileNotFoundError: If required files are missing
        """
        if self.verbose:
            logger.info(f"Starting build process for workspace: {workspace_path}")

        # Step 1: Validate workspace
        self._validate_build_prerequisites(workspace_path)

        # Step 2: Load metadata
        metadata = self.metadata_service.load_metadata(workspace_path)

        # Step 3: Determine build mode
        build_mode = options.get('build_mode', metadata.build_mode)

        if build_mode == 'local':
            return self._build_local(workspace_path, metadata, options)
        elif build_mode == 'cloud':
            return self._build_cloud(workspace_path, metadata, options)
        else:
            raise ValueError(f"Unsupported build mode: {build_mode}")

    def _validate_build_prerequisites(self, workspace_path: Path) -> None:
        """Validate that the workspace is ready for building."""
        if not workspace_path.exists():
            raise ValueError(f"Workspace directory does not exist: {workspace_path}")

        # Check for Dockerfile
        dockerfile_path = workspace_path / "Dockerfile"
        if not dockerfile_path.exists():
            raise ValueError(f"Dockerfile not found in workspace: {dockerfile_path}")

        if self.verbose:
            logger.debug(f"Build prerequisites validated for: {workspace_path}")

    def _build_local(
        self,
        workspace_path: Path,
        metadata,
        options: Dict[str, Any]
    ) -> Dict[str, Any]:
        """Build the image using local Docker."""
        if self.verbose:
            logger.info("Starting local Docker build")

        # Check Docker availability
        if not self.docker_service.check_docker_available():
            raise RuntimeError("Docker is not available or not running")

        # Prepare build arguments
        build_args = {}

        # Add proxy if provided
        proxy = options.get('proxy')
        if proxy:
            build_args.update({
                'http_proxy': proxy,
                'https_proxy': proxy,
                'HTTP_PROXY': proxy,
                'HTTPS_PROXY': proxy
            })
            if self.verbose:
                logger.info(f"Using proxy: {proxy}")

        # Build the image
        dockerfile_path = workspace_path / "Dockerfile"
        image_name = metadata.agent_name.lower().replace(' ', '-')
        tag = options.get('tag', 'latest')

        build_result = self.docker_service.build_image(
            dockerfile_path=dockerfile_path,
            context_path=workspace_path,
            image_name=image_name,
            tag=tag,
            build_args=build_args
        )

        # Update metadata with build information
        self._update_build_metadata(workspace_path, metadata, build_result)

        result = {
            "image_name": build_result["image_name"],
            "image_tag": build_result["image_name"],
            "image_size": build_result["image_size"],
            "build_time": build_result["build_time"],
            "build_mode": "local"
        }

        if self.verbose:
            logger.info(f"Local build completed: {result}")

        return result

    def _build_cloud(
        self,
        workspace_path: Path,
        metadata,
        options: Dict[str, Any]
    ) -> Dict[str, Any]:
        """Build the image using cloud services."""
        # TODO: Implement cloud build functionality
        if self.verbose:
            logger.info("Cloud build not yet implemented, falling back to local build")

        # For MVP, fall back to local build
        return self._build_local(workspace_path, metadata, options)

    def _update_build_metadata(
        self,
        workspace_path: Path,
        metadata,
        build_result: Dict[str, str]
    ) -> None:
        """Update metadata with build information."""
        image_info = {
            "repository_url": build_result["image_name"],
            "tag": "latest",
            "build_mode": "local",
            "build_size": build_result["image_size"],
            "build_time": build_result["build_time"]
        }

        # Update metadata
        updates = {"image": image_info}
        self.metadata_service.update_metadata(workspace_path, updates)

        if self.verbose:
            logger.debug(f"Updated metadata with build info: {image_info}")


logger = logging.getLogger(__name__)