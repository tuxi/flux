package nodes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"flux/definition"
	"flux/domain"
	"flux/eventbus"
	"flux/tool"
	"flux/utils"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// ReuseSnapshot 恢复快照
type ReuseSnapshot struct {
	TaskID int64
	Nodes  map[string]*domain.NodeRuntime
	Output map[string]any
}

type Context struct {
	// 数据锁（业务数据）
	mu sync.RWMutex
	// expr 专用锁（避免污染主锁）
	exprMu sync.Mutex

	Ctx  context.Context
	Task *domain.Task

	Workflow *definition.WorkflowDefinition

	// Input 任务的输入结构
	Input map[string]any

	// Output 是最终输出的持久化输出结构（final data contract） 最终会写入数据库，所以Output = 持久化结构
	// 主要用于：API、DB、DAG UI、Debug、Replay
	// 强制统一结构：
	// {
	// "input": {
	//   "topic": "AI Workflow"
	// },
	// "nodes": {
	//
	//   "generate_script": {
	//       "status": "success",
	//       "started_at": "2026-03-06T10:00",
	//       "finished_at": "2026-03-06T10:01",
	//       "output": {
	//           "script": "AI is transforming..."
	//       }
	//   },
	//
	//   "generate_image": {
	//       "status": "success",
	//       "started_at": "...",
	//       "finished_at": "...",
	//       "output": {
	//           "image_url": "..."
	//       }
	//   }
	//
	// }
	//}
	Output map[string]any
	// Runtime 运行时节点
	Runtime map[string]*domain.NodeRuntime
	// ActivatedEdges 激活边（active edge）
	// 已激活的边，运行时真实发生的路径（Runtime Path）
	ActivatedEdges map[string]bool // from->to

	EventBus *eventbus.EventBus
	// 递归限制，防止无限创建 Task
	Depth     int
	exprCache map[string]*vm.Program
	final     *domain.TaskOutput
	// 单独包含final的数据锁
	finalMu sync.RWMutex

	// ===== 新增 =====
	ParentSnapshot *ReuseSnapshot
	// InjectedOutputs 不是调度必需字段，而是“本次 run 中哪些节点是从父快照注入复用的”这一层运行时观测数据
	InjectedOutputs map[string]map[string]any // nodeName -> output
	DirtyNodes      map[string]bool
	MapItemReuse    map[string]map[int]bool

	// ✅ 降级为 debug / UI 辅助信息
	// ❌ 不再参与执行逻辑
	Patches      []domain.RuntimePatch
	ResumeFrom   string
	PatchedNodes map[string]bool
}

func (c *Context) EmitToolEvent(nodeName string, event tool.ToolEvent) {
	nodeIndex := c.GetNodeIndex(nodeName)
	nodeTotal := c.GetNodeTotal()

	message := event.Message

	errMsg := ""
	if v, ok := event.Data["error"].(string); ok {
		errMsg = v
	}
	if event.Type == "progress" {
		if r, ok := c.Runtime[nodeName]; ok {
			r.Progress = event.Progress
		}
	}
	c.EventBus.Publish(
		c.Task.RootID,
		&domain.TaskEvent{
			TaskID:     c.Task.ID,
			RootTaskID: c.Task.RootID,
			Step:       nodeName,
			Type:       taskEventType(event),
			Grade:      taskEventGrade(event),
			Message:    message,
			Error:      errMsg,
			Progress:   event.Progress,
			Meta:       event.Data,
			CreatedAt:  time.Now(),
			NodeTotal:  nodeTotal,
			NodeIndex:  nodeIndex,
			Level:      event.LogLevel,
		})

}

// taskEventType resolves the TaskEvent.Type for a ToolEvent.
// If CustomType is set it is used directly; otherwise the type is prefixed with "tool_".
func taskEventType(event tool.ToolEvent) string {
	if event.CustomType != "" {
		return event.CustomType
	}
	return "tool_" + event.Type
}

// taskEventGrade resolves the TaskEvent.Grade for a ToolEvent.
// Timeline events (CustomType) are Persistent; tool_stream/log/progress are Transient.
func taskEventGrade(event tool.ToolEvent) domain.EventGrade {
	if event.CustomType != "" {
		return domain.GradePersistent
	}
	switch event.Type {
	case "stream", "stream_end", "progress", "log":
		return domain.GradeTransient
	default:
		return domain.GradePersistent
	}
}

func (c *Context) EmitNodeEvent(nodeName string, event NodeEvent) {
	nodeIndex := c.GetNodeIndex(nodeName)
	nodeTotal := c.GetNodeTotal()
	if event.Type == "progress" {
		if r, ok := c.Runtime[nodeName]; ok {
			r.Progress = event.Progress
		}
	}
	nodeEventType := "node_" + event.Type
	c.EventBus.Publish(
		c.Task.RootID,
		&domain.TaskEvent{
			TaskID:     c.Task.ID,
			RootTaskID: c.Task.RootID,
			Step:       nodeName,
			Type:       nodeEventType,
			Grade:      nodeEventGrade(event.Type),
			Message:    event.Message,
			Progress:   event.Progress,
			Meta:       event.Data,
			CreatedAt:  time.Now(),
			NodeIndex:  nodeIndex,
			NodeTotal:  nodeTotal,
		},
	)

}

// nodeEventGrade resolves the grade for a NodeEvent by its type.
func nodeEventGrade(eventType string) domain.EventGrade {
	switch eventType {
	case "debug":
		return domain.GradeTransient
	default:
		return domain.GradePersistent
	}
}

// SetNodeOutput 将单个节点的成功结果存入全局上下文
func (c *Context) SetNodeOutput(
	nodeName string,
	data map[string]any,
	schema tool.DataSchema,
) error {

	c.EnsureOutputInitialized()

	c.mu.Lock()
	defer c.mu.Unlock()

	// 1️⃣ 校验输出字段
	for field, fs := range schema.Fields {

		val, exists := data[field]
		if exists && val == nil && fs.Type == "array" {
			val = []any{}
			data[field] = val
		}

		if fs.Required && !exists {
			return fmt.Errorf("输出缺少必填字段: %s。 用于：%s", field, fs.Desc)
		}

		if exists {
			if err := ValidateFieldTypeStrict(fs, val); err != nil {
				return fmt.Errorf("输出字段 %s 类型错误: %w", field, err)
			}
		}
	}

	// 写持久结构
	nodesMap := c.Output["nodes"].(map[string]any)

	node, ok := nodesMap[nodeName].(map[string]any)
	if !ok {
		node = map[string]any{}
		nodesMap[nodeName] = node
	}

	node["output"] = data

	return nil
}

func (c *Context) GetNodeOutput(node string) map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	nodesMap, ok := c.Output["nodes"].(map[string]any)
	if !ok {
		return nil
	}

	nodeData, ok := nodesMap[node].(map[string]any)
	if !ok {
		return nil
	}

	out, _ := nodeData["output"].(map[string]any)
	return out
}

// CalculateInputHash 计算节点的输入哈希（带 nodeVersion 盐值）
func (c *Context) CalculateInputHash(
	nodeVersion string,
	input map[string]any,
) string {
	return c.canonicalHash(nodeVersion, input)
}

// CalculateOutputHash 计算节点的输出哈希（不带盐值，纯粹计算数据本身）
func (c *Context) CalculateOutputHash(
	output map[string]any,
) string {
	return c.canonicalHash("", output)
}

// canonicalHash 是底层的统一核心实现，集成了你写好的 JSON 往返规范化和排序逻辑
func (c *Context) canonicalHash(salt string, data map[string]any) string {
	if data == nil {
		return ""
	}

	// 1. 🔧 JSON 往返规范化：消除 Go 原生类型差异
	canonical := canonicalizeForHash(data)

	// 2. 排序所有的 Map Key 并规范化 Slice
	normalized := utils.NormalizeMap(canonical)

	// 3. 最终序列化
	finalBytes, _ := json.Marshal(normalized)

	// 4. 计算哈希
	h := sha256.New()
	if salt != "" {
		h.Write([]byte(salt))
	}
	h.Write(finalBytes)

	return hex.EncodeToString(h.Sum(nil))
}

// canonicalizeForHash 通过 JSON 往返将任意 Go 类型统一为 JSON 兼容类型，
// 消除 map_analyze_segments 等大数据节点在规划期与执行期之间的类型差异。
func canonicalizeForHash(input map[string]any) map[string]any {
	b, err := json.Marshal(input)
	if err != nil {
		// 极端情况回退：深拷贝原始 map（保留原行为）。
		return deepCopyMapSimple(input)
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber() // 锁死数字字面量，防止 ID 或高精度浮点数变形
	if err := decoder.Decode(&out); err != nil {
		return deepCopyMapSimple(input)
	}
	return out
}

// deepCopyMapSimple 浅层深拷贝，作为 JSON 序列化失败时的回退方案。
func deepCopyMapSimple(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// EnsureOutputInitialized Output初始化函数
func (c *Context) EnsureOutputInitialized() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Output == nil {
		c.Output = make(map[string]any)
	}

	if c.ActivatedEdges == nil {
		c.ActivatedEdges = make(map[string]bool)
	}

	if _, ok := c.Output["input"]; !ok {
		c.Output["input"] = c.Input
	}

	if _, ok := c.Output["nodes"]; !ok {
		c.Output["nodes"] = make(map[string]any)
	}
}

// UpdateNodeStatus 更新节点状态到Output
func (c *Context) UpdateNodeStatus(
	nodeName string,
	status string,
) {

	c.EnsureOutputInitialized()

	c.mu.Lock()
	defer c.mu.Unlock()

	nodesMap := c.Output["nodes"].(map[string]any)

	node, ok := nodesMap[nodeName].(map[string]any)
	if !ok {
		node = map[string]any{}
		nodesMap[nodeName] = node
	}

	node["status"] = status

}

// Progress 计算任务执行的估算进度
func (c *Context) Progress() float64 {
	total := len(c.Runtime)
	if total == 0 {
		return 0
	}
	done := 0
	for _, runtime := range c.Runtime {
		if runtime.State == domain.NodeSuccess ||
			runtime.State == domain.NodeSkipped {
			done += 1
		}
	}
	return float64(done) / float64(total)
}

func (c *Context) GetNodeIndex(node string) int {

	r, ok := c.Runtime[node]
	if !ok {
		return 0
	}

	return r.BizIndex
}

func (c *Context) GetNodeTotal() int {
	total := 0
	for _, runtime := range c.Runtime {
		if IsSystemNode(runtime.Name) {
			continue
		}
		total += 1
	}
	return total
}

// IsSystemNode 是否是系统节点，只有start和end是系统节点
func IsSystemNode(nodeName string) bool {
	switch nodeName {
	case "start", "end":
		return true
	default:
		return false
	}
}

// CalculateTaskProgress 计算任务进度
func (c *Context) CalculateTaskProgress() float64 {
	progress := 0.0
	totalWeight := 0.0

	for _, r := range c.Runtime {
		if r.Weight <= 0 {
			continue // 权重为0，不参与进度
		}

		nodeProgress := r.Progress
		if r.State == domain.NodeSuccess {
			nodeProgress = 1
		}

		progress += nodeProgress * r.Weight
		totalWeight += r.Weight
	}

	if totalWeight == 0 {
		return 0
	}

	return progress / totalWeight
}

func (c *Context) buildExprEnv() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()

	env := map[string]any{
		"input": c.Input,
	}

	rawNodes, _ := c.Output["nodes"].(map[string]any)

	normalizedNodes := make(map[string]any, len(c.Workflow.Nodes))

	for _, defNode := range c.Workflow.Nodes {
		name := defNode.Name

		nodeEnv := map[string]any{
			"status": "",
			"output": map[string]any{},
		}

		if raw, ok := rawNodes[name].(map[string]any); ok {
			if status, ok := raw["status"].(string); ok {
				nodeEnv["status"] = status
			}
			if output, ok := raw["output"].(map[string]any); ok && output != nil {
				nodeEnv["output"] = output
			}
		}

		normalizedNodes[name] = nodeEnv

		// 兼容纯净模型写法：upload_result.url
		nodeEnvOutput := nodeEnv["output"].(map[string]any)
		env[name] = nodeEnvOutput
	}

	env["nodes"] = normalizedNodes
	return env
}

// EvalBool 专门用于 Condition 的表达式
func (c *Context) EvalBool(exprStr string) (bool, error) {
	out, err := c.eval(exprStr)
	if err != nil {
		return false, err
	}
	if out == nil {
		return false, fmt.Errorf("expr result is nil")
	}
	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("expr result is not bool: %v", out)
	}
	return b, nil
}

// EvalAny 通用表达式
func (c *Context) EvalAny(exprStr string) (any, error) {
	return c.eval(exprStr)
}

func (c *Context) buildExprEnvSchema() map[string]any {
	env := map[string]any{
		"input": map[string]any{},
		"nodes": map[string]any{},
	}

	nodesSchema := env["nodes"].(map[string]any)

	for _, node := range c.Workflow.Nodes {
		nodesSchema[node.Name] = map[string]any{
			"status": "",
			"output": map[string]any{},
		}

		// 兼容 upload_result.url 这种纯净模型
		env[node.Name] = map[string]any{}
	}

	return env
}

// expr 公共逻辑抽取
func (c *Context) eval(exprStr string) (any, error) {
	// 只锁cache
	c.exprMu.Lock()
	if c.exprCache == nil {
		c.exprCache = make(map[string]*vm.Program)
	}

	prog, ok := c.exprCache[exprStr]
	if !ok {
		compiled, err := expr.Compile(exprStr,
			expr.Env(c.buildExprEnvSchema()),
		)
		if err != nil {
			c.exprMu.Unlock()
			return nil, err
		}
		prog = compiled
		c.exprCache[exprStr] = compiled
	}
	c.exprMu.Unlock()

	// 无锁执行
	env := c.buildExprEnv()
	return expr.Run(prog, env)
}

// GetFinal 按照规范输出最终结果
func (c *Context) GetFinal() (*domain.TaskOutput, error) {
	c.finalMu.RLock()
	if c.final != nil {
		res := c.final
		c.finalMu.RUnlock()
		return res, nil
	}
	c.finalMu.RUnlock()

	// 1. 初始化标准输出结构
	outputDef := c.Workflow.Output
	final := &domain.TaskOutput{
		ResultType: outputDef.ResultType, // 类型通常是常量，直接赋值
		Extras:     make(map[string]any),
	}

	// 2. 核心字段解析映射
	// 定义一个内部助手函数，简化表达式求值逻辑
	resolve := func(path string) (string, error) {
		if path == "" {
			return "", nil
		}

		val, err := c.eval(path)
		if err != nil {
			return "", err
		}
		// 强制转为 string，因为 URL/ID 等在 DSL 中通常映射为字符串
		if s, ok := val.(string); ok {
			return s, nil
		}
		return "", nil
	}

	// 填充标准字段
	var err error
	if final.PrimaryFileUrl, err = resolve(outputDef.PrimaryFileUrl); err != nil {
		return nil, err
	}

	if outputDef.CoverUrl != "" {
		if coverUrl, err := resolve(outputDef.CoverUrl); err == nil {
			final.CoverUrl = &coverUrl
		}
	}

	if outputDef.PreviewUrl != "" {
		if previewUrl, err := resolve(outputDef.PreviewUrl); err == nil {
			final.PreviewUrl = &previewUrl
		}
	}

	// 填充数值字段（需要根据你的 eval 返回类型进行断言或转换）
	if outputDef.Width != "" {
		var width = c.evalInt64(outputDef.Width)
		final.Width = &width
	}

	if outputDef.Height != "" {
		var height = c.evalInt64(outputDef.Height)
		final.Height = &height
	}

	if outputDef.Duration != "" {
		var duration = c.evalFloat64(outputDef.Duration)
		final.Duration = &duration
	}

	// 3. 处理扩展字段 Extras
	if outputDef.Extras != nil {
		for k, path := range outputDef.Extras {
			val, err := c.eval(path)
			if err != nil {
				return nil, err
			}
			final.Extras[k] = val
		}

	}

	// 4. 写锁 + 双层检测 (DCL)
	c.finalMu.Lock()
	defer c.finalMu.Unlock()
	if c.final == nil {
		c.final = final
	}
	return c.final, nil
}

// 辅助方法：处理数值类型的转换（示例）
func (c *Context) evalInt64(path string) int64 {
	val, _ := c.eval(path)
	return utils.ToInt64(val)
}

func (c *Context) evalFloat64(path string) float64 {
	val, _ := c.eval(path)
	return utils.ToFloat64(val)
}

// ValidateFieldTypeStrict 类型校验
func ValidateFieldTypeStrict(fs tool.FieldSchema, val any) error {
	if val == nil {
		if !fs.Required {
			return nil
		}
		return fmt.Errorf("值为 nil")
	}

	switch fs.Type {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("期望 string，实际 %T", val)
		}
	case "integer":
		switch val.(type) {
		case int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64:
		default:
			return fmt.Errorf("期望 integer，实际 %T", val)
		}
	case "number":
		switch val.(type) {
		case int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64:
		case string:
			_, err := strconv.ParseInt(val.(string), 10, 64)
			if err != nil {
				return fmt.Errorf("期望 number，实际 %T", val)
			}
			return nil
		default:
			return fmt.Errorf("期望 number，实际 %T", val)
		}

	case "bool", "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("期望 bool，实际 %T", val)
		}

	case "object":
		if !utils.IsObject(val) {
			return fmt.Errorf("期望 object，实际 %T", val)
		}

	case "array":
		if !utils.IsSlice(val) {
			return fmt.Errorf("期望 array，实际 %T", val)
		}
	}

	return nil
}

// ActivatedEdgesMerge 合并激活边
func (c *Context) ActivatedEdgesMerge(m map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range m {
		c.ActivatedEdges[k] = v
	}
}

func (c *Context) ClearNodeOutput(nodeName string) {
	c.EnsureOutputInitialized()

	c.mu.Lock()
	defer c.mu.Unlock()

	nodesMap, _ := c.Output["nodes"].(map[string]any)
	if nodesMap == nil {
		return
	}

	nodeData, _ := nodesMap[nodeName].(map[string]any)
	if nodeData == nil {
		nodeData = map[string]any{}
		nodesMap[nodeName] = nodeData
	}

	delete(nodeData, "output")
}
