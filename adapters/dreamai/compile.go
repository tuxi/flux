// Package dreamai 提供 DreamAI WorkflowDefinition → Flux runtime.Plan 的编译适配。
//
// Phase 1 接缝：DreamAI 的 WorkflowDefinition 结构与 Flux 的 definition 包完全一致，
// workflow.Compile 已能直接编译。本包提供 DreamAI 语义层：
//   - 异步工具自动检测（基于 tool.Registry 的 Mode()）
//   - 未来：provider 路由优化、缓存节点识别
//
// 用法：
//
//	plan, err := dreamai.Compile(def, toolReg)
//	scheduler.Run(ctx, runtime.NewStaticSource(plan), state)
package dreamai

import (
	"flux/definition"
	"flux/runtime"
	"flux/tool"
	"flux/workflow"
)

// Compile 把 DreamAI 的 WorkflowDefinition 编译为 Flux runtime.Plan。
// 自动从 tool.Registry 检测哪些工具是异步的（Mode() == AsyncExecution）。
func Compile(def *definition.WorkflowDefinition, reg *tool.Registry) (*runtime.Plan, error) {
	return workflow.Compile(def, func(toolName string) bool {
		t, ok := reg.Get(toolName)
		if !ok {
			return false
		}
		return t.Mode() == tool.AsyncExecution
	})
}

// CompileWith 编译 WorkflowDefinition，使用自定义的异步判定函数。
// 适用于测试或不依赖 tool.Registry 的场景。
func CompileWith(def *definition.WorkflowDefinition, asyncTool func(string) bool) (*runtime.Plan, error) {
	return workflow.Compile(def, asyncTool)
}
