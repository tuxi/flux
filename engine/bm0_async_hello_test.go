package engine

// B-M0：验证 engine 异步基底的 suspend→persist→resume 闭环的原语层。
//
// 不接外部 Provider、不碰 v2 kernel、不用 Redis、不引入分布式锁。
//
// 关键分界：CompleteAwaitNode 内部分两层——
//   - binding 层：ClaimCompleting → 状态转换（原子的、可单独验证）
//   - task 层：ResumeTask → runDAG（需要完整 engine + 分布式锁）
//
// B-M0 验证 binding 层（async 地基），task 层留给 B-M1 的真实 Provider 场景。

import (
	"context"
	"testing"

	"flux/definition"
	"flux/domain"
	"flux/eventbus"
	"flux/pkg/uuid"
	"flux/workflow/nodes"

	"github.com/stretchr/testify/require"
)

// TestBM0_AsyncHello_SuspendBindingAndComplete 验证 binding 状态机：
//   executeAwaitNode → binding waiting → ClaimCompleting → completed
func TestBM0_AsyncHello_SuspendBindingAndComplete(t *testing.T) {
	taskRepo := newFakeTaskRepo(&domain.Task{ID: 1, RootID: 1, WorkflowVersionID: 1})
	nodeRepo := newFakeNodeRepo()
	awaitRepo := newFakeAwaitBindingRepo()

	e := &Engine{
		awaitBindingRepo: awaitRepo,
		nodeRepo:         nodeRepo,
		taskRepo:         taskRepo,
		iSrv:             *uuid.NewNode(3),
	}

	runCtx := &nodes.Context{
		Ctx: context.Background(),
		Task: &domain.Task{
			ID:                1,
			RootID:            1,
			WorkflowVersionID: 1,
		},
		Runtime:        map[string]*domain.NodeRuntime{},
		Output:         map[string]any{"nodes": map[string]any{}},
		EventBus:       eventbus.NewEventBus(nil, nil),
		ActivatedEdges: map[string]bool{},
	}

	rt := &domain.NodeRuntime{
		TaskID: 1,
		Name:   "async_hello",
		State:  domain.NodeRunning,
	}
	runCtx.Runtime[rt.Name] = rt

	node := nodes.Node{
		Name: "async_hello",
		Type: definition.NodeAwait,
		Config: map[string]any{
			"await_type": "external_task",
			"source":     "webhook_or_poll",
		},
	}
	execCtx := &nodes.NodeExecContext{
		Input:  map[string]any{"greeting": "hello from async world"},
		NodeDef: &definition.NodeDefinition{Name: "async_hello"},
	}

	// ── 阶段 1：suspend ──
	err := e.executeAwaitNode(runCtx, rt, node, execCtx)
	var suspended *domain.WorkflowSuspendedError
	require.ErrorAs(t, err, &suspended, "应返回 WorkflowSuspendedError")
	require.Equal(t, domain.NodeAwaiting, rt.State, "节点应进入 NodeAwaiting")

	binding, getErr := awaitRepo.GetByTaskAndNode(runCtx.Ctx, 1, "async_hello")
	require.NoError(t, getErr)
	require.NotNil(t, binding, "应创建 AwaitBinding")
	require.Equal(t, domain.AwaitBindingWaiting, binding.Status, "binding 应是 waiting")

	t.Logf("B-M0 ✅ suspend：node=%s binding=%d status=%s", rt.Name, binding.ID, binding.Status)

	// ── 阶段 2：claim（外部回调到达时，CompleteAwaitNode 的第一步）──
	claimed, claimErr := awaitRepo.ClaimCompleting(
		context.Background(),
		binding.ID,
		[]domain.AwaitBindingStatus{domain.AwaitBindingWaiting},
	)
	require.NoError(t, claimErr)
	require.True(t, claimed, "应成功 claim binding（模拟回调到达）")

	binding.Status = domain.AwaitBindingCompleting
	_ = awaitRepo.Update(context.Background(), binding)

	t.Logf("B-M0 ✅ claim：binding=%d → completing", binding.ID)

	// ── 阶段 3：complete（外部任务成功完成后，binding 转为 completed）──
	ok, transErr := awaitRepo.TransitionStatus(
		context.Background(),
		binding.ID,
		domain.AwaitBindingCompleting,
		domain.AwaitBindingCompleted,
	)
	require.NoError(t, transErr)
	require.True(t, ok, "binding completing→completed 应成功")

	bindingAfter, _ := awaitRepo.GetByID(context.Background(), binding.ID)
	require.Equal(t, domain.AwaitBindingCompleted, bindingAfter.Status)

	t.Logf("B-M0 ✅ complete：binding=%d → completed", binding.ID)

	// ── 阶段 4：重复 claim 应被拒绝（幂等安全）──
	claimed2, _ := awaitRepo.ClaimCompleting(
		context.Background(),
		binding.ID,
		[]domain.AwaitBindingStatus{domain.AwaitBindingWaiting},
	)
	require.False(t, claimed2, "已完成 binding 不应被重复 claim（幂等）")

	t.Log("B-M0 ✅ 幂等：已完成 binding 重复 claim 被正确拒绝")
	t.Log("B-M0 ✅ 地基成立：suspend→claim→complete 闭环完好，async 层可用")
}
