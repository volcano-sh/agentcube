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
from agentrun.runtime.status_runtime import StatusRuntime

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
        console.print(f"AgentRun CLI (kubectl agentrun) version: [bold green]{__version__}[/bold green]")
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
    provider: str = typer.Option(
        "agentcube",
        "--provider",
        help="Target provider for deployment (agentcube, k8s).",
    ),
    node_port: Optional[int] = typer.Option(
        None,
        "--node-port",
        help="Specific NodePort to use (30000-32767) for K8s deployment",
    ),
    replicas: Optional[int] = typer.Option(
        None,
        "--replicas",
        help="Number of replicas for K8s deployment (default: 1)",
    ),
    verbose: bool = typer.Option(
        False,
        "--verbose",
        help="Enable detailed logging",
    ),
) -> None:
    """
    Publish the agent image to AgentCube or local Kubernetes cluster.

    This command publishes the built agent to AgentCube or deploys it to
    a local Kubernetes cluster for testing and development.
    """
    try:
        with Progress(
            SpinnerColumn(),
            TextColumn("[progress.description]{task.description}"),
            console=console,
        ) as progress:
            task = progress.add_task("Publishing agent...", total=None)

            runtime = PublishRuntime(verbose=verbose, provider=provider)
            workspace_path = Path(workspace).resolve()

            options = {
                "version": version,
                "image_url": image_url,
                "image_username": image_username,
                "image_password": image_password,
                "description": description,
                "region": region,
                "cloud_provider": cloud_provider,
                "provider": provider, # Pass provider down
                "node_port": node_port,
                "replicas": replicas,
            }

            # Filter out None values
            options = {k: v for k, v in options.items() if v is not None}

            result = runtime.publish(workspace_path, **options)

            progress.update(task, description="Publishing completed! ✅")

        console.print(f"✅ Successfully published agent: [bold green]{result['agent_name']}[/bold green]")
        console.print(f"🆔 Agent ID: [blue]{result['agent_id']}[/blue]")
        if "agent_endpoint" in result:
            console.print(f"🌐 Endpoint: [blue]{result['agent_endpoint']}[/blue]")

        if provider == "agentcube" or provider == "k8s":
            console.print(f"📦 Namespace: [blue]{result.get('namespace', 'agentrun')}[/blue]")
            if "status" in result:
                 console.print(f"📊 Status: [blue]{result['status']}[/blue]")
            if "node_port" in result: # For standard K8s provider if it returns node_port
                console.print(f"🔌 NodePort: [blue]{result['node_port']}[/blue]")

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
    provider: str = typer.Option(
        "agentcube",
        "--provider",
        help="Target provider for invocation (agentcube, k8s).",
    ),
    verbose: bool = typer.Option(
        False,
        "--verbose",
        help="Enable detailed logging",
    ),
) -> None:
    """
    Invoke a published agent via AgentCube or Kubernetes.

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

            runtime = InvokeRuntime(verbose=verbose, provider=provider)
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
    provider: str = typer.Option(
        "agentcube",
        "--provider",
        help="Target provider for status check (agentcube, k8s)",
    ),
    verbose: bool = typer.Option(
        False,
        "--verbose",
        help="Enable detailed logging",
    ),
) -> None:
    """
    Check the status of a published agent.

    This command queries AgentCube or Kubernetes for the current state
    of the agent associated with the workspace.
    """
    try:
        runtime = StatusRuntime(verbose=verbose, provider=provider)
        workspace_path = Path(workspace).resolve()

        status_info = runtime.get_status(workspace_path, provider=provider)

        if status_info.get("status") == "not_published":
            console.print("❌ No agent found. Please publish an agent first.")
            raise typer.Exit(1)

        if status_info.get("status") == "error":
            console.print(f"❌ Error checking status: {status_info.get('error')}")
            raise typer.Exit(1)

        # Display status information
        table = Table(title="Agent Status")
        table.add_column("Property", style="cyan")
        table.add_column("Value", style="green")

        table.add_row("Agent Name", status_info.get("agent_name", "N/A"))
        table.add_row("Agent ID", status_info.get("agent_id", "N/A"))
        table.add_row("Status", status_info.get("status", "N/A"))
        table.add_row("Version", status_info.get("version", "N/A"))
        table.add_row("Language", status_info.get("language", "N/A"))
        table.add_row("Build Mode", status_info.get("build_mode", "N/A"))

        if status_info.get("agent_endpoint"):
            table.add_row("Endpoint", status_info["agent_endpoint"])

        # Add last activity if available (from AgentCube)
        if status_info.get("last_activity"):
            table.add_row("Last Activity", status_info["last_activity"])

        # Add note if available (e.g., when API is unavailable)
        if status_info.get("note"):
            table.add_row("Note", status_info["note"])

        # Add K8s-specific information if available
        if "k8s_deployment" in status_info:
            k8s_info = status_info["k8s_deployment"]
            table.add_row("Namespace", k8s_info.get("namespace", "N/A"))
            table.add_row("NodePort", str(k8s_info.get("node_port", "N/A")))

            if "replicas" in k8s_info:
                replicas = k8s_info["replicas"]
                table.add_row(
                    "Replicas",
                    f"{replicas.get('ready', 0)}/{replicas.get('desired', 0)}"
                )

            if "pods" in k8s_info:
                pods_status = ", ".join([
                    f"{pod['name']}: {pod['phase']}"
                    for pod in k8s_info["pods"][:3]  # Show first 3 pods
                ])
                table.add_row("Pods", pods_status)

        console.print(table)

    except Exception as e:
        console.print(f"❌ Error checking status: [red]{str(e)}[/red]")
        if verbose:
            import traceback
            console.print(traceback.format_exc())
        raise typer.Exit(1)

if __name__ == "__main__":
    app()