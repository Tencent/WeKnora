FROM python:3.11-slim

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .
RUN pip install --no-cache-dir -e .

ENV MCP_HOST=0.0.0.0
ENV MCP_PORT=8000
ENV WEKNORA_BASE_URL=http://app:8080/api/v1
# Clients present their own WeKnora API keys (Authorization: Bearer or
# X-WeKnora-Key); WEKNORA_API_KEY is the fallback identity, and
# MCP_REQUIRE_USER_KEY=1 rejects requests without a personal key.

EXPOSE 8000

CMD ["weknora-mcp-server", "--transport", "http", "--host", "0.0.0.0", "--port", "8000"]
