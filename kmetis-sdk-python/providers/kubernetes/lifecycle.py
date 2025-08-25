import os
import time
from typing import Optional, Dict, Any

from kubernetes.client import V1Pod
from kubernetes.client.rest import ApiException

from constants import POD_START_TIMEOUT, POD_CHECK_INTERVAL, DEFAULT_NAMESPACE, DEFAULT_SSH_PORT, DEFAULT_SSH_USER
from models.pod_templates import get_default_pod_template
from models.sandbox_info import PodState, SandboxInstance
from services.exceptions import ResourceError, ProviderError, OperationTimeoutError
from services.log import get_logger
from services.resource_tracker import ResourceTracker


def _get_pod_failure_details(pod: V1Pod) -> Dict:
    """Extract failure details from pod status."""
    details = {"phase": pod.status.phase}

    if pod.status.container_statuses:
        container_state = pod.status.container_statuses[0].state
        if container_state.terminated:
            details.update({
                "exit_code": container_state.terminated.exit_code,
                "reason": container_state.terminated.reason,
                "message": container_state.terminated.message
            })

    return details


def _create_metadata(pod: V1Pod) -> Dict:
    """Create metadata dictionary from pod info."""
    return {
        "namespace": pod.metadata.namespace,
        "pod_name": pod.metadata.name,
        "creation_timestamp": str(pod.metadata.creation_timestamp),
        "labels": pod.metadata.labels,
        "conditions": [
            {
                "type": c.type,
                "status": c.status,
                "reason": getattr(c, 'reason', None)
            }
            for c in pod.status.conditions or []
        ]
    }


class LifecycleManager:
    def __init__(self, k8s_client):
        self._logger = get_logger(f"{__name__}.LifecycleManager")
        self.k8s = k8s_client
        self._tracker = ResourceTracker()

    def create(self, sandbox_id: str = None, namespace: str = "default",
               sandbox_config: Optional[Dict[str, Any]] = None) -> SandboxInstance:
        # Generate unique sandbox ID
        self._logger.info(f"Create sandbox: id({sandbox_id})")
        if sandbox_id is None:
            sandbox_id = f"sandbox-{int(time.time())}"
        sandbox_config = sandbox_config if sandbox_config else {}
        sandbox_config["sandbox_id"] = sandbox_id

        pod, exists = self._check_existing_pod(sandbox_id, namespace)
        if exists and pod is not None:
            self._logger.info(f"Sandbox ({sandbox_id}) already exists")
            if not self._tracker.get_resources(sandbox_id):
                self._tracker.track(sandbox_id, "pod", pod.metadata.name)
            return SandboxInstance(
                id=sandbox_id,
                ip_address=pod.status.pod_ip,
                port = DEFAULT_SSH_PORT,
                username = sandbox_config.get("username", "root"),
                status=pod.status.phase,
                metadata=_create_metadata(pod)
            )

        # 处理configmap的创建
        self._logger.info(f"Create sandbox configmap {sandbox_id} if necessary")
        sandbox_config = self._create_config_map(sandbox_config)

        # 利用最新的sandbox_config生成pod_spec
        pod_spec = get_default_pod_template(sandbox_config)
        try:
            # Create pod
            pod = self._create_pod(namespace, pod_spec)
            self._tracker.track(sandbox_id, "pod", pod.metadata.name)

            # Wait for pod to be in the running state
            ready = self._wait_for_pod_ready(
                pod.metadata.name,
                namespace,
                timeout=POD_START_TIMEOUT
            )
            if not ready:
                raise OperationTimeoutError(f"Pod {pod.metadata.name} failed to start within timeout")

            pod = self.k8s.get_pod(pod.metadata.name, namespace)

            # Cache sandbox information including port
            return SandboxInstance(
                id=sandbox_id,
                ip_address=pod.status.pod_ip,
                port = DEFAULT_SSH_PORT,
                username = sandbox_config.get("username", "root"),
                status=pod.status.phase,
                metadata=_create_metadata(pod)
            )
        except ApiException as api_error:
            self._cleanup_resources(sandbox_id, namespace)
            raise ProviderError(
                f"Kubernetes API error: {api_error.reason}",
                {"status": api_error.status}
            ) from api_error

        except Exception as error:
            self._cleanup_resources(sandbox_id, namespace)
            raise ProviderError("Failed to create sandbox") from error

    def _create_pod(self, namespace: str, spec: V1Pod) -> V1Pod:
        """Create pod and handle API response."""
        try:
            pod = self.k8s.create_pod(namespace, spec)

            if self._logger:
                self._logger.info(
                    "Pod created",
                    {
                        "name": pod.metadata.name,
                        "namespace": namespace
                    }
                )

            return pod
        except ApiException as e:
            raise ProviderError(
                f"Failed to create pod: {e.reason}",
                {"namespace": namespace}
            ) from e

    def _wait_for_pod_ready(
            self,
            pod_name: str,
            namespace: str,
            timeout: int
    ) -> bool:
        """Wait for pod to reach running state with timeout."""
        start_time = time.time()

        while time.time() - start_time < timeout:
            pod = self.k8s.get_pod(pod_name, namespace)

            if pod.status.phase == PodState.RUNNING.value:
                if self._logger:
                    self._logger.info(
                        "Pod is running",
                        {
                            "name": pod_name,
                            "namespace": namespace,
                            "ip": pod.status.pod_ip
                        }
                    )
                return True

            if pod.status.phase == PodState.FAILED.value:
                raise ProviderError(
                    f"Pod {pod_name} failed to start",
                    _get_pod_failure_details(pod=pod)
                )

            time.sleep(POD_CHECK_INTERVAL)

        return False

    def delete(self, sandbox: SandboxInstance) -> bool:
        """Delete a sandbox pod"""
        try:
            namespace = sandbox.metadata.get("namespace", DEFAULT_NAMESPACE)
            resources = self._tracker.get_resources(sandbox.id)
            if not resources:
                self._logger.info(
                    "No resources to delete",
                    {"sandbox_id": sandbox.id}
                )
                return False

            # Delete main pod
            if "pod" in resources:
                self.k8s.delete_pod(resources["pod"], namespace)

            # Delete additional resources (configmaps, services, etc.)
            self._delete_associated_resources(sandbox.id, namespace)

            # Release from tracker
            self._tracker.release(sandbox.id)

            if self._logger:
                self._logger.info(
                    "Pod deleted",
                    {
                        "name": sandbox.id,
                        "namespace": namespace
                    }
                )

            return True

        except ApiException as api_error:
            if api_error.status != 404:  # Not found is acceptable for deletion
                raise ProviderError(
                    f"Failed to delete resources: {api_error.reason}",
                    {"sandbox_id": sandbox.id}
                ) from api_error
            return False

    def _create_config_map(self, sandbox_config: Dict[str, Any]) -> Dict[str, Any]:
        # 处理ConfigMap创建和文件Mount
        if "configmap_items" in sandbox_config:
            cm_items = sandbox_config.get("configmap_items")
            volumes = sandbox_config.get("volumes", [])
            volume_mounts = sandbox_config.get("volume_mounts", [])

            for cm_item in cm_items:
                cm_name = cm_item.get("name", None)
                cm_key = cm_item.get("cm_key", None)
                cm_value = cm_item.get("cm_value", None)
                cm_value_file_path = cm_item.get("cm_value_file_path", None)
                if cm_name is None or cm_key is None:
                    raise ResourceError(
                        f"Configmap cannot be created for: {cm_item} as cm_name or cm_key is None")
                if cm_value is None and cm_value_file_path is not None:
                    if not os.path.exists(cm_value_file_path):
                        raise ResourceError(f"File not found: {cm_value_file_path}")
                    with open(cm_value_file_path, 'r') as f:
                        cm_value = f.read()
                try:
                    # 创建ConfigMap
                    self.k8s.create_configmap(
                        cm_name=cm_name,
                        cm_key=cm_key,
                        cm_value=cm_value
                    )
                except Exception as e:
                    self._logger.error(f"Failed to create ConfigMap: {e}")
                    raise ResourceError(f"ConfigMap creation failed: {str(e)}")

                cm_key_path = cm_item.get("cm_key_path", None)
                cm_mount_path = cm_item.get("cm_mount_path", None)
                cm_mount_sub_path = cm_item.get("cm_mount_sub_path", None)
                # 添加自动生成的挂载配置
                volumes.append({
                    "name": f"{cm_name}-vol",
                    "configMap": {
                        "name": cm_name,
                        "items": {
                            "key": cm_key,
                            "path": cm_key_path if cm_key_path else cm_key
                        }
                    }
                })
                mount_path = cm_mount_path if cm_mount_path else cm_value_file_path
                sub_path = cm_mount_sub_path if cm_mount_sub_path else os.path.basename(mount_path)
                volume_mounts.append({
                    "name": f"{cm_name}-vol",
                    "mountPath": f"{mount_path}",
                    "subPath": f"{sub_path}",
                    "readOnly": True
                })


            # 更新到sandbox_config中
            sandbox_config["volumes"] = volumes
            sandbox_config["volume_mounts"] = volume_mounts

        return sandbox_config

    def _check_existing_pod(self, sandbox_id: str, namespace: str = "default") -> (Optional[V1Pod], bool):
        pod = self.k8s.get_pod(name=sandbox_id, namespace=namespace)
        if pod is None:
            return None, False
        self._logger.debug(f"Waiting for pod {sandbox_id} to reach RunningState: {pod.status.phase}")
        if pod.status.phase == PodState.RUNNING.value:
            return pod, True

        if pod.status.phase in [state.value for state in [PodState.FAILED, PodState.UNKNOWN]]:
            raise ResourceError(f"Pod {sandbox_id} failed with status {pod.status.phase} already exists")

        return None, False

    def _delete_associated_resources(self, sandbox_id: str, namespace: str) -> None:
        """Delete resources associated with this sandbox."""
        resources = self._tracker.get_resources(sandbox_id)

        # Delete config maps
        if "configmaps" in resources:
            for cm_name in resources["configmaps"]:
                try:
                    self.k8s.delete_configmap(cm_name, namespace)
                except ApiException as e:
                    if e.status != 404:
                        raise

        # Delete services
        if "services" in resources:
            for svc_name in resources["services"]:
                try:
                    self.k8s.delete_service(svc_name, namespace)
                except ApiException as e:
                    if e.status != 404:
                        raise

    def _cleanup_resources(self, sandbox_id: str, namespace: str) -> None:
        """Cleanup resources on failure."""
        if self._logger:
            self._logger.info(
                "Cleaning up resources",
                {"sandbox_id": sandbox_id}
            )

        try:
            self.delete(SandboxInstance(
                id=sandbox_id,
                ip_address="",
                port=DEFAULT_SSH_PORT,
                username=DEFAULT_SSH_USER,
                status="STOPPED",
                metadata={"namespace": namespace}
            ))
        except Exception as cleanup_error:
            if self._logger:
                self._logger.error(
                    "Cleanup failed",
                    {
                        "sandbox_id": sandbox_id,
                        "error": str(cleanup_error)
                    }
                )
