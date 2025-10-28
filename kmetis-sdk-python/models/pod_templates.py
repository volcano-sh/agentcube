"""
Kubernetes Pod templates for Sandbox SDK with flexible parameter handling
"""
from typing import Dict, Any

from constants import DEFAULT_CPU, DEFAULT_MEM, DEFAULT_SANDBOX_IMAGE


def get_default_pod_template(params: Dict[str, Any]) -> Dict[str, Any]:
    """
    Get the default Pod template configuration using a flexible parameter dictionary

    Args:
        params: Dictionary containing all required parameters:
            - sandbox_id: Unique sandbox ID
            - image: Container image
            - cpu: CPU resources
            - memory: Memory resources
            - public_key: SSH public key (optional)

    Returns:
        Dict with Pod specification
    """
    sandbox_id = params["sandbox_id"]
    # Required parameters with defaults if not provided
    image = params.get("image", DEFAULT_SANDBOX_IMAGE)
    cpu = params.get("cpu", DEFAULT_CPU)
    memory = params.get("memory", DEFAULT_MEM)
    extra_volumes = params.get("volumes", [])
    extra_volume_mounts = params.get("volume_mounts", [])
    use_local_image = params.get("use_local_image", False)

    return {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": sandbox_id,
            "labels": {
                "app": "k8s-sandbox",
                "sandbox-id": sandbox_id
            }
        },
        "spec": {
            "containers": [{
                "name": "sandbox",
                "image": image,
                "imagePullPolicy": "Never" if use_local_image else "IfNotPresent",
                "env": [{
                    "name": "SSH_ENABLE_ROOT",
                    "value": "true"
                }],
                "ports": [{
                    "containerPort": 22
                }],
                "resources": {
                    "requests": {"cpu": cpu, "memory": memory},
                    "limits": {"cpu": cpu, "memory": memory}
                },
                "volumeMounts": [
                    {
                        "name": "ssh-keys-volume",
                        "mountPath": "/root/.ssh/authorized_keys",
                        "subPath": "authorized_keys",
                        "readOnly": True
                    },
                    *extra_volume_mounts
                ]
            }],
            "volumes": [
                {
                    "name": "ssh-keys-volume",
                    "configMap": {
                        "name": "ssh-authorized-keys",
                        "items": [{
                            "key": "root",
                            "path": "authorized_keys"
                        }]
                    }
                },
                *extra_volumes
            ],
            "restartPolicy": "Never"
        }
    }


def get_custom_pod_template(params: Dict[str, Any]) -> Dict[str, Any]:
    """
    Example of another template with different configuration

    Args:
        params: Dictionary containing all template parameters

    Returns:
        Custom Pod specification
    """
    # Implement custom template logic here
    base_template = get_default_pod_template(params)

    # Example modification - add additional volumes
    if "extra_volumes" in params:
        base_template["spec"]["volumes"].extend(params["extra_volumes"])

    return base_template