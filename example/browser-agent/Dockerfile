# Browser Agent Image
# Build context: repository root
#
# Build:
#   docker build -t browser-agent:latest -f example/browser-agent/Dockerfile .

FROM ghcr.io/astral-sh/uv:python3.12-bookworm-slim

WORKDIR /app

COPY example/browser-agent/requirements.txt ./
RUN uv venv && uv pip install -r requirements.txt

COPY example/browser-agent/browser_agent.py ./

ENV PYTHONPATH="/app" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

EXPOSE 8000

CMD ["uv", "run", "browser_agent.py"]
