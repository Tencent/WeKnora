#!/usr/bin/env python3
"""
WeKnora MCP Server 安装脚本
"""

from setuptools import setup
import os

# 读取 README 文件
def read_readme():
    try:
        with open("README.md", "r", encoding="utf-8") as f:
            return f.read()
    except FileNotFoundError:
        return "WeKnora MCP Server - Model Context Protocol server for WeKnora API"

# 读取依赖
def read_requirements():
    try:
        with open("requirements.txt", "r", encoding="utf-8") as f:
            return [line.strip() for line in f if line.strip() and not line.startswith("#")]
    except FileNotFoundError:
        return ["mcp>=1.0.0", "requests>=2.31.0"]

setup(
    name="weknora-mcp-server",
    version="1.0.0",
    author="WeKnora Team",
    author_email="support@weknora.com",
    description="WeKnora MCP Server - Model Context Protocol server for WeKnora API",
    long_description=read_readme(),
    long_description_content_type="text/markdown",
    url="https://github.com/NannaOlympicBroadcast/WeKnoraMCP",
    py_modules=["weknora_mcp_server", "main", "run_server", "run", "test_module"],
    classifiers=[
        "Development Status :: 4 - Beta",
        "Intended Audience :: Developers",
        "License :: OSI Approved :: MIT License",
        "Operating System :: OS Independent",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.8",
        "Programming Language :: Python :: 3.9",
        "Programming Language :: Python :: 3.10",
        "Programming Language :: Python :: 3.11",
        "Programming Language :: Python :: 3.12",
        "Topic :: Software Development :: Libraries :: Python Modules",
        "Topic :: Internet :: WWW/HTTP :: HTTP Servers",
        "Topic :: Scientific/Engineering :: Artificial Intelligence",
    ],
    python_requires=">=3.8",
    install_requires=read_requirements(),
    entry_points={
        "console_scripts": [
            "weknora-mcp-server=main:sync_main",
            "weknora-server=run_server:main",
        ],
    },
    include_package_data=True,
    data_files=[
        ("", ["README.md", "requirements.txt", "LICENSE"]),
    ],
    keywords="mcp model-context-protocol weknora knowledge-management api-server",
)