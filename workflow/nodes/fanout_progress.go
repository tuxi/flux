package nodes

import "flux/tool"

const TaskEventFanoutProgress = "fanout_progress"

type FanoutProgress struct {
	Kind         FanoutKind
	ParentNode   string
	ParentLabel  string
	Total        int
	Done         int
	Running      int
	Failed       int
	Reused       int
	CurrentIndex int
	Progress     float64
}

func EmitFanoutProgress(execCtx *NodeExecContext, p FanoutProgress) {
	if execCtx == nil || execCtx.TaskContext == nil || execCtx.TaskContext.EventBus == nil {
		return
	}
	if p.ParentNode == "" && execCtx.NodeDef != nil {
		p.ParentNode = execCtx.NodeDef.Name
	}
	if p.ParentLabel == "" && execCtx.NodeDef != nil {
		p.ParentLabel = execCtx.NodeDef.Label
	}
	if p.ParentLabel == "" {
		p.ParentLabel = p.ParentNode
	}
	if p.Progress == 0 && p.Total > 0 {
		p.Progress = float64(p.Done) / float64(p.Total)
	}
	execCtx.EmitToolEvent(tool.ToolEvent{
		Type:       "fanout",
		CustomType: TaskEventFanoutProgress,
		Message:    p.ParentLabel,
		Progress:   p.Progress,
		Data: map[string]any{
			"event_type":    TaskEventFanoutProgress,
			"fanout_kind":   string(p.Kind),
			"parent_node":   p.ParentNode,
			"parent_label":  p.ParentLabel,
			"total":         p.Total,
			"done":          p.Done,
			"running":       p.Running,
			"failed":        p.Failed,
			"reused":        p.Reused,
			"current_index": p.CurrentIndex,
			"progress":      p.Progress,
		},
	})
}
