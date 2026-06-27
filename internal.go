package flux

import (
	"context"
	"fmt"

	"flux/runtime"
	"flux/store"
	"flux/tool"
)

// ── 内部适配器：把 Backend / Store 接口适配成 runtime 端口 ──

type internalInvoker struct{ reg *tool.Registry }

func (i *internalInvoker) Invoke(ctx context.Context, toolName string, input map[string]any, _ runtime.Emitter) (map[string]any, error) {
	t, ok := i.reg.Get(toolName)
	if !ok {
		return nil, nil
	}
	res, err := t.Execute(ctx, input, nil)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return map[string]any{}, nil
	}
	if !res.Success {
		return nil, nil
	}
	return res.Data, nil
}

// ── Await 适配器 ──

type internalAwait struct {
	backend Backend
	taskID  string
	tr      *taskRuntime // 存 binding 索引

	// v3: 可选的 Store 接口，优先于 Backend
	awaitStore store.AwaitStore
}

func (a *internalAwait) Begin(ctx context.Context, node *runtime.PlanNode, input map[string]any) (int64, error) {
	providerTaskID := realProviderTaskID(input)
	if providerTaskID == "" {
		providerTaskID = a.taskID + "_" + node.Name
	}

	var bindingID string
	if a.awaitStore != nil {
		// v3 path: 使用 AwaitStore
		b := store.AwaitBinding{
			BindingID:      fmt.Sprintf("%s_%s", a.taskID, node.Name),
			TaskID:         a.taskID,
			NodeName:       node.Name,
			ProviderTaskID: providerTaskID,
			Status:         store.AwaitStatusAwaiting,
			Input:          input,
		}
		if err := a.awaitStore.CreateBinding(ctx, b); err != nil {
			return 0, err
		}
		bindingID = b.BindingID
	} else {
		// Backend path (向后兼容)
		var err error
		bindingID, err = a.backend.CreateAwait(ctx, a.taskID, node.Name, providerTaskID, input)
		if err != nil {
			return 0, err
		}
	}

	a.tr.awaitIndex[providerTaskID] = bindingRef{
		bindingID: bindingID,
		nodeName:  node.Name,
	}

	return int64(len(a.tr.awaitIndex)), nil
}

// ── Store 适配器 ──

type internalStore struct {
	backend Backend
	taskID  string

	// v3: 可选的 Store 接口，优先于 Backend
	workflowStore store.WorkflowStore
}

func (s *internalStore) PersistNode(ctx context.Context, node string, state runtime.NodeState, out map[string]any) error {
	if s.workflowStore != nil {
		// v3 path: 使用 WorkflowStore
		return s.workflowStore.PersistNode(ctx, s.taskID, node, state, out)
	}
	// Backend path (向后兼容)
	return s.backend.PersistNode(ctx, s.taskID, node, NodeState(state), out)
}

var _ runtime.Invoker = (*internalInvoker)(nil)
var _ runtime.AwaitController = (*internalAwait)(nil)
var _ runtime.Store = (*internalStore)(nil)

// realProviderTaskID 从 resolved input 中提取外部 Provider 返回的真实任务 ID。
// 不同 Provider 用的字段名不同：TTS → job_id，图片/视频 → task_id，通用 → provider_task_id。
func realProviderTaskID(input map[string]any) string {
	for _, key := range []string{"provider_task_id", "job_id"} {
		if v, ok := input[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
