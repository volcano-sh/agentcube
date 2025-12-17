"""
This module defines the data models used in the pack runtime.
"""

from dataclasses import dataclass
from typing import Optional, Dict, Any

@dataclass
class MetadataOptions:
    """Dataclass for holding metadata options."""
    agent_name: Optional[str] = None
    language: Optional[str] = 'python'
    entrypoint: Optional[str] = None
    port: Optional[int] = 8080
    build_mode: Optional[str] = 'local'
    requirements_file: Optional[str] = None
    description: Optional[str] = None
    workload_manager_url: Optional[str] = ""
    router_url: Optional[str] = ""
    readiness_probe_path: Optional[str] = ""
    readiness_probe_port: Optional[int] = 8080
    registry_url: Optional[str] = ""
    registry_username: Optional[str] = ""
    registry_password: Optional[str] = ""
    agent_endpoint: Optional[str] = ""

    @classmethod
    def from_options(cls, options: Dict[str, Any]) -> "MetadataOptions":
        """Create an instance from a dictionary of options."""
        return cls(
            agent_name=options.get('agent_name'),
            language=options.get('language'),
            entrypoint=options.get('entrypoint'),
            port=options.get('port'),
            build_mode=options.get('build_mode'),
            description=options.get('description'),
            registry_url=options.get('registry_url'),
            registry_username=options.get('registry_username'),
            registry_password=options.get('registry_password'),
        )
