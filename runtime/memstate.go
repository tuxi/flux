package runtime

import (
	"encoding/json"
	"os"
)

// MemState 是 ExecState 的最小内存实现，供 demo / planner / 测试复用（纯 stdlib）。
// 生产环境中 nodes.Context 也实现 ExecState（见 workflow 包的适配）。
type MemState struct {
	input  map[string]any
	out    map[string]map[string]any
	states map[string]NodeState
}

func NewMemState(input map[string]any) *MemState {
	return &MemState{
		input:  input,
		out:    map[string]map[string]any{},
		states: map[string]NodeState{},
	}
}

func (m *MemState) Input() map[string]any                  { return m.input }
func (m *MemState) Output(node string) map[string]any      { return m.out[node] }
func (m *MemState) SetOutput(node string, o map[string]any) { m.out[node] = o }
func (m *MemState) State(node string) NodeState             { return m.states[node] }
func (m *MemState) Transition(node string, to NodeState)    { m.states[node] = to }

func (m *MemState) Nodes() []string {
	names := make([]string, 0, len(m.states))
	for n := range m.states {
		names = append(names, n)
	}
	return names
}

// ── Crash-recovery：MemState 的序列化/反序列化 ──

type memSnapshot struct {
	Input map[string]any                `json:"input"`
	Nodes map[string]memSnapshotNode    `json:"nodes"`
}

type memSnapshotNode struct {
	State  NodeState      `json:"state"`
	Output map[string]any `json:"output,omitempty"`
}

// SaveSnapshot 把完整执行状态写入文件（crash 前最后一道防线）。
func (m *MemState) SaveSnapshot(path string) error {
	s := memSnapshot{Input: m.input, Nodes: map[string]memSnapshotNode{}}
	for name, st := range m.states {
		s.Nodes[name] = memSnapshotNode{State: st, Output: m.out[name]}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(s)
}

// LoadSnapshot 从文件恢复完整执行状态（进程重启后的第一条指令）。
func LoadSnapshot(path string) (*MemState, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var s memSnapshot
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return nil, err
	}
	m := &MemState{
		input:  s.Input,
		out:    map[string]map[string]any{},
		states: map[string]NodeState{},
	}
	for name, nd := range s.Nodes {
		m.states[name] = nd.State
		if len(nd.Output) > 0 {
			m.out[name] = nd.Output
		}
	}
	return m, nil
}
