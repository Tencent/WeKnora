FROM python:3.11-slim

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .
RUN pip install --no-cache-dir -e .

ENV MCP_HOST=0.0.0.0
ENV MCP_PORT=8000
ENV WEKNORA_BASE_URL=http://app:8080/api/v1

EXPOSE 8000

CMD ["weknora-mcp-server", "--transport", "http", "--host", "0.0.0.0", "--port", "8000"]
