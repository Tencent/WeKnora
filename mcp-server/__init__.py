#!/usr/bin/env python3
"""
WeKnora MCP Server Package

A Model Context Protocol server that provides access to the WeKnora knowledge management API.
"""

__version__ = "1.0.0"
__author__ = "WeKnora Team"
__description__ = "WeKnora MCP Server - Model Context Protocol server for WeKnora API"

from .weknora_mcp_server import WeKnoraClient, run

__all__ = ["WeKnoraClient", "run"]