// Package mockprovider 提供可控的异步任务模拟 HTTP 服务，供 B-M1 验证用。
//
// 模拟真实外部 Provider 的 submit→poll/callback 语义，但所有行为可控：
//   - 延迟（模拟网络/处理耗时）
//   - 失败（模拟外部错误）
//   - 重复回调（模拟幂等测试）
//
// 三个端点：
//
//	POST /submit  → {"task_id": "xxx"}
//	GET  /poll    → {"status": "pending|done", "result": {...}}
//	POST /callback → 触发回调（预留）
package mockprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// TaskBehavior 控制单个 task 的行为（B-M2：不同分支不同命运）。
type TaskBehavior struct {
	ResultStatus string         // "done" 或 "failed"
	Result       map[string]any // 完成时的结果（仅 done）
	Duration     time.Duration  // 覆盖全局 ProcessTime
}

// Provider 是一个可控的异步任务模拟器。
type Provider struct {
	mu    sync.Mutex
	tasks map[string]*Task

	// SubmitDelay 模拟提交耗时
	SubmitDelay time.Duration
	// ProcessTime 模拟处理耗时（submit 后多久 poll 返回 done）
	ProcessTime time.Duration
	// ShouldFail 控制 /poll 是否返回错误
	ShouldFail bool

	// behaviors 按 taskID 配置行为（B-M2 fanout：不同分支不同命运）
	behaviors map[string]TaskBehavior
}

// Task 表示一个已提交的异步任务。
type Task struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"` // "pending" | "done" | "failed"
	Result    map[string]any `json:"result,omitempty"`
	Input     map[string]any `json:"input"`
	CreatedAt time.Time      `json:"created_at"`
	DoneAt    time.Time      `json:"done_at,omitempty"`
}

// New 创建 Provider。processTime 是 submit 后任务变为 done 的默认耗时。
func New(processTime time.Duration) *Provider {
	return &Provider{
		tasks:      map[string]*Task{},
		behaviors:  map[string]TaskBehavior{},
		ProcessTime: processTime,
	}
}

// SetTaskBehavior 为指定 task 配置行为（submit 后调用）。
func (p *Provider) SetTaskBehavior(taskID string, b TaskBehavior) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.behaviors[taskID] = b
	if t, ok := p.tasks[taskID]; ok && b.Duration > 0 {
		t.DoneAt = t.CreatedAt.Add(b.Duration)
	}
}

// ServeHTTP 实现 http.Handler。
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/submit":
		p.handleSubmit(w, r)
	case "/poll":
		p.handlePoll(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *Provider) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if p.SubmitDelay > 0 {
		time.Sleep(p.SubmitDelay)
	}

	var input map[string]any
	_ = json.NewDecoder(r.Body).Decode(&input)

	p.mu.Lock()
	id := fmt.Sprintf("task_%d", len(p.tasks)+1)
	task := &Task{
		ID:        id,
		Status:    "pending",
		Input:     input,
		CreatedAt: time.Now(),
		DoneAt:    time.Now().Add(p.ProcessTime),
	}
	p.tasks[id] = task
	p.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"task_id": id,
		"status":  "pending",
	})
}

func (p *Provider) handlePoll(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing task_id"})
		return
	}

	p.mu.Lock()
	task, ok := p.tasks[taskID]
	p.mu.Unlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	if p.ShouldFail {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "simulated failure"})
		return
	}

	// 检查 per-task behavior（B-M2 fanout 不同命运）
	p.mu.Lock()
	behavior, hasBehavior := p.behaviors[taskID]
	p.mu.Unlock()

	if hasBehavior && behavior.ResultStatus == "failed" {
		task.Status = "failed"
		writeJSON(w, http.StatusOK, map[string]any{
			"task_id": task.ID,
			"status":  "failed",
			"error":   "simulated task failure",
		})
		return
	}

	// 检查是否已到完成时间
	if hasBehavior && behavior.Duration > 0 {
		// 用 per-task 时间判断
		if time.Now().After(task.DoneAt) {
			p.mu.Lock()
			task.Status = "done"
			if behavior.Result != nil {
				task.Result = behavior.Result
			} else if task.Result == nil {
				task.Result = map[string]any{"completed": true, "output": task.Input}
			}
			p.mu.Unlock()
		}
	} else if time.Now().After(task.DoneAt) {
		p.mu.Lock()
		task.Status = "done"
		if task.Result == nil {
			task.Result = map[string]any{"completed": true, "output": task.Input}
		}
		p.mu.Unlock()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"task_id": task.ID,
		"status":  task.Status,
		"result":  task.Result,
	})
}

// ForceComplete 强制将任务标记为 done（模拟跳过等待直接完成）。
func (p *Provider) ForceComplete(taskID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.tasks[taskID]; ok {
		t.Status = "done"
		t.Result = map[string]any{"forced": true}
		t.DoneAt = time.Now()
	}
}

// GetTask 返回任务信息。
func (p *Provider) GetTask(taskID string) *Task {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tasks[taskID]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
