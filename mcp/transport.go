package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// Transport 是 JSON-RPC 消息的承载层。Client 只依赖这个接口；
// 现已实现 stdio，HTTP/SSE 将来加一个实现即可，Client 不动（预留位）。
type Transport interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	Notify(ctx context.Context, method string, params any) error
	Close() error
}

// stdioTransport：把 MCP server 跑成子进程，stdin/stdout 走换行分隔的 JSON-RPC。
// 一个常驻 reader goroutine 按 id 分发响应；调用方并发安全。
type stdioTransport struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcResponse

	closed    chan struct{}
	closeOnce sync.Once

	stderrMu sync.Mutex
	stderr   bytes.Buffer // 有界保留，供出错时给上下文
}

func newStdioTransport(command string, args, extraEnv []string) (*stdioTransport, error) {
	cmd := exec.Command(command, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	t := &stdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		pending: map[int]chan rpcResponse{},
		closed:  make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %q: %w", command, err)
	}
	go t.readLoop(stdout)
	go t.drainStderr(stderr)
	return t, nil
}

func (t *stdioTransport) readLoop(stdout io.Reader) {
	defer t.markClosed()
	r := bufio.NewReader(stdout)
	for {
		// ReadBytes 每次返回新分配的切片 —— resp.Result(RawMessage)别名它是安全的。
		line, err := r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var resp rpcResponse
			if json.Unmarshal(trimmed, &resp) == nil && resp.ID != nil {
				t.mu.Lock()
				ch := t.pending[*resp.ID]
				delete(t.pending, *resp.ID)
				t.mu.Unlock()
				if ch != nil {
					ch <- resp
				}
			}
			// 无 id（server 通知/请求）当前切面忽略。
		}
		if err != nil {
			return
		}
	}
}

func (t *stdioTransport) drainStderr(stderr io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		if n > 0 {
			t.stderrMu.Lock()
			// 只保留尾部 ~8KB，避免无界增长
			if t.stderr.Len() > 8192 {
				t.stderr.Reset()
			}
			t.stderr.Write(buf[:n])
			t.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *stdioTransport) stderrTail() string {
	t.stderrMu.Lock()
	defer t.stderrMu.Unlock()
	return t.stderr.String()
}

func (t *stdioTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	ch := make(chan rpcResponse, 1)
	t.pending[id] = ch
	err := t.writeLocked(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	t.mu.Unlock()
	if err != nil {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return nil, fmt.Errorf("mcp: write %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return nil, ctx.Err()
	case <-t.closed:
		return nil, fmt.Errorf("mcp: transport closed before %s response (stderr: %s)", method, t.stderrTail())
	case resp := <-ch:
		if resp.Error != nil {
			return nil, &RPCError{Code: resp.Error.Code, Message: resp.Error.Message}
		}
		return resp.Result, nil
	}
}

func (t *stdioTransport) Notify(ctx context.Context, method string, params any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writeLocked(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *stdioTransport) writeLocked(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = t.stdin.Write(data)
	return err
}

func (t *stdioTransport) markClosed() {
	t.closeOnce.Do(func() { close(t.closed) })
}

func (t *stdioTransport) Close() error {
	_ = t.stdin.Close() // 先关 stdin，让 server 有机会优雅退出
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	_ = t.cmd.Wait()
	t.markClosed()
	return nil
}
