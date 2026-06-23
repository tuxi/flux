package definition

// NodeDefinition 描述工作流中一个节点的定义，也是工作流中的数据流
type NodeDefinition struct {
	Name         string            `json:"name"`
	Label        string            `json:"label,omitempty"` // 面向客户端的展示名，不影响执行语义和版本
	Type         NodeType          `json:"type"`
	Weight       float64           `json:"weight"` // Weight 0～1 节点权重，关系到节点进度的计算
	Version      string            `json:"version"`
	InputMapping map[string]string `json:"input_mapping"` // 表达式输入
	Config       map[string]any    `json:"config"`        // 静态配置
}

type NodeType string

const (
	// NodeStart 开始节点
	NodeStart = "start"
	// NodeEnd 结束节点
	NodeEnd = "end"
	// NodeTool 工具节点
	NodeTool = "tool"
	// NodeSubWorkflow 子工作流节点
	//  子工作流共享 TaskID
	//	子节点写入父 Context
	//  SubWorkflow 是 Sync Node
	//	支持 InputMapping
	//  定义方式：
	// {
	//  "name": "frame_pipeline",
	//  "type": "subworkflow",
	//  "depends_on": ["loop_images"],
	//  "config": {
	//     "workflow": "frame_generation_workflow"
	//  },
	//  "input_mapping": {
	//     "image": "loop_images.image",
	//     "prompt": "prompt_enhance.prompt"
	//  }
	// }
	NodeSubWorkflow = "subworkflow"
	// NodeMap
	// {
	//  "name": "map_generate_video",
	//  "nodes": [
	//    {
	//      "name": "map_images",
	//      "type": "map",
	//      "config": {
	//        "items": "input.images",
	//        "iterator": "image",
	//        "workflow": "image_to_video_generate",
	//        "parallel": 4
	//      }
	//    }
	//  ]
	// }
	NodeMap = "map" // 并行生产

	NodeLoop = "loop" // 连续叙事能力的节点

	// NodeAwait 等待外部事件/输入的节点
	// 该节点的核心语义不是执行一个长任务，而是在运行到当前节点时：
	// 1. 创建等待订阅（AwaitBinding）
	// 2. 将任务挂起
	// 3. 等待 webhook / signal / poll 等外部来源唤醒
	NodeAwait = "await"
)
