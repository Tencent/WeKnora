#!/usr/bin/env python3
"""
WeKnora MCP Server 主入口点

这个文件提供了一个统一的入口点来启动 WeKnora MCP 服务器。
可以通过多种方式运行：
1. python main.py
2. python -m weknora_mcp_server
3. weknora-mcp-server (安装后)
"""

import os
import sys
import asyncio
import argparse
from pathlib import Path

def setup_environment():
    """设置环境和路径"""
    # 确保当前目录在 Python 路径中
    current_dir = Path(__file__).parent.absolute()
    if str(current_dir) not in sys.path:
        sys.path.insert(0, str(current_dir))

def check_dependencies():
    """检查依赖是否已安装"""
    try:
        import mcp
        import requests
        return True
    except ImportError as e:
        print(f"缺少依赖: {e}")
        print("请运行: pip install -r requirements.txt")
        return False

def check_environment_variables():
    """检查环境变量配置"""
    base_url = os.getenv("WEKNORA_BASE_URL")
    api_key = os.getenv("WEKNORA_API_KEY")
    
    print("=== WeKnora MCP Server 环境检查 ===")
    print(f"Base URL: {base_url or 'http://localhost:8080/api/v1 (默认)'}")
    print(f"API Key: {'已设置' if api_key else '未设置 (警告)'}")
    
    if not base_url:
        print("提示: 可以设置 WEKNORA_BASE_URL 环境变量")
    
    if not api_key:
        print("警告: 建议设置 WEKNORA_API_KEY 环境变量")
    
    print("=" * 40)
    return True

def parse_arguments():
    """解析命令行参数"""
    parser = argparse.ArgumentParser(
        description="WeKnora MCP Server - Model Context Protocol server for WeKnora API",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
示例:
  python main.py                    # 使用默认配置启动
  python main.py --check-only       # 仅检查环境，不启动服务器
  python main.py --verbose          # 启用详细日志
  
环境变量:
  WEKNORA_BASE_URL    WeKnora API 基础 URL (默认: http://localhost:8080/api/v1)
  WEKNORA_API_KEY     WeKnora API 密钥
        """
    )
    
    parser.add_argument(
        "--check-only",
        action="store_true",
        help="仅检查环境配置，不启动服务器"
    )
    
    parser.add_argument(
        "--verbose", "-v",
        action="store_true",
        help="启用详细日志输出"
    )
    
    parser.add_argument(
        "--version",
        action="version",
        version="WeKnora MCP Server 1.0.0"
    )
    
    return parser.parse_args()

async def main():
    """主函数"""
    args = parse_arguments()
    
    # 设置环境
    setup_environment()
    
    # 检查依赖
    if not check_dependencies():
        sys.exit(1)
    
    # 检查环境变量
    check_environment_variables()
    
    # 如果只是检查环境，则退出
    if args.check_only:
        print("环境检查完成。")
        return
    
    # 设置日志级别
    if args.verbose:
        import logging
        logging.basicConfig(level=logging.DEBUG)
        print("已启用详细日志模式")
    
    try:
        print("正在启动 WeKnora MCP Server...")
        
        # 导入并运行服务器
        from weknora_mcp_server import run
        await run()
        
    except ImportError as e:
        print(f"导入错误: {e}")
        print("请确保所有文件都在正确的位置")
        sys.exit(1)
    except KeyboardInterrupt:
        print("\n服务器已停止")
    except Exception as e:
        print(f"服务器运行错误: {e}")
        if args.verbose:
            import traceback
            traceback.print_exc()
        sys.exit(1)

def sync_main():
    """同步版本的主函数，用于 entry_points"""
    asyncio.run(main())

if __name__ == "__main__":
    asyncio.run(main())