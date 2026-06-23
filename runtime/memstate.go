package runtime

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
