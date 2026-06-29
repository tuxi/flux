package definition

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/tuxi/flux/utils"
	"sort"
)

// WorkflowDefinition 表示一个“工作流产品”，定义一个工作流的摸吧
// Graph DSL
// 标准的 DSL 数据结构
//
//	{
//	 "name": "text_to_video",
//	 "nodes": [
//	   { "name": "start", "type": "start" },
//	   { "name": "script_gen", "type": "llm" },
//	   { "name": "video_gen", "type": "video" },
//	   { "name": "end", "type": "end" }
//	 ],
//	 "edges": [
//	   { "from": "start", "to": "script_gen" },
//	   { "from": "script_gen", "to": "video_gen" },
//	   { "from": "video_gen", "to": "end" }
//	 ],
//	 "output_mapping": {
//	   "video_url": "nodes.video_gen.output.url",
//	   "script": "nodes.script_gen.output.text"
//	 }
//	}

// OutputDefinition 定义了如何从节点中提取数据来填充最终的 WorkflowOutput
type OutputDefinition struct {
	ResultType     string `json:"result_type"`      // 固定值，如 "video"
	PrimaryFileUrl string `json:"primary_file_url"` // 表达式，如 "nodes.upload_result.output.url"
	CoverUrl       string `json:"cover_url"`        // 表达式
	PreviewUrl     string `json:"preview_url"`      // 表达式
	Width          string `json:"width"`            // 表达式
	Height         string `json:"height"`           // 表达式
	Duration       string `json:"duration"`         // 表达式

	// 允许通过表达式注入一些业务自定义字段
	Extras map[string]string `json:"extras"` // Value 也是表达式
}

// CreativeDetailBuilderDefinition 定义查询 creative_detail 时使用的只读构建器。
// 它不参与 DAG 执行，只用于 CreativeDetailService 从 task_nodes 动态再生详情。
type CreativeDetailBuilderDefinition struct {
	Tool         string            `json:"tool"`
	Config       map[string]any    `json:"config,omitempty"`
	InputMapping map[string]string `json:"input_mapping,omitempty"`
	Version      string            `json:"version,omitempty"`
}

// TimelineBuilderDefinition 定义查询视频时间轴时使用的只读构建器。
// 它不参与 DAG 执行，只用于 VideoTimelineService 从 task_nodes 动态再生剪辑时间轴。
type TimelineBuilderDefinition struct {
	Tool         string            `json:"tool"`
	Config       map[string]any    `json:"config,omitempty"`
	InputMapping map[string]string `json:"input_mapping,omitempty"`
	Version      string            `json:"version,omitempty"`
}

type WorkflowDefinition struct {
	Name                  string                           `json:"name"`
	Nodes                 []NodeDefinition                 `json:"nodes"`
	Edges                 []EdgeDefinition                 `json:"edges"`
	Output                OutputDefinition                 `json:"output"`
	CreativeDetailBuilder *CreativeDetailBuilderDefinition `json:"creative_detail_builder,omitempty"`
	TimelineBuilder       *TimelineBuilderDefinition       `json:"timeline_builder,omitempty"`
	Desc                  string                           `json:"description"`
}

func (def *WorkflowDefinition) Hash() string {
	hash := hashWorkflow(def)
	return hash
}

type workflowHashStruct struct {
	Name                  string                           `json:"name"`
	Desc                  string                           `json:"desc,omitempty"`
	Output                OutputDefinition                 `json:"output,omitempty"`
	CreativeDetailBuilder *creativeDetailBuilderHashStruct `json:"creative_detail_builder,omitempty"`
	TimelineBuilder       *timelineBuilderHashStruct       `json:"timeline_builder,omitempty"`
	Nodes                 []nodeHashStruct                 `json:"nodes"`
	Edges                 []edgeHashStruct                 `json:"edges"`
}

type creativeDetailBuilderHashStruct struct {
	Tool         string            `json:"tool"`
	Config       map[string]any    `json:"config,omitempty"`
	InputMapping map[string]string `json:"input_mapping,omitempty"`
	Version      string            `json:"version,omitempty"`
}

type timelineBuilderHashStruct struct {
	Tool         string            `json:"tool"`
	Config       map[string]any    `json:"config,omitempty"`
	InputMapping map[string]string `json:"input_mapping,omitempty"`
	Version      string            `json:"version,omitempty"`
}

type nodeHashStruct struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Weight       float64           `json:"weight"`
	Config       map[string]any    `json:"config,omitempty"`
	InputMapping map[string]string `json:"input_mapping,omitempty"`
}

type edgeHashStruct struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Type      string `json:"type"`
	Condition string `json:"condition,omitempty"`
	CaseKey   string `json:"case_key,omitempty"`
	Priority  int    `json:"priority,omitempty"`
	Label     string `json:"label,omitempty"`
}

// HashWorkflow Workflow Hash 算法
// 只要影响执行语义的字段变化，hash 就必须变化
func hashWorkflow(def *WorkflowDefinition) string {
	hashStruct := workflowHashStruct{
		Name: def.Name,
		Desc: def.Desc,
		// 使用克隆方法
		Output:                def.Output.cloneOutputDefinition(),
		CreativeDetailBuilder: cloneCreativeDetailBuilderHash(def.CreativeDetailBuilder),
		TimelineBuilder:       cloneTimelineBuilderHash(def.TimelineBuilder),
		Nodes:                 make([]nodeHashStruct, 0, len(def.Nodes)),
		Edges:                 make([]edgeHashStruct, 0, len(def.Edges)),
	}

	for _, node := range def.Nodes {
		hashStruct.Nodes = append(hashStruct.Nodes, nodeHashStruct{
			Name:         node.Name,
			Type:         string(node.Type),
			Weight:       node.Weight,
			Config:       utils.NormalizeAnyMap(node.Config),
			InputMapping: utils.CloneStringMap(node.InputMapping),
		})
	}

	for _, edge := range def.Edges {
		hashStruct.Edges = append(hashStruct.Edges, edgeHashStruct{
			From:      edge.From,
			To:        edge.To,
			Type:      string(edge.Type),
			Condition: edge.Condition,
			CaseKey:   edge.CaseKey,
			Priority:  edge.Priority,
			Label:     edge.Label,
		})
	}

	// 保证顺序稳定
	sort.Slice(hashStruct.Nodes, func(i, j int) bool {
		return hashStruct.Nodes[i].Name < hashStruct.Nodes[j].Name
	})

	sort.Slice(hashStruct.Edges, func(i, j int) bool {
		a := hashStruct.Edges[i]
		b := hashStruct.Edges[j]

		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Condition != b.Condition {
			return a.Condition < b.Condition
		}
		if a.CaseKey != b.CaseKey {
			return a.CaseKey < b.CaseKey
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.Label < b.Label
	})

	js, _ := json.Marshal(hashStruct)
	sum := sha256.Sum256(js)
	return hex.EncodeToString(sum[:])
}

// cloneOutputDefinition 深度克隆 OutputDefinition 结构体
func (src OutputDefinition) cloneOutputDefinition() OutputDefinition {
	dst := src
	// Extras 是 map，属于引用类型，必须执行深拷贝
	if src.Extras != nil {
		dst.Extras = make(map[string]string, len(src.Extras))
		for k, v := range src.Extras {
			dst.Extras[k] = v
		}
	}
	return dst
}

func cloneCreativeDetailBuilderHash(src *CreativeDetailBuilderDefinition) *creativeDetailBuilderHashStruct {
	if src == nil {
		return nil
	}
	return &creativeDetailBuilderHashStruct{
		Tool:         src.Tool,
		Config:       utils.NormalizeAnyMap(src.Config),
		InputMapping: utils.CloneStringMap(src.InputMapping),
		Version:      src.Version,
	}
}

func cloneTimelineBuilderHash(src *TimelineBuilderDefinition) *timelineBuilderHashStruct {
	if src == nil {
		return nil
	}
	return &timelineBuilderHashStruct{
		Tool:         src.Tool,
		Config:       utils.NormalizeAnyMap(src.Config),
		InputMapping: utils.CloneStringMap(src.InputMapping),
		Version:      src.Version,
	}
}
