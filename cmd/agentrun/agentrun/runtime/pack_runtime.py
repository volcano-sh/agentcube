"""
Pack runtime for AgentRun.

This module implements the pack command functionality, handling
the packaging of agent applications into standardized workspaces.
"""

import logging
import shutil
from pathlib import Path
from typing import Any, Dict, Optional

from agentrun.services.metadata_service import AgentMetadata, MetadataService

logger = logging.getLogger(__name__)


class PackRuntime:
    """Runtime for the pack command."""

    def __init__(self, verbose: bool = False) -> None:
        self.verbose = verbose
        self.metadata_service = MetadataService(verbose=verbose)

        if verbose:
            logging.basicConfig(level=logging.DEBUG)

    def pack(
        self,
        workspace_path: Path,
        **options: Any
    ) -> Dict[str, Any]:
        """
        Package an agent application into a standardized workspace.

        Args:
            workspace_path: Path to the agent workspace directory
            **options: Additional options (agent_name, language, entrypoint, etc.)

        Returns:
            Dict containing packaging results

        Raises:
            ValueError: If packaging fails
            FileNotFoundError: If required files are missing
        """
        if self.verbose:
            logger.info(f"Starting pack process for workspace: {workspace_path}")

        # Step 1: Validate workspace structure
        self._validate_workspace_structure(workspace_path)

        # Step 2: Load or create metadata
        metadata = self._load_or_create_metadata(workspace_path, options)

        # Step 3: Apply CLI options overrides
        metadata = self._apply_option_overrides(metadata, options)

        # Step 4: Validate language compatibility
        self._validate_language_compatibility(workspace_path, metadata)

        # Step 5: Process dependencies
        self._process_dependencies(workspace_path, metadata)

        # Step 6: Generate Dockerfile if needed
        dockerfile_path = self._generate_dockerfile(workspace_path, metadata)

        # Step 7: Update metadata
        self._update_pack_metadata(workspace_path, metadata, dockerfile_path)

        # Step 8: Prepare output path if specified
        final_workspace = self._prepare_output_path(workspace_path, options.get('output'))

        result = {
            "agent_name": metadata.agent_name,
            "workspace_path": str(final_workspace),
            "metadata_path": str(final_workspace / "agent_metadata.yaml"),
            "language": metadata.language,
            "build_mode": metadata.build_mode,
            "dockerfile_path": str(dockerfile_path) if dockerfile_path else None,
        }

        if self.verbose:
            logger.info(f"Pack completed successfully: {result}")

        return result

    def _validate_workspace_structure(self, workspace_path: Path) -> None:
        """Validate the basic workspace structure."""
        if not workspace_path.exists():
            raise ValueError(f"Workspace directory does not exist: {workspace_path}")

        if not workspace_path.is_dir():
            raise ValueError(f"Workspace path is not a directory: {workspace_path}")

        if self.verbose:
            logger.debug(f"Workspace structure validation passed: {workspace_path}")

    def _load_or_create_metadata(self, workspace_path: Path, options: Dict[str, Any]) -> AgentMetadata:
        """Load existing metadata or create new one from options."""
        try:
            # Try to load existing metadata
            metadata = self.metadata_service.load_metadata(workspace_path)
            if self.verbose:
                logger.debug("Loaded existing metadata")
        except FileNotFoundError:
            # Create new metadata from options or defaults
            if self.verbose:
                logger.debug("Creating new metadata")

            # Extract required fields from options or infer from workspace
            agent_name = options.get('agent_name')
            if not agent_name:
                agent_name = workspace_path.name

            language = options.get('language', 'python')
            entrypoint = options.get('entrypoint', self._infer_entrypoint(workspace_path, language))

            metadata_dict = {
                "agent_name": agent_name,
                "language": language,
                "entrypoint": entrypoint,
                "port": options.get('port', 8080),
                "build_mode": options.get('build_mode', 'local'),
                "requirements_file": "requirements.txt" if language == 'python' else None,
            }

            # Add description if provided
            if options.get('description'):
                metadata_dict["description"] = options["description"]

            metadata = AgentMetadata(**metadata_dict)

            # Save the new metadata
            self.metadata_service.save_metadata(workspace_path, metadata)

        return metadata

    def _apply_option_overrides(self, metadata: AgentMetadata, options: Dict[str, Any]) -> AgentMetadata:
        """Apply CLI option overrides to metadata."""
        overrides = {}

        # Map CLI options to metadata fields
        option_mappings = {
            'agent_name': 'agent_name',
            'language': 'language',
            'entrypoint': 'entrypoint',
            'port': 'port',
            'build_mode': 'build_mode',
        }

        for cli_option, metadata_field in option_mappings.items():
            if cli_option in options and options[cli_option] is not None:
                overrides[metadata_field] = options[cli_option]

        if overrides:
            # Create new metadata with overrides
            metadata_dict = metadata.dict()
            metadata_dict.update(overrides)

            metadata = AgentMetadata(**metadata_dict)

            if self.verbose:
                logger.debug(f"Applied option overrides: {overrides}")

        return metadata

    def _validate_language_compatibility(self, workspace_path: Path, metadata: AgentMetadata) -> None:
        """Validate language-specific compatibility."""
        if metadata.language == 'python':
            self._validate_python_compatibility(workspace_path)
        elif metadata.language == 'java':
            self._validate_java_compatibility(workspace_path)
        else:
            raise ValueError(f"Unsupported language: {metadata.language}")

    def _validate_python_compatibility(self, workspace_path: Path) -> None:
        """Validate Python project compatibility."""
        # Check for Python files
        python_files = list(workspace_path.glob("*.py"))
        if not python_files:
            raise ValueError("No Python files found in workspace")

        # Check for requirements.txt (optional for now)
        requirements_file = workspace_path / "requirements.txt"
        if not requirements_file.exists():
            if self.verbose:
                logger.warning("No requirements.txt found, dependencies may be missing")

    def _validate_java_compatibility(self, workspace_path: Path) -> None:
        """Validate Java project compatibility."""
        # Check for pom.xml
        pom_file = workspace_path / "pom.xml"
        if not pom_file.exists():
            raise ValueError("Java projects require a pom.xml file")

    def _process_dependencies(self, workspace_path: Path, metadata: AgentMetadata) -> None:
        """Process and validate dependencies."""
        if metadata.language == 'python':
            self._process_python_dependencies(workspace_path)
        elif metadata.language == 'java':
            self._process_java_dependencies(workspace_path)

    def _process_python_dependencies(self, workspace_path: Path) -> None:
        """Process Python dependencies."""
        requirements_file = workspace_path / "requirements.txt"
        if requirements_file.exists():
            if self.verbose:
                logger.debug("Found requirements.txt, processing dependencies")

            # Basic validation of requirements.txt
            try:
                with open(requirements_file, 'r') as f:
                    content = f.read().strip()
                    if not content:
                        logger.warning("requirements.txt is empty")
            except Exception as e:
                raise ValueError(f"Error reading requirements.txt: {e}")

    def _process_java_dependencies(self, workspace_path: Path) -> None:
        """Process Java dependencies."""
        # Maven dependencies are defined in pom.xml, which we already validated
        if self.verbose:
            logger.debug("Java dependencies will be processed by Maven during build")

    def _generate_dockerfile(self, workspace_path: Path, metadata: AgentMetadata) -> Optional[Path]:
        """Generate Dockerfile for the agent."""
        if metadata.build_mode == 'cloud':
            # For cloud build, we still need a Dockerfile
            pass

        dockerfile_path = workspace_path / "Dockerfile"

        # Don't overwrite existing Dockerfile
        if dockerfile_path.exists():
            if self.verbose:
                logger.debug("Dockerfile already exists, skipping generation")
            return dockerfile_path

        # Generate Dockerfile based on language
        if metadata.language == 'python':
            dockerfile_content = self._generate_python_dockerfile(metadata)
        elif metadata.language == 'java':
            dockerfile_content = self._generate_java_dockerfile(metadata)
        else:
            raise ValueError(f"Unsupported language for Dockerfile generation: {metadata.language}")

        with open(dockerfile_path, 'w') as f:
            f.write(dockerfile_content)

        if self.verbose:
            logger.debug(f"Generated Dockerfile: {dockerfile_path}")

        return dockerfile_path

    def _generate_python_dockerfile(self, metadata: AgentMetadata) -> str:
        """Generate Dockerfile for Python agent."""
        return f"""# Python Agent Dockerfile
FROM python:3.11-slim

# Set working directory
WORKDIR /app

# Install system dependencies
RUN apt-get update && apt-get install -y \\
    gcc \\
    && rm -rf /var/lib/apt/lists/*

# Copy requirements first for better caching
{f'COPY {metadata.requirements_file} .' if metadata.requirements_file else '# No requirements file specified'}
{f'RUN pip install --no-cache-dir -r {metadata.requirements_file}' if metadata.requirements_file else '# No dependencies to install'}

# Copy application code
COPY . .

# Expose port
EXPOSE {metadata.port}

# Set environment variables
ENV PYTHONPATH=/app
ENV PYTHONUNBUFFERED=1

# Run the application
CMD {metadata.entrypoint}
"""

    def _generate_java_dockerfile(self, metadata: AgentMetadata) -> str:
        """Generate Dockerfile for Java agent."""
        return f"""# Java Agent Dockerfile
FROM maven:3.9-openjdk-17-slim AS builder

# Set working directory
WORKDIR /app

# Copy Maven configuration
COPY pom.xml .

# Download dependencies
RUN mvn dependency:go-offline -B

# Copy source code
COPY src ./src

# Build the application
RUN mvn clean package -DskipTests

# Runtime stage
FROM openjdk:17-jre-slim

# Set working directory
WORKDIR /app

# Copy the built jar
COPY --from=builder /app/target/*.jar app.jar

# Expose port
EXPOSE {metadata.port}

# Run the application
CMD ["java", "-jar", "app.jar"]
"""

    def _update_pack_metadata(self, workspace_path: Path, metadata: AgentMetadata, dockerfile_path: Optional[Path]) -> None:
        """Update metadata with pack-related information."""
        updates = {}

        if dockerfile_path:
            updates["has_dockerfile"] = True

        if updates:
            self.metadata_service.update_metadata(workspace_path, updates)

    def _prepare_output_path(self, workspace_path: Path, output_path: Optional[str]) -> Path:
        """Prepare the output path for the packaged workspace."""
        if output_path:
            output = Path(output_path).resolve()

            # Create directory if it doesn't exist
            output.mkdir(parents=True, exist_ok=True)

            # If output path is different from workspace, copy files
            if output != workspace_path:
                if self.verbose:
                    logger.debug(f"Copying workspace from {workspace_path} to {output}")

                # Remove existing directory if it exists
                if output.exists():
                    shutil.rmtree(output)

                # Copy workspace contents
                shutil.copytree(workspace_path, output, ignore=shutil.ignore_patterns('.git', '__pycache__'))

                return output

        return workspace_path

    def _infer_entrypoint(self, workspace_path: Path, language: str) -> str:
        """Infer entrypoint from workspace files."""
        if language == 'python':
            # Look for common Python entry points
            for entrypoint in ["main.py", "app.py", "run.py"]:
                if (workspace_path / entrypoint).exists():
                    return f"python {entrypoint}"
            return "python main.py"  # Default
        elif language == 'java':
            # For Java, we'll use Maven to run
            return "mvn spring-boot:run"  # Default for Spring Boot
        else:
            raise ValueError(f"Cannot infer entrypoint for language: {language}")