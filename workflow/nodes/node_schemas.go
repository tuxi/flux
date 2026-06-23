package nodes

import (
	"flux/definition"
	"flux/tool"
)

var startNodeScheme = &NodeTypeSchema{
	Type:        definition.NodeStart,
	Description: "Start Node",
	ConfigSchema: tool.DataSchema{
		Fields: map[string]tool.FieldSchema{},
	},
}

var endNodeScheme = &NodeTypeSchema{
	Type:        definition.NodeEnd,
	Description: "End Node",
	ConfigSchema: tool.DataSchema{
		Fields: map[string]tool.FieldSchema{},
	},
}

var toolNodeScheme = &NodeTypeSchema{
	Type:        definition.NodeTool,
	Description: "Tool execution node",
	ConfigSchema: tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"tool": {
				Type:     "string",
				Required: true,
				Desc:     "Tool name",
			},
		},
	},
}

var MapNodeSchema = &NodeTypeSchema{

	Type: definition.NodeMap,

	Description: "Map execution node",

	ConfigSchema: tool.DataSchema{

		Fields: map[string]tool.FieldSchema{

			"items": {
				Type:     "string",
				Required: true,
			},

			"iterator": {
				Type:     "string",
				Required: true,
			},
			"workflow": {
				Type: "string",
				Desc: "Workflow name",
			},
			"parallel": {
				Type:     "number",
				Required: false,
				Desc:     "最大并发数",
			},
			"failure_policy": {
				Type:     "string",
				Required: false,
				Desc:     "子任务失败策略: fail_fast（默认，任一失败则Map失败）| partial（允许部分成功，失败项使用fallback兜底）",
			},
			"max_child_retries": {
				Type:     "number",
				Required: false,
				Desc:     "每个子任务最大自动重试次数，-1使用全局默认",
			},
			"fallback_source": {
				Type:     "string",
				Required: false,
				Desc:     "fallback数据来源: item（使用原始迭代项）",
			},
			"max_fallback_ratio": {
				Type:     "number",
				Required: false,
				Desc:     "允许的最大 fallback 比率（0.0-1.0），超出时 Map 失败（Phase 2 强制执行）",
			},
		},
	},
	ExprConfigFields: map[string]bool{
		"items": true, // items 不是静态变量，它是表达式
	},
}

var SubWorkflowNodeSchema = &NodeTypeSchema{

	Type: definition.NodeSubWorkflow,

	Description: "SubWorkflow node",

	ConfigSchema: tool.DataSchema{

		Fields: map[string]tool.FieldSchema{

			"workflow": {
				Type:     "string",
				Required: true,
			},
		},
	},
}

var LoopNodeSchema = &NodeTypeSchema{

	Type: definition.NodeLoop,

	Description: "Loop execution node",

	ConfigSchema: tool.DataSchema{

		Fields: map[string]tool.FieldSchema{

			"items": {
				Type:     "string",
				Required: true,
			},
			"iterator": {
				Type:     "string",
				Required: true,
			},
			"workflow": {
				Type: "string",
				Desc: "Workflow name",
			},
			// 每一轮执行loop携带的参数
			// "carry": {
			//  "prev_tail_frame": "tail_frame_url"
			// }
			// 下一轮 input.prev_tail_frame = 上一轮 output.tail_frame_url
			"carry": {
				Type:     "object",
				Required: false,
				Desc:     "每一轮循环携带的参数",
			},
			// 初始执行 loop 节点的参数
			// "initial": {
			//  "prev_tail_frame": "image_prepare.image_url"
			// }
			"initial": {
				Type:     "object",
				Required: false,
				Desc:     "初始时的参数",
			},
		},
	},
	ExprConfigFields: map[string]bool{
		"items": true, // items 不是静态变量，它是表达式
	},
}

var AwaitNodeSchema = &NodeTypeSchema{
	Type:        definition.NodeAwait,
	Description: "Await external event/input node",
	ConfigSchema: tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"await_type": {
				Type:     "string",
				Required: true,
			},
			"source": {
				Type:     "string",
				Required: true,
			},
			"provider": {
				Type:     "string",
				Required: false,
			},
			"signal_name": {
				Type:     "string",
				Required: false,
			},
			"callback_token_expr": {
				Type:     "string",
				Required: false,
			},
			"correlation": {
				Type:     "object",
				Required: false,
			},
			"completion": {
				Type:     "object",
				Required: false,
			},
			"fallback_poll": {
				Type:     "object",
				Required: false,
			},
			"timeout_seconds": {
				Type:     "number",
				Required: false,
			},
		},
	},
}
