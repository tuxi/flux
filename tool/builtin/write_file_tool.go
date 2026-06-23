package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"flux/tool"
)

// WriteFileTool 把内容写入工作目录下的文件。M1 用于让 planner 生成/修改代码。
type WriteFileTool struct{ baseDir string }

func NewWriteFileTool(baseDir string) *WriteFileTool { return &WriteFileTool{baseDir: baseDir} }

func (WriteFileTool) Name() string { return "write_file" }

func (WriteFileTool) Description() string {
	return "把内容写入工作目录下的文件（path 相对工作目录）。用于生成或修改代码。"
}

func (WriteFileTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"path":    {Type: "string", Required: true, Desc: "相对工作目录的文件路径，如 main.go"},
		"content": {Type: "string", Required: true, Desc: "文件完整内容"},
	}}
}

func (WriteFileTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"path":  {Type: "string", Desc: "写入的绝对路径"},
		"bytes": {Type: "number", Desc: "写入字节数"},
	}}
}

func (t WriteFileTool) Execute(_ context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	rel, _ := input["path"].(string)
	content, _ := input["content"].(string)
	if rel == "" {
		return tool.Fail(fmt.Errorf("write_file: path 不能为空")), nil
	}
	full := filepath.Join(t.baseDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return tool.Success(map[string]any{"path": full, "bytes": len(content)}), nil
}

func (WriteFileTool) Mode() tool.ExecutionMode { return tool.SyncExecution }
