# Copyright The Volcano Authors.
#
# SPDX-License-Identifier: Apache-2.0

# Build from repository root:
#   docker build -f integrations/code-interpreter-mcp/Dockerfile -t agentcube-code-interpreter-mcp:latest .

FROM python:3.12-slim

WORKDIR /app

COPY sdk-python /build/sdk-python
COPY integrations/code-interpreter-mcp /build/mcp

RUN pip install --no-cache-dir /build/sdk-python \
    && pip install --no-cache-dir /build/mcp \
    && rm -rf /build

ENV MCP_TRANSPORT=streamable-http \
    MCP_HOST=0.0.0.0 \
    MCP_PORT=8000

EXPOSE 8000

ENTRYPOINT ["python", "-m", "agentcube_code_interpreter_mcp", "--transport", "streamable-http", "--host", "0.0.0.0", "--port", "8000"]
