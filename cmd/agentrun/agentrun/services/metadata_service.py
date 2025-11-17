"""
Metadata service for handling agent metadata configuration.

This service provides functionality to load, validate, update, and merge
agent metadata from the agent_metadata.yaml file.
"""

import logging
from pathlib import Path
from typing import Any, Dict, Optional

import yaml
from pydantic import BaseModel, Field, validator

logger = logging.getLogger(__name__)


class AgentMetadata(BaseModel):
    """Pydantic model for agent metadata validation."""

    agent_name: str = Field(..., description="Unique name identifying the agent")
    description: Optional[str] = Field(None, description="Human-readable summary of the agent's purpose")
    language: str = Field("python", description="Programming language used")
    entrypoint: str = Field(..., description="Command to launch the agent")
    port: int = Field(8080, description="Port exposed by the agent runtime")

    build_mode: str = Field("local", description="Build strategy: local or cloud")
    region: Optional[str] = Field(None, description="Deployment region")

    version: Optional[str] = Field(None, description="Semantic version string for publishing")

    # Image information (filled during build/publish)
    image: Optional[Dict[str, Any]] = Field(None, description="Container image information")

    # Authentication information
    auth: Optional[Dict[str, Any]] = Field(None, description="Authentication configuration")

    # Build configuration
    requirements_file: Optional[str] = Field("requirements.txt", description="Python dependency file")

    # AgentCube specific fields (filled after publish)
    agent_id: Optional[str] = Field(None, description="Agent ID assigned by AgentCube")
    agent_endpoint: Optional[str] = Field(None, description="Agent endpoint URL")

    @validator('language')
    def validate_language(cls, v):
        supported_languages = ['python', 'java']
        if v.lower() not in supported_languages:
            raise ValueError(f"Language '{v}' is not supported. Supported languages: {supported_languages}")
        return v.lower()

    @validator('build_mode')
    def validate_build_mode(cls, v):
        supported_modes = ['local', 'cloud']
        if v.lower() not in supported_modes:
            raise ValueError(f"Build mode '{v}' is not supported. Supported modes: {supported_modes}")
        return v.lower()

    @validator('port')
    def validate_port(cls, v):
        if not (1 <= v <= 65535):
            raise ValueError(f"Port {v} is not in the valid range (1-65535)")
        return v


class MetadataService:
    """Service for managing agent metadata."""

    def __init__(self, verbose: bool = False) -> None:
        self.verbose = verbose
        if verbose:
            logging.basicConfig(level=logging.DEBUG)

    def load_metadata(self, workspace_path: Path) -> AgentMetadata:
        """
        Load metadata from the workspace directory.

        Args:
            workspace_path: Path to the agent workspace directory

        Returns:
            AgentMetadata: Validated metadata object

        Raises:
            FileNotFoundError: If metadata file is not found
            ValidationError: If metadata is invalid
        """
        metadata_file = workspace_path / "agent_metadata.yaml"

        if not metadata_file.exists():
            # Try alternative names
            for alt_name in ["agent.yaml", "metadata.yaml"]:
                alt_file = workspace_path / alt_name
                if alt_file.exists():
                    metadata_file = alt_file
                    break
            else:
                raise FileNotFoundError(
                    f"Metadata file not found in {workspace_path}. "
                    "Expected 'agent_metadata.yaml'"
                )

        if self.verbose:
            logger.debug(f"Loading metadata from: {metadata_file}")

        try:
            with open(metadata_file, 'r', encoding='utf-8') as f:
                metadata_dict = yaml.safe_load(f) or {}

            return AgentMetadata(**metadata_dict)

        except yaml.YAMLError as e:
            raise ValueError(f"Invalid YAML in metadata file: {e}")
        except Exception as e:
            raise ValueError(f"Error loading metadata: {e}")

    def save_metadata(self, workspace_path: Path, metadata: AgentMetadata) -> None:
        """
        Save metadata to the workspace directory.

        Args:
            workspace_path: Path to the agent workspace directory
            metadata: Metadata object to save
        """
        metadata_file = workspace_path / "agent_metadata.yaml"

        if self.verbose:
            logger.debug(f"Saving metadata to: {metadata_file}")

        metadata_dict = metadata.dict(exclude_none=True)

        with open(metadata_file, 'w', encoding='utf-8') as f:
            yaml.dump(metadata_dict, f, default_flow_style=False, indent=2)

    def update_metadata(
        self,
        workspace_path: Path,
        updates: Dict[str, Any]
    ) -> AgentMetadata:
        """
        Update metadata with new values.

        Args:
            workspace_path: Path to the agent workspace directory
            updates: Dictionary of fields to update

        Returns:
            AgentMetadata: Updated metadata object
        """
        # Load existing metadata
        metadata = self.load_metadata(workspace_path)

        # Apply updates
        metadata_dict = metadata.dict()
        metadata_dict.update(updates)

        # Validate and return new metadata
        updated_metadata = AgentMetadata(**metadata_dict)

        # Save updated metadata
        self.save_metadata(workspace_path, updated_metadata)

        if self.verbose:
            logger.debug(f"Updated metadata with: {updates}")

        return updated_metadata

    def validate_workspace(self, workspace_path: Path) -> bool:
        """
        Validate the workspace structure and files.

        Args:
            workspace_path: Path to the agent workspace directory

        Returns:
            bool: True if workspace is valid

        Raises:
            ValueError: If workspace structure is invalid
        """
        if not workspace_path.exists():
            raise ValueError(f"Workspace directory does not exist: {workspace_path}")

        if not workspace_path.is_dir():
            raise ValueError(f"Workspace path is not a directory: {workspace_path}")

        # Check for source code files based on language
        metadata = self.load_metadata(workspace_path)

        if metadata.language == "python":
            self._validate_python_workspace(workspace_path, metadata)
        elif metadata.language == "java":
            self._validate_java_workspace(workspace_path, metadata)

        if self.verbose:
            logger.debug(f"Workspace validation passed: {workspace_path}")

        return True

    def _validate_python_workspace(self, workspace_path: Path, metadata: AgentMetadata) -> None:
        """Validate Python workspace structure."""
        # Check for entrypoint file
        entrypoint_parts = metadata.entrypoint.split()
        if entrypoint_parts[0] == "python":
            entrypoint_file = workspace_path / entrypoint_parts[1] if len(entrypoint_parts) > 1 else workspace_path / "main.py"
            if not entrypoint_file.exists():
                raise ValueError(f"Entrypoint file not found: {entrypoint_file}")

        # Check for requirements file
        if metadata.requirements_file:
            requirements_file = workspace_path / metadata.requirements_file
            if not requirements_file.exists():
                raise ValueError(f"Requirements file not found: {requirements_file}")

    def _validate_java_workspace(self, workspace_path: Path, metadata: AgentMetadata) -> None:
        """Validate Java workspace structure."""
        # Check for pom.xml
        pom_file = workspace_path / "pom.xml"
        if not pom_file.exists():
            raise ValueError("Maven pom.xml file not found for Java project")

        # TODO: Add more Java-specific validation
        pass