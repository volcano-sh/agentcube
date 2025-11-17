"""
Main CLI entry point for AgentRun.

This module defines the command-line interface using Typer, providing
a rich and developer-friendly experience for managing AI agents.
"""

from pathlib import Path
from typing import Optional

import typer
from rich.console import Console
from rich.progress import Progress, SpinnerColumn, TextColumn
from rich.table import Table

from agentrun.runtime.build_runtime import BuildRuntime
from agentrun.runtime.invoke_runtime import InvokeRuntime
from agentrun.runtime.pack_runtime import PackRuntime
from agentrun.runtime.publish_runtime import PublishRuntime
from agentrun.services.metadata_service import MetadataService

# Initialize rich console for beautiful output
console = Console()

# Create the main Typer application
app = typer.Typer(
    name="agentrun",
    help="AgentRun CLI - A developer tool for packaging, building, and deploying AI agents to AgentCube",
    no_args_is_help=True,
    rich_markup_mode="rich",
    add_completion=False,
)

# Version callback
def version_callback(value: bool) -> None:
    """Show version information and exit."""
    if value:
        from agentrun import __version__
        console.print(f"AgentRun CLI version: [bold green]{__version__}[/bold green]")
        raise typer.Exit()

@app.callback()
def main(
    version: Optional[bool] = typer.Option(
        None,
        "--version",
        "-v",
        help="Show version and exit",
        callback=version_callback,
        is_eager=True,
    ),
    verbose: bool = typer.Option(
        False,
        "--verbose",
        help="Enable verbose output",
    ),
) -> None:
    """AgentRun CLI - A developer tool for AI agent lifecycle management."""
    # Set global verbosity level
    if verbose:
        import logging
        logging.basicConfig(level=logging.DEBUG)

@app.command()
def pack(
    workspace: str = typer.Option(
        ".",
        "-f",
        "--workspace",
        help="Path to the agent workspace directory",
        show_default=True,
    ),
    agent_name: Optional[str] = typer.Option(
        None,
        "--agent-name",
        help="Override the agent name",
    ),
    language: Optional[str] = typer.Option(
        None,
        "--language",
        help="Programming language (python, java)",
    ),
    entrypoint: Optional[str] = typer.Option(
        None,
        "--entrypoint",
        help="Override the entrypoint command",
    ),
    port: Optional[int] = typer.Option(
        None,
        "--port",
        help="Port to expose in the Dockerfile",
    ),
    build_mode: Optional[str] = typer.Option(
        None,
        "--build-mode",
        help="Build strategy: local or cloud",
    ),
    description: Optional[str] = typer.Option(
        None,
        "--description",
        help="Agent description",
    ),
    output: Optional[str] = typer.Option(
        None,
        "--output",
        help="Output path for packaged workspace",
    ),
    verbose: bool = typer.Option(
        False,
        "--verbose",
        help="Enable detailed logging",
    ),
) -> None:
    """
    Package the agent application into a standardized workspace.

    This command validates the agent structure, processes dependencies,
    and prepares the workspace for building and deployment.
    """
    try:
        with Progress(
            SpinnerColumn(),
            TextColumn("[progress.description]{task.description}"),
            console=console,
        ) as progress:
            task = progress.add_task("Packing agent...", total=None)

            runtime = PackRuntime(verbose=verbose)
            workspace_path = Path(workspace).resolve()

            options = {
                "agent_name": agent_name,
                "language": language,
                "entrypoint": entrypoint,
                "port": port,
                "build_mode": build_mode,
                "description": description,
                "output": output,
            }

            # Filter out None values
            options = {k: v for k, v in options.items() if v is not None}

            result = runtime.pack(workspace_path, **options)

            progress.update(task, description="Packaging completed! ✅")

        console.print(f"✅ Successfully packaged agent: [bold green]{result['agent_name']}[/bold green]")
        console.print(f"📁 Workspace: [blue]{result['workspace_path']}[/blue]")
        console.print(f"📄 Metadata: [blue]{result['metadata_path']}[/blue]")

    except Exception as e:
        console.print(f"❌ Error packaging agent: [red]{str(e)}[/red]")
        if verbose:
            import traceback
            console.print(traceback.format_exc())
        raise typer.Exit(1)

@app.command()
def build(
    workspace: str = typer.Option(
        ".",
        "-f",
        "--workspace",
        help="Path to the agent workspace directory",
        show_default=True,
    ),
    proxy: Optional[str] = typer.Option(
        None,
        "-p",
        "--proxy",
        help="Custom proxy URL for dependency resolution",
    ),
    cloud_provider: Optional[str] = typer.Option(
        None,
        "--cloud-provider",
        help="Cloud provider name (e.g., huawei)",
    ),
    output: Optional[str] = typer.Option(
        None,
        "--output",
        help="Output path for build artifacts",
    ),
    verbose: bool = typer.Option(
        False,
        "--verbose",
        help="Enable detailed logging",
    ),
) -> None:
    """
    Build the agent image based on the packaged workspace.

    This command builds a container image from the agent workspace,
    supporting both local Docker builds and cloud builds.
    """
    try:
        with Progress(
            SpinnerColumn(),
            TextColumn("[progress.description]{task.description}"),
            console=console,
        ) as progress:
            task = progress.add_task("Building agent image...", total=None)

            runtime = BuildRuntime(verbose=verbose)
            workspace_path = Path(workspace).resolve()

            options = {
                "proxy": proxy,
                "cloud_provider": cloud_provider,
                "output": output,
            }

            # Filter out None values
            options = {k: v for k, v in options.items() if v is not None}

            result = runtime.build(workspace_path, **options)

            progress.update(task, description="Build completed! ✅")

        console.print(f"✅ Successfully built agent image: [bold green]{result['image_name']}[/bold green]")
        console.print(f"🏷️  Tag: [blue]{result['image_tag']}[/blue]")
        console.print(f"📏 Size: [blue]{result['image_size']}[/blue]")

    except Exception as e:
        console.print(f"❌ Error building agent: [red]{str(e)}[/red]")
        if verbose:
            import traceback
            console.print(traceback.format_exc())
        raise typer.Exit(1)

@app.command()
def publish(
    workspace: str = typer.Option(
        ".",
        "-f",
        "--workspace",
        help="Path to the agent workspace directory",
        show_default=True,
    ),
    version: Optional[str] = typer.Option(
        None,
        "--version",
        help="Semantic version string (e.g., v1.0.0)",
    ),
    image_url: Optional[str] = typer.Option(
        None,
        "--image-url",
        help="Image repository URL (required in local build mode)",
    ),
    image_username: Optional[str] = typer.Option(
        None,
        "--image-username",
        help="Username for image repository",
    ),
    image_password: Optional[str] = typer.Option(
        None,
        "--image-password",
        help="Password for image repository",
    ),
    description: Optional[str] = typer.Option(
        None,
        "--description",
        help="Agent description",
    ),
    region: Optional[str] = typer.Option(
        None,
        "--region",
        help="Deployment region",
    ),
    cloud_provider: Optional[str] = typer.Option(
        None,
        "--cloud-provider",
        help="Cloud provider name (e.g., huawei)",
    ),
    verbose: bool = typer.Option(
        False,
        "--verbose",
        help="Enable detailed logging",
    ),
) -> None:
    """
    Publish the agent image to AgentCube.

    This command publishes the built agent to AgentCube, making it
    available for invocation, sharing, and collaboration.
    """
    try:
        with Progress(
            SpinnerColumn(),
            TextColumn("[progress.description]{task.description}"),
            console=console,
        ) as progress:
            task = progress.add_task("Publishing agent...", total=None)

            runtime = PublishRuntime(verbose=verbose)
            workspace_path = Path(workspace).resolve()

            options = {
                "version": version,
                "image_url": image_url,
                "image_username": image_username,
                "image_password": image_password,
                "description": description,
                "region": region,
                "cloud_provider": cloud_provider,
            }

            # Filter out None values
            options = {k: v for k, v in options.items() if v is not None}

            result = runtime.publish(workspace_path, **options)

            progress.update(task, description="Publishing completed! ✅")

        console.print(f"✅ Successfully published agent: [bold green]{result['agent_name']}[/bold green]")
        console.print(f"🆔 Agent ID: [blue]{result['agent_id']}[/blue]")
        console.print(f"🌐 Endpoint: [blue]{result['agent_endpoint']}[/blue]")

    except Exception as e:
        console.print(f"❌ Error publishing agent: [red]{str(e)}[/red]")
        if verbose:
            import traceback
            console.print(traceback.format_exc())
        raise typer.Exit(1)

@app.command()
def invoke(
    workspace: str = typer.Option(
        ".",
        "-f",
        "--workspace",
        help="Path to the agent workspace directory",
        show_default=True,
    ),
    payload: str = typer.Option(
        "{}",
        "--payload",
        help="JSON-formatted input passed to the agent",
    ),
    header: Optional[str] = typer.Option(
        None,
        "--header",
        help="Custom HTTP headers (e.g., 'Authorization: Bearer token')",
    ),
    verbose: bool = typer.Option(
        False,
        "--verbose",
        help="Enable detailed logging",
    ),
) -> None:
    """
    Invoke a published agent via AgentCube.

    This command sends a request to a published agent, allowing you
    to test and interact with your deployed agent.
    """
    try:
        with Progress(
            SpinnerColumn(),
            TextColumn("[progress.description]{task.description}"),
            console=console,
        ) as progress:
            task = progress.add_task("Invoking agent...", total=None)

            runtime = InvokeRuntime(verbose=verbose)
            workspace_path = Path(workspace).resolve()

            # Parse payload
            import json
            try:
                payload_data = json.loads(payload)
            except json.JSONDecodeError:
                console.print(f"❌ Invalid JSON payload: [red]{payload}[/red]")
                raise typer.Exit(1)

            # Parse headers
            headers = {}
            if header:
                for h in header:
                    if ':' in h:
                        key, value = h.split(':', 1)
                        headers[key.strip()] = value.strip()

            result = runtime.invoke(workspace_path, payload_data, headers)

            progress.update(task, description="Invocation completed! ✅")

        console.print(f"✅ Successfully invoked agent")
        console.print(f"📤 Response: [green]{result}[/green]")

    except Exception as e:
        console.print(f"❌ Error invoking agent: [red]{str(e)}[/red]")
        if verbose:
            import traceback
            console.print(traceback.format_exc())
        raise typer.Exit(1)

@app.command()
def status(
    workspace: str = typer.Option(
        ".",
        "-f",
        "--workspace",
        help="Path to the agent workspace directory",
        show_default=True,
    ),
    verbose: bool = typer.Option(
        False,
        "--verbose",
        help="Enable detailed logging",
    ),
) -> None:
    """
    Check the status of a published agent.

    This command queries AgentCube for the current state of the agent
    associated with the workspace.
    """
    try:
        runtime = InvokeRuntime(verbose=verbose)
        workspace_path = Path(workspace).resolve()

        metadata_service = MetadataService(verbose=verbose)
        metadata = metadata_service.load_metadata(workspace_path)

        if not metadata.get("agent_id"):
            console.print("❌ No agent found. Please publish an agent first.")
            raise typer.Exit(1)

        # TODO: Implement status check via AgentCube API
        # For now, show basic metadata info

        table = Table(title="Agent Status")
        table.add_column("Property", style="cyan")
        table.add_column("Value", style="green")

        table.add_row("Agent Name", metadata.get("agent_name", "N/A"))
        table.add_row("Agent ID", metadata.get("agent_id", "N/A"))
        table.add_row("Version", metadata.get("version", "N/A"))
        table.add_row("Language", metadata.get("language", "N/A"))
        table.add_row("Build Mode", metadata.get("build_mode", "N/A"))

        if metadata.get("agent_endpoint"):
            table.add_row("Endpoint", metadata["agent_endpoint"])

        console.print(table)

    except Exception as e:
        console.print(f"❌ Error checking status: [red]{str(e)}[/red]")
        if verbose:
            import traceback
            console.print(traceback.format_exc())
        raise typer.Exit(1)

if __name__ == "__main__":
    app()