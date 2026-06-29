package builtin

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tuxi/flux/tool"
)

// GrepTool 在工作目录用 `grep -rn` 按内容搜索文件（代码 agent 必备）
type GrepTool struct{ baseDir string }

func NewGrepTool(baseDir string) *GrepTool { return &GrepTool{baseDir: baseDir} }

func (GrepTool) Name() string { return "grep" }

func (GrepTool) Description() string {
	return "在工作目录用 grep -rn 搜索文件内容。返回 file:line:匹配行 的列表。支持正则模式。用于查找函数定义、引用、错误信息等。大目录会很慢——先用小范围路径或精确模式。"
}

func (GrepTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"pattern": {Type: "string", Required: true, Desc: "grep 搜索的正则模式，如 'EmitToolEvent' 或 'func.*Subscribe'"},
		"path":    {Type: "string", Required: false, Desc: "限定搜索路径（相对工作目录），如 'workflow/nodes' 或 '.'。默认 '.'"},
	}}
}

func (GrepTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"matches": {Type: "string", Desc: "匹配行列表，格式 file:line:text，每行一条，最多 200 条"},
		"count":   {Type: "integer", Desc: "匹配行数"},
	}}
}

func (t GrepTool) Execute(ctx context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return tool.Fail(fmt.Errorf("grep: pattern 不能为空")), nil
	}
	searchPath, _ := input["path"].(string)
	if searchPath == "" {
		searchPath = "."
	}
	// 安全：只搜 baseDir 内
	absPath := filepath.Join(t.baseDir, searchPath)
	if !strings.HasPrefix(absPath, t.baseDir) && t.baseDir != absPath {
		absPath = t.baseDir // 路径穿越就退到 baseDir
	}

	cmd := exec.CommandContext(ctx, "grep", "-rn", "--", pattern, absPath)
	cmd.Dir = t.baseDir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()

	// grep 返回码 1 = 无匹配（正常），>1 = 真错误
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() > 1 {
			// 如 grep 本身失败（权限/binary）
			return tool.Fail(fmt.Errorf("grep failed: %s", errBuf.String())), nil
		}
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}

	// 裁剪到 200 行，避免 LLM 上下文爆炸
	truncated := false
	if len(lines) > 200 {
		lines = lines[:200]
		truncated = true
	}
	result := strings.Join(lines, "\n")
	if truncated {
		result += fmt.Sprintf("\n……(共 %d 条匹配，仅显示前 200)", lineCount(outBuf.String()))
	}

	// 结构化摘要：每个匹配的 file:line
	summary := make([]string, 0, len(lines))
	for _, l := range lines {
		if idx := strings.Index(l, ":"); idx > 0 {
			if idx2 := strings.Index(l[idx+1:], ":"); idx2 > 0 {
				summary = append(summary, l[:idx+1+idx2])
			}
		}
	}

	return tool.Success(map[string]any{
		"matches":  result,
		"count":    len(lines),
		"summary":  summary,
		"has_more": truncated,
	}), nil
}

func (GrepTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

func lineCount(s string) int {
	sc := bufio.NewScanner(strings.NewReader(s))
	n := 0
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n
}
