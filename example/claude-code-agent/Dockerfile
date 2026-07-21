# Claude Code AgentRuntime example image.
# Build context: repository root
#
# Build:
#   docker build -t claude-code-agent:latest -f example/claude-code-agent/Dockerfile .

FROM ghcr.io/astral-sh/uv:python3.12-bookworm-slim

WORKDIR /app

ARG NODE_VERSION=22.11.0
ARG TARGETARCH
ARG APT_MIRROR=https://mirrors.aliyun.com/debian
ARG APT_SECURITY_MIRROR=https://mirrors.aliyun.com/debian-security
ARG NODE_DIST_BASE=https://npmmirror.com/mirrors/node
ARG NPM_REGISTRY=https://registry.npmmirror.com
ARG PYPI_INDEX_URL=https://mirrors.aliyun.com/pypi/simple/
RUN sed -i "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g; s|http://deb.debian.org/debian|${APT_MIRROR}|g" /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git xz-utils \
    && rm -rf /var/lib/apt/lists/* \
    && case "${TARGETARCH}" in \
        arm64) node_arch="arm64" ;; \
        amd64) node_arch="x64" ;; \
        *) echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL "${NODE_DIST_BASE}/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" \
        | tar -xJ --strip-components=1 -C /usr/local \
    && npm config set registry "${NPM_REGISTRY}" \
    && npm install -g @anthropic-ai/claude-code

COPY example/claude-code-agent/requirements.txt ./
RUN uv pip install --system --no-cache --index-url "${PYPI_INDEX_URL}" -r requirements.txt

COPY example/claude-code-agent/agent.py ./

RUN mkdir -p /workspace

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    WORKSPACE_DIR=/workspace \
    PORT=8080

EXPOSE 8080

CMD ["python", "agent.py"]
