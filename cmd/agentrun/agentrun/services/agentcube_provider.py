"""
AgentCube provider for deploying AgentRuntime CRs to Kubernetes.

This service provides functionality to deploy agents as Custom Resources (CRs)
specifically designed for the AgentCube ecosystem.
"""

import logging
from typing import Any, Dict, Optional

try:
    from kubernetes import client, config
    from kubernetes.client.rest import ApiException
except ImportError:
    client = None
    config = None
    ApiException = None

logger = logging.getLogger(__name__)


class AgentCubeProvider:
    """Service for deploying AgentCube-specific CRs to Kubernetes cluster."""

    def __init__(
        self,
        namespace: str = "agentrun",
        verbose: bool = False,
        kubeconfig: Optional[str] = None
    ) -> None:
        """
        Initialize AgentCube provider.

        Args:
            namespace: Kubernetes namespace for agent deployments
            verbose: Enable verbose logging
            kubeconfig: Path to kubeconfig file (uses default if not specified)
        """
        self.namespace = namespace
        self.verbose = verbose

        if verbose:
            logging.basicConfig(level=logging.DEBUG)

        if client is None or config is None:
            raise ImportError(
                "kubernetes package is required for K8s provider. "
                "Install with: pip install kubernetes"
            )

        # Load Kubernetes configuration
        try:
            if kubeconfig:
                config.load_kube_config(config_file=kubeconfig)
            else:
                # Try in-cluster config first, then local kubeconfig
                try:
                    config.load_incluster_config()
                    if self.verbose:
                        logger.info("Loaded in-cluster Kubernetes config")
                except Exception:
                    config.load_kube_config()
                    if self.verbose:
                        logger.info("Loaded local Kubernetes config")

            # Initialize API clients
            self.core_api = client.CoreV1Api()
            self.custom_api = client.CustomObjectsApi()

            if self.verbose:
                logger.info(f"AgentCube provider initialized for namespace: {namespace}")

        except Exception as e:
            raise RuntimeError(f"Failed to initialize Kubernetes client for AgentCubeProvider: {str(e)}")

        # Ensure namespace exists
        self._ensure_namespace()

    def _ensure_namespace(self) -> None:
        """Ensure the target namespace exists, create if it doesn't."""
        try:
            self.core_api.read_namespace(name=self.namespace)
            if self.verbose:
                logger.debug(f"Namespace {self.namespace} already exists")
        except ApiException as e:
            if e.status == 404:
                # Namespace doesn't exist, create it
                namespace = client.V1Namespace(
                    metadata=client.V1ObjectMeta(name=self.namespace)
                )
                self.core_api.create_namespace(body=namespace)
                if self.verbose:
                    logger.info(f"Created namespace: {self.namespace}")
            else:
                raise

    def deploy_agent_runtime(
        self,
        agent_name: str,
        image_url: str,
        port: int,
        entrypoint: Optional[str] = None,
        env_vars: Optional[Dict[str, str]] = None
    ) -> Dict[str, Any]:
        """
        Deploy an AgentRuntime CR to Kubernetes cluster.

        Args:
            agent_name: Name of the agent
            image_url: Docker image URL
            port: Container port to expose
            entrypoint: Optional custom entrypoint command
            env_vars: Optional environment variables

        Returns:
            Dict containing deployment information
        """
        if self.verbose:
            logger.info(f"Deploying AgentRuntime {agent_name} to K8s cluster")

        k8s_name = self._sanitize_name(agent_name)
        group = "runtime.agentcube.io"
        version = "v1alpha1"
        plural = "agentruntimes"

        # Prepare container spec
        container = {
            "name": "runtime",
            "image": image_url,
            "imagePullPolicy": "IfNotPresent",
            "ports": [{"name": "http", "containerPort": port, "protocol": "TCP"}],
        }

        if entrypoint:
            parts = entrypoint.split()
            if len(parts) > 0:
                container["command"] = [parts[0]]
                if len(parts) > 1:
                    container["args"] = parts[1:]

        if env_vars:
            env_list = [{"name": k, "value": str(v)} for k, v in env_vars.items()]
            container["env"] = env_list

        # Prepare AgentRuntime manifest
        agent_runtime = {
            "apiVersion": f"{group}/{version}",
            "kind": "AgentRuntime",
            "metadata": {
                "name": k8s_name,
                "namespace": self.namespace,
                "labels": {"app": k8s_name}
            },
            "spec": {
                "ports": [
                    {
                        "name": "http",
                        "port": port,
                        "protocol": "HTTP",
                        "pathPrefix": "/"
                    }
                ],
                "template": {
                    "spec": {
                        "containers": [container],
                        "restartPolicy": "Always"
                    }
                },
                "sessionTimeout": "15m",
                "maxSessionDuration": "1h"
            }
        }

        try:
            # Try to get existing CR
            try:
                self.custom_api.get_namespaced_custom_object(
                    group=group,
                    version=version,
                    namespace=self.namespace,
                    plural=plural,
                    name=k8s_name
                )
                # Update existing CR
                self.custom_api.patch_namespaced_custom_object(
                    group=group,
                    version=version,
                    namespace=self.namespace,
                    plural=plural,
                    name=k8s_name,
                    body=agent_runtime
                )
                if self.verbose:
                    logger.info(f"Updated existing AgentRuntime: {k8s_name}")
                
            except ApiException as e:
                if e.status == 404:
                    # Create new CR
                    self.custom_api.create_namespaced_custom_object(
                        group=group,
                        version=version,
                        namespace=self.namespace,
                        plural=plural,
                        body=agent_runtime
                    )
                    if self.verbose:
                        logger.info(f"Created new AgentRuntime: {k8s_name}")
                else:
                    raise

            return {
                "deployment_name": k8s_name,
                "namespace": self.namespace,
                "status": "deployed",
                "type": "AgentRuntime"
            }

        except Exception as e:
            raise RuntimeError(f"Failed to deploy AgentRuntime to K8s: {str(e)}")

    def _sanitize_name(self, name: str) -> str:
        """
        Sanitize agent name to be Kubernetes DNS-1123 compliant.

        K8s names must:
        - contain only lowercase alphanumeric characters or '-'
        - start with an alphanumeric character
        - end with an alphanumeric character
        - be at most 63 characters

        Args:
            name: Original agent name

        Returns:
            Sanitized name suitable for K8s resources
        """
        # Convert to lowercase
        sanitized = name.lower()

        # Replace underscores and spaces with hyphens
        sanitized = sanitized.replace("_", "-").replace(" ", "-")

        # Remove any characters that aren't alphanumeric or hyphen
        sanitized = "".join(c for c in sanitized if c.isalnum() or c == "-")

        # Ensure it starts with alphanumeric
        while sanitized and not sanitized[0].isalnum():
            sanitized = sanitized[1:]

        # Ensure it ends with alphanumeric
        while sanitized and not sanitized[-1].isalnum():
            sanitized = sanitized[:-1]

        # Truncate to 63 characters
        sanitized = sanitized[:63]

        # If empty after sanitization, use default
        if not sanitized:
            sanitized = "agent"

        return sanitized

    def get_agent_runtime(self, name: str, namespace: str) -> Optional[Dict[str, Any]]:
        """
        Fetches an AgentRuntime Custom Resource by name and namespace.

        Args:
            name: The name of the AgentRuntime CR.
            namespace: The namespace of the AgentRuntime CR.

        Returns:
            A dictionary representing the AgentRuntime CR, or None if not found.
        """
        group = "runtime.agentcube.io"
        version = "v1alpha1"
        plural = "agentruntimes"

        try:
            cr = self.custom_api.get_namespaced_custom_object(
                group=group,
                version=version,
                name=name,
                namespace=namespace,
                plural=plural
            )
            return cr
        except ApiException as e:
            if e.status == 404:
                if self.verbose:
                    logger.debug(f"AgentRuntime CR '{name}' not found in namespace '{namespace}'.")
                return None
            else:
                logger.error(f"Error fetching AgentRuntime CR '{name}': {e}")
                raise
