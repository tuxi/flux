package flux

import (
	"context"

	"flux/runtime"
	"flux/tool"
)

// ── 内部适配器：把 Backend 接口适配成 runtime 端口 ──

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

type internalAwait struct {
	backend Backend
	taskID  string
}

func (a *internalAwait) Begin(ctx context.Context, node *runtime.PlanNode, input map[string]any) (int64, error) {
	bindingID, err := a.backend.CreateAwait(ctx, a.taskID, node.Name, "", input)
	if err != nil {
		return 0, err
	}
	_ = bindingID
	return 1, nil // Phase 2: 返回真实的 binding 数字 ID
}

type internalStore struct {
	backend Backend
	taskID  string
}

func (s *internalStore) PersistNode(ctx context.Context, node string, state runtime.NodeState, out map[string]any) error {
	return s.backend.PersistNode(ctx, s.taskID, node, NodeState(state), out)
}

var _ runtime.Invoker = (*internalInvoker)(nil)
var _ runtime.AwaitController = (*internalAwait)(nil)
var _ runtime.Store = (*internalStore)(nil)
