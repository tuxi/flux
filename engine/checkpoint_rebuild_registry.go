package engine

import (
	"flux/domain"
	"flux/workflow/nodes"
	"fmt"
	"sync"
)

// engine/checkpoint_rebuild_registry.go

type CheckpointOutputRebuilder func(
	nodeDef nodes.Node,
	runtime *domain.NodeRuntime,
) (map[string]any, error)

type checkpointRebuildRegistry struct {
	mu         sync.RWMutex
	byNodeType map[string]CheckpointOutputRebuilder
}

func newCheckpointRebuildRegistry() *checkpointRebuildRegistry {
	return &checkpointRebuildRegistry{
		byNodeType: map[string]CheckpointOutputRebuilder{},
	}
}

func (r *checkpointRebuildRegistry) Register(nodeType string, fn CheckpointOutputRebuilder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byNodeType[nodeType] = fn
}

func (r *checkpointRebuildRegistry) Get(nodeType string) (CheckpointOutputRebuilder, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.byNodeType[nodeType]
	return fn, ok
}

func (r *checkpointRebuildRegistry) MustGet(nodeType string) CheckpointOutputRebuilder {
	fn, ok := r.Get(nodeType)
	if !ok {
		panic(fmt.Sprintf("checkpoint output rebuilder not registered for node type: %s", nodeType))
	}
	return fn
}
