"""
Configuration module for Kubernetes Sandbox SDK
"""
import os
from typing import Optional
from dotenv import load_dotenv


# Load environment variables from .env file
load_dotenv()


def get_k8s_namespace() -> str:
    """
    Get Kubernetes namespace from environment or return default
    
    Returns:
        Kubernetes namespace
    """
    return os.getenv("K8S_NAMESPACE", "default")


def get_ssh_config() -> dict:
    """
    Get SSH configuration from environment variables
    
    Returns:
        Dictionary with SSH configuration
    """
    return {
        "username": os.getenv("SSH_USERNAME", "root"),
        "port": int(os.getenv("SSH_PORT", "22")),
        "timeout": int(os.getenv("SSH_TIMEOUT", "30"))
    }