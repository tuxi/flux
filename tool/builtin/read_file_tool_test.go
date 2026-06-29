package builtin_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuxi/flux/tool/builtin"
)

const sample = "line one\nline two\nline three\nline four\nline five\n"

func read(t *testing.T, root string, input map[string]any) (string, error) {
	t.Helper()
	res, err := builtin.NewReadFileTool(root).Execute(context.Background(), input, nil)
	if err != nil {
		return "", err
	}
	if !res.Success {
		return "", errors.New(res.Error)
	}
	return res.Data["content"].(string), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func TestReadFullWithLineNumbers(t *testing.T) {
	dir := t.TempDir(); os.WriteFile(filepath.Join(dir, "f.txt"), []byte(sample), 0o644)
	out, err := read(t, dir, map[string]any{"path": "f.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "1\tline one") {
		t.Errorf("expected numbered first line, got %q", firstLine(out))
	}
	if !strings.Contains(out, "5\tline five") {
		t.Errorf("expected last line 5:\n%s", out)
	}
}

func TestReadOffset(t *testing.T) {
	dir := t.TempDir(); os.WriteFile(filepath.Join(dir, "f.txt"), []byte(sample), 0o644)
	out, _ := read(t, dir, map[string]any{"path": "f.txt", "offset": 3})
	if !strings.HasPrefix(out, "3\tline three") {
		t.Errorf("got %q", firstLine(out))
	}
	if strings.Contains(out, "line two") {
		t.Error("offset 3 should skip line 2")
	}
}

func TestReadLimit(t *testing.T) {
	dir := t.TempDir(); os.WriteFile(filepath.Join(dir, "f.txt"), []byte(sample), 0o644)
	out, _ := read(t, dir, map[string]any{"path": "f.txt", "limit": 2})
	if n := strings.Count(out, "\n") + 1; n != 2 {
		t.Errorf("limit 2 → %d lines:\n%s", n, out)
	}
}

func TestReadOffsetAndLimit(t *testing.T) {
	dir := t.TempDir(); os.WriteFile(filepath.Join(dir, "f.txt"), []byte(sample), 0o644)
	out, _ := read(t, dir, map[string]any{"path": "f.txt", "offset": 2, "limit": 2})
	if !strings.HasPrefix(out, "2\tline two") {
		t.Errorf("got %q", firstLine(out))
	}
	if strings.Contains(out, "line four") {
		t.Error("should stop at line 3")
	}
}

func TestReadOffsetBeyondEOFIsEmpty(t *testing.T) {
	dir := t.TempDir(); os.WriteFile(filepath.Join(dir, "f.txt"), []byte(sample), 0o644)
	out, err := read(t, dir, map[string]any{"path": "f.txt", "offset": 999})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("offset beyond EOF should be empty, got %q", out)
	}
}

func TestReadPathRequired(t *testing.T) {
	dir := t.TempDir()
	_, err := read(t, dir, map[string]any{})
	if err == nil {
		t.Error("expected an error when path is missing")
	}
}

func TestReadLargeFileFullErrorsWindowWorks(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 1; i <= 50000; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(sb.String()), 0o644)
	tl := builtin.NewReadFileTool(dir)
	tl.MaxBytes = 10_000

	// 全读拒绝
	res, _ := tl.Execute(context.Background(), map[string]any{"path": "big.txt"}, nil)
	if res.Success {
		t.Fatal("大文件全读应拒绝")
	}
	if !strings.Contains(res.Error, "offset") {
		t.Errorf("应提示 offset/limit，得 %q", res.Error)
	}

	// 窗口读可以通过
	res2, _ := tl.Execute(context.Background(), map[string]any{"path": "big.txt", "offset": 100, "limit": 3}, nil)
	if !res2.Success { t.Fatalf("窗口读大文件应成功: %s", res2.Error) }
	content := res2.Data["content"].(string)
	if !strings.Contains(content, "\tline 100") || !strings.Contains(content, "\tline 102") {
		t.Errorf("窗口读返回错误切片:\n%s", content)
	}
	if strings.Contains(content, "\tline 103") {
		t.Error("limit 3 应停在 line 102")
	}
}

func TestReadDirErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := read(t, dir, map[string]any{"path": "."})
	if err == nil {
		t.Error("目录应报错")
	}
}

func TestReadPathEscapesWorkspace(t *testing.T) {
	dir := t.TempDir()
	_, err := read(t, dir, map[string]any{"path": "../etc/passwd"})
	if err == nil {
		t.Error("路径穿越应报错")
	}
}
