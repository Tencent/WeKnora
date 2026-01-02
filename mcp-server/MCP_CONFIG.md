# uv를 사용하여 WeKnora MCP 서버 실행

> Python 기반 MCP 서비스를 실행하려면 `uv`를 사용하는 것이 좋습니다.

## 1. uv 설치

```bash
# macOS/Linux
curl -LsSf https://astral.sh/uv/install.sh | sh

# 또는 Homebrew 사용 (macOS)
brew install uv

# Windows
powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"
```

## 2. MCP 클라이언트 구성

### Claude Desktop 구성

Claude Desktop 설정에 다음을 추가합니다:

```json
{
  "mcpServers": {
    "weknora": {
      "args": [
        "--directory",
        "/path/WeKnora/mcp-server",
        "run",
        "run_server.py"
      ],
      "command": "uv",
      "env": {
        "WEKNORA_API_KEY": "your_api_key_here",
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1"
      }
    }
  }
}
```

### Cursor 구성

Cursor에서 MCP 구성 파일(일반적으로 `~/.cursor/mcp-config.json`)을 편집합니다:

```json
{
  "mcpServers": {
    "weknora": {
      "command": "uv",
      "args": [
        "--directory",
        "/path/WeKnora/mcp-server",
        "run",
        "run_server.py"
      ],
      "env": {
        "WEKNORA_API_KEY": "your_api_key_here",
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1"
      }
    }
  }
}
```

### KiloCode 구성

KiloCode 또는 MCP를 지원하는 다른 편집기의 경우 다음과 같이 구성합니다:

```json
{
  "mcpServers": {
    "weknora": {
      "command": "uv",
      "args": [
        "--directory",
        "/path/WeKnora/mcp-server",
        "run",
        "run_server.py"
      ],
      "env": {
        "WEKNORA_API_KEY": "your_api_key_here",
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1"
      }
    }
  }
}
```

### 기타 MCP 클라이언트

일반적인 MCP 클라이언트 구성:

```json
{
  "mcpServers": {
    "weknora": {
      "command": "uv",
      "args": [
        "--directory",
        "/path/WeKnora/mcp-server",
        "run",
        "run_server.py"
      ],
      "env": {
        "WEKNORA_API_KEY": "your_api_key_here",
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1"
      }
    }
  }
}
```
