// Package session 定义 agent/workflow 的"对话会话"持久化抽象。
//
// 统一模型（见 docs/v2/session-model.md）：一个 session 是一段对话；它之上是执行层
// ——现有的 tasks / task_nodes / task_events（agent 跑一次 = 一个 task）。
//
// Store 是端口：
//   - 单机 CLI       → FileStore（本地 JSON）
//   - dream-ai 服务端 → Postgres 实现（sessions / session_messages 表 + 现有 repos）
//
// 同一接口、同一数据形状，只是落地介质不同 —— 避免会话与原有 workflow 数据脱离/碎片化。
package session

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"flux/model"
)

// Session 是一段对话：LLM 上下文（Messages）+ 元数据。
// 对应未来 DB 的 sessions（元数据）+ session_messages（Messages，一行一条）。
type Session struct {
	Key       string          `json:"key"`     // CLI: 工作目录；server: session id
	Title     string          `json:"title,omitempty"`
	Workdir   string          `json:"workdir,omitempty"`
	Messages  []model.Message `json:"messages"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Store 会话持久化端口。Load 未命中返回 (nil, nil)（不是错误）。
type Store interface {
	Load(ctx context.Context, key string) (*Session, error)
	Save(ctx context.Context, s *Session) error
}

// FileStore 把每个 session 存成 <dir>/<key哈希>.json —— 单机 CLI 的实现。
type FileStore struct{ dir string }

func NewFileStore(dir string) *FileStore { return &FileStore{dir: dir} }

var _ Store = (*FileStore)(nil)

func (f *FileStore) path(key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(f.dir, hex.EncodeToString(sum[:8])+".json")
}

func (f *FileStore) Load(_ context.Context, key string) (*Session, error) {
	data, err := os.ReadFile(f.path(key))
	if os.IsNotExist(err) {
		return nil, nil // 未命中：还没有会话
	}
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (f *FileStore) Save(_ context.Context, s *Session) error {
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.path(s.Key), data, 0o644)
}
