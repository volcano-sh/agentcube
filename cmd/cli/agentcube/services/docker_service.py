"""
Docker service for building and managing container images.

This service provides functionality to build Docker images, push to registries,
and manage container operations using Docker SDK.
"""

import logging
import time
from pathlib import Path
from typing import Dict, Optional

import docker
from docker.errors import DockerException, BuildError, APIError

logger = logging.getLogger(__name__)


class DockerService:
    """Service for Docker operations using Docker SDK."""

    def __init__(self, verbose: bool = False) -> None:
        self.verbose = verbose
        if verbose:
            logging.basicConfig(level=logging.DEBUG)

        try:
            self.client = docker.from_env()
            # Test connection
            self.client.ping()
            if self.verbose:
                logger.info("Successfully connected to Docker daemon")
        except DockerException as e:
            logger.error(f"Failed to connect to Docker: {e}")
            self.client = None

    def check_docker_available(self) -> bool:
        """Check if Docker is available and running."""
        try:
            if not self.client:
                return False

            # Test Docker connection
            self.client.ping()

            if self.verbose:
                version_info = self.client.version()
                logger.info(f"Docker version: {version_info.get('Version', 'Unknown')}")
            return True
        except DockerException as e:
            logger.error(f"Docker not available: {e}")
            return False

    def build_image(
        self,
        dockerfile_path: Path,
        context_path: Path,
        image_name: str,
        tag: str = "latest",
        build_args: Optional[Dict[str, str]] = None
    ) -> Dict[str, str]:
        """
        Build a Docker image using Docker SDK.

        Args:
            dockerfile_path: Path to Dockerfile
            context_path: Path to build context
            image_name: Name for the image
            tag: Image tag
            build_args: Additional build arguments

        Returns:
            Dict containing build results

        Raises:
            RuntimeError: If build fails
        """
        if not self.check_docker_available():
            raise RuntimeError("Docker is not available or not running")

        full_image_name = f"{image_name}:{tag}"

        if self.verbose:
            logger.info(f"Building Docker image: {full_image_name}")
            logger.info(f"Dockerfile: {dockerfile_path}")
            logger.info(f"Context: {context_path}")

        # Prepare build arguments
        build_kwargs = {
            "path": str(context_path),
            "dockerfile": str(dockerfile_path),
            "tag": full_image_name,
            "rm": True,  # Remove intermediate containers
        }

        if build_args:
            build_kwargs["buildargs"] = build_args
            if self.verbose:
                logger.info(f"Build args: {build_args}")

        try:
            start_time = time.time()

            # Build the image
            image = self.client.images.build(**build_kwargs)[0]
            build_time = time.time() - start_time

            if self.verbose:
                logger.info(f"Docker image built successfully in {build_time:.1f}s")

            # Get image details
            image_info = self._get_image_info(full_image_name)

            result = {
                "image_name": full_image_name,
                "image_id": image_info.get("id", image.id),
                "image_size": image_info.get("size", "Unknown"),
                "build_time": f"{build_time:.1f}s"
            }

            if self.verbose:
                logger.debug(f"Build result: {result}")

            return result

        except BuildError as e:
            error_msg = f"Docker build failed: {e}"
            logger.error(error_msg)
            # Log build logs if available
            if hasattr(e, 'build_log'):
                logger.error("Docker build logs:")
                for line in e.build_log:
                    if 'stream' in line:
                        logger.error(line['stream'].strip())
            raise RuntimeError(error_msg)
        except APIError as e:
            error_msg = f"Docker API error during build: {e}"
            logger.error(error_msg)
            raise RuntimeError(error_msg)
        except Exception as e:
            error_msg = f"Docker build error: {str(e)}"
            logger.error(error_msg)
            raise RuntimeError(error_msg)

    def get_image_info(self, image_name: str) -> Dict[str, str]:
        """Get information about a Docker image using SDK."""
        return self._get_image_info(image_name)

    def _get_image_info(self, image_name: str) -> Dict[str, str]:
        """Get information about a Docker image using SDK."""
        try:
            image = self.client.images.get(image_name)

            return {
                "id": image.id.split(":")[1][:12] if ":" in image.id else image.id[:12],
                "size": self._format_size(image.attrs.get('Size', 0))
            }
        except Exception as e:
            logger.warning(f"Failed to get image info for {image_name}: {e}")
            return {}

    def _format_size(self, size_bytes: int) -> str:
        """Format size in bytes to human readable format."""
        if size_bytes == 0:
            return "Unknown"

        for unit in ['B', 'KB', 'MB', 'GB']:
            if size_bytes < 1024.0:
                return f"{size_bytes:.1f}{unit}"
            size_bytes /= 1024.0
        return f"{size_bytes:.1f}TB"

    def push_image(
        self,
        image_name: str,
        registry_url: Optional[str] = None,
        username: Optional[str] = None,
        password: Optional[str] = None
    ) -> Dict[str, str]:
        """
        Push a Docker image to a registry using Docker SDK.

        Args:
            image_name: Name of the image to push
            registry_url: Registry URL (if different from image name)
            username: Registry username
            password: Registry password

        Returns:
            Dict containing push results

        Raises:
            RuntimeError: If push fails
        """
        if not self.check_docker_available():
            raise RuntimeError("Docker is not available or not running")

        # Login if credentials are provided
        if username and password:
            self._docker_login_sdk(registry_url, username, password)

        # Determine the full image name to push
        full_image_name = image_name
        if registry_url and not image_name.startswith(registry_url):
            # Extract the part after the last slash from original image name
            image_tag = image_name.split(":")[-1] if ":" in image_name else "latest"
            base_name = image_name.split("/")[-1].split(":")[0]
            full_image_name = f"{registry_url}/{base_name}:{image_tag}"

        # Tag the image if needed
        if full_image_name != image_name:
            self._tag_image_sdk(image_name, full_image_name)

        if self.verbose:
            logger.info(f"Pushing Docker image: {full_image_name}")

        try:
            start_time = time.time()

            # Push the image
            push_logs = self.client.images.push(full_image_name, stream=True, decode=True)

            # Process push logs
            for log_entry in push_logs:
                if self.verbose:
                    logger.debug(f"Push log: {log_entry}")

                if "error" in log_entry:
                    error_msg = f"Docker push failed: {log_entry['error']}"
                    logger.error(error_msg)
                    raise RuntimeError(error_msg)

                if "status" in log_entry and log_entry["status"] == "pushed":
                    if self.verbose:
                        logger.info(f"Successfully pushed: {log_entry}")

            push_time = time.time() - start_time

            if self.verbose:
                logger.info(f"Docker image pushed successfully in {push_time:.1f}s")

            return {
                "pushed_image": full_image_name,
                "push_time": f"{push_time:.1f}s"
            }

        except APIError as e:
            error_msg = f"Docker API error during push: {e}"
            logger.error(error_msg)
            raise RuntimeError(error_msg)
        except Exception as e:
            error_msg = f"Docker push error: {str(e)}"
            logger.error(error_msg)
            raise RuntimeError(error_msg)

    def _docker_login_sdk(self, registry: Optional[str], username: str, password: str) -> None:
        """Login to Docker registry using SDK."""
        try:
            if self.verbose:
                logger.info(f"Logging in to Docker registry: {registry or 'default'}")

            self.client.login(
                username=username,
                password=password,
                registry=registry,
                reauth=True
            )

            if self.verbose:
                logger.info("Successfully logged into Docker registry")

        except APIError as e:
            raise RuntimeError(f"Docker login failed: {e}")
        except Exception as e:
            raise RuntimeError(f"Docker login error: {str(e)}")

    def _tag_image_sdk(self, source_image: str, target_image: str) -> None:
        """Tag a Docker image using SDK."""
        try:
            image = self.client.images.get(source_image)
            image.tag(target_image)

            if self.verbose:
                logger.info(f"Successfully tagged {source_image} as {target_image}")

        except Exception as e:
            raise RuntimeError(f"Docker tag error: {str(e)}")

    def remove_image(self, image_name: str) -> bool:
        """Remove a Docker image using SDK."""
        try:
            self.client.images.remove(image_name, force=True)

            if self.verbose:
                logger.info(f"Successfully removed image: {image_name}")

            return True
        except Exception as e:
            logger.warning(f"Failed to remove image {image_name}: {e}")
            return False