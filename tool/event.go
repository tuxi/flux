package tool

import (
	"context"
)

type executionMetaKey struct{}

type ExecutionMeta struct {
	UserID   int64
	TaskID   int64
	RootID   int64
	NodeName string
}

func ContextWithExecutionMeta(ctx context.Context, meta ExecutionMeta) context.Context {
	return context.WithValue(ctx, executionMetaKey{}, meta)
}

func ExecutionMetaFromContext(ctx context.Context) (ExecutionMeta, bool) {
	meta, ok := ctx.Value(executionMetaKey{}).(ExecutionMeta)
	return meta, ok
}

// Tool 实际能力，标准化工具
type Tool interface {
	Name() string
	Description() string
	InputSchema() DataSchema
	OutputSchema() DataSchema
	Execute(ctx context.Context, input map[string]any, emitter ToolEmitter) (*Result, error)
	Mode() ExecutionMode
}

// ExecutionMode 执行模式
// SyncExecution 短任务同步执行
// AsyncExecution 长任务异步执行
type ExecutionMode string

const (
	SyncExecution  ExecutionMode = "sync"
	AsyncExecution ExecutionMode = "async"
)

// ToolEvent 工具事件，不会直接对外发送，而是被 转换为 TaskEvent
type ToolEvent struct {
	Type       string // started | stream | stream_end | log | completed | failed
	CustomType string // 不为空时直接作为 TaskEvent.Type，绕过 "tool_" 前缀
	Message    string
	Progress   float64
	Data       map[string]any
	LogLevel   string // log level
}

// ToolEmitter 天然适配流式输出，为实时字幕、前端进度展示预留了扩展点 —— 这是接入 LLM 流式能力的关键。
type ToolEmitter interface {
	EmitToolEvent(event ToolEvent)
}
