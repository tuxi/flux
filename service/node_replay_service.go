package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"flux/definition"
	"flux/domain"
	engine2 "flux/engine"
	"flux/repository"
	"flux/tool"
)

type NodeReplayService interface {
	ReplayTaskNode(ctx context.Context, taskID int64, nodeName string, execute bool) (*NodeReplayResult, error)
}

type NodeReplayResult struct {
	TaskID         int64          `json:"task_id"`
	NodeName       string         `json:"node_name"`
	NodeState      string         `json:"node_state"`
	NodeType       string         `json:"node_type"`
	Tool           string         `json:"tool,omitempty"`
	ResolvedInput  map[string]any `json:"resolved_input,omitempty"`
	OriginalOutput map[string]any `json:"original_output,omitempty"`
	OriginalError  string         `json:"original_error,omitempty"`
	ReplayOutput   map[string]any `json:"replay_output,omitempty"`
	ReplayError    string         `json:"replay_error,omitempty"`
	Executed       bool           `json:"executed"`
}

type nodeReplayService struct {
	taskRepo            repository.TaskRepository
	nodeRuntimeRepo     repository.NodeRuntimeRepository
	workflowVersionRepo repository.WorkflowVersionRepository
	replayEngine        *engine2.Engine
	toolRegistry        *tool.Registry
}

func NewNodeReplayService(
	taskRepo repository.TaskRepository,
	nodeRuntimeRepo repository.NodeRuntimeRepository,
	workflowVersionRepo repository.WorkflowVersionRepository,
	replayEngine *engine2.Engine,
	toolRegistry *tool.Registry,
) NodeReplayService {
	return &nodeReplayService{
		taskRepo:            taskRepo,
		nodeRuntimeRepo:     nodeRuntimeRepo,
		workflowVersionRepo: workflowVersionRepo,
		replayEngine:        replayEngine,
		toolRegistry:        toolRegistry,
	}
}

func (s *nodeReplayService) ReplayTaskNode(ctx context.Context, taskID int64, nodeName string, execute bool) (*NodeReplayResult, error) {
	if s == nil {
		return nil, fmt.Errorf("node replay service is nil")
	}
	if s.replayEngine == nil {
		return nil, fmt.Errorf("node replay engine is nil")
	}
	if execute && s.toolRegistry == nil {
		return nil, fmt.Errorf("node replay tool registry is nil")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return nil, fmt.Errorf("node name is required")
	}

	task, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task not found: %d", taskID)
	}

	runtime, err := s.nodeRuntimeRepo.FindByTaskIDAndNode(ctx, taskID, nodeName)
	if err != nil || runtime == nil {
		return nil, fmt.Errorf("node runtime not found: %s", nodeName)
	}
	if !isNodeReplayAllowedState(runtime.State) {
		return nil, fmt.Errorf("node %s is in state %q, only success/failed nodes can be replayed", nodeName, runtime.State)
	}

	nodeDef, err := s.loadNodeDefinition(ctx, task.WorkflowVersionID, nodeName)
	if err != nil {
		return nil, err
	}
	if nodeDef.Type != definition.NodeTool {
		return nil, fmt.Errorf("node %s type %q is not replayable; only tool nodes are supported", nodeName, nodeDef.Type)
	}
	toolName, _ := nodeDef.Config["tool"].(string)
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return nil, fmt.Errorf("node %s missing config.tool", nodeName)
	}

	trace, err := s.replayEngine.Replay(ctx, taskID)
	if err != nil {
		return nil, err
	}
	frame := findReplayFrame(trace, nodeName)
	if frame == nil {
		return nil, fmt.Errorf("node %s was not found in replay trace", nodeName)
	}

	result := &NodeReplayResult{
		TaskID:         taskID,
		NodeName:       nodeName,
		NodeState:      string(runtime.State),
		NodeType:       string(nodeDef.Type),
		Tool:           toolName,
		ResolvedInput:  cloneMap(frame.ResolvedInput),
		OriginalOutput: cloneMap(frame.Output),
		OriginalError:  runtime.Error,
		Executed:       execute,
	}
	if !execute {
		return result, nil
	}

	t, ok := s.toolRegistry.Get(toolName)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", toolName)
	}
	if t.Mode() != tool.SyncExecution {
		return nil, fmt.Errorf("tool %s mode %q is not replayable via sync debug endpoint", toolName, t.Mode())
	}

	execCtx := tool.ContextWithExecutionMeta(ctx, tool.ExecutionMeta{
		UserID:   task.UserID,
		TaskID:   task.ID,
		RootID:   task.RootID,
		NodeName: nodeName,
	})
	replayOutput, replayErr := t.Execute(execCtx, cloneMap(frame.ResolvedInput), noopToolEmitter{})
	if replayErr != nil {
		result.ReplayError = replayErr.Error()
		return result, nil
	}
	if replayOutput == nil {
		result.ReplayError = "tool returned nil result"
		return result, nil
	}
	if !replayOutput.Success && strings.TrimSpace(replayOutput.Error) != "" {
		result.ReplayError = replayOutput.Error
	}
	result.ReplayOutput = cloneMap(replayOutput.Data)
	return result, nil
}

func (s *nodeReplayService) loadNodeDefinition(ctx context.Context, workflowVersionID int64, nodeName string) (*definition.NodeDefinition, error) {
	dbVersion, err := s.workflowVersionRepo.Get(ctx, workflowVersionID)
	if err != nil {
		return nil, err
	}
	if dbVersion == nil {
		return nil, fmt.Errorf("workflow version not found: %d", workflowVersionID)
	}
	var def definition.WorkflowDefinition
	if err := json.Unmarshal(dbVersion.DefinitionJSON, &def); err != nil {
		return nil, err
	}
	for i := range def.Nodes {
		if def.Nodes[i].Name == nodeName {
			return &def.Nodes[i], nil
		}
	}
	return nil, fmt.Errorf("node definition not found: %s", nodeName)
}

func isNodeReplayAllowedState(state domain.NodeState) bool {
	switch state {
	case domain.NodeSuccess, domain.NodeSuccessPendingEdges, domain.NodeFailed, domain.NodeFailedPendingEdges:
		return true
	default:
		return false
	}
}

func findReplayFrame(trace *engine2.ReplayTrace, nodeName string) *engine2.NodeTraceFrame {
	if trace == nil {
		return nil
	}
	for i := range trace.Nodes {
		if trace.Nodes[i].Name == nodeName {
			return &trace.Nodes[i]
		}
	}
	return nil
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		out := make(map[string]any, len(in))
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		out = make(map[string]any, len(in))
		for k, v := range in {
			out[k] = v
		}
	}
	return out
}

type noopToolEmitter struct{}

func (noopToolEmitter) EmitToolEvent(event tool.ToolEvent) {}
