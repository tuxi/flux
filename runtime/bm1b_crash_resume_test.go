package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"flux/runtime"
)

// echoInvoker 实现 Invoker 端口：对 "echo" 工具回显 input。
type echoInvoker struct{}

func (echoInvoker) Invoke(_ context.Context, toolName string, input map[string]any, _ runtime.Emitter) (map[string]any, error) {
	if toolName == "echo" {
		return input, nil
	}
	return nil, nil
}

// TestBM1b_CrashResume_ColdRestart 验证：
//
//	进程 A：Run → async_hello 挂起 → 状态落盘
//	进程 B（冷启动）：加载状态 → 模拟回调 → Resume → 完成
//
// 这是 B 的命门——不接外部 Provider、不碰 Redis，只用文件系统证明
// "async 状态能跨进程存活"。
func TestBM1b_CrashResume_ColdRestart(t *testing.T) {
	dir := t.TempDir()

	// ═══════════════════════════════════════════════
	// 进程 A：启动 → 挂起
	// ═══════════════════════════════════════════════
	stateA := runtime.NewMemState(map[string]any{"topic": "crash-resume"})
	storeA := runtime.NewFileStore(dir, stateA)
	awaitA := runtime.NewFileAwait(dir)

	schedA := runtime.NewScheduler(echoInvoker{}, awaitA, storeA, runtime.NopEmitter{})

	t.Log("🚀 进程 A：Run...")
	resA, errA := schedA.Run(context.Background(), runtime.SimplePlanSource(), stateA)
	if errA != nil {
		t.Fatalf("进程 A Run: %v", errA)
	}
	if resA.Status != runtime.StatusSuspended {
		t.Fatalf("进程 A 应挂起，但 status=%d", resA.Status)
	}

	// 验证 async_hello 挂了
	if stateA.State(runtime.AsyncHelloNode) != runtime.NodeAwaiting {
		t.Fatalf("async_hello 应为 NodeAwaiting，实际=%d", stateA.State(runtime.AsyncHelloNode))
	}
	// echo 还在等
	if stateA.State(runtime.EchoNode) != runtime.NodePending {
		t.Fatalf("echo 应仍为 NodePending，实际=%d", stateA.State(runtime.EchoNode))
	}

	// 验证文件落地
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		t.Fatalf("state.json 未落盘: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bindings.json")); err != nil {
		t.Fatalf("bindings.json 未落盘: %v", err)
	}

	// 记录 binding ID（进程 A 创建的）
	bindingIDs := awaitA.BindingIDs()
	if len(bindingIDs) != 1 {
		t.Fatalf("应有 1 个 binding，实际=%d", len(bindingIDs))
	}
	bindingID := bindingIDs[0]
	t.Logf("✅ 进程 A 挂起：async_hello=NodeAwaiting, binding=%d", bindingID)

	// ═══════════════════════════════════════════════
	// 💥 进程 A 崩溃（模拟：丢弃所有内存对象）
	// ═══════════════════════════════════════════════
	_ = stateA // gone
	_ = schedA // gone
	_ = awaitA // gone

	// ═══════════════════════════════════════════════
	// 进程 B：冷启动 → 恢复 → Resume
	// ═══════════════════════════════════════════════
	t.Log("💥 进程崩溃 → 冷启动进程 B")

	// 从文件恢复执行状态
	stateB, err := runtime.LoadSnapshot(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("进程 B 加载状态: %v", err)
	}
	if stateB.State(runtime.AsyncHelloNode) != runtime.NodeAwaiting {
		t.Fatalf("恢复后 async_hello 应为 NodeAwaiting，实际=%d", stateB.State(runtime.AsyncHelloNode))
	}

	// 从文件恢复 binding（FileAwait 构造时自动读 bindings.json）
	awaitB := runtime.NewFileAwait(dir)
	storeB := runtime.NewFileStore(dir, stateB)

	// 验证 binding 可恢复
	recoveredIDs := awaitB.BindingIDs()
	if len(recoveredIDs) != 1 {
		t.Fatalf("恢复后应有 1 个 binding，实际=%d", len(recoveredIDs))
	}
	if recoveredIDs[0] != bindingID {
		t.Fatalf("恢复的 bindingID=%d，期望=%d", recoveredIDs[0], bindingID)
	}

	// 验证 binding 内容
	nodeName, input, ok := awaitB.Complete(recoveredIDs[0])
	if !ok {
		t.Fatal("恢复的 binding 应可 Complete")
	}
	if nodeName != runtime.AsyncHelloNode {
		t.Fatalf("binding 节点名=%q，期望=%q", nodeName, runtime.AsyncHelloNode)
	}
	if input["greeting"] != "hello" {
		t.Fatalf("binding input=%v", input)
	}
	t.Logf("✅ 进程 B 恢复：state=ok, binding=%d, node=%s", recoveredIDs[0], nodeName)

	// 重建 scheduler（新进程 = 新 scheduler 实例，预载入 plan 避免覆写恢复的状态）
	schedB := runtime.NewScheduler(echoInvoker{}, awaitB, storeB, runtime.NopEmitter{}).
		WithPlan(runtime.SimplePlan())

	// 模拟外部事件到达：async_hello 完成，产出 output
	resB, errB := schedB.Resume(
		context.Background(),
		runtime.SimplePlanSource(),
		stateB,
		runtime.AsyncHelloNode,
		map[string]any{"message": "recovered after crash!", "from": "external callback"},
	)
	if errB != nil {
		t.Fatalf("进程 B Resume: %v", errB)
	}
	if resB.Status != runtime.StatusCompleted {
		t.Fatalf("进程 B 应完成，但 status=%d", resB.Status)
	}

	// 验证完整性
	if stateB.State(runtime.AsyncHelloNode) != runtime.NodeSuccess {
		t.Fatalf("Resume 后 async_hello 应为 NodeSuccess，实际=%d", stateB.State(runtime.AsyncHelloNode))
	}
	if stateB.State(runtime.EchoNode) != runtime.NodeSuccess {
		t.Fatalf("Resume 后 echo 应为 NodeSuccess，实际=%d", stateB.State(runtime.EchoNode))
	}

	// echo 拿到了 async_hello 的产出
	echoOut := stateB.Output(runtime.EchoNode)
	if echoOut["message"] != "recovered after crash!" {
		t.Fatalf("echo 应拿到恢复后的输出，实际=%v", echoOut)
	}

	t.Logf("✅ 进程 B 完成：async_hello=%v, echo=%v", stateB.Output(runtime.AsyncHelloNode), echoOut)
	t.Log("✅✅ B-M1b 命门通过：Run → Suspend → Persist → 💥 Crash → Restore → Resume → Complete")
}

// TestBM1b_ResumeIsIdempotent 验证：对已完成的节点重复 Resume 是安全的。
func TestBM1b_ResumeIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	state := runtime.NewMemState(map[string]any{})
	store := runtime.NewFileStore(dir, state)
	await := runtime.NewFileAwait(dir)
	sched := runtime.NewScheduler(echoInvoker{}, await, store, runtime.NopEmitter{})

	// Run → suspend
	res, _ := sched.Run(context.Background(), runtime.SimplePlanSource(), state)
	if res.Status != runtime.StatusSuspended {
		t.Fatalf("应挂起，status=%d", res.Status)
	}

	// 第一次 Resume
	res1, _ := sched.Resume(context.Background(), runtime.SimplePlanSource(), state,
		runtime.AsyncHelloNode, map[string]any{"msg": "first"})
	if res1.Status != runtime.StatusCompleted {
		t.Fatalf("首次 Resume 应完成，status=%d", res1.Status)
	}

	// 第二次 Resume（重复）——应安全，不能 panic 或破坏状态
	res2, err2 := sched.Resume(context.Background(), runtime.SimplePlanSource(), state,
		runtime.AsyncHelloNode, map[string]any{"msg": "second"})
	if err2 != nil {
		t.Fatalf("重复 Resume 不应报错: %v", err2)
	}
	if state.State(runtime.AsyncHelloNode) != runtime.NodeSuccess {
		t.Fatal("重复 Resume 后状态应保持 NodeSuccess")
	}
	// echo 产出应仍是第一次的值
	if state.Output(runtime.EchoNode)["msg"] != "first" {
		t.Fatalf("重复 Resume 不应改变 echo 产出: %v", state.Output(runtime.EchoNode))
	}

	t.Log("✅ B-M1b 幂等：重复 Resume 安全，不改变已完成状态")
	t.Logf("  状态 = %d, 最终产出 = %v", res2.Status, state.Output(runtime.EchoNode))
}
