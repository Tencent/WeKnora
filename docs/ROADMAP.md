# Weknora Roadmap
📌 This roadmap defines the core direction of the project, and its content will be dynamically updated based on requirements and contributions.

## Vision
Independently deploy a personal knowledge base that supports documents, data, and images, expanding more LLM application scenarios based on the traditional RAG framework.

## Next Phase
- Configurable upload file size
- Agent mode: The model automatically determines whether to call functions such as document retrieval and web retrieval
- Configuration management: Configure models, prompts, and enable storage/retrieval modules via the web interface
- Support for more document types (csv, xls, xlsx, html, etc.)
- Vector database support (Milvus, etc.)
- Optimize document parsing speed and accuracy, and improve chunking strategies

## Future
- Expand more calling tools for Agent mode
- Data Agent: Support data statistical analysis
- Batch knowledge management: Import, export, and migration
- Configurable startup modules: Customize optional components to reduce dependencies
- Simplified configuration: Unified management via the web interface, reducing configuration files
- Permission management: Deconstruct permissions for users, tenants, knowledge bases, etc., supporting administrators, user groups, etc.
- Richer chunking modes (semantic chunking, keyword chunking, etc.)
- Expanded parsing tools (minerU, pp-structure, etc.)
- Expanded vector databases (Milvus, etc.)
- Diversified OCR models

## Done
- Multilingual support (Chinese, English, Russian)
- Support for Neo4j graph database
- XSS injection protection
- User login function
- MCP server implementation
- Official Docker image provided, supporting Windows, Linux, and macOS
- Support for Alibaba Cloud model integration

## How to Participate
1. Have ideas for any features? Initiate discussions in Issues (label: `roadmap-discuss`);
2. When submitting a PR, associate it with the corresponding Roadmap phase;
3. Discover a requirement gap? Submit an Issue with the `feature-request` label, and we will include it in the Roadmap after evaluation.

## Change Notice
- This Roadmap will be updated irregularly to sync the latest progress and requirement adjustments;
- Major direction changes will be announced in Issues;
- Priorities will be dynamically adjusted based on user feedback and contributor resources.
