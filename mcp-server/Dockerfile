FROM python:3.11-slim

WORKDIR /app
COPY requirements.txt .
# Use a mirror by default to avoid PyPI timeouts in CN networks; override with --build-arg.
ARG PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
RUN pip install --no-cache-dir --default-timeout=120 \
    -i "${PIP_INDEX_URL}" \
    -r requirements.txt

COPY . .
RUN pip install --no-cache-dir --default-timeout=120 \
    -i "${PIP_INDEX_URL}" \
    -e .

ENV MCP_HOST=0.0.0.0
ENV MCP_PORT=8087
ENV WEKNORA_BASE_URL=http://app:8088/api/v1
# Set MCP_SERVER_AUTH_TOKEN at runtime; HTTP transport refuses to start without it.

EXPOSE 8087

CMD ["weknora-mcp-server", "--transport", "http", "--host", "0.0.0.0", "--port", "8087"]
