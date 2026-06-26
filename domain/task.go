package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// EventGrade 事件等级，决定事件的持久化和推送策略
type EventGrade string

const (
	GradeTransient  EventGrade = "transient"  // WS only，不入库不replay
	GradePersistent EventGrade = "persistent" // DB + Sequence + WS
	GradeAudit      EventGrade = "audit"      // DB only，不推送WS
)

// TaskEvent 用于日志输出或外部监控（Step、Message、时间）
type TaskEvent struct {
	ID         int64          `json:"-"`
	TaskID     int64          `json:"task_id"` // 任务ID
	RootTaskID int64          `json:"root_task_id"`
	Step       string         `json:"step"`       // 步骤就是节点
	Message    string         `json:"message"`    // UI 文案
	Error      string         `json:"error"`      // 错误信息
	Meta       map[string]any `json:"meta"`       // 结构化数据
	CreatedAt  time.Time      `json:"created_at"` // 创建时间
	Type       string         `json:"type"`       // 事件类型
	Progress   float64        `json:"progress"`   // 节点运行的进度，当前Step节点内的进度，非任务进度
	Level      string         `json:"level"`      // info system debug
	// 任务进度计算： overall_progress = (node_index + node_progress) / node_total
	NodeIndex int `json:"node_index"`
	NodeTotal int `json:"node_total"`
	// 事件分层
	Grade    EventGrade `json:"grade"`    // transient / persistent / audit
	Sequence int64      `json:"sequence"` // 全局递增序号，仅 Persistent 事件有值
}

const (
	TaskEventStarted            = "task_started"
	TaskEventSucceeded          = "task_succeeded"
	TaskEventFailed             = "task_failed"
	TaskEventSuspended          = "task_suspended"
	TaskEventFinalFailed        = "task_final_failed"
	TaskEventNodeCompleteAsync  = "node_complete_async"
	TaskEventPointsRefunded     = "task_points_refunded"
	TaskEventPointsRefundFailed = "task_points_refund_failed"
)

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSuccess   TaskStatus = "success"
	TaskFailed    TaskStatus = "failed"
	TaskSuspended TaskStatus = "suspended"
	TaskCanceled  TaskStatus = "canceled"
)

// MaxAutoRetryCount 自动重试上限，防止任务/子任务陷入无限重试循环消耗 API 费用。
const MaxAutoRetryCount = 5

type Task struct {
	ID       int64      `json:"id"`
	UserID   int64      `json:"user_id"`
	ParentID *int64     `json:"parent_id"` // 父任务，子任务支持
	RootID   int64      `json:"root_id"`   // 根任务，创建主任务时必须保证RootID=自己的ID，子任务的RootID=父任务的RootID
	Type     string     `json:"type"`
	Status   TaskStatus `json:"status"`

	InputJSON  []byte `json:"input_json"`
	OutputJSON []byte `json:"output_json"`

	RetryCount   int    `json:"retry_count"`
	ErrorMessage string `json:"error_message"`

	WorkflowVersionID    int64 `json:"workflow_version_id"`
	WorkflowDefinitionID int64 `json:"workflow_definition_id"`

	SubKey *string `json:"-"` // 恢复子工作流的key

	// 任务抢占字段
	WorkerID  string    `json:"-"`
	StartedAt time.Time `json:"-"`

	ParentNode *string `json:"-"` // 当前任务所属的父节点
	MapIndex   *int    `json:"-"`

	Progress float64 `json:"progress"` // 任务进度

	// ===== 实现DAG 引擎从执行器升级为可回放、可分叉、可局部重做的运行时 =====
	BaseRunID  int64   `json:"base_run_id"` // 最初原始任务ID
	ForkedFrom *int64  `json:"forked_from"` // 本次fork来源任务ID
	RunDepth   int     `json:"run_depth"`   // fork深度，每fork一次就会+1
	EditAction *string `json:"edit_action"` // replace_start_image / replace_end_image / edit_user_prompt
	EditLabel  *string `json:"edit_label"`  // 给UI看，比如“替换起始图”

	// ===== patch / resume =====
	ResumeFrom *string `json:"resume_from"` // 本次 fork 从哪个节点开始恢复
	PatchJSON  []byte  `json:"patch_json"`  // []RuntimePatch 的 JSON

	// ===== 业务归属字段 =====
	EntryType         string `json:"entry_type"` // tool / template / workflow
	ToolDefinitionID  *int64 `json:"tool_definition_id"`
	ToolModeID        *int64 `json:"tool_mode_id"`
	ToolModeVersionID *int64 `json:"tool_mode_version_id"`
	TemplateID        *int64 `json:"template_id"`
	TemplateVersionID *int64 `json:"template_version_id"`

	// 展示冗余字段（方便列表页直出）
	EntryTitle    *string `json:"entry_title"`    // 比如“视频生成”
	EntrySubtitle *string `json:"entry_subtitle"` // 比如“图生视频”
	RouteKey      *string `json:"route_key"`      // video_generation
	ModeKey       *string `json:"mode_key"`       // image_to_video

	EstimatedCostTotal float64 `json:"estimated_cost_total"`
	ActualCostTotal    float64 `json:"actual_cost_total"`
	CostStatus         string  `json:"cost_status"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsMapSubTask 是否为Map 节点创建的子任务
//func (t *Task) IsMapSubTask() bool {
//	return t.MapIndex != nil
//}

// TaskOutput 最终结果层 (给客户端的 final Response)
type TaskOutput struct {
	ResultType     string          `json:"result_type"`
	PrimaryFileUrl string          `json:"primary_file_url"`
	CoverUrl       *string         `json:"cover_url,omitempty"`
	PreviewUrl     *string         `json:"preview_url,omitempty"`
	Width          *int64          `json:"width,omitempty"`           // 像素用整数
	Height         *int64          `json:"height,omitempty"`          // 像素用整数
	Duration       *float64        `json:"duration,omitempty"`        // 时长用浮点数（秒）
	Extras         map[string]any  `json:"extras,omitempty"`          // 扩展
	CreativeDetail *CreativeDetail `json:"creative_detail,omitempty"` // 创意详情
}

func ParseFinal(outputJSON []byte) (*TaskOutput, error) {
	data := outputJSON
	if len(data) == 0 {
		return nil, fmt.Errorf("empty output")
	}

	// 1. 先整体 Unmarshal
	var fullOutput map[string]any
	if err := json.Unmarshal(data, &fullOutput); err != nil {
		return nil, err
	}

	// 2. 尝试获取 final 节点
	finalRaw, hasFinal := fullOutput["final"]

	// 3. 逻辑分流：如果存在 final 节点，尝试按新规范解析
	if hasFinal {
		finalData, _ := json.Marshal(finalRaw)
		var standardOut TaskOutput
		// 如果能直接解析成新结构，且包含核心必填字段，直接返回
		if err := json.Unmarshal(finalData, &standardOut); err == nil {
			// 这里根据业务补全一些可能的空缺
			if standardOut.ResultType == "" {
				standardOut.ResultType = "video" // 默认兜底
			}
			return &standardOut, nil
		}
	}

	// 4. 旧版本兼容逻辑：如果 final 节点解析失败或不存在，手动从根节点或 final 内部提取旧字段
	// 假设旧版本字段名为 video_url, cover_image 等
	target := make(map[string]any)
	if hasFinal {
		if f, ok := finalRaw.(map[string]any); ok {
			target = f
		}
	} else {
		target = fullOutput
	}

	// 尝试映射旧字段到 TaskOutput 结构
	return mapOldVersionToNew(target), nil
}

// mapOldVersionToNew 处理旧字段到新规范的映射
func mapOldVersionToNew(data map[string]any) *TaskOutput {
	// 提取 URL 的函数（处理多个可能的旧 Key）
	getStr := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := data[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}

	resultType := inferLegacyResultType(data)

	var primary string
	switch resultType {
	case "image":
		primary = getStr("image_url", "image", "url", "primary_file_url")
	default:
		primary = getStr("video_url", "video", "url", "primary_file_url")
	}
	if primary == "" && resultType != "timeline" {
		return nil // 连主文件都没有，解析失败
	}

	cover := getStr("cover_url", "cover_image", "cover")

	// 构造新规范对象
	out := &TaskOutput{
		ResultType:     resultType,
		PrimaryFileUrl: primary,
	}

	if cover != "" {
		out.CoverUrl = &cover
	}

	// 默认 PreviewUrl 等于 PrimaryFileUrl (客户端兼容逻辑)
	out.PreviewUrl = &primary

	//if d, ok := data["creative_detail"]; ok {
	//	creativeDetail, _ := ParseCreativeDetail(d)
	//	out.CreativeDetail = creativeDetail
	//}

	// 处理数字类型（防止从 JSON 解析出来是 float64）
	if w, ok := data["width"]; ok {
		val := int64(asFloat(w))
		if val > 0 {
			out.Width = &val
		}
	}
	if h, ok := data["height"]; ok {
		val := int64(asFloat(h))
		if val > 0 {
			out.Height = &val
		}
	}
	if d, ok := data["duration"]; ok {
		val := asFloat(d)
		if val > 0 {
			out.Duration = &val
		}
	}

	// 剩下的所有东西塞进 Extras
	out.Extras = data

	return out
}

func inferLegacyResultType(data map[string]any) string {
	getStr := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := data[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}

	if rt := strings.ToLower(getStr("result_type")); rt != "" {
		switch rt {
		case "image", "video":
			return rt
		case "timeline":
			return rt
		}
	}

	if getStr("image_url", "image") != "" {
		return "image"
	}
	if getStr("video_url", "video") != "" {
		return "video"
	}

	primary := strings.ToLower(getStr("primary_file_url", "url"))
	switch {
	case hasImageExtension(primary):
		return "image"
	case hasVideoExtension(primary):
		return "video"
	}

	if asFloat(data["duration"]) > 0 {
		return "video"
	}

	return "video"
}

func hasImageExtension(url string) bool {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".heic", ".heif"} {
		if strings.Contains(url, ext) {
			return true
		}
	}
	return false
}

func hasVideoExtension(url string) bool {
	for _, ext := range []string{".mp4", ".mov", ".m4v", ".avi", ".webm", ".mkv"} {
		if strings.Contains(url, ext) {
			return true
		}
	}
	return false
}

//func ParseCreativeDetail(d any) (*CreativeDetail, error) {
//	var creativeDetail CreativeDetail
//	switch d.(type) {
//	case map[string]interface{}:
//		bytes, err := json.Marshal(d)
//		if err != nil {
//			return nil, err
//		}
//		err = json.Unmarshal(bytes, &creativeDetail)
//		if err != nil {
//			return nil, err
//		}
//	case CreativeDetail:
//		creativeDetail = d.(CreativeDetail)
//	case *CreativeDetail:
//		if d, ok := d.(*CreativeDetail); ok {
//			creativeDetail = *d
//		}
//	}
//	return &creativeDetail, nil
//}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func (t *Task) IsTerminal() bool {
	if t == nil {
		return false
	}
	switch t.Status {
	case TaskSuccess, TaskFailed, TaskCanceled:
		return true
	default:
		return false
	}
}

func (t *Task) IsActive() bool {
	if t == nil {
		return false
	}
	switch t.Status {
	case TaskPending, TaskRunning, TaskSuspended:
		return true
	default:
		return false
	}
}
