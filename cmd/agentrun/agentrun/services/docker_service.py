"""
Docker service for building and managing container images.

This service provides functionality to build Docker images, push to registries,
and manage container operations.
"""

import logging
import subprocess
import time
from pathlib import Path
from typing import Dict, Optional, Tuple


logger = logging.getLogger(__name__)


class DockerService:
    """Service for Docker operations."""

    def __init__(self, verbose: bool = False) -> None:
        self.verbose = verbose
        if verbose:
            logging.basicConfig(level=logging.DEBUG)

    def check_docker_available(self) -> bool:
        """Check if Docker is available and running."""
        try:
            result = subprocess.run(
                ["docker", "--version"],
                capture_output=True,
                text=True,
                timeout=10
            )
            if result.returncode == 0:
                if self.verbose:
                    logger.info(f"Docker version: {result.stdout.strip()}")
                return True
            else:
                logger.error(f"Docker not available: {result.stderr}")
                return False
        except (subprocess.TimeoutExpired, FileNotFoundError) as e:
            logger.error(f"Docker check failed: {e}")
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
        Build a Docker image.

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

        cmd = [
            "docker", "build",
            "-f", str(dockerfile_path),
            "-t", full_image_name,
            str(context_path)
        ]

        # Add build args if provided
        if build_args:
            for key, value in build_args.items():
                cmd.extend(["--build-arg", f"{key}={value}"])

        if self.verbose:
            logger.info(f"Building Docker image: {full_image_name}")
            logger.info(f"Command: {' '.join(cmd)}")

        try:
            start_time = time.time()
            result = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                timeout=600  # 10 minutes timeout
            )
            build_time = time.time() - start_time

            if result.returncode == 0:
                if self.verbose:
                    logger.info(f"Docker image built successfully in {build_time:.1f}s")

                # Get image size
                image_info = self.get_image_info(full_image_name)
                image_size = image_info.get("size", "Unknown")

                return {
                    "image_name": full_image_name,
                    "image_id": image_info.get("id", "Unknown"),
                    "image_size": image_size,
                    "build_time": f"{build_time:.1f}s"
                }
            else:
                error_msg = f"Docker build failed: {result.stderr}"
                logger.error(error_msg)
                raise RuntimeError(error_msg)

        except subprocess.TimeoutExpired:
            raise RuntimeError("Docker build timed out after 10 minutes")
        except Exception as e:
            raise RuntimeError(f"Docker build error: {str(e)}")

    def get_image_info(self, image_name: str) -> Dict[str, str]:
        """Get information about a Docker image."""
        try:
            result = subprocess.run(
                ["docker", "images", image_name, "--format", "{{.ID}}\t{{.Size}}"],
                capture_output=True,
                text=True,
                timeout=30
            )

            if result.returncode == 0 and result.stdout.strip():
                parts = result.stdout.strip().split('\t')
                if len(parts) >= 2:
                    return {
                        "id": parts[0],
                        "size": parts[1]
                    }
            return {}
        except Exception as e:
            logger.warning(f"Failed to get image info: {e}")
            return {}

    def push_image(
        self,
        image_name: str,
        registry_url: Optional[str] = None,
        username: Optional[str] = None,
        password: Optional[str] = None
    ) -> Dict[str, str]:
        """
        Push a Docker image to a registry.

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
            self._docker_login(registry_url, username, password)

        # Determine the full image name to push
        full_image_name = image_name
        if registry_url and not image_name.startswith(registry_url):
            # Extract the part after the last slash from original image name
            image_tag = image_name.split(":")[-1] if ":" in image_name else "latest"
            base_name = image_name.split("/")[-1].split(":")[0]
            full_image_name = f"{registry_url}/{base_name}:{image_tag}"

        # Tag the image if needed
        if full_image_name != image_name:
            self._tag_image(image_name, full_image_name)

        if self.verbose:
            logger.info(f"Pushing Docker image: {full_image_name}")

        try:
            start_time = time.time()
            result = subprocess.run(
                ["docker", "push", full_image_name],
                capture_output=True,
                text=True,
                timeout=1200  # 20 minutes timeout
            )
            push_time = time.time() - start_time

            if result.returncode == 0:
                if self.verbose:
                    logger.info(f"Docker image pushed successfully in {push_time:.1f}s")

                return {
                    "pushed_image": full_image_name,
                    "push_time": f"{push_time:.1f}s"
                }
            else:
                error_msg = f"Docker push failed: {result.stderr}"
                logger.error(error_msg)
                raise RuntimeError(error_msg)

        except subprocess.TimeoutExpired:
            raise RuntimeError("Docker push timed out after 20 minutes")
        except Exception as e:
            raise RuntimeError(f"Docker push error: {str(e)}")

    def _docker_login(self, registry: Optional[str], username: str, password: str) -> None:
        """Login to Docker registry."""
        cmd = ["docker", "login"]
        if registry:
            cmd.append(registry)

        try:
            if self.verbose:
                logger.info(f"Logging in to Docker registry: {registry or 'default'}")

            # Use subprocess to provide credentials securely
            process = subprocess.Popen(
                cmd,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True
            )

            stdout, stderr = process.communicate(input=f"{username}\n{password}\n")

            if process.returncode != 0:
                raise RuntimeError(f"Docker login failed: {stderr}")

        except Exception as e:
            raise RuntimeError(f"Docker login error: {str(e)}")

    def _tag_image(self, source_image: str, target_image: str) -> None:
        """Tag a Docker image."""
        try:
            result = subprocess.run(
                ["docker", "tag", source_image, target_image],
                capture_output=True,
                text=True,
                timeout=30
            )

            if result.returncode != 0:
                raise RuntimeError(f"Docker tag failed: {result.stderr}")

        except Exception as e:
            raise RuntimeError(f"Docker tag error: {str(e)}")

    def remove_image(self, image_name: str) -> bool:
        """Remove a Docker image."""
        try:
            result = subprocess.run(
                ["docker", "rmi", "-f", image_name],
                capture_output=True,
                text=True,
                timeout=60
            )
            return result.returncode == 0
        except Exception as e:
            logger.warning(f"Failed to remove image {image_name}: {e}")
            return False