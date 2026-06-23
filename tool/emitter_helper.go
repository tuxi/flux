package tool

func EmitStart(e ToolEmitter, msg string, data map[string]any) {
	if e == nil {
		return
	}
	e.EmitToolEvent(ToolEvent{
		Type:    "started",
		Message: msg,
		Data:    data,
	})
}

func EmitProgress(e ToolEmitter, progress float64, data map[string]any) {
	if e == nil {
		return
	}
	e.EmitToolEvent(ToolEvent{
		Type:     "progress",
		Progress: progress,
		Data:     data,
	})
}

func EmitStream(e ToolEmitter, message string, progress float64, data map[string]any) {
	if e == nil {
		return
	}
	e.EmitToolEvent(ToolEvent{
		Type:     "stream",
		Message:  message,
		Progress: progress,
		Data:     data,
	})
}

func EmitLog(e ToolEmitter, msg string, data map[string]any) {
	if e == nil {
		return
	}
	e.EmitToolEvent(ToolEvent{
		Type:     "log",
		Message:  msg,
		Data:     data,
		LogLevel: "info",
	})
}

func EmitWarning(e ToolEmitter, msg string, data map[string]any) {
	if e == nil {
		return
	}
	e.EmitToolEvent(ToolEvent{
		Type:     "log",
		Message:  msg,
		Data:     data,
		LogLevel: "warning",
	})
}

func EmitComplete(e ToolEmitter, msg string, data map[string]any) {
	if e == nil {
		return
	}
	e.EmitToolEvent(ToolEvent{
		Type:    "completed",
		Message: msg,
		Data:    data,
	})
}

func EmitFail(e ToolEmitter, err error, data map[string]any) {
	if e == nil {
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}

	e.EmitToolEvent(ToolEvent{
		Type:    "failed",
		Message: msg,
		Data:    data,
	})
}

// EmitTimeline sends a timeline-specific event (scene_created, ai_shot_started, etc.)
// through the ToolEmitter. The eventType is forwarded as-is through CustomType,
// bypassing the "tool_" prefix in TaskEvent.Type.
func EmitTimeline(e ToolEmitter, eventType string, data map[string]any) {
	if e == nil {
		return
	}
	e.EmitToolEvent(ToolEvent{
		Type:       "timeline",
		CustomType: eventType,
		Data:       data,
	})
}

// EmitPipelineStage sends a generation_pipeline_stage event carrying the full
// pipeline snapshot. Each event is a self-contained full snapshot — clients
// replace their local state directly without incremental accumulation.
// Uses CustomType to bypass the "tool_" prefix, same as EmitTimeline.
func EmitPipelineStage(e ToolEmitter, data map[string]any) {
	if e == nil {
		return
	}
	e.EmitToolEvent(ToolEvent{
		Type:       "pipeline",
		CustomType: "generation_pipeline_stage",
		Data:       data,
	})
}
