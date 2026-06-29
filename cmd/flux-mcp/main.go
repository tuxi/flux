// Command flux-mcp 把 Flux 的内置工具暴露成一个 MCP server（stdio）。
//
// 这是主线二 stage B(expose) 的真实可用产物：把它挂进 Claude Code / Codex 等 MCP 客户端，
// 就能从那些客户端调用 Flux 的工具。
//
//	go build -o flux-mcp ./cmd/flux-mcp
//
// Claude Code 的 mcp 配置示例（工作目录作为参数传入）：
//
//	{ "command": "/abs/path/flux-mcp", "args": ["/abs/work/dir"] }
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tuxi/flux/mcp"
	"github.com/tuxi/flux/tool"
	"github.com/tuxi/flux/tool/builtin"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())
	reg.Register(builtin.NewWriteFileTool(dir))
	reg.Register(builtin.NewCompileTool(dir))
	reg.Register(builtin.NewShellTool(dir)) // 解锁视频/媒体剪辑等 shell 节点（ffmpeg 等）

	srv := mcp.NewServer(reg)
	if err := srv.ServeStdio(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "flux-mcp:", err)
		os.Exit(1)
	}
}
