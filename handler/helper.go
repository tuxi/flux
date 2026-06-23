package handler

import (
	"flux/definition"
	"flux/domain"
	"flux/workflow/nodes"
	"fmt"
	"sort"
	"strings"
)

func extractMapItemsPath(parentNode nodes.Node) string {
	raw, ok := parentNode.Config["items"]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func getValueByPath(root map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}

	parts := strings.Split(path, ".")
	var current any = root

	for _, p := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[p]
		if !ok {
			return nil, false
		}
		current = v
	}

	return current, true
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int8:
		return int(n)
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint:
		return int(n)
	case uint8:
		return int(n)
	case uint16:
		return int(n)
	case uint32:
		return int(n)
	case uint64:
		return int(n)
	case float32:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func intPtr(v int) *int {
	return &v
}

func stateAt(states []string, idx int) string {
	if idx >= 0 && idx < len(states) && states[idx] != "" {
		return states[idx]
	}
	return string(domain.NodePending)
}

func progressFromState(state string) float64 {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "success", "node_success", "task_success", "reuse":
		return 1
	case "running", "ready", "retrying":
		return 0.5
	case "failed", "node_failed", "task_failed":
		return 1
	default:
		return 0
	}
}

func actionFromItemState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "reuse":
		return "reuse"
	default:
		return "execute"
	}
}

func aggregateItemStates(states []string) string {
	if len(states) == 0 {
		return string(domain.NodePending)
	}

	hasRunning := false
	hasFailed := false
	allSuccess := true

	for _, s := range states {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "failed", "node_failed", "task_failed":
			hasFailed = true
			allSuccess = false
		case "running", "ready", "retrying":
			hasRunning = true
			allSuccess = false
		case "success", "node_success", "task_success", "reuse":
			// keep allSuccess possible
		default:
			allSuccess = false
		}
	}

	switch {
	case hasFailed:
		return string(domain.NodeFailed)
	case hasRunning:
		return string(domain.NodeRunning)
	case allSuccess:
		return string(domain.NodeSuccess)
	default:
		return string(domain.NodePending)
	}
}

func edgeKindFromDef(e definition.EdgeDefinition) string {
	if e.Condition != "" || e.CaseKey != "" || e.Type == definition.EdgeCondition {
		return "condition"
	}
	return "normal"
}

func sanitizeBranchScope(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "branch"
	}
	replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ".", "_", ":", "_", "-", "_")
	return replacer.Replace(s)
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := m[s]; ok {
			continue
		}
		m[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func containsString(arr []string, target string) bool {
	for _, s := range arr {
		if s == target {
			return true
		}
	}
	return false
}
