package nodes

// 子工作流输入继承统一规则：
//
// 1. 所有会派发子工作流的节点（SubWorkflow / Map / Loop）
//    都必须以 execCtx.Input 作为 base input。
//    execCtx.Input 是当前节点已经完成 Config + InputMapping + fallback 解析后的最终节点输入。
//
// 2. 禁止直接使用 execCtx.TaskContext.Input 作为子工作流继承来源。
//    TaskContext.Input 是任务/工作流级输入，不代表当前节点最终解析结果，
//    在复杂 DAG 场景下会导致节点 InputMapping 丢失，或节点配置字段污染子工作流输入。
//
// 3. Map / Loop 等节点自己的执行配置字段（如 items / iterator / workflow / carry / initial / parallel）
//    属于节点运行时配置，不应传递给子工作流业务层。
//
// 4. 子工作流的 iterator item、carry state、system fields
//    在 base input 之后再注入，必要时覆盖同名字段。

func isSubWorkflowSystemKey(key string) bool {
	switch key {
	case "index",
		"__map_item_hash",
		"__loop_attempt_token":
		return true
	default:
		return false
	}
}

func isFanoutInternalInputKey(key string) bool {
	switch key {
	case "items",
		"iterator",
		"workflow",
		"carry",
		"initial",
		"parallel":
		return true
	default:
		return false
	}
}

// cloneNodeResolvedInputForSubWorkflow 继承当前节点 resolved input”的公共函数
func cloneNodeResolvedInputForSubWorkflow(
	execCtx *NodeExecContext,
	skipFn func(string) bool,
) map[string]any {
	out := map[string]any{}

	for k, v := range execCtx.Input {
		if skipFn != nil && skipFn(k) {
			continue
		}
		if isSubWorkflowSystemKey(k) {
			continue
		}
		out[k] = v
	}

	return out
}

// buildLoopSubWorkflowInput Loop 节点的 input 专用构造器
func buildLoopSubWorkflowInput(
	execCtx *NodeExecContext,
	cp map[string]any,
	iterator string,
	item any,
	currentIndex int,
	attemptToken string,
) map[string]any {
	subInput := map[string]any{
		iterator:               item,
		"index":                currentIndex,
		loopHiddenAttemptToken: attemptToken,
	}

	// 统一规则：继承当前节点解析后的输入
	for k, v := range execCtx.Input {
		if k == iterator {
			continue
		}
		if isFanoutInternalInputKey(k) {
			continue
		}
		subInput[k] = v
	}

	// Loop 的 carry_state 最后覆盖
	carryState, _ := cp[loopCPCarryState].(map[string]any)
	if carryState != nil {
		for k, v := range carryState {
			subInput[k] = v
		}
	}

	return subInput
}

// buildMapSubWorkflowInput Map 节点的 input 专用构造器
func buildMapSubWorkflowInput(
	execCtx *NodeExecContext,
	iterator string,
	item any,
	index int,
	itemHash string,
) map[string]any {
	subInput := map[string]any{
		iterator:          item,
		"index":           index,
		"__map_item_hash": itemHash,
	}

	// 统一规则：子工作流继承“当前节点已解析后的输入”
	for k, v := range execCtx.Input {
		if k == iterator {
			continue
		}
		if isFanoutInternalInputKey(k) {
			continue
		}
		subInput[k] = v
	}

	return subInput
}
