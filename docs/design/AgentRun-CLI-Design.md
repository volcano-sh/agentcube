# AgentRun CLI Design
Author: Layne Peng
# Motivation
Modern AI agent frameworks are increasingly complex, often involving a multi-stage development lifecycle—from initial coding and local testing to cloud deployment and public publishing. This complexity introduces friction for developers who must manage configurations, dependencies, runtime environments, and deployment targets across heterogeneous platforms.

To streamline this process, we propose a dedicated Command Line Interface (CLI) that provides:
- Lightweight orchestration of agent development workflow, including initialization, packaging, building, testing, and publishing;
- Extensible architecture supporting local environments, Kubernetes clusters, and multi-cloud build and deployment targets;
- Python SDK bindings for programmatic access to CLI functionality, enabling integration into CI workflows and continuous development pipelines.

This CLI will serve as a foundational tool enabling:
- Rapid prototyping and local iteration of agents
- Seamless transitions from development to staging and production environments powered by AgentCube
- Native integration with Kubernetes clusters and cloud services for scalable deployment
- Version control and reproducibility of agent configurations and runtime environments

By abstracting away operational overhead and providing a consistent interface, the CLI lowers the barrier to entry for agent development on AgentCube. It empowers developers to focus on innovation rather than infrastructure, fosters best practices, and accelerates collaboration across the open-source community.
## Use Case 1: Kickstart Agent Development and Publish to AgentCube
A developer wants to create a new agent from scratch and publish it to AgentCube for public access or team collaboration. The CLI supports agents built with any framework, offering a standardized workflow for packaging, building, and publishing. 

After completing development, the CLI enables the following steps:
1. `agentrun pack -f ./` Packages the agent source code and runtime metadata into a structured workspace directory, preparing it for image creation
2. `agentrun build -f ./` Builds a container image from the workspace, compatible with AgentCube’s Kubernetes-based runtime environment
3. `agentrun publish -f ./` Publishes the built agent image to AgentCube, making it available for invocation, sharing, and collaboration

## Use Case 2: Check Published Agent Status
After publishing an agent to AgentCube, a developer may want to verify that the agent is fully registered and ready for use. The CLI provides a simple status check command:

```agentrun status -f ./```

This command queries AgentCube for the current state of the agent associated with the workspace. It returns key information such as the agent ID, endpoint URL, latest version, and log reference. This helps ensure the agent is correctly deployed and ready for invocation.

## Use Case 3: Invoke published Agent
After publishing an agent to AgentCube, a developer may want to invoke it for testing purposes or integrate it into other system components. The CLI provides a simple and consistent interface to trigger agent execution using the local workspace directory and a structured payload:

```agentrun invoke -f ./ -payload '{"prompt": "what is the weather today in Shanghai?"}'```

# Scope
In Scope:
* support agent packaging and workspace generation
* support agent image build for Kubernetes runtime
* support agent publishing to AgentCube
* support agent invocation via CLI
* support Python SDK for CI/CD integration
# Function Detail
### 1. Pack, Build and Publish 
**Step 0:** The developer creates the agent application using any preferred framework and defines the required runtime metadata. The agent must expose an HTTP interface to support standardized invocation. The metadata should include:
1. **Agent name** – unique identifier for the agent
2. **Entrypoint command** – entrypoint command to launch the agent
3. **Port** – network port exposed by the agent
4. **Endpoint URI** – HTTP endpoint for agent invocation
5. **Region to deploy** – required only when publishing to public cloud targets
6. **observability_enabled** – flag to enable metrics and logging integration
7. **build_mode** – specifies build context: `local` or `cloud`

**Step 1:** The developer runs the `agentrun pack -f ./` command to package the agent application into a standardized workspace. This workspace includes the source code and runtime metadata required for building and deployment. 

The `agentrun pack` command supports options that mirror the fields defined in the metadata configuration file. Its behavior follows these rules:
- If no options are provided beyond `-f`, the CLI expects a valid metadata config file to be present in the specified workspace directory
- Any options explicitly passed via the `agentrun pack` command take precedence over values defined in the metadata config file.
- The CLI can validate and update the metadata config file based on the provided options, ensuring consistency and completeness.

**Step 1.1** Validate Source Structure and Metadata Configuration
The `agentrun pack` command is processed by the **pack** service, which performs a series of validation checks to ensure the agent workspace is correctly structured and compatible with downstream build and deployment steps.

The validation includes:
1. **Language Compatibility**
	 Verifies that the development language matches the value specified in the metadata configuration file.
	- For **Python**: supports either source code or pre-built `.whl` (wheel) packages
	- For **Java**: supports `.jar` or `.war` artifacts
2. **Dependency Definition** 
	 Ensures that all required dependencies are properly declared and available for packaging.
    - For **Python**: checks for a valid `requirements.txt` file
    - For **Java**: checks for a valid `pom.xml` file

**Step 1.2** Build Mode Selection
Based on the `build_mode` specified in the metadata configuration file, the CLI supports two build strategies: **local** and **cloud**. Each mode determines how the agent image is constructed and where the build process is executed.

**Step 1.2.1** Local mode
In local mode, the CLI generates a Dockerfile tailored to the agent’s runtime requirements. This process includes:
- Downloading a predefined Dockerfile template based on the agent’s language and framework
- Replacing template placeholders with values from the metadata configuration file (e.g., entrypoint command, exposed port)

**Step 1.2.2** Cloud mode
In cloud mode, the CLI prepares the agent workspace for remote build services such as **Huawei Cloud CodeArts** or other supported platforms. This includes:
- Packaging the workspace and metadata into a cloud-compatible format
- Setting up required roles, permissions, and credentials for the cloud build service

**Step 2:**  After packaging the agent workspace, the developer initiates the build process using: 

```agentrun build -f ./```

This command triggers the image build based on the workspace contents and metadata configuration. The CLI supports an optional `-p` flag to specify a **custom proxy**, which is particularly useful for environments with restricted network access or internal mirrors.
- For **Python agents**, the proxy is applied to `pip` commands during dependency installation   
- For **Java agents**, the proxy is applied to `mvn` (Maven) commands during build resolution

**Step 2.1** Build Service Validation
The `agentrun build` command is handled by the **Build** service, which performs a series of validation checks to ensure the build process can succeed in both **local** and **cloud** modes.

Key validations include:
- **Workspace integrity**: Verifies that the agent workspace is correctly structured and includes all required files (e.g., source code, metadata, dependencies).
- **Metadata consistency**: Confirms that runtime metadata matches the expected format and values needed for image generation.
- **Build mode compatibility**:
    - For **local builds**, the system checks whether Docker or Podman is installed and accessible on the developer’s machine.    
    - For **cloud builds**, the service validates credentials, roles, and connectivity to the configured cloud build provider.

**Step 2.1.1** Local Build
In local build mode, the CLI invokes the local container runtime (Docker or Podman) to build the agent image. The image is tagged using the agent name defined in the metadata configuration file, ensuring traceability and consistency.

**Step 2.1.2** Cloud Build
TBD

**Step 3** Once the agent image is successfully built, the developer can publish it to AgentCube using:

```agentrun publish -f ./```

This command initiates the publishing process, registering the agent with AgentCube and making it available for invocation, collaboration, and public or team access.

The behavior of the publish process depends on the **build** mode. It requires options:
- **Image Repository URL and Credentials**
    - **Local build mode**: The developer must explicitly provide the image repository URL, along with username and password, via CLI options.
    - **Cloud build mode**: The image is already built and stored in the cloud image repository. The image location is automatically retrieved and merged into the metadata configuration file during the build process.
- **Agent Version**
    - A semantic version string (e.g., `v1.0.0`) used to register the agent.
    - If publishing a new version, the version must be unique.
    - If updating an existing version, the same version number can be reused.
- **Additional Metadata**
    - Any other fields required by AgentCube’s `create_or_update_agent` API, such as:
        - Agent description
        - Deployment region

**Step 3.1**: Update Metadata Configuration File
As part of the publish process, the CLI updates and merges deployment-related fields into the agent’s metadata configuration file to ensure consistency and traceability.

**Fields updated include:**
- **Agent version** – Used for version tracking and rollback
- **Image repository URL** – Required for deployment and invocation
- **Build mode and deployment region** – Confirmed or updated based on the current publish context
- **Additional metadata** – Such as tags, and description, as required by AgentCube 

**Step 3.2**: Tag and Push the Agent Image
- The agent image is tagged using the specified version (e.g., `my-agent:v1.0.0`).
- **Local build mode**:
    - The CLI logs into the specified image repository using provided credentials
    - The image is pushed to the repository.
    - The final image URL is retrieved and merged into the metadata configuration file.
- **Cloud build mode**:
    - The image is already present in the cloud repository.
    - The CLI verifies the image location and ensures it is correctly recorded in the metadata configuration file.

**Step 3.3** Trigger Agent Registration via AgentCube API
The CLI calls the `create_or_update_agent()` API to register or update the agent in AgentCube. This is an **asynchronous API**, allowing AgentCube to process the request in the background while the CLI monitors for completion.

The API payload includes:
- Agent metadata (name, version, description, tags, etc.)
- Image repository URL
- Runtime configuration (entrypoint, port, endpoint URI)
- Deployment region and build mode

**Step 3.4** Await AgentCube Response 
The CLI waits for the asynchronous response from AgentCube, which confirms the result of the registration or update process. The response includes:
- **agent_id** – Unique identifier assigned by AgentCube
- **agent_endpoint** – The fully qualified HTTP endpoint for invoking the agent

This response ensures the agent is now accessible within the AgentCube ecosystem.

**Step 3.5** Merge Response into Metadata Configuration
The CLI parses the response and updates the metadata configuration file with the returned values:
- **agent_id** – Used for future updates, versioning, and invocation tracking
- **agent_endpoint** – Enables direct invocation and integration
- Any other relevant fields returned by AgentCube

This final step ensures the agent workspace remains complete, consistent, and ready for future operations such as invocation, or monitoring and redeployment. 
### Check Status
In the same workspace, developers can use the following command to check the status of a published agent: 

```agentrun status```

This command queries AgentCube for the current state of the agent associated with the workspace. The output includes:
- **Agent ID** – Unique identifier assigned by AgentCube
- **Agent Name** – Human-readable name of the agent
- **Agent Endpoint** – Fully qualified URL for invoking the agent
- **Latest Version** – The most recently published version of the agent
- **Log Location** – Reference to the agent’s runtime logs (note: in the initial release, the CLI does not tunnel or stream logs directly to the developer)

This status check helps developers verify successful publication, retrieve invocation details, and confirm versioning—all without leaving the local development environment.
### Invocation
Developers can invoke a published agent either from the current workspace or by specifying the workspace directory using the `-f` option. The invocation is performed via:

```agentrun invoke --payload {"prompt": "What is the weather today in Shanghai?"}```

This command initiates an HTTP POST request to the agent’s endpoint. The payload structure depends on the agent’s design and is passed directly to the agent application as the HTTP body.

The CLI also supports basic HTTP options:
- **Header** – Custom HTTP headers (e.g., authorization, content-type)
- **Payload** – JSON-formatted input passed to the agent’s entrypoint method

#### **Invocation Workflow**

**Step 1: Load Metadata Configuration** The CLI reads the metadata configuration file to retrieve:
- Agent endpoint URL
- Deployment region
- Latest version
- Authorization and authentication details

**Step 2: Build HTTP Request** Construct the HTTP POST request using:
- Endpoint URL from metadata
- Payload provided via CLI
- Optional headers (e.g., `Authorization`, `Content-Type`)

**Step 3: Send Request to AgentCube** The CLI sends the HTTP request to AgentCube, which routes it to the correct agent instance based on metadata and deployment context.

**Step 4: Await Agent Response** AgentCube forwards the request to the deployed agent. The agent processes the payload via its entrypoint method and returns a response.

**Step 5: Return Result to Developer** The CLI receives the response and displays the result to the developer in the terminal.

## Implementation

### Overview

```mermaid
%%{init: {'themeVariables': {'width': '100%'}} }%%
flowchart TD
  %% Style definitions
  classDef layer fill:#f9f9f9,stroke:#333,stroke-width:1px
  classDef external fill:#e3f2fd,stroke:#1e88e5,stroke-width:1px
  classDef actor fill:#fff3e0,stroke:#ef6c00,stroke-width:1px
  classDef flow fill:#ffffff,stroke:#9e9e9e,stroke-dasharray: 5 5

  %% Developer
  Dev["Developer"]
  class Dev actor

  %% CLI layers
  Dev --> CLI["Command Line Layer<br/>Typer-based CLI"]
  class CLI layer

  CLI --> Runtime["Runtimes Layer<br/>Python SDK"]
  class Runtime layer

  Runtime --> Ops["Operations Layer<br/>Business Logic"]
  class Ops layer

  Ops --> Services["Services Layer<br/>External Integrations"]
  class Services layer

  %% External systems
  Services --> AgentCube["AgentCube<br/>Agent Registry & Runtime"]
  Services --> Container["Local Container Runtime<br/>Docker / Podman"]
  Services --> Metadata["Metadata Handler<br/>Read / Update / Merge"]
  Services --> CloudInterface["Cloud Provider Interface<br/>Unified Abstraction Layer"]
  class AgentCube,Container,Metadata,CloudInterface external

  %% Cloud provider grouping
  subgraph Cloud Providers
    direction TB
    CloudInterface --> Huawei["Huawei Cloud CodeArts"]
    CloudInterface --> AWS["AWS CodeBuild"]
    CloudInterface --> Azure["Azure Container Registry"]
    CloudInterface --> GCP["Google Cloud Build"]
    class Huawei,AWS,Azure,GCP external
  end

  %% Lifecycle flow
  subgraph Flow["Agent Lifecycle Flow"]
    CLI -->|parse command & route execution| Runtime
    Runtime -->| delegate domain-specific logic | Ops
    Ops -->| call | Services
    Services -->|send request| AgentCube
  end
  class Flow flow

```

The AgentRun CLI is organized into four modular layers, each responsible for a distinct aspect of functionality and extensibility:
#### **1. Command Line Layer**
- Built using the `typer` library, a modern CLI framework for Python.
- Defines the CLI interface and command syntax (`agentrun pack`, `agentrun build`, etc.).
- Parses user input and routes commands to the corresponding runtime logic.
- Provides help messages, argument validation, and interactive UX.
#### **2. Runtimes Layer**
- Implements the business logic for each CLI subcommand.
- Each runtime class corresponds to a specific command (e.g., `PackRuntime`, `BuildRuntime`, `PublishRuntime`).
- Exposed as a **Python SDK**, enabling developers to integrate AgentRun workflows into CI/CD pipelines or custom automation scripts.
- Acts as the bridge between CLI input and deeper operational logic.

#### **3. Operations Layer**
- Encapsulates detailed business logic that powers runtime methods.
- Handles validation, transformation, and orchestration of agent metadata, build artifacts, and deployment configurations.
- Delegates external interactions to the Services layer while maintaining domain-specific logic.
- Promotes reusability and testability across runtimes.

#### **4. Services Layer**
- Interfaces with external systems such as:
    - **AgentCube APIs** for agent registration, status, and invocation
    - **Cloud providers** (e.g., Huawei Cloud CodeArts) for remote builds and image hosting
    - **Local container runtimes** (Docker, Podman) for image creation and tagging
    - **Metadata handler** for retrieving, updating, and merging data into the agent’s metadata configuration file
- Provides low-level utilities for HTTP requests, authentication, file I/O, and cloud SDK integration.
- Designed for extensibility to support future platforms and runtime environments.

### Metadata Configuration File

AgentRun relies on a standardized metadata configuration file named `agent_metadata.yaml`, located in the agent workspace. This file defines the agent’s identity, runtime behavior, build strategy, and deployment settings. It is referenced by all core CLI commands (`pack`, `build`, `publish`, `status`, `invoke`) to ensure consistency and traceability across the agent lifecycle.

#### Sample Structure
```
# agent_metadata.yaml

agent_name: weather-agent
description: Provides weather forecasts based on user queries
language: python
entrypoint: python main.py
port: 8080

build_mode: local
region: cn-east-1

version: v1.0.0

image:
  repository_url: registry.example.com/weather-agent
  tag: v1.0.0
  endpoint: https://registry.example.com/weather-agent:v1.0.0

auth:
  type: bearer
  token: YOUR_AUTH_TOKEN

requirements_file: requirements.txt
```
#### Key Fields

|Field|Description|
|---|---|
|`agent_name`|Unique name identifying the agent|
|`description`|Human-readable summary of the agent’s purpose|
|`language`|Programming language used (`python`, `java`, etc.)|
|`entrypoint`|Command to launch the agent|
|`port`|Port exposed by the agent runtime|
|`build_mode`|Build strategy: `local` or `cloud`|
|`region`|Deployment region|
|`version`|Semantic version string for publishing|
|`image.repository_url`|Container registry where the agent image is stored|
|`image.tag`|Image tag used for versioning|
|`image.endpoint`|Full URL to the deployed image|
|`auth`|Authentication configuration for invoking the agent|
|`requirements_file`|Python dependency file used during packaging and build|

This configuration file is automatically validated and updated by the CLI during packaging, building, and publishing. It serves as the single source of truth for agent metadata throughout the development and deployment lifecycle.

### AgentRun CLI Subcommand API Design
#### `agentrun pack`

**Purpose**
Packages the agent application into a standardized workspace, including source code and runtime metadata, preparing it for build and deployment.

**Behavior Overview**
- If only `-f` is provided, the CLI expects a valid metadata config file `agent_metadata.yaml` in the workspace.
- Options passed via CLI override values in the metadata file.
- The CLI validates and updates the metadata file to ensure consistency.
- The packaged workspace is prepared for either local or cloud build.

**Command Syntax**
```
agentrun pack -f <workspace_path> [OPTIONS]
```
**Required Argument**

| Option              | Type  | Description                                                                      |
| ------------------- | ----- | -------------------------------------------------------------------------------- |
| `-f`, `--workspace` | `str` | Path to the agent workspace directory containing source code and metadata config |

**Optional Parameters**

| Option             | Type   | Description                                                        |
| ------------------ | ------ | ------------------------------------------------------------------ |
| `--agent-name`     | `str`  | Override the agent name defined in metadata                        |
| `--language`       | `str`  | Override the language defined in metadata (`python`, `java`, etc.) |
| `--entrypoint`     | `str`  | Override the entrypoint command for the agent                      |
| `--port`           | `int`  | Port to expose in the Dockerfile                                   |
| `--build-mode`     | `str`  | Build strategy: `local` or `cloud`                                 |
| `--cloud-provider` | `str`  | Cloud provider name (e.g., `huawei`) if using cloud mode           |
| `--output`         | `str`  | Path to save the packaged workspace (default: overwrite in place)  |
| `--verbose`        | `bool` | Enable detailed logging output                                     |

**Validation Logic (Pack Service)**

**Language Compatibility**

| Language | Supported Formats             |
| -------- | ----------------------------- |
| Python   | Source code or `.whl` package |
| Java     | `.jar` or `.war` artifacts    |

**Dependency Definition**

| Language | Required File      |
| -------- | ------------------ |
| Python   | `requirements.txt` |
| Java     | `pom.xml`          |

**Build Mode Handling**

**Local Mode**
- Generates Dockerfile from language-specific template
- Injects metadata values (entrypoint, port, etc.)

**Cloud Mode**
- Prepares cloud-compatible archive 
- Configures credentials and permissions for remote build
- Generates Dockerfile from language-specific template
- Injects metadata values (entrypoint, port, etc.)
#### `agentrun build`

**Purpose** Builds the agent image based on the packaged workspace and metadata configuration, preparing it for deployment in either local or cloud environments.

**Behavior Overview**
- If only `-f` is provided, the CLI reads metadata from the workspace and builds the image accordingly.
- The CLI supports a `-p` option to specify a custom proxy for dependency resolution.
- The build process supports both local and cloud modes, determined by the metadata configuration.
- The image is tagged using the agent name defined in the metadata file.

**Command Syntax**
```
agentrun build -f <workspace_path> [OPTIONS]
```
**Required Argument**

| Option              | Type  | Description                                                                      |
| ------------------- | ----- | -------------------------------------------------------------------------------- |
| `-f`, `--workspace` | `str` | Path to the agent workspace directory containing source code and metadata config |

**Optional Parameters**

| Option             | Type   | Description                                                          |
| ------------------ | ------ | -------------------------------------------------------------------- |
| `--proxy`, `-p`    | `str`  | Custom proxy URL for dependency resolution (applies to pip or Maven) |
| `--cloud-provider` | `str`  | Cloud provider name (e.g., `huawei`) if using cloud build mode       |
| `--output`         | `str`  | Path to save the built image or build logs (optional)                |
| `--verbose`        | `bool` | Enable detailed logging output                                       |

**Validation Logic (Build Service)**

**Workspace Integrity**

|Check|Description|
|---|---|
|Directory structure|Ensures required files (source, metadata, dependencies) are present|

**Metadata Consistency**

| Check            | Description                                                          |
| ---------------- | -------------------------------------------------------------------- |
| Runtime metadata | Confirms metadata fields are valid and complete for image generation |

**Build Mode Compatibility**

|Mode|Validation|
|---|---|
|Local|Verifies Docker or Podman is installed and accessible|
|Cloud|Validates credentials, roles, and connectivity to the configured provider|

**Local Build**
- Invokes Docker or Podman to build the image locally.
- Tags the image using the agent name from metadata.
- Uses proxy settings (if provided) for dependency installation.

**Cloud Build**
- TBD
#### `agentrun publish`

**Purpose** Publishes the agent image to AgentCube, registering it for invocation, collaboration, and public or team access.

**Behavior Overview**
- The CLI reads metadata from the workspace and prepares the agent for publishing.
- Behavior depends on the build mode (`local` or `cloud`).
- In local mode, image credentials must be provided; in cloud mode, image location is auto-resolved.
- The CLI updates the metadata configuration file with deployment details and registration results.

**Sequence Diagram**

```mermaid
sequenceDiagram
  participant Developer
  participant CLI Layer
  participant PublishRuntime as Runtimes Layer
  participant PublishOps as Operations Layer
  participant Services Layer
  participant AgentCubeAPI

  Developer->>CLI Layer: agentrun publish -f ./
  CLI Layer->>PublishRuntime: Parse command and delegate to PublishRuntime
  PublishRuntime->>PublishOps: Initialize publish context and validate metadata
  PublishOps->>Services Layer: Resolve image (push if local, confirm if cloud)
  Services Layer-->>PublishOps: Return image URL and resolution status
  PublishOps->>Services Layer: Prepare and submit agent registration request
  Services Layer->>AgentCubeAPI: Call create_or_update_agent()
  AgentCubeAPI-->>Services Layer: Return agent_id and agent_endpoint
  Services Layer-->>PublishOps: Deliver registration result
  PublishOps->>PublishRuntime: Merge response into metadata
  PublishRuntime->>CLI Layer: Format and return result
  CLI Layer-->>Developer: agent_id, agent_endpoint

```

**Command Syntax**
```
agentrun publish -f <workspace_path> [OPTIONS]
```

**Required Argument**

|Option|Type|Description|
|---|---|---|
|`-f`, `--workspace`|`str`|Path to the agent workspace directory containing source code and metadata config|

**Optional Parameters**

| Option             | Type   | Description                                                    |
| ------------------ | ------ | -------------------------------------------------------------- |
| `--version`        | `str`  | Semantic version string (e.g., `v1.0.0`)                       |
| `--image-url`      | `str`  | Image repository URL (required in local build mode)            |
| `--image-username` | `str`  | Username for image repository (required in local build mode)   |
| `--image-password` | `str`  | Password for image repository (required in local build mode)   |
| `--description`    | `str`  | Agent description                                              |
| `--region`         | `str`  | Deployment region                                              |
| `--cloud-provider` | `str`  | Cloud provider name (e.g., `huawei`) if using cloud build mode |
| `--verbose`        | `bool` | Enable detailed logging output                                 |

**Metadata Update Logic**

| Field         | Description                                           |
| ------------- | ----------------------------------------------------- |
| `version`     | Used for version tracking and rollback                |
| `image.repository_url`   | Required for deployment and invocation     |
| `build_mode`  | Confirmed or updated based on current publish context |
| `region`      | Deployment region                                     |
| `description` | Agent description                                     |
| `tags`        | Optional metadata tags                                |

**Image Push Logic**

**Local Build Mode**
- Tags image using specified version (e.g., `my-agent:v1.0.0`)
- Logs into image repository using provided credentials
- Pushes image and retrieves final image URL
- Updates metadata configuration file with image location

**Cloud Build Mode**
- Image already present in cloud repository
- Verifies image location and updates metadata configuration file

**Agent Registration Logic**
- Calls `create_or_update_agent()` API asynchronously
- Payload includes:
    - Agent metadata (name, version, description, tags)
    - Image repository URL
    - Runtime configuration (entrypoint, port, endpoint URI)
    - Deployment region and build mode

**AgentCube Response Handling**

|Field|Description|
|---|---|
|`agent_id`|Unique identifier assigned by AgentCube|
|`agent_endpoint`|Fully qualified HTTP endpoint for invoking the agent|

**Metadata Merge After Response**
- Updates metadata file with `agent_id` and `agent_endpoint`
- Ensures workspace is complete and ready for future operations
#### `agentrun status`

**Purpose** 
Retrieves the current status of the agent associated with the workspace by querying AgentCube. This includes metadata, endpoint, version, and log reference.

**Behavior Overview**
- The CLI reads the metadata configuration file from the workspace.
- It extracts the agent identifier and queries AgentCube for the latest status.
- The response includes agent details such as ID, name, endpoint, version, and log location.
- This helps developers verify successful publication and retrieve invocation details without leaving the local environment.

**Command Syntax**
```
agentrun status -f <workspace_path> [OPTIONS]
```

**Required Argument**

| Option              | Type  | Description                                                      |
| ------------------- | ----- | ---------------------------------------------------------------- |
| `-f`, `--workspace` | `str` | Path to the agent workspace directory containing metadata config |

**Optional Parameters**

|Option|Type|Description|
|---|---|---|
|`--verbose`|`bool`|Enable detailed logging output|

**Agent Status Output**

| Field            | Description                                                 |
| ---------------- | ----------------------------------------------------------- |
| `agent_id`       | Unique identifier assigned by AgentCube                     |
| `agent_name`     | Human-readable name of the agent                            |
| `agent_endpoint` | Fully qualified URL for invoking the agent                  |
| `latest_version` | Most recently published version of the agent                |
| `log_location`   | Reference to runtime logs (not streamed in initial release) |

#### `agentrun invoke`

**Purpose** Sends a request to a published agent via AgentCube, allowing developers to invoke the agent’s entrypoint method with a custom payload and optional headers.

**Behavior Overview**
- The CLI reads the metadata configuration file to retrieve endpoint, region, version, and authentication details.
- It constructs an HTTP POST request using the provided payload and optional headers.
- The request is sent to AgentCube, which routes it to the correct agent instance.
- The agent processes the payload and returns a response.
- The CLI displays the result to the developer in the terminal.

**Command Syntax**

```
agentrun invoke [OPTIONS]
```

**Optional Parameters**

|Option|Type|Description|
|---|---|---|
|`-f`, `--workspace`|`str`|Path to the agent workspace directory (if not invoking from current directory)|
|`--payload`|`str`|JSON-formatted input passed to the agent’s entrypoint method|
|`--header`|`str`|Custom HTTP headers (e.g., `Authorization`, `Content-Type`)|
|`--verbose`|`bool`|Enable detailed logging output|

**Invocation Workflow**

**Step 1: Load Metadata Configuration**
- Retrieve agent endpoint URL
- Retrieve deployment region
- Retrieve latest version
- Retrieve authorization and authentication details

**Step 2: Build HTTP Request**
- Use endpoint URL from metadata
- Include payload from CLI
- Attach optional headers

**Step 3: Send Request to AgentCube**
- AgentCube routes the request to the correct agent instance

**Step 4: Await Agent Response**

- Agent processes the payload and returns a response

**Step 5: Return Result to Developer**
- CLI displays the response in the terminal

