// Package planner 是 v2 的编排前端：把"目标 + 工具清单"变成 runtime 可执行的计划。
//
// M1 验证类型 A（control loop / IncrementalPlanSource）。本文件提供把 tool.Registry
// 接入 runtime 内核所需的端口适配器；真实的 LLM PlanSource（Claude）在后续文件加入，
// 它实现 runtime.PlanSource.Next —— 看目标 + 工具 + ExecState 决定下一步。
package planner

import (
	"context"
	"fmt"

	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/tool"
)

// ToolInvoker 把 tool.Registry 适配成 runtime.Invoker（Tool-First：节点 = 一次工具调用）。
type ToolInvoker struct{ Reg *tool.Registry }

func NewToolInvoker(reg *tool.Registry) ToolInvoker { return ToolInvoker{Reg: reg} }

func (ti ToolInvoker) Invoke(ctx context.Context, name string, input map[string]any, emit runtime.Emitter) (map[string]any, error) {
	t, ok := ti.Reg.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	res, err := t.Execute(ctx, input, emitterBridge{emit: emit, node: name})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return map[string]any{}, nil
	}
	if !res.Success {
		return nil, fmt.Errorf("tool %s failed: %s", name, res.Error)
	}
	return res.Data, nil
}

// emitterBridge 把 runtime.Emitter 适配成 tool.ToolEmitter。
type emitterBridge struct {
	emit runtime.Emitter
	node string
}

func (b emitterBridge) EmitToolEvent(e tool.ToolEvent) {
	if b.emit == nil {
		return
	}
	b.emit.Emit(runtime.Event{Node: b.node, Type: e.Type, Message: e.Message, Progress: e.Progress, Data: e.Data})
}

// ── M1（同步 loop）用的无操作端口 ──

type NopStore struct{}

func (NopStore) PersistNode(context.Context, string, runtime.NodeState, map[string]any) error {
	return nil
}

type NopEmitter struct{}

func (NopEmitter) Emit(runtime.Event) {}

// NopAwait：M1 是同步 loop，不应触发异步路径；触发即报错暴露问题。
type NopAwait struct{}

func (NopAwait) Begin(context.Context, *runtime.PlanNode, map[string]any) (int64, error) {
	return 0, fmt.Errorf("async not supported in M1 sync loop")
}
