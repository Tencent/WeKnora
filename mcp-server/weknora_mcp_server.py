#!/usr/bin/env python3
"""
WeKnora MCP Server

A Model Context Protocol server that provides access to the WeKnora knowledge management API.
"""

import os
import json
import logging
from typing import Dict, List, Any, Optional
from datetime import datetime
import requests
from requests.exceptions import RequestException
import mcp.server.stdio
import mcp.types as types
from mcp.server import NotificationOptions, Server
from mcp.server.models import InitializationOptions

# Set up logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Configuration
WEKNORA_BASE_URL = os.getenv("WEKNORA_BASE_URL", "http://localhost:8080/api/v1")
WEKNORA_API_KEY = os.getenv("WEKNORA_API_KEY", "")

class WeKnoraClient:
    """Client for interacting with WeKnora API"""
    
    def __init__(self, base_url: str, api_key: str):
        self.base_url = base_url
        self.api_key = api_key
        self.session = requests.Session()
        self.session.headers.update({
            "X-API-Key": api_key,
            "Content-Type": "application/json"
        })
    
    def _request(self, method: str, endpoint: str, **kwargs) -> Dict[str, Any]:
        """Make a request to the WeKnora API"""
        url = f"{self.base_url}{endpoint}"
        try:
            response = self.session.request(method, url, **kwargs)
            response.raise_for_status()
            return response.json()
        except RequestException as e:
            logger.error(f"API request failed: {e}")
            raise
    
    # Tenant Management
    def create_tenant(self, name: str, description: str, business: str, retriever_engines: Dict) -> Dict:
        """Create a new tenant"""
        data = {
            "name": name,
            "description": description,
            "business": business,
            "retriever_engines": retriever_engines
        }
        return self._request("POST", "/tenants", json=data)
    
    def get_tenant(self, tenant_id: str) -> Dict:
        """Get tenant information"""
        return self._request("GET", f"/tenants/{tenant_id}")
    
    def list_tenants(self) -> Dict:
        """List all tenants"""
        return self._request("GET", "/tenants")
    
    # Knowledge Base Management
    def create_knowledge_base(self, name: str, description: str, config: Dict) -> Dict:
        """Create a new knowledge base"""
        data = {
            "name": name,
            "description": description,
            **config
        }
        return self._request("POST", "/knowledge-bases", json=data)
    
    def list_knowledge_bases(self) -> Dict:
        """List all knowledge bases"""
        return self._request("GET", "/knowledge-bases")
    
    def get_knowledge_base(self, kb_id: str) -> Dict:
        """Get knowledge base details"""
        return self._request("GET", f"/knowledge-bases/{kb_id}")
    
    def update_knowledge_base(self, kb_id: str, updates: Dict) -> Dict:
        """Update knowledge base"""
        return self._request("PUT", f"/knowledge-bases/{kb_id}", json=updates)
    
    def delete_knowledge_base(self, kb_id: str) -> Dict:
        """Delete knowledge base"""
        return self._request("DELETE", f"/knowledge-bases/{kb_id}")
    
    def hybrid_search(self, kb_id: str, query: str, config: Dict) -> Dict:
        """Perform hybrid search in knowledge base"""
        data = {
            "query_text": query,
            **config
        }
        return self._request("GET", f"/knowledge-bases/{kb_id}/hybrid-search", json=data)
    
    # Knowledge Management
    def create_knowledge_from_file(self, kb_id: str, file_path: str, enable_multimodel: bool = True) -> Dict:
        """Create knowledge from file"""
        with open(file_path, 'rb') as f:
            files = {'file': f}
            data = {'enable_multimodel': str(enable_multimodel).lower()}
            # Temporarily remove Content-Type for multipart request
            headers = self.session.headers.copy()
            del headers['Content-Type']
            response = requests.post(
                f"{self.base_url}/knowledge-bases/{kb_id}/knowledge/file",
                headers=headers,
                files=files,
                data=data
            )
            response.raise_for_status()
            return response.json()
    
    def create_knowledge_from_url(self, kb_id: str, url: str, enable_multimodel: bool = True) -> Dict:
        """Create knowledge from URL"""
        data = {
            "url": url,
            "enable_multimodel": enable_multimodel
        }
        return self._request("POST", f"/knowledge-bases/{kb_id}/knowledge/url", json=data)
    
    def list_knowledge(self, kb_id: str, page: int = 1, page_size: int = 20) -> Dict:
        """List knowledge in a knowledge base"""
        params = {"page": page, "page_size": page_size}
        return self._request("GET", f"/knowledge-bases/{kb_id}/knowledge", params=params)
    
    def get_knowledge(self, knowledge_id: str) -> Dict:
        """Get knowledge details"""
        return self._request("GET", f"/knowledge/{knowledge_id}")
    
    def delete_knowledge(self, knowledge_id: str) -> Dict:
        """Delete knowledge"""
        return self._request("DELETE", f"/knowledge/{knowledge_id}")
    
    # Model Management
    def create_model(self, name: str, model_type: str, source: str, description: str, parameters: Dict, is_default: bool = False) -> Dict:
        """Create a new model"""
        data = {
            "name": name,
            "type": model_type,
            "source": source,
            "description": description,
            "parameters": parameters,
            "is_default": is_default
        }
        return self._request("POST", "/models", json=data)
    
    def list_models(self) -> Dict:
        """List all models"""
        return self._request("GET", "/models")
    
    def get_model(self, model_id: str) -> Dict:
        """Get model details"""
        return self._request("GET", f"/models/{model_id}")
    
    # Session Management
    def create_session(self, kb_id: str, strategy: Dict) -> Dict:
        """Create a new chat session"""
        data = {
            "knowledge_base_id": kb_id,
            "session_strategy": strategy
        }
        return self._request("POST", "/sessions", json=data)
    
    def get_session(self, session_id: str) -> Dict:
        """Get session details"""
        return self._request("GET", f"/sessions/{session_id}")
    
    def list_sessions(self, page: int = 1, page_size: int = 20) -> Dict:
        """List sessions"""
        params = {"page": page, "page_size": page_size}
        return self._request("GET", "/sessions", params=params)
    
    def delete_session(self, session_id: str) -> Dict:
        """Delete session"""
        return self._request("DELETE", f"/sessions/{session_id}")
    
    # Chat Functionality
    def chat(self, session_id: str, query: str) -> Dict:
        """Send a chat message"""
        data = {"query": query}
        # Note: This returns SSE stream, simplified here
        return self._request("POST", f"/knowledge-chat/{session_id}", json=data)
    
    # Chunk Management
    def list_chunks(self, knowledge_id: str, page: int = 1, page_size: int = 20) -> Dict:
        """List chunks of knowledge"""
        params = {"page": page, "page_size": page_size}
        return self._request("GET", f"/chunks/{knowledge_id}", params=params)
    
    def delete_chunk(self, knowledge_id: str, chunk_id: str) -> Dict:
        """Delete a chunk"""
        return self._request("DELETE", f"/chunks/{knowledge_id}/{chunk_id}")

# Initialize MCP server
app = Server("weknora-server")
client = WeKnoraClient(WEKNORA_BASE_URL, WEKNORA_API_KEY)

# Tool definitions
@app.list_tools()
async def handle_list_tools() -> list[types.Tool]:
    """List all available WeKnora tools"""
    return [
        # Tenant Management
        types.Tool(
            name="create_tenant",
            description="Create a new tenant in WeKnora",
            inputSchema={
                "type": "object",
                "properties": {
                    "name": {"type": "string", "description": "Tenant name"},
                    "description": {"type": "string", "description": "Tenant description"},
                    "business": {"type": "string", "description": "Business type"},
                    "retriever_engines": {
                        "type": "object",
                        "description": "Retriever engine configuration",
                        "properties": {
                            "engines": {
                                "type": "array",
                                "items": {
                                    "type": "object",
                                    "properties": {
                                        "retriever_type": {"type": "string"},
                                        "retriever_engine_type": {"type": "string"}
                                    }
                                }
                            }
                        }
                    }
                },
                "required": ["name", "description", "business"]
            }
        ),
        types.Tool(
            name="list_tenants",
            description="List all tenants",
            inputSchema={"type": "object", "properties": {}}
        ),
        
        # Knowledge Base Management
        types.Tool(
            name="create_knowledge_base",
            description="Create a new knowledge base",
            inputSchema={
                "type": "object",
                "properties": {
                    "name": {"type": "string", "description": "Knowledge base name"},
                    "description": {"type": "string", "description": "Knowledge base description"},
                    "embedding_model_id": {"type": "string", "description": "Embedding model ID"},
                    "summary_model_id": {"type": "string", "description": "Summary model ID"}
                },
                "required": ["name", "description"]
            }
        ),
        types.Tool(
            name="list_knowledge_bases",
            description="List all knowledge bases",
            inputSchema={"type": "object", "properties": {}}
        ),
        types.Tool(
            name="get_knowledge_base",
            description="Get knowledge base details",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"}
                },
                "required": ["kb_id"]
            }
        ),
        types.Tool(
            name="delete_knowledge_base",
            description="Delete a knowledge base",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"}
                },
                "required": ["kb_id"]
            }
        ),
        types.Tool(
            name="hybrid_search",
            description="Perform hybrid search in knowledge base",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "query": {"type": "string", "description": "Search query"},
                    "vector_threshold": {"type": "number", "description": "Vector similarity threshold", "default": 0.5},
                    "keyword_threshold": {"type": "number", "description": "Keyword match threshold", "default": 0.3},
                    "match_count": {"type": "integer", "description": "Number of results to return", "default": 5}
                },
                "required": ["kb_id", "query"]
            }
        ),
        
        # Knowledge Management
        types.Tool(
            name="create_knowledge_from_file",
            description="Create knowledge from a local file on the server filesystem",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "file_path": {"type": "string", "description": "Absolute path to the local file on the server"},
                    "enable_multimodel": {"type": "boolean", "description": "Enable multimodal processing", "default": True}
                },
                "required": ["kb_id", "file_path"]
            }
        ),
        types.Tool(
            name="create_knowledge_from_url",
            description="Create knowledge from URL",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "url": {"type": "string", "description": "URL to create knowledge from"},
                    "enable_multimodel": {"type": "boolean", "description": "Enable multimodal processing", "default": True}
                },
                "required": ["kb_id", "url"]
            }
        ),
        types.Tool(
            name="list_knowledge",
            description="List knowledge in a knowledge base",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "page": {"type": "integer", "description": "Page number", "default": 1},
                    "page_size": {"type": "integer", "description": "Page size", "default": 20}
                },
                "required": ["kb_id"]
            }
        ),
        types.Tool(
            name="get_knowledge",
            description="Get knowledge details",
            inputSchema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"}
                },
                "required": ["knowledge_id"]
            }
        ),
        types.Tool(
            name="delete_knowledge",
            description="Delete knowledge",
            inputSchema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"}
                },
                "required": ["knowledge_id"]
            }
        ),
        
        # Model Management
        types.Tool(
            name="create_model",
            description="Create a new model",
            inputSchema={
                "type": "object",
                "properties": {
                    "name": {"type": "string", "description": "Model name"},
                    "type": {"type": "string", "description": "Model type (KnowledgeQA, Embedding, Rerank)"},
                    "source": {"type": "string", "description": "Model source", "default": "local"},
                    "description": {"type": "string", "description": "Model description"},
                    "base_url": {"type": "string", "description": "Model API base URL", "default": ""},
                    "api_key": {"type": "string", "description": "Model API key", "default": ""},
                    "is_default": {"type": "boolean", "description": "Set as default model", "default": False}
                },
                "required": ["name", "type", "description"]
            }
        ),
        types.Tool(
            name="list_models",
            description="List all models",
            inputSchema={"type": "object", "properties": {}}
        ),
        types.Tool(
            name="get_model",
            description="Get model details",
            inputSchema={
                "type": "object",
                "properties": {
                    "model_id": {"type": "string", "description": "Model ID"}
                },
                "required": ["model_id"]
            }
        ),
        
        # Session Management
        types.Tool(
            name="create_session",
            description="Create a new chat session",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "max_rounds": {"type": "integer", "description": "Maximum conversation rounds", "default": 5},
                    "enable_rewrite": {"type": "boolean", "description": "Enable query rewriting", "default": True},
                    "fallback_response": {"type": "string", "description": "Fallback response", "default": "Sorry, I cannot answer this question."},
                    "summary_model_id": {"type": "string", "description": "Summary model ID"}
                },
                "required": ["kb_id"]
            }
        ),
        types.Tool(
            name="get_session",
            description="Get session details",
            inputSchema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID"}
                },
                "required": ["session_id"]
            }
        ),
        types.Tool(
            name="list_sessions",
            description="List chat sessions",
            inputSchema={
                "type": "object",
                "properties": {
                    "page": {"type": "integer", "description": "Page number", "default": 1},
                    "page_size": {"type": "integer", "description": "Page size", "default": 20}
                }
            }
        ),
        types.Tool(
            name="delete_session",
            description="Delete a session",
            inputSchema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID"}
                },
                "required": ["session_id"]
            }
        ),
        
        # Chat Functionality
        types.Tool(
            name="chat",
            description="Send a chat message to a session",
            inputSchema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID"},
                    "query": {"type": "string", "description": "User query"}
                },
                "required": ["session_id", "query"]
            }
        ),
        
        # Chunk Management
        types.Tool(
            name="list_chunks",
            description="List chunks of knowledge",
            inputSchema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"},
                    "page": {"type": "integer", "description": "Page number", "default": 1},
                    "page_size": {"type": "integer", "description": "Page size", "default": 20}
                },
                "required": ["knowledge_id"]
            }
        ),
        types.Tool(
            name="delete_chunk",
            description="Delete a chunk",
            inputSchema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"},
                    "chunk_id": {"type": "string", "description": "Chunk ID"}
                },
                "required": ["knowledge_id", "chunk_id"]
            }
        )
    ]

@app.call_tool()
async def handle_call_tool(
    name: str, arguments: dict | None
) -> list[types.TextContent | types.ImageContent | types.EmbeddedResource]:
    """Handle tool execution"""
    
    try:
        args = arguments or {}
        
        # Tenant Management
        if name == "create_tenant":
            result = client.create_tenant(
                args["name"],
                args["description"],
                args["business"],
                args.get("retriever_engines", {
                    "engines": [
                        {"retriever_type": "keywords", "retriever_engine_type": "postgres"},
                        {"retriever_type": "vector", "retriever_engine_type": "postgres"}
                    ]
                })
            )
        elif name == "list_tenants":
            result = client.list_tenants()
        
        # Knowledge Base Management
        elif name == "create_knowledge_base":
            config = {
                "chunking_config": args.get("chunking_config", {
                    "chunk_size": 1000,
                    "chunk_overlap": 200,
                    "separators": ["."],
                    "enable_multimodal": True
                }),
                "embedding_model_id": args.get("embedding_model_id", ""),
                "summary_model_id": args.get("summary_model_id", "")
            }
            result = client.create_knowledge_base(
                args["name"],
                args["description"],
                config
            )
        elif name == "list_knowledge_bases":
            result = client.list_knowledge_bases()
        elif name == "get_knowledge_base":
            result = client.get_knowledge_base(args["kb_id"])
        elif name == "delete_knowledge_base":
            result = client.delete_knowledge_base(args["kb_id"])
        elif name == "hybrid_search":
            config = {
                "vector_threshold": args.get("vector_threshold", 0.5),
                "keyword_threshold": args.get("keyword_threshold", 0.3),
                "match_count": args.get("match_count", 5)
            }
            result = client.hybrid_search(args["kb_id"], args["query"], config)
        
        # Knowledge Management
        elif name == "create_knowledge_from_file":
            result = client.create_knowledge_from_file(
                args["kb_id"],
                args["file_path"],
                args.get("enable_multimodel", True)
            )
        elif name == "create_knowledge_from_url":
            result = client.create_knowledge_from_url(
                args["kb_id"],
                args["url"],
                args.get("enable_multimodel", True)
            )
        elif name == "list_knowledge":
            result = client.list_knowledge(
                args["kb_id"],
                args.get("page", 1),
                args.get("page_size", 20)
            )
        elif name == "get_knowledge":
            result = client.get_knowledge(args["knowledge_id"])
        elif name == "delete_knowledge":
            result = client.delete_knowledge(args["knowledge_id"])
        
        # Model Management
        elif name == "create_model":
            parameters = {
                "base_url": args.get("base_url", ""),
                "api_key": args.get("api_key", "")
            }
            result = client.create_model(
                args["name"],
                args["type"],
                args.get("source", "local"),
                args["description"],
                parameters,
                args.get("is_default", False)
            )
        elif name == "list_models":
            result = client.list_models()
        elif name == "get_model":
            result = client.get_model(args["model_id"])
        
        # Session Management
        elif name == "create_session":
            strategy = {
                "max_rounds": args.get("max_rounds", 5),
                "enable_rewrite": args.get("enable_rewrite", True),
                "fallback_strategy": "FIXED_RESPONSE",
                "fallback_response": args.get("fallback_response", "Sorry, I cannot answer this question."),
                "embedding_top_k": 10,
                "keyword_threshold": 0.5,
                "vector_threshold": 0.7,
                "summary_model_id": args.get("summary_model_id", "")
            }
            result = client.create_session(args["kb_id"], strategy)
        elif name == "get_session":
            result = client.get_session(args["session_id"])
        elif name == "list_sessions":
            result = client.list_sessions(
                args.get("page", 1),
                args.get("page_size", 20)
            )
        elif name == "delete_session":
            result = client.delete_session(args["session_id"])
        
        # Chat Functionality
        elif name == "chat":
            result = client.chat(args["session_id"], args["query"])
        
        # Chunk Management
        elif name == "list_chunks":
            result = client.list_chunks(
                args["knowledge_id"],
                args.get("page", 1),
                args.get("page_size", 20)
            )
        elif name == "delete_chunk":
            result = client.delete_chunk(args["knowledge_id"], args["chunk_id"])
        
        else:
            return [types.TextContent(
                type="text",
                text=f"Unknown tool: {name}"
            )]
        
        return [types.TextContent(
            type="text",
            text=json.dumps(result, indent=2, ensure_ascii=False)
        )]
        
    except Exception as e:
        logger.error(f"Tool execution failed: {e}")
        return [types.TextContent(
            type="text",
            text=f"Error executing {name}: {str(e)}"
        )]

async def run():
    """Run the MCP server"""
    async with mcp.server.stdio.stdio_server() as (read_stream, write_stream):
        await app.run(
            read_stream,
            write_stream,
            InitializationOptions(
                server_name="weknora-server",
                server_version="1.0.0",
                capabilities=app.get_capabilities(
                    notification_options=NotificationOptions(),
                    experimental_capabilities={},
                ),
            ),
        )

def main():
    """主函数入口点，用于 console_scripts"""
    import asyncio
    asyncio.run(run())

if __name__ == "__main__":
    main()
