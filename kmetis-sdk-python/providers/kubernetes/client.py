from typing import Optional, Dict, Any

from kubernetes import client, config
from kubernetes.client.rest import ApiException

from services.log import get_logger


class KubernetesClient:
    """
    Kubernetes API client wrapper with automatic config loading
    """

    def __init__(self, namespace: str = "default"):
        self.namespace = namespace
        self._logger = get_logger(f"{__name__}.KubernetesClient")
        self._configure_client()

    def _configure_client(self):
        try:
            config.load_incluster_config()
            self._logger.info("Using in-cluster Kubernetes config")
        except config.ConfigException:
            config.load_kube_config()
            self._logger.info("Using local Kubernetes config")

        self.core_v1 = client.CoreV1Api()

    def get_pod(self, name: str, namespace: str = "default") -> Optional[client.V1Pod]:
        try:
            return self.core_v1.read_namespaced_pod(name, namespace)
        except ApiException as e:
            if e.status == 404:
                return None
            raise

    def create_pod(self, namespace: str, pod_spec: Dict[str, Any]) -> client.V1Pod:
        try:
            return self.core_v1.create_namespaced_pod(namespace, body=pod_spec)
        except ApiException as exception:
            raise exception

    def delete_pod(self, name: str, namespace: str) -> bool:
        try:
            self.core_v1.delete_namespaced_pod(
                name=name,
                namespace=namespace,
                body=client.V1DeleteOptions()
            )
            return True
        except ApiException as exception:
            if exception.status == 404:
                return False
            raise exception

    def create_configmap_from_file(
            self,
            cm_name: str,
            cm_key: str,
            cm_value: str,
            labels: Optional[Dict[str, str]] = None
    ) -> str:
        """
        Create ConfigMap from local file

        Args:
            cm_name: ConfigMap name
            cm_key: configmap key
            cm_value: Configmap value
            labels: Optional labels for ConfigMap

        Returns:
            Created ConfigMap name

        Raises:
            ConfigMapCreationError: If creation fails
        """
        try:
            metadata = {
                "name": cm_name,
                "labels": labels or {}
            }

            body = client.V1ConfigMap(
                metadata=metadata,
                data={cm_key: cm_value}
            )

            self.core_v1.create_namespaced_config_map(
                namespace=self.namespace,
                body=body
            )
            return cm_name

        except Exception as e:
            raise e
