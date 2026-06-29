package mcp_test

// Stage B(expose) 验证 —— 自环闭合，hermetic（无 npx/无 LLM/无网络）：
// 用 stage A 的 mcp.Client 连 stage B 的 mcp.Server（in-process io.Pipe），
// 证明 consume 客户端 ↔ expose 服务端 协议互通：initialize / tools/list / tools/call 全程跑通。
//
// 这同时证明了 stage C：server 端经 tool.DefinitionOf 对外暴露 JSON Schema，client 端原样收到。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tuxi/flux/mcp"
	"github.com/tuxi/flux/tool"
	"github.com/tuxi/flux/tool/builtin"
)

// pipeTransport 把 mcp.Client 接到一个 in-process mcp.Server 上（同步实现，测试串行调用）。
type pipeTransport struct {
	w  io.Writer
	r  *bufio.Reader
	id int
}

type rpcResp struct {
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (t *pipeTransport) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	t.id++
	id := t.id
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := t.w.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	for {
		line, err := t.r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var resp rpcResp
			if json.Unmarshal(trimmed, &resp) == nil && resp.ID != nil && *resp.ID == id {
				if resp.Error != nil {
					return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
				}
				return resp.Result, nil
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

func (t *pipeTransport) Notify(_ context.Context, method string, params any) error {
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	_, err := t.w.Write(append(req, '\n'))
	return err
}

func (t *pipeTransport) Close() error { return nil }

func TestServer_ExposeRegistry_RoundTrip(t *testing.T) {
	// 暴露一个 registry：merge_result（同步、回显 input，便于断言）
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())
	srv := mcp.NewServer(reg)

	csR, csW := io.Pipe() // client → server
	scR, scW := io.Pipe() // server → client

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serveDone := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, csR, scW)
		_ = scW.Close()
		close(serveDone)
	}()
	defer func() {
		_ = csW.Close() // 关 client→server，server 读到 EOF 后 Serve 返回
		<-serveDone
	}()

	pt := &pipeTransport{w: csW, r: bufio.NewReader(scR)}
	client, err := mcp.NewClient(ctx, pt) // 真实 stage A 客户端，跑 initialize 握手
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Logf("握手成功：server=%s v%s proto=%s", client.ServerName, client.ServerVersion, client.ProtocolVersion)
	if client.ServerName != "flux" {
		t.Fatalf("serverInfo.name 应为 flux，得 %q", client.ServerName)
	}

	// tools/list：应有 merge_result，且 InputSchema 是合法 JSON Schema（经 DefinitionOf 产出）
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var found *mcp.ToolInfo
	for i := range tools {
		if tools[i].Name == "merge_result" {
			found = &tools[i]
		}
	}
	if found == nil {
		t.Fatalf("应暴露 merge_result，实际 %d 个工具", len(tools))
	}
	if !json.Valid(found.InputSchema) {
		t.Fatalf("merge_result InputSchema 应是合法 JSON Schema，得 %s", found.InputSchema)
	}
	t.Logf("✅ tools/list 暴露 merge_result，schema=%s", found.InputSchema)

	// tools/call：merge_result 回显 input
	res, err := client.CallTool(ctx, "merge_result", map[string]any{"hello": "world"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("不应 isError: %s", res.Text())
	}
	if !strings.Contains(res.Text(), "hello") || !strings.Contains(res.Text(), "world") {
		t.Fatalf("merge_result 应回显 input，得 %q", res.Text())
	}
	t.Logf("✅ tools/call merge_result 回显: %s", res.Text())

	// 未知工具：按 MCP 约定走 isError 结果，而非协议错误
	bad, err := client.CallTool(ctx, "does_not_exist", nil)
	if err != nil {
		t.Fatalf("未知工具不应是传输错误: %v", err)
	}
	if !bad.IsError {
		t.Fatal("未知工具应 isError=true")
	}
	t.Logf("✅ 未知工具 → isError 结果（非协议崩溃）: %s", bad.Text())
}
