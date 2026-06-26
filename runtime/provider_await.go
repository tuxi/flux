package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// ProviderAwait 实现 AwaitController，对真实 HTTP Provider 做 submit。
// Begin() → POST /submit → 拿到 provider_task_id → 存入 binding。
// crash 恢复时从文件重建 binding 映射。
type ProviderAwait struct {
	Dir        string
	ProviderURL string
	HTTPClient  *http.Client

	bindings map[int64]providerBinding
	nextID   int64
	mu       sync.Mutex
}

type providerBinding struct {
	NodeName       string         `json:"node_name"`
	Input          map[string]any `json:"input"`
	ProviderTaskID string         `json:"provider_task_id"`
}

// NewProviderAwait 创建 ProviderAwait。从文件恢复已有 binding（crash 恢复）。
func NewProviderAwait(dir, providerURL string) *ProviderAwait {
	pa := &ProviderAwait{
		Dir:         dir,
		ProviderURL: providerURL,
		HTTPClient:  http.DefaultClient,
		bindings:    map[int64]providerBinding{},
		nextID:      1,
	}
	pa.loadBindings()
	return pa
}

// Begin 向 Provider 提交异步任务，创建 binding。
func (pa *ProviderAwait) Begin(_ context.Context, node *PlanNode, input map[string]any) (int64, error) {
	// POST /submit
	body, _ := json.Marshal(input)
	resp, err := pa.HTTPClient.Post(
		pa.ProviderURL+"/submit",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, fmt.Errorf("provider submit: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("provider submit response: %w", err)
	}
	if result.TaskID == "" {
		return 0, fmt.Errorf("provider returned empty task_id")
	}

	pa.mu.Lock()
	id := pa.nextID
	pa.nextID++
	pa.bindings[id] = providerBinding{
		NodeName:       node.Name,
		Input:          input,
		ProviderTaskID: result.TaskID,
	}
	pa.mu.Unlock()

	pa.saveBindings()
	return id, nil
}

// ProviderTaskID 返回 binding 对应的 provider task_id（供轮询用）。
func (pa *ProviderAwait) ProviderTaskID(bindingID int64) (string, bool) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	b, ok := pa.bindings[bindingID]
	return b.ProviderTaskID, ok
}

// NodeForBinding 返回 binding 对应的节点名（供 Resume 用）。
func (pa *ProviderAwait) NodeForBinding(bindingID int64) (string, bool) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	b, ok := pa.bindings[bindingID]
	return b.NodeName, ok
}

// BindingIDs 返回所有活跃 binding ID（crash 恢复后用于扫描未完成的 provider 任务）。
func (pa *ProviderAwait) BindingIDs() []int64 {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	ids := make([]int64, 0, len(pa.bindings))
	for id := range pa.bindings {
		ids = append(ids, id)
	}
	return ids
}

// PollProvider 轮询 Provider 指定任务的状态。返回 (status, result, error)。
func (pa *ProviderAwait) PollProvider(taskID string) (status string, result map[string]any, err error) {
	resp, err := pa.HTTPClient.Get(fmt.Sprintf("%s/poll?task_id=%s", pa.ProviderURL, taskID))
	if err != nil {
		return "", nil, fmt.Errorf("provider poll: %w", err)
	}
	defer resp.Body.Close()

	var r struct {
		Status string         `json:"status"`
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", nil, fmt.Errorf("provider poll response: %w", err)
	}
	return r.Status, r.Result, nil
}

func (pa *ProviderAwait) saveBindings() {
	data, _ := json.Marshal(pa.bindings)
	_ = os.WriteFile(filepath.Join(pa.Dir, "provider_bindings.json"), data, 0o644)
}

func (pa *ProviderAwait) loadBindings() {
	data, err := os.ReadFile(filepath.Join(pa.Dir, "provider_bindings.json"))
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &pa.bindings)
	for id := range pa.bindings {
		if id >= pa.nextID {
			pa.nextID = id + 1
		}
	}
}

var _ AwaitController = (*ProviderAwait)(nil)
