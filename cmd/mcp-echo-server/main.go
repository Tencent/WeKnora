// MCP echo server——IntentGate T41 验收专用的最小 MCP 服务（issue #18/#31）。
//
// 单工具 echo(text) 原样回显参数，供「require_approval 复用审批通道」的
// live 验收断言"工具真正执行/未执行"。streamable HTTP 传输，监听
// -addr（默认 :8765）。只做验收用途，不注册进产品容器。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
)

func main() {
	addr := flag.String("addr", ":8765", "listen address")
	flag.Parse()

	server := sdkserver.NewMCPServer("intentgate-echo", "0.1", sdkserver.WithToolCapabilities(false))
	server.AddTool(
		sdkmcp.NewTool("echo", sdkmcp.WithString("text", sdkmcp.Required())),
		func(_ context.Context, request sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return sdkmcp.NewToolResultText("echo:" + request.GetArguments()["text"].(string)), nil
		},
	)
	transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
	log.Printf("intentgate-echo MCP server listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, transport))
}
