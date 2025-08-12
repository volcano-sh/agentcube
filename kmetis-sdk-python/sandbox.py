"""
Kubernetes Sandbox SDK for Python
"""
import logging
import threading
import time
from datetime import datetime
from typing import Dict, Optional, Any
from kubernetes import client, config
from kubernetes.client.rest import ApiException

from exceptions import (
    SandboxNotFoundError, 
    SandboxCreationError, 
    SandboxDeletionError,
    CommandExecutionError,
    FileTransferError
)
from ssh_manager import SSHManager


class Sandbox:
    """
    SDK for managing Kubernetes sandboxes (Pods)
    """
    
    def __init__(self, namespace: str = "default"):
        """
        Initialize the Sandbox
        
        Args:
            namespace: Kubernetes namespace to operate in
        """
        self.namespace = namespace
        self.ssh_manager = SSHManager()
        self._sandbox_cache: Dict[str, Dict[str, Any]] = {}
        self._cache_lock = threading.Lock()
        self.logger = logging.getLogger(__name__)
        
        # Configure Kubernetes client
        try:
            # Try to load in-cluster config first
            config.load_incluster_config()
            self.logger.info("Loaded in-cluster Kubernetes configuration")
        except config.ConfigException:
            try:
                # Fall back to kubeconfig
                config.load_kube_config()
                self.logger.info("Loaded kubeconfig")
            except Exception as e:
                self.logger.error(f"Failed to load Kubernetes configuration: {e}")
                raise
        
        self.v1 = client.CoreV1Api()
    
    def create_sandbox(
        self, 
        public_key: str, 
        image: str = "panubo/sshd:latest",
        cpu: str = "1",
        memory: str = "1Gi",
        name_prefix: str = "sandbox"
    ) -> Dict[str, Any]:
        """
        Create a new sandbox (Pod) in Kubernetes
        
        Args:
            public_key: SSH public key to configure in the sandbox
            image: Container image to use (default: ubuntu:20.04)
            cpu: CPU resource limit (default: 1)
            memory: Memory resource limit (default: 1Gi)
            name_prefix: Prefix for the sandbox name (default: sandbox)
            
        Returns:
            Dict containing sandbox_id, ip_address, and created_at
            
        Raises:
            SandboxCreationError: If sandbox creation fails
        """
        # Generate unique sandbox ID
        sandbox_id = f"{name_prefix}-{int(time.time())}"
        
        # Create Pod specification
        pod_spec = {
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
                    "env": [{
                        "name": "SSH_ENABLE_ROOT",
                        "value": "true"
                    }],
                    "ports": [{
                        "containerPort": 22
                    }],
                    "resources": {
                        "requests": {
                            "cpu": cpu,
                            "memory": memory
                        },
                        "limits": {
                            "cpu": cpu,
                            "memory": memory
                        }
                    },
                    "volumeMounts": [{
                        "name": "ssh-keys-volume",
                        "mountPath": "/root/.ssh/authorized_keys",
                        "subPath": "authorized_keys",  # Mount public key as authorized_keys
                        "readOnly": True
                    }]
                }],
                "volumes": [{
                    "name": "ssh-keys-volume",
                    "configMap": {
                        "name": "ssh-authorized-keys",
                        "items": [{
                            "key": "root",
                            "path": "authorized_keys"
                        }]
                    }
                }],
                "restartPolicy": "Never"
            }
        }
        
        try:
            # Create the Pod
            pod = self.v1.create_namespaced_pod(namespace=self.namespace, body=pod_spec)
            self.logger.info(f"Created sandbox Pod {sandbox_id}")
            
            # Wait for Pod to be running
            ip_address = self._wait_for_pod_running(sandbox_id)
            
            # Cache sandbox information including port
            sandbox_info = {
                "sandbox_id": sandbox_id,
                "ip_address": ip_address,
                "port": "22",  # Default SSH port for the container
                "created_at": datetime.utcnow().isoformat()
            }
            
            with self._cache_lock:
                self._sandbox_cache[sandbox_id] = sandbox_info
            
            self.logger.info(f"Sandbox {sandbox_id} is ready with IP {ip_address}")
            return sandbox_info
            
        except Exception as e:
            self.logger.error(f"Failed to create sandbox {sandbox_id}: {e}")
            raise SandboxCreationError(f"Failed to create sandbox: {str(e)}")
    
    def _wait_for_pod_running(self, sandbox_id: str, timeout: int = 120) -> str:
        """
        Wait for a Pod to enter Running state and return its IP address
        
        Args:
            sandbox_id: ID of the sandbox to wait for
            timeout: Timeout in seconds (default: 120)
            
        Returns:
            IP address of the Pod
            
        Raises:
            SandboxCreationError: If Pod doesn't become ready in time
        """
        start_time = time.time()
        
        while time.time() - start_time < timeout:
            try:
                pod = self.v1.read_namespaced_pod(name=sandbox_id, namespace=self.namespace)
                
                if pod.status.phase == "Running":
                    if pod.status.pod_ip:
                        return pod.status.pod_ip
                    else:
                        raise SandboxCreationError(f"Pod {sandbox_id} is running but has no IP address")
                
                if pod.status.phase in ["Failed", "Unknown"]:
                    raise SandboxCreationError(f"Pod {sandbox_id} entered {pod.status.phase} state")
                
                time.sleep(2)
                
            except ApiException as e:
                raise SandboxCreationError(f"Failed to check Pod status: {e}")
        
        raise SandboxCreationError(f"Pod {sandbox_id} did not become ready within {timeout} seconds")
    
    def delete_sandbox(self, sandbox_id: str) -> bool:
        """
        Delete a sandbox (Pod) from Kubernetes
        
        Args:
            sandbox_id: ID of the sandbox to delete
            
        Returns:
            True if deletion was successful, False otherwise
            
        Raises:
            SandboxDeletionError: If deletion fails
        """
        try:
            # Close SSH session if exists
            with self._cache_lock:
                if sandbox_id in self._sandbox_cache:
                    ip_address = self._sandbox_cache[sandbox_id].get("ip_address")
                    if ip_address:
                        self.ssh_manager.close_session(ip_address, "root")
                    del self._sandbox_cache[sandbox_id]
            
            # Delete the Pod
            self.v1.delete_namespaced_pod(
                name=sandbox_id,
                namespace=self.namespace,
                body=client.V1DeleteOptions()
            )
            
            self.logger.info(f"Deleted sandbox {sandbox_id}")
            return True
            
        except ApiException as e:
            if e.status == 404:
                self.logger.warning(f"Sandbox {sandbox_id} not found")
                return False
            else:
                self.logger.error(f"Failed to delete sandbox {sandbox_id}: {e}")
                raise SandboxDeletionError(f"Failed to delete sandbox: {str(e)}")
        except Exception as e:
            self.logger.error(f"Unexpected error deleting sandbox {sandbox_id}: {e}")
            raise SandboxDeletionError(f"Failed to delete sandbox: {str(e)}")
    
    def execute_command(
        self, 
        sandbox_id: str, 
        private_key: str, 
        command: str, 
        timeout: int = 30
    ) -> Dict[str, Any]:
        """
        Execute a command in a sandbox via SSH
        
        Args:
            sandbox_id: ID of the sandbox
            private_key: SSH private key for authentication
            command: Command to execute
            timeout: Execution timeout in seconds (default: 30)
            
        Returns:
            Dict containing stdout, stderr, and return_code
            
        Raises:
            SandboxNotFoundError: If sandbox doesn't exist
            CommandExecutionError: If command execution fails
        """
        # Get sandbox IP from cache or refresh
        ip_address = self._get_sandbox_ip(sandbox_id)
        
        try:
            # Get port from cache
            port = self._get_sandbox_port(sandbox_id)
            
            # Get SSH session
            ssh_client = self.ssh_manager.get_session(ip_address, port, "root", private_key)
            
            # Execute command with timeout
            stdin, stdout, stderr = ssh_client.exec_command(command, timeout=timeout)
            
            # Get results
            output = stdout.read().decode('utf-8')
            error = stderr.read().decode('utf-8')
            return_code = stdout.channel.recv_exit_status()
            
            return {
                "stdout": output,
                "stderr": error,
                "return_code": return_code
            }
            
        except Exception as e:
            self.logger.error(f"Failed to execute command in sandbox {sandbox_id}: {e}")
            raise CommandExecutionError(f"Command execution failed: {str(e)}")
    
    def upload_file(
        self,
        sandbox_id: str,
        private_key: str,
        local_path: str,
        remote_path: str,
        timeout: int = 60
    ) -> bool:
        """
        Upload a file to a sandbox via SSH
        
        Args:
            sandbox_id: ID of the sandbox
            private_key: SSH private key for authentication
            local_path: Local file path
            remote_path: Remote file path
            timeout: Transfer timeout in seconds (default: 60)
            
        Returns:
            True if upload was successful, False otherwise
            
        Raises:
            SandboxNotFoundError: If sandbox doesn't exist
            FileTransferError: If file transfer fails
        """
        # Get sandbox IP from cache or refresh
        ip_address = self._get_sandbox_ip(sandbox_id)
        
        try:
            # Get port from cache
            port = self._get_sandbox_port(sandbox_id)
            
            # Get SSH session
            ssh_client = self.ssh_manager.get_session(ip_address, port, "root", private_key)
            
            # Create SFTP client
            sftp = ssh_client.open_sftp()
            
            # Upload file
            sftp.put(local_path, remote_path)
            sftp.close()
            
            self.logger.info(f"Uploaded {local_path} to {sandbox_id}:{remote_path}")
            return True
            
        except Exception as e:
            self.logger.error(f"Failed to upload file to sandbox {sandbox_id}: {e}")
            raise FileTransferError(f"File upload failed: {str(e)}")
    
    def download_file(
        self,
        sandbox_id: str,
        private_key: str,
        remote_path: str,
        local_path: str,
        timeout: int = 60
    ) -> bool:
        """
        Download a file from a sandbox via SSH
        
        Args:
            sandbox_id: ID of the sandbox
            private_key: SSH private key for authentication
            remote_path: Remote file path
            local_path: Local file path
            timeout: Transfer timeout in seconds (default: 60)
            
        Returns:
            True if download was successful, False otherwise
            
        Raises:
            SandboxNotFoundError: If sandbox doesn't exist
            FileTransferError: If file transfer fails
        """
        # Get sandbox IP from cache or refresh
        ip_address = self._get_sandbox_ip(sandbox_id)
        
        try:
            # Get port from cache
            port = self._get_sandbox_port(sandbox_id)
            
            # Get SSH session
            ssh_client = self.ssh_manager.get_session(ip_address, port, "root", private_key)
            
            # Create SFTP client
            sftp = ssh_client.open_sftp()
            
            # Download file
            sftp.get(remote_path, local_path)
            sftp.close()
            
            self.logger.info(f"Downloaded {sandbox_id}:{remote_path} to {local_path}")
            return True
            
        except Exception as e:
            self.logger.error(f"Failed to download file from sandbox {sandbox_id}: {e}")
            raise FileTransferError(f"File download failed: {str(e)}")
    
    def shutdown_sandbox(self, sandbox_id: str) -> bool:
        """
        Shutdown and delete a sandbox (alias for delete_sandbox)
        
        Args:
            sandbox_id: ID of the sandbox to shutdown
            
        Returns:
            True if shutdown was successful, False otherwise
        """
        return self.delete_sandbox(sandbox_id)
    
    def update_sandbox_ip(self, sandbox_id: str, new_ip: str) -> bool:
        """
        Update the IP address of a sandbox in the cache (for testing purposes)
        
        Args:
            sandbox_id: ID of the sandbox
            new_ip: New IP address to set
            
        Returns:
            True if update was successful, False if sandbox not found in cache
        """
        with self._cache_lock:
            if sandbox_id in self._sandbox_cache:
                self._sandbox_cache[sandbox_id]["ip_address"] = new_ip
                self.logger.info(f"Updated IP address for sandbox {sandbox_id} to {new_ip}")
                return True
            else:
                self.logger.warning(f"Sandbox {sandbox_id} not found in cache")
                return False
    
    def update_sandbox_port(self, sandbox_id: str, new_port: str) -> bool:
        """
        Update the SSH port of a sandbox in the cache (for testing purposes)
        
        Args:
            sandbox_id: ID of the sandbox
            new_port: New SSH port to set
            
        Returns:
            True if update was successful, False if sandbox not found in cache
        """
        with self._cache_lock:
            if sandbox_id in self._sandbox_cache:
                self._sandbox_cache[sandbox_id]["port"] = new_port
                self.logger.info(f"Updated SSH port for sandbox {sandbox_id} to {new_port}")
                return True
            else:
                self.logger.warning(f"Sandbox {sandbox_id} not found in cache")
                return False
    
    def add_sandbox_to_cache(self, sandbox_id: str, ip_address: str, port: str = "22", created_at: Optional[str] = None) -> bool:
        """
        Add or update sandbox information in the cache (for testing purposes)
        
        Args:
            sandbox_id: ID of the sandbox
            ip_address: IP address of the sandbox
            port: SSH port of the sandbox (default: "22")
            created_at: ISO format timestamp (optional, defaults to current time)
            
        Returns:
            True if addition was successful
        """
        if created_at is None:
            created_at = datetime.utcnow().isoformat()
            
        sandbox_info = {
            "sandbox_id": sandbox_id,
            "ip_address": ip_address,
            "port": port,
            "created_at": created_at
        }
        
        with self._cache_lock:
            self._sandbox_cache[sandbox_id] = sandbox_info
            
        self.logger.info(f"Added sandbox {sandbox_id} to cache with IP {ip_address}")
        return True
    
    def get_sandbox_info(self, sandbox_id: str) -> Dict[str, Any]:
        """
        Get information about a sandbox
        
        Args:
            sandbox_id: ID of the sandbox
            
        Returns:
            Dict containing sandbox information
            
        Raises:
            SandboxNotFoundError: If sandbox doesn't exist
        """
        try:
            # Get current Pod status
            pod = self.v1.read_namespaced_pod(name=sandbox_id, namespace=self.namespace)
            
            # Calculate running time
            created_at = pod.metadata.creation_timestamp
            running_time = None
            if created_at:
                running_time = (datetime.utcnow() - created_at.replace(tzinfo=None)).total_seconds()
            
            # Get IP address from cache or Pod status
            ip_address = None
            with self._cache_lock:
                if sandbox_id in self._sandbox_cache:
                    ip_address = self._sandbox_cache[sandbox_id].get("ip_address")
            
            if not ip_address and pod.status.pod_ip:
                ip_address = pod.status.pod_ip
            
            return {
                "sandbox_id": sandbox_id,
                "ip_address": ip_address,
                "status": pod.status.phase,
                "created_at": created_at.isoformat() if created_at else None,
                "running_time": running_time
            }
            
        except ApiException as e:
            if e.status == 404:
                raise SandboxNotFoundError(f"Sandbox {sandbox_id} not found")
            else:
                self.logger.error(f"Failed to get sandbox info for {sandbox_id}: {e}")
                raise
        except Exception as e:
            self.logger.error(f"Unexpected error getting sandbox info for {sandbox_id}: {e}")
            raise
    
    def _get_sandbox_ip(self, sandbox_id: str) -> str:
        """
        Get sandbox IP address, refreshing cache if necessary
        
        Args:
            sandbox_id: ID of the sandbox
            
        Returns:
            IP address of the sandbox
            
        Raises:
            SandboxNotFoundError: If sandbox doesn't exist
        """
        # Check cache first
        with self._cache_lock:
            if sandbox_id in self._sandbox_cache:
                return self._sandbox_cache[sandbox_id]["ip_address"]
        
        # Refresh from Kubernetes API
        try:
            pod = self.v1.read_namespaced_pod(name=sandbox_id, namespace=self.namespace)
            if pod.status.pod_ip:
                # Update cache
                with self._cache_lock:
                    if sandbox_id not in self._sandbox_cache:
                        self._sandbox_cache[sandbox_id] = {}
                    self._sandbox_cache[sandbox_id]["ip_address"] = pod.status.pod_ip
                
                return pod.status.pod_ip
            else:
                raise SandboxNotFoundError(f"Sandbox {sandbox_id} has no IP address")
                
        except ApiException as e:
            if e.status == 404:
                raise SandboxNotFoundError(f"Sandbox {sandbox_id} not found")
            else:
                raise
    
    def _get_sandbox_port(self, sandbox_id: str) -> str:
        """
        Get sandbox SSH port, refreshing cache if necessary
        
        Args:
            sandbox_id: ID of the sandbox
            
        Returns:
            SSH port of the sandbox
            
        Raises:
            SandboxNotFoundError: If sandbox doesn't exist
        """
        # Check cache first
        with self._cache_lock:
            if sandbox_id in self._sandbox_cache:
                return self._sandbox_cache[sandbox_id].get("port", "22")
        
        # Refresh from Kubernetes API
        try:
            pod = self.v1.read_namespaced_pod(name=sandbox_id, namespace=self.namespace)
            # For now, we'll default to port 22 since that's what the container uses
            # In a more advanced implementation, we might extract this from service definitions
            port = "22"
            
            # Update cache
            with self._cache_lock:
                if sandbox_id not in self._sandbox_cache:
                    self._sandbox_cache[sandbox_id] = {}
                self._sandbox_cache[sandbox_id]["port"] = port
            
            return port
                
        except ApiException as e:
            if e.status == 404:
                raise SandboxNotFoundError(f"Sandbox {sandbox_id} not found")
            else:
                raise