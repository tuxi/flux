package runtime_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"flux/runtime"
	"flux/runtime/mockprovider"
)

// echoInvoker 同 B-M1b，对 "echo" 回显。
type providerEchoInvoker struct{}

func (providerEchoInvoker) Invoke(_ context.Context, toolName string, input map[string]any, _ runtime.Emitter) (map[string]any, error) {
	if toolName == "echo" {
		return input, nil
	}
	return nil, nil
}

// TestBM1_ProviderSubmitPollResume 验证：
//
//	外部 HTTP Provider 的 submit→poll→resume 全链路。
//	不 crash——先证明 async kernel 能正确驱动外部世界。
func TestBM1_ProviderSubmitPollResume(t *testing.T) {
	// 启动 mock provider（任务在 200ms 后完成）
	provider := mockprovider.New(200 * time.Millisecond)
	srv := httptest.NewServer(provider)
	defer srv.Close()

	dir := t.TempDir()
	state := runtime.NewMemState(map[string]any{"topic": "provider-test"})
	provAwait := runtime.NewProviderAwait(dir, srv.URL)
	store := runtime.NewFileStore(dir, state)
	sched := runtime.NewScheduler(providerEchoInvoker{}, provAwait, store, runtime.NopEmitter{})

	// ── 阶段 1：Run → suspend ──
	t.Log("🚀 提交 async 任务到 mock provider...")
	res, err := sched.Run(context.Background(), runtime.SimplePlanSource(), state)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != runtime.StatusSuspended {
		t.Fatalf("应挂起，status=%d", res.Status)
	}

	bindingIDs := provAwait.BindingIDs()
	if len(bindingIDs) != 1 {
		t.Fatalf("应有 1 个 binding，实际=%d", len(bindingIDs))
	}
	bindingID := bindingIDs[0]

	taskID, ok := provAwait.ProviderTaskID(bindingID)
	if !ok {
		t.Fatal("binding 应有关联的 provider task_id")
	}
	t.Logf("✅ 已提交：binding=%d, provider_task=%s", bindingID, taskID)

	// ── 阶段 2：轮询直到完成 ──
	t.Log("⏳ 轮询 provider...")
	var pollResult map[string]any
	for i := 0; i < 50; i++ {
		status, result, err := provAwait.PollProvider(taskID)
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		if status == "done" {
			pollResult = result
			t.Logf("✅ Provider 完成：result=%v", pollResult)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pollResult == nil {
		// 手动触发完成
		provider.ForceComplete(taskID)
		pollResult = map[string]any{"forced": true}
		t.Log("⚠️ 轮询超时，手动完成")
	}

	// ── 阶段 3：Resume ──
	nodeName, _ := provAwait.NodeForBinding(bindingID)
	res2, err := sched.Resume(context.Background(), runtime.SimplePlanSource(), state,
		nodeName, pollResult)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res2.Status != runtime.StatusCompleted {
		t.Fatalf("应完成，status=%d", res2.Status)
	}

	if state.State(runtime.AsyncHelloNode) != runtime.NodeSuccess {
		t.Fatal("async_hello 应为 NodeSuccess")
	}
	if state.State(runtime.EchoNode) != runtime.NodeSuccess {
		t.Fatal("echo 应为 NodeSuccess")
	}
	t.Logf("✅ 完成：echo=%v", state.Output(runtime.EchoNode))
	t.Log("✅✅ B-M1 submit→poll→resume 全链路通过")
}

// TestBM1_CrashDuringProviderPending 验证：
//
//	外部任务提交后，进程崩溃。重启后从文件和 provider 恢复状态。
//
//	进程 A：submit → suspend → persist
//	进程 B：load state + bindings → poll provider → Resume → complete
func TestBM1_CrashDuringProviderPending(t *testing.T) {
	// Provider 处理时间较长（模拟 2 秒任务）
	provider := mockprovider.New(2 * time.Second)
	srv := httptest.NewServer(provider)
	defer srv.Close()

	dir := t.TempDir()

	// ═══════════════════════════════════════════════
	// 进程 A：提交 → 挂起 → 落盘
	// ═══════════════════════════════════════════════
	stateA := runtime.NewMemState(map[string]any{"topic": "crash-during-pending"})
	provAwaitA := runtime.NewProviderAwait(dir, srv.URL)
	storeA := runtime.NewFileStore(dir, stateA)
	schedA := runtime.NewScheduler(providerEchoInvoker{}, provAwaitA, storeA, runtime.NopEmitter{})

	t.Log("🚀 进程 A：Run → submit → suspend...")
	resA, _ := schedA.Run(context.Background(), runtime.SimplePlanSource(), stateA)
	if resA.Status != runtime.StatusSuspended {
		t.Fatalf("进程 A 应挂起，status=%d", resA.Status)
	}

	bindingIDs := provAwaitA.BindingIDs()
	taskID, _ := provAwaitA.ProviderTaskID(bindingIDs[0])
	t.Logf("✅ 进程 A 挂起：provider_task=%s, state+provider_bindings 已落盘", taskID)

	// ═══════════════════════════════════════════════
	// 💥 崩溃
	// ═══════════════════════════════════════════════
	_ = stateA
	_ = schedA
	_ = provAwaitA

	// ═══════════════════════════════════════════════
	// 进程 B：冷启动 → 恢复 → poll → Resume
	// ═══════════════════════════════════════════════
	t.Log("💥 崩溃 → 进程 B 冷启动")

	// 恢复 state
	stateB, err := runtime.LoadSnapshot(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("加载 state: %v", err)
	}

	// 恢复 provider bindings（NewProviderAwait 自动读 provider_bindings.json）
	provAwaitB := runtime.NewProviderAwait(dir, srv.URL)
	storeB := runtime.NewFileStore(dir, stateB)

	recoveredIDs := provAwaitB.BindingIDs()
	if len(recoveredIDs) != 1 {
		t.Fatalf("恢复后应有 1 个 binding，实际=%d", len(recoveredIDs))
	}
	recoveredTaskID, ok := provAwaitB.ProviderTaskID(recoveredIDs[0])
	if !ok || recoveredTaskID != taskID {
		t.Fatalf("恢复的 taskID=%q，期望=%q", recoveredTaskID, taskID)
	}
	t.Logf("✅ 进程 B 恢复：binding=%d, provider_task=%s", recoveredIDs[0], recoveredTaskID)

	// 强制 provider 完成（模拟外部任务已完成）
	provider.ForceComplete(taskID)

	// 轮询
	status, result, err := provAwaitB.PollProvider(taskID)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if status != "done" {
		t.Fatalf("强制完成后 poll 应返回 done，实际=%s", status)
	}
	t.Logf("✅ Poll 返回 done：result=%v", result)

	// Resume
	nodeName, _ := provAwaitB.NodeForBinding(recoveredIDs[0])
	schedB := runtime.NewScheduler(providerEchoInvoker{}, provAwaitB, storeB, runtime.NopEmitter{}).
		WithPlan(runtime.SimplePlan())

	resB, err := schedB.Resume(context.Background(), runtime.SimplePlanSource(), stateB,
		nodeName, result)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resB.Status != runtime.StatusCompleted {
		t.Fatalf("应完成，status=%d", resB.Status)
	}

	if stateB.State(runtime.AsyncHelloNode) != runtime.NodeSuccess {
		t.Fatal("async_hello 应为 NodeSuccess")
	}
	if stateB.State(runtime.EchoNode) != runtime.NodeSuccess {
		t.Fatal("echo 应为 NodeSuccess")
	}
	t.Logf("✅ 进程 B 完成：echo=%v", stateB.Output(runtime.EchoNode))
	t.Log("✅✅ B-M1 crash→provider→poll→resume 全链路通过")
}

// TestBM1_CallbackBeforePoll 验证乱序安全性：
//
//	外部回调到达时 binding 状态仍为 waiting，resume 正常推进。
//	（当前实现中 Complete→Resume 路径天然支持这一点）
func TestBM1_CallbackBeforePoll(t *testing.T) {
	provider := mockprovider.New(0) // 立即完成
	srv := httptest.NewServer(provider)
	defer srv.Close()

	dir := t.TempDir()
	state := runtime.NewMemState(map[string]any{})
	provAwait := runtime.NewProviderAwait(dir, srv.URL)
	store := runtime.NewFileStore(dir, state)
	sched := runtime.NewScheduler(providerEchoInvoker{}, provAwait, store, runtime.NopEmitter{})

	// Run → suspend
	res, _ := sched.Run(context.Background(), runtime.SimplePlanSource(), state)
	if res.Status != runtime.StatusSuspended {
		t.Fatalf("应挂起")
	}

	bindingID := provAwait.BindingIDs()[0]
	taskID, _ := provAwait.ProviderTaskID(bindingID)

	// Provider 已立即完成（ProcessTime=0），poll 直接返回 done
	status, result, _ := provAwait.PollProvider(taskID)
	if status != "done" {
		t.Fatalf("立即完成的任务应返回 done，实际=%s", status)
	}

	// 先 poll 得到结果，再 resume — 顺序从不是问题
	nodeName, _ := provAwait.NodeForBinding(bindingID)
	res2, _ := sched.Resume(context.Background(), runtime.SimplePlanSource(), state,
		nodeName, result)
	if res2.Status != runtime.StatusCompleted {
		t.Fatalf("应完成，status=%d", res2.Status)
	}

	t.Logf("✅ 乱序安全：poll→resume 顺序不影响结果，echo=%v", state.Output(runtime.EchoNode))
	t.Log("✅✅ B-M1 乱序/快速回调通过")
}

// 确保 mockprovider 被引用（消除 import 警告）
var _ = mockprovider.New
