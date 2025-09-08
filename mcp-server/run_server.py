#!/usr/bin/env python3
"""
WeKnora MCP Server 启动脚本
"""

import os
import sys
import asyncio

def check_environment():
    """检查环境配置"""
    base_url = os.getenv("WEKNORA_BASE_URL")
    api_key = os.getenv("WEKNORA_API_KEY")
    
    if not base_url:
        print("警告: WEKNORA_BASE_URL 环境变量未设置，使用默认值: http://localhost:8080/api/v1")
    
    if not api_key:
        print("警告: WEKNORA_API_KEY 环境变量未设置")
    
    print(f"WeKnora Base URL: {base_url or 'http://localhost:8080/api/v1'}")
    print(f"API Key: {'已设置' if api_key else '未设置'}")

def main():
    """主函数"""
    print("启动 WeKnora MCP Server...")
    check_environment()
    
    try:
        from weknora_mcp_server import run
        asyncio.run(run())
    except ImportError as e:
        print(f"导入错误: {e}")
        print("请确保已安装所有依赖: pip install -r requirements.txt")
        sys.exit(1)
    except KeyboardInterrupt:
        print("\n服务器已停止")
    except Exception as e:
        print(f"服务器运行错误: {e}")
        sys.exit(1)

if __name__ == "__main__":
    main()