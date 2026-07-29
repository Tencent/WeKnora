#!/usr/bin/env python3
"""
手动 MCP 导入检查脚本（非 unittest 测试）。

用法: python check_imports.py
"""

try:
    import mcp.types as types

    print("✓ mcp.types 导入成功")
except ImportError as e:
    print(f"✗ mcp.types 导入失败: {e}")

try:
    from mcp.server import NotificationOptions, Server

    print("✓ mcp.server 导入成功")
except ImportError as e:
    print(f"✗ mcp.server 导入失败: {e}")

try:
    import mcp.server.stdio

    print("✓ mcp.server.stdio 导入成功")
except ImportError as e:
    print(f"✗ mcp.server.stdio 导入失败: {e}")

try:
    from mcp.server.models import InitializationOptions

    print("✓ InitializationOptions 从 mcp.server.models 导入成功")
except ImportError:
    try:
        from mcp import InitializationOptions

        print("✓ InitializationOptions 从 mcp 导入成功")
    except ImportError as e:
        print(f"✗ InitializationOptions 导入失败: {e}")

# 检查 MCP 包结构
import mcp

print(f"\nMCP 包版本: {getattr(mcp, '__version__', '未知')}")
print(f"MCP 包路径: {mcp.__file__}")
print(f"MCP 包内容: {dir(mcp)}")
