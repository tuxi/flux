package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileStore 实现 Store 端口，每次 PersistNode 时把完整 ExecState 快照写到文件。
// B-M1b 验证用：crash 后从快照恢复状态。
type FileStore struct {
	Dir   string
	State *MemState // 共享引用——Scheduler 和 Store 操作同一份 MemState
	mu    sync.Mutex
}

func NewFileStore(dir string, state *MemState) *FileStore {
	return &FileStore{Dir: dir, State: state}
}

func (fs *FileStore) PersistNode(_ context.Context, _ string, _ NodeState, _ map[string]any) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.State.SaveSnapshot(filepath.Join(fs.Dir, "state.json"))
}

// FileAwait 实现 AwaitController 端口：在内存中管理 binding，同时持久化到文件。
// B-M1b 验证用：crash 后从 binding 文件恢复。
type FileAwait struct {
	Dir      string
	bindings map[int64]fileBinding // bindingID → binding
	nextID   int64
	mu       sync.Mutex
}

type fileBinding struct {
	NodeName string         `json:"node_name"`
	Input    map[string]any `json:"input"`
}

func NewFileAwait(dir string) *FileAwait {
	fa := &FileAwait{
		Dir:      dir,
		bindings: map[int64]fileBinding{},
		nextID:   1,
	}
	fa.loadBindings()
	return fa
}

func (fa *FileAwait) Begin(_ context.Context, node *PlanNode, input map[string]any) (int64, error) {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	id := fa.nextID
	fa.nextID++
	fa.bindings[id] = fileBinding{NodeName: node.Name, Input: input}
	fa.saveBindings()
	return id, nil
}

// Complete 模拟外部回调完成：返回 nodeName 和 input（验证用）。
func (fa *FileAwait) Complete(bindingID int64) (nodeName string, input map[string]any, ok bool) {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	b, exists := fa.bindings[bindingID]
	if !exists {
		return "", nil, false
	}
	return b.NodeName, b.Input, true
}

// BindingIDs 返回所有活跃 binding ID（crash 恢复后用于扫描）。
func (fa *FileAwait) BindingIDs() []int64 {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	ids := make([]int64, 0, len(fa.bindings))
	for id := range fa.bindings {
		ids = append(ids, id)
	}
	return ids
}

func (fa *FileAwait) saveBindings() {
	data, _ := json.Marshal(fa.bindings)
	_ = os.WriteFile(filepath.Join(fa.Dir, "bindings.json"), data, 0o644)
}

func (fa *FileAwait) loadBindings() {
	data, err := os.ReadFile(filepath.Join(fa.Dir, "bindings.json"))
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &fa.bindings)
	for id := range fa.bindings {
		if id >= fa.nextID {
			fa.nextID = id + 1
		}
	}
}

// 编译期验证端口实现
var _ Store = (*FileStore)(nil)
var _ AwaitController = (*FileAwait)(nil)

// NopEmitter 是无操作的事件发射器（B-M1b 这类同步验证不需要 trace）。
type NopEmitter struct{}

func (NopEmitter) Emit(Event) {}

// SimplePlanSource 把 SimplePlan 包成一次性 StaticSource。
func SimplePlanSource() PlanSource { return NewStaticSource(SimplePlan()) }

// ── B-M1b 共享的测试夹具 ──

const AsyncHelloNode = "async_hello"
const EchoNode = "echo"

// SimplePlan 构造 B-M1b 验证用的 async_hello → echo 计划。
func SimplePlan() *Plan {
	return &Plan{
		Nodes: map[string]*PlanNode{
			AsyncHelloNode: {
				Name:     AsyncHelloNode,
				ToolName: "async_hello",
				Async:    true,
				Join:     JoinAll,
				Resolve: func(_ context.Context, _ ExecState) (map[string]any, error) {
					return map[string]any{"greeting": "hello"}, nil
				},
			},
			EchoNode: {
				Name:      EchoNode,
				ToolName:  "echo",
				DependsOn: []string{AsyncHelloNode},
				Join:      JoinAll,
				Resolve: func(_ context.Context, state ExecState) (map[string]any, error) {
					return state.Output(AsyncHelloNode), nil
				},
			},
		},
	}
}

// ── B-M2 共享的测试夹具（fanout）──

// FanoutPlan 构造 N 个并行的 async 节点 + 一个 join 节点。
//
//	DAG: [async_0, async_1, ..., async_N-1] → join
//	所有 async 节点无依赖（并行提交），join 依赖所有 async（JoinAll）。
//	join 的 Resolve 收集所有分支的输出（包括失败分支——通过 state 检查）。
func FanoutPlan(n int) *Plan {
	nodes := map[string]*PlanNode{}
	depNames := make([]string, n)
	for i := 0; i < n; i++ {
		name := fanNodeName(i)
		depNames[i] = name
		idx := i // 闭包捕获
		nodes[name] = &PlanNode{
			Name:     name,
			ToolName: "provider_async",
			Async:    true,
			Join:     JoinAll,
			Resolve: func(_ context.Context, _ ExecState) (map[string]any, error) {
				return map[string]any{"branch": idx, "item": "fanout"}, nil
			},
		}
	}
	nodes["join"] = &PlanNode{
		Name:      "join",
		ToolName:  "echo",
		DependsOn: depNames,
		Join:      JoinAll,
		Resolve: func(_ context.Context, state ExecState) (map[string]any, error) {
			// 收集所有分支状态和输出
			results := map[string]any{}
			for _, d := range depNames {
				st := state.State(d)
				out := state.Output(d)
				results[d] = map[string]any{"state": int(st), "output": out}
			}
			return results, nil
		},
	}
	return &Plan{Nodes: nodes}
}

func fanNodeName(i int) string {
	return fmt.Sprintf("async_%d", i)
}

// FanNodeName 返回 fanout 中第 i 个分支的节点名（暴露给测试）。
func FanNodeName(i int) string { return fanNodeName(i) }

// FanoutPlanSource 把 FanoutPlan 包成一次性 StaticSource。
func FanoutPlanSource(n int) PlanSource { return NewStaticSource(FanoutPlan(n)) }
