package builtin

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"flux/tool"
)

// ReadFileTool 读取工作目录下的 UTF-8 文本文件。每行带行号前缀，支持 offset/limit 窗口读取。
// 大型文件拒绝全读并提示用 offset/limit —— 专为 LLM 设计。
//
// 这是对标 MCP read_text_file 的本地工具，保留了原作者的全部核心设计：行号右对齐、
// 大文件预算拒绝、窗口截断提示、路径穿越防御、UTF-8 校验。
type ReadFileTool struct {
	baseDir string
	MaxBytes int64
}

func NewReadFileTool(baseDir string) *ReadFileTool {
	return &ReadFileTool{baseDir: baseDir, MaxBytes: 200_000}
}

func (ReadFileTool) Name() string { return "read_file" }

func (ReadFileTool) Description() string {
	return "读取工作目录下的 UTF-8 文本文件。每行带 `行号\\t` 前缀（行号仅供引用，不在文件内容中）。" +
		"用 offset（起始行，1-based）和 limit（行数）可窗口读大文件——比全读更高效且不会爆上下文。"
}

func (ReadFileTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"path":   {Type: "string", Required: true, Desc: "相对工作目录的文件路径"},
		"offset": {Type: "integer", Required: false, Desc: "起始行号（1-based），默认 1"},
		"limit":  {Type: "integer", Required: false, Desc: "从 offset 起读多少行，默认读到底"},
	}}
}

func (ReadFileTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"content":    {Type: "string", Desc: "带行号的文本内容"},
		"totalLines": {Type: "integer", Desc: "文件总行数"},
		"truncated":  {Type: "boolean", Desc: "输出是否被截断"},
	}}
}

func (r ReadFileTool) Execute(_ context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	rel, _ := input["path"].(string)
	if rel == "" {
		return tool.Fail(fmt.Errorf("read_file: path 不能为空")), nil
	}

	// 路径穿越防御：clean + 前缀校验
	target := filepath.Clean(filepath.Join(r.baseDir, rel))
	if !strings.HasPrefix(target, r.baseDir) && target != r.baseDir {
		return tool.Fail(fmt.Errorf("path escapes workspace: %s", rel)), nil
	}

	info, err := os.Stat(target)
	if err != nil {
		return tool.Fail(err), nil
	}
	if info.IsDir() {
		return tool.Fail(fmt.Errorf("path 是目录: %s", rel)), nil
	}

	startLine := toInt(input["offset"], 1)
	limit := toInt(input["limit"], 0)
	windowed := startLine > 1 || limit > 0

	// 非窗口全读大文件 → 拒绝 + 提示
	if !windowed && info.Size() > r.MaxBytes {
		return tool.Fail(fmt.Errorf("文件过大(%d bytes)不能全读，用 offset/limit 窗口读", info.Size())), nil
	}

	f, err := os.Open(target)
	if err != nil {
		return tool.Fail(err), nil
	}
	defer f.Close()

	type line struct{ no int; text string }
	var collected []line
	var bytesOut int64
	truncated := false

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo < startLine { continue }
		if limit > 0 && len(collected) >= limit { break }

		text := sc.Text()
		if !utf8.ValidString(text) {
			return tool.Fail(fmt.Errorf("文件不是有效 UTF-8: %s", rel)), nil
		}
		bytesOut += int64(len(text)) + 1
		if windowed && bytesOut > r.MaxBytes {
			truncated = true
			break
		}
		collected = append(collected, line{lineNo, text})
	}
	if err := sc.Err(); err != nil {
		return tool.Fail(err), nil
	}

	// 右对齐行号为显示宽度
	if len(collected) == 0 {
		return tool.Success(map[string]any{"content": "", "totalLines": lineNo, "truncated": false}), nil
	}
	width := len(strconv.Itoa(collected[len(collected)-1].no))
	var b strings.Builder
	for _, l := range collected {
		fmt.Fprintf(&b, "%*d\t%s\n", width, l.no, l.text)
	}
	out := strings.TrimRight(b.String(), "\n")
	if truncated {
		out += "\n... (输出被截断；请用更小的 limit 或更高的 offset 续读)"
	}
	return tool.Success(map[string]any{
		"content":    out,
		"totalLines": lineNo,
		"truncated":  truncated,
	}), nil
}

func (ReadFileTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

func toInt(input any, def int) int {
	switch v := input.(type) {
	case float64: return int(v)
	case int: return v
	case int64: return int(v)
	}
	return def
}
