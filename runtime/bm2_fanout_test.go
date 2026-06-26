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

// TestBM2_FanoutAllSuccess 验证：N 个并行 async 任务全部成功 → join 收集结果。
func TestBM2_FanoutAllSuccess(t *testing.T) {
	provider := mockprovider.New(100 * time.Millisecond)
	srv := httptest.NewServer(provider)
	defer srv.Close()

	dir := t.TempDir()
	state := runtime.NewMemState(map[string]any{"fanout": 3})
	pa := runtime.NewProviderAwait(dir, srv.URL)
	store := runtime.NewFileStore(dir, state)
	sched := runtime.NewScheduler(providerEchoInvoker{}, pa, store, runtime.NopEmitter{})

	n := 3
	t.Logf("🚀 提交 %d 个并行 async 任务...", n)
	res, err := sched.Run(context.Background(), runtime.FanoutPlanSource(n), state)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != runtime.StatusSuspended {
		t.Fatalf("应挂起，status=%d", res.Status)
	}

	bindingIDs := pa.BindingIDs()
	if len(bindingIDs) != n {
		t.Fatalf("应有 %d 个 binding，实际=%d", n, len(bindingIDs))
	}
	t.Logf("✅ %d 个任务已提交，全部挂起", n)

	// 轮询完成所有任务
	for _, bid := range bindingIDs {
		taskID, _ := pa.ProviderTaskID(bid)
		nodeName, _ := pa.NodeForBinding(bid)
		// 等待完成为止
		var result map[string]any
		for i := 0; i < 30; i++ {
			status, r, _ := pa.PollProvider(taskID)
			if status == "done" {
				result = r
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if result == nil {
			provider.ForceComplete(taskID)
			result = map[string]any{"forced": true, "branch": bid}
		}
		// 逐个 Resume
		res2, err := sched.Resume(context.Background(), runtime.FanoutPlanSource(n), state, nodeName, result)
		if err != nil {
			t.Fatalf("Resume %s: %v", nodeName, err)
		}
		_ = res2
	}
	t.Log("✅ 所有分支完成")

	// 验证 join 收到了所有分支结果
	joinOut := state.Output("join")
	if len(joinOut) != n {
		t.Fatalf("join 应收到 %d 个分支结果，实际=%d：%v", n, len(joinOut), joinOut)
	}
	for i := 0; i < n; i++ {
		name := runtime.FanNodeName(i)
		if _, ok := joinOut[name]; !ok {
			t.Fatalf("join 遗漏分支 %s", name)
		}
	}
	t.Logf("✅ join 收集到全部 %d 个分支：%v", n, joinOut)
	t.Log("✅✅ B-M2 happy fanout：N 并行 → 全部成功 → join 收集完成")
}

// TestBM2_PartialFailure_Propagation 验证：一个分支失败，其余成功，join 收到失败信息。
func TestBM2_PartialFailure_Propagation(t *testing.T) {
	provider := mockprovider.New(100 * time.Millisecond)
	srv := httptest.NewServer(provider)
	defer srv.Close()

	dir := t.TempDir()
	state := runtime.NewMemState(map[string]any{})
	pa := runtime.NewProviderAwait(dir, srv.URL)
	store := runtime.NewFileStore(dir, state)
	sched := runtime.NewScheduler(providerEchoInvoker{}, pa, store, runtime.NopEmitter{})

	n := 3
	res, _ := sched.Run(context.Background(), runtime.FanoutPlanSource(n), state)
	if res.Status != runtime.StatusSuspended {
		t.Fatalf("应挂起")
	}

	bindingIDs := pa.BindingIDs()
	// 配置：async_0 失败，其余成功
	failNode := runtime.FanNodeName(0)
	for _, bid := range bindingIDs {
		nodeName, _ := pa.NodeForBinding(bid)
		taskID, _ := pa.ProviderTaskID(bid)
		if nodeName == failNode {
			provider.SetTaskBehavior(taskID, mockprovider.TaskBehavior{ResultStatus: "failed"})
			t.Logf("💥 %s (%s) → 设为 failed", nodeName, taskID)
		}
	}

	// 处理所有分支（按 node 名匹配行为）
	for _, bid := range bindingIDs {
		taskID, _ := pa.ProviderTaskID(bid)
		nodeName, _ := pa.NodeForBinding(bid)

		status, result, _ := pa.PollProvider(taskID)
		switch status {
		case "done":
			sched.Resume(context.Background(), runtime.FanoutPlanSource(n), state, nodeName, result)
			t.Logf("✅ %s → done", nodeName)
		case "failed":
			state.Transition(nodeName, runtime.NodeFailed)
			_ = store.PersistNode(context.Background(), nodeName, runtime.NodeFailed, nil)
			t.Logf("💥 %s → NodeFailed", nodeName)
		default:
			provider.ForceComplete(taskID)
			_, result, _ := pa.PollProvider(taskID)
			sched.Resume(context.Background(), runtime.FanoutPlanSource(n), state, nodeName, result)
		}
	}

	// 所有分支终态后，Run 一次让 join 执行
	_, _ = sched.Run(context.Background(), runtime.FanoutPlanSource(n), state)

	// 验证
	if state.State(failNode) != runtime.NodeFailed {
		t.Fatalf("%s 应为 NodeFailed，实际=%d", failNode, state.State(failNode))
	}
	if state.State(runtime.FanNodeName(1)) != runtime.NodeSuccess {
		t.Fatal("分支 1 应为 NodeSuccess")
	}
	if state.State(runtime.FanNodeName(2)) != runtime.NodeSuccess {
		t.Fatal("分支 2 应为 NodeSuccess")
	}

	joinOut := state.Output("join")
	if joinOut == nil {
		t.Fatal("join 应已执行并收集结果")
	}
	// join 收到 3 个分支信息（包括失败的那个）
	if len(joinOut) != n {
		t.Fatalf("join 应收到 %d 个分支，实际=%d：%v", n, len(joinOut), joinOut)
	}

	// 验证失败分支在 join 结果中标为 NodeFailed
	branchInfo, ok := joinOut[failNode].(map[string]any)
	if !ok {
		t.Fatalf("join 结果中 %s 应为 map，实际=%T", failNode, joinOut[failNode])
	}
	// state 值来自 Go int（非 JSON float64）
	var stateVal int
	switch v := branchInfo["state"].(type) {
	case int:
		stateVal = v
	case float64:
		stateVal = int(v)
	}
	if stateVal != int(runtime.NodeFailed) {
		t.Fatalf("分支 %s state 应为 NodeFailed(%d)，实际=%v", failNode, runtime.NodeFailed, branchInfo["state"])
	}

	t.Logf("✅ join 正确收到：1 failed + 2 success")
	t.Logf("   失败分支 %s: state=NodeFailed", failNode)
	t.Logf("   成功分支: state=NodeSuccess")
	t.Log("✅✅ B-M2 partial failure propagation：部分失败 → join 感知 → 继续执行")
}

// TestBM2_CrashDuringFanout 验证：fanout 提交后崩溃，新进程恢复所有 binding 并完成。
func TestBM2_CrashDuringFanout(t *testing.T) {
	provider := mockprovider.New(2 * time.Second)
	srv := httptest.NewServer(provider)
	defer srv.Close()

	dir := t.TempDir()
	n := 3

	// ── 进程 A：提交全部 fanout 任务 → 挂起 ──
	stateA := runtime.NewMemState(map[string]any{"fanout": n})
	paA := runtime.NewProviderAwait(dir, srv.URL)
	storeA := runtime.NewFileStore(dir, stateA)
	schedA := runtime.NewScheduler(providerEchoInvoker{}, paA, storeA, runtime.NopEmitter{})

	t.Logf("🚀 进程 A：提交 %d 个并行任务...", n)
	resA, _ := schedA.Run(context.Background(), runtime.FanoutPlanSource(n), stateA)
	if resA.Status != runtime.StatusSuspended {
		t.Fatalf("应挂起")
	}
	taskIDs := make(map[int64]string)
	for _, bid := range paA.BindingIDs() {
		tid, _ := paA.ProviderTaskID(bid)
		taskIDs[bid] = tid
	}
	t.Logf("✅ 进程 A 挂起：%d 个 binding，taskIDs=%v", len(taskIDs), taskIDs)

	// 💥
	_ = stateA
	_ = schedA
	_ = paA

	// ── 进程 B：恢复 → 完成全部 → Resume ──
	t.Log("💥 崩溃 → 进程 B")
	stateB, _ := runtime.LoadSnapshot(filepath.Join(dir, "state.json"))
	paB := runtime.NewProviderAwait(dir, srv.URL)
	storeB := runtime.NewFileStore(dir, stateB)

	if len(paB.BindingIDs()) != n {
		t.Fatalf("恢复后应有 %d 个 binding，实际=%d", n, len(paB.BindingIDs()))
	}
	t.Logf("✅ 进程 B 恢复 %d 个 binding", n)

	schedB := runtime.NewScheduler(providerEchoInvoker{}, paB, storeB, runtime.NopEmitter{}).
		WithPlan(runtime.FanoutPlan(n))

	// 强制完成所有 provider 任务并 resume
	for _, bid := range paB.BindingIDs() {
		taskID, _ := paB.ProviderTaskID(bid)
		nodeName, _ := paB.NodeForBinding(bid)
		provider.ForceComplete(taskID)
		_, result, _ := paB.PollProvider(taskID)
		schedB.Resume(context.Background(), runtime.FanoutPlanSource(n), stateB, nodeName, result)
	}

	if stateB.State("join") != runtime.NodeSuccess {
		t.Fatalf("join 应为 NodeSuccess，实际=%d", stateB.State("join"))
	}
	t.Logf("✅ 进程 B 完成：join=%v", stateB.Output("join"))
	t.Log("✅✅ B-M2 crash during fanout：N 并行 → 💥 → 恢复 → 全部 revive → join")
}
