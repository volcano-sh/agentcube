"""
Kubernetes provider for local agent deployment.

This service provides functionality to deploy agents to a local Kubernetes cluster,
exposing them via NodePort services for testing and development.
"""

import logging
import time
from typing import Any, Dict, Optional
from kubernetes import client, config
from kubernetes.client.rest import ApiException

logger = logging.getLogger(__name__)


class KubernetesProvider:
    """Service for deploying agents to Kubernetes cluster."""

    def __init__(
        self,
        namespace: str = "agentrun",
        verbose: bool = False,
        kubeconfig: Optional[str] = None
    ) -> None:
        """
        Initialize Kubernetes provider.

        Args:
            namespace: Kubernetes namespace for agent deployments
            verbose: Enable verbose logging
            kubeconfig: Path to kubeconfig file (uses default if not specified)
        """
        self.namespace = namespace
        self.verbose = verbose

        if verbose:
            logging.basicConfig(level=logging.DEBUG)

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
                except:
                    config.load_kube_config()
                    if self.verbose:
                        logger.info("Loaded local Kubernetes config")

            # Initialize API clients
            self.core_api = client.CoreV1Api()
            self.apps_api = client.AppsV1Api()

            if self.verbose:
                logger.info(f"Kubernetes provider initialized for namespace: {namespace}")

        except Exception as e:
            raise RuntimeError(f"Failed to initialize Kubernetes client: {str(e)}")

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

    def deploy_agent(
        self,
        agent_name: str,
        image_url: str,
        port: int,
        entrypoint: Optional[str] = None,
        replicas: int = 1,
        node_port: Optional[int] = None,
        env_vars: Optional[Dict[str, str]] = None
    ) -> Dict[str, Any]:
        """
        Deploy an agent to Kubernetes cluster.

        Args:
            agent_name: Name of the agent (used for deployment and service names)
            image_url: Docker image URL
            port: Container port to expose
            entrypoint: Optional custom entrypoint command
            replicas: Number of replicas (default: 1)
            node_port: Specific NodePort to use (30000-32767), auto-assigned if None
            env_vars: Optional environment variables

        Returns:
            Dict containing deployment information including service URL and NodePort

        Raises:
            RuntimeError: If deployment fails
        """
        if self.verbose:
            logger.info(f"Deploying agent {agent_name} to K8s cluster")

        # Sanitize agent name for K8s (must be DNS-1123 compliant)
        k8s_name = self._sanitize_name(agent_name)

        try:
            # Create or update Deployment
            deployment_info = self._create_deployment(
                name=k8s_name,
                image_url=image_url,
                port=port,
                entrypoint=entrypoint,
                replicas=replicas,
                env_vars=env_vars
            )

            # Create or update Service
            service_info = self._create_service(
                name=k8s_name,
                port=port,
                node_port=node_port
            )

            result = {
                "deployment_name": k8s_name,
                "service_name": k8s_name,
                "namespace": self.namespace,
                "replicas": replicas,
                "container_port": port,
                "node_port": service_info["node_port"],
                "service_url": f"http://localhost:{service_info['node_port']}",
            }

            if self.verbose:
                logger.info(f"K8s resources created for agent: {result}")

            return result

        except Exception as e:
            raise RuntimeError(f"Failed to deploy agent to K8s: {str(e)}")

    def _create_deployment(
        self,
        name: str,
        image_url: str,
        port: int,
        entrypoint: Optional[str],
        replicas: int,
        env_vars: Optional[Dict[str, str]]
    ) -> Dict[str, Any]:
        """Create or update a Kubernetes Deployment."""
        # Prepare container spec
        container_args = None
        if entrypoint:
            # Parse entrypoint into command and args
            parts = entrypoint.split()
            container_args = parts if len(parts) > 1 else None

        # Prepare environment variables
        env = []
        if env_vars:
            for key, value in env_vars.items():
                env.append(client.V1EnvVar(name=key, value=value))

        # Define container
        container = client.V1Container(
            name=name,
            image=image_url,
            ports=[client.V1ContainerPort(container_port=port)],
            args=container_args,
            env=env if env else None,
            image_pull_policy="IfNotPresent"  # Use local images if available
        )

        # Define pod template
        template = client.V1PodTemplateSpec(
            metadata=client.V1ObjectMeta(labels={"app": name}),
            spec=client.V1PodSpec(containers=[container])
        )

        # Define deployment spec
        spec = client.V1DeploymentSpec(
            replicas=replicas,
            selector=client.V1LabelSelector(match_labels={"app": name}),
            template=template
        )

        # Create deployment object
        deployment = client.V1Deployment(
            api_version="apps/v1",
            kind="Deployment",
            metadata=client.V1ObjectMeta(name=name, namespace=self.namespace),
            spec=spec
        )

        try:
            # Try to get existing deployment
            existing = self.apps_api.read_namespaced_deployment(
                name=name,
                namespace=self.namespace
            )
            # Update existing deployment
            self.apps_api.patch_namespaced_deployment(
                name=name,
                namespace=self.namespace,
                body=deployment
            )
            if self.verbose:
                logger.info(f"Updated existing deployment: {name}")
        except ApiException as e:
            if e.status == 404:
                # Create new deployment
                self.apps_api.create_namespaced_deployment(
                    namespace=self.namespace,
                    body=deployment
                )
                if self.verbose:
                    logger.info(f"Created new deployment: {name}")
            else:
                raise

        return {"name": name, "replicas": replicas}

    def _create_service(
        self,
        name: str,
        port: int,
        node_port: Optional[int]
    ) -> Dict[str, Any]:
        """Create or update a Kubernetes Service with NodePort."""
        # Define service spec
        spec = client.V1ServiceSpec(
            type="NodePort",
            selector={"app": name},
            ports=[
                client.V1ServicePort(
                    port=port,
                    target_port=port,
                    node_port=node_port  # K8s will auto-assign if None
                )
            ]
        )

        # Create service object
        service = client.V1Service(
            api_version="v1",
            kind="Service",
            metadata=client.V1ObjectMeta(name=name, namespace=self.namespace),
            spec=spec
        )

        try:
            # Try to get existing service
            existing = self.core_api.read_namespaced_service(
                name=name,
                namespace=self.namespace
            )
            # Update existing service
            result = self.core_api.patch_namespaced_service(
                name=name,
                namespace=self.namespace,
                body=service
            )
            if self.verbose:
                logger.info(f"Updated existing service: {name}")
        except ApiException as e:
            if e.status == 404:
                # Create new service
                result = self.core_api.create_namespaced_service(
                    namespace=self.namespace,
                    body=service
                )
                if self.verbose:
                    logger.info(f"Created new service: {name}")
            else:
                raise

        # Get the assigned NodePort
        actual_node_port = result.spec.ports[0].node_port

        return {
            "name": name,
            "port": port,
            "node_port": actual_node_port
        }

    def _wait_for_deployment_ready(self, name: str, timeout: int = 120) -> None:
        """Wait for deployment to be ready."""
        if self.verbose:
            logger.info(f"Waiting for deployment {name} to be ready...")

        start_time = time.time()
        while time.time() - start_time < timeout:
            try:
                deployment = self.apps_api.read_namespaced_deployment(
                    name=name,
                    namespace=self.namespace
                )

                # Check if deployment is ready
                if (deployment.status.ready_replicas is not None and
                    deployment.status.ready_replicas >= deployment.spec.replicas):
                    if self.verbose:
                        logger.info(f"Deployment {name} is ready")
                    return

                time.sleep(2)

            except ApiException as e:
                logger.error(f"Error checking deployment status: {e}")
                raise

        raise TimeoutError(f"Deployment {name} did not become ready within {timeout} seconds")

    def get_agent_status(self, agent_name: str) -> Dict[str, Any]:
        """
        Get the status of a deployed agent.

        Args:
            agent_name: Name of the agent

        Returns:
            Dict containing deployment status information

        Raises:
            RuntimeError: If status check fails
        """
        k8s_name = self._sanitize_name(agent_name)

        try:
            # Get deployment status
            deployment = self.apps_api.read_namespaced_deployment(
                name=k8s_name,
                namespace=self.namespace
            )

            # Get service info
            service = self.core_api.read_namespaced_service(
                name=k8s_name,
                namespace=self.namespace
            )

            # Get pod status
            pods = self.core_api.list_namespaced_pod(
                namespace=self.namespace,
                label_selector=f"app={k8s_name}"
            )

            pod_statuses = []
            for pod in pods.items:
                pod_statuses.append({
                    "name": pod.metadata.name,
                    "phase": pod.status.phase,
                    "ready": all(cs.ready for cs in pod.status.container_statuses or [])
                })

            node_port = service.spec.ports[0].node_port

            return {
                "deployment_name": k8s_name,
                "namespace": self.namespace,
                "replicas": {
                    "desired": deployment.spec.replicas,
                    "ready": deployment.status.ready_replicas or 0,
                    "available": deployment.status.available_replicas or 0
                },
                "service_url": f"http://localhost:{node_port}",
                "node_port": node_port,
                "pods": pod_statuses,
                "status": "ready" if deployment.status.ready_replicas == deployment.spec.replicas else "not_ready"
            }

        except ApiException as e:
            if e.status == 404:
                return {
                    "status": "not_deployed",
                    "message": f"Agent {agent_name} is not deployed to K8s cluster"
                }
            else:
                raise RuntimeError(f"Failed to get agent status: {str(e)}")

    def delete_agent(self, agent_name: str) -> Dict[str, Any]:
        """
        Delete an agent deployment from the cluster.

        Args:
            agent_name: Name of the agent

        Returns:
            Dict containing deletion status

        Raises:
            RuntimeError: If deletion fails
        """
        k8s_name = self._sanitize_name(agent_name)

        if self.verbose:
            logger.info(f"Deleting agent {agent_name} from K8s cluster")

        try:
            # Delete deployment
            try:
                self.apps_api.delete_namespaced_deployment(
                    name=k8s_name,
                    namespace=self.namespace
                )
                if self.verbose:
                    logger.info(f"Deleted deployment: {k8s_name}")
            except ApiException as e:
                if e.status != 404:
                    raise

            # Delete service
            try:
                self.core_api.delete_namespaced_service(
                    name=k8s_name,
                    namespace=self.namespace
                )
                if self.verbose:
                    logger.info(f"Deleted service: {k8s_name}")
            except ApiException as e:
                if e.status != 404:
                    raise

            return {
                "status": "deleted",
                "deployment_name": k8s_name,
                "namespace": self.namespace
            }

        except Exception as e:
            raise RuntimeError(f"Failed to delete agent: {str(e)}")

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