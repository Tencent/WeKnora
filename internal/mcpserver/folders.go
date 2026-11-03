package mcpserver

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
)

func requestFolder(req mcp.CallToolRequest) (string, error) {
	raw, ok := req.GetArguments()["folder_path"]
	if !ok {
		return "", nil
	}
	folder, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("folder_path must be a string")
	}
	return types.ValidateKnowledgeRelativeFolder(folder)
}
