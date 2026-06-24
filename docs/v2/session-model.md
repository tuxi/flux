# Flux v2 会话模型

> 写作时间：2026-06-24。回答"agent 的对话/执行如何与现有 workflow 数据统一，而不分裂"。

---

## 核心洞察：agent run = task

agent 执行一个目标的过程——规划、调工具、看结果、再调——就是现有的 `tasks` + `task_nodes` + `task_events` 在建模的东西。**workflow 的一次执行和 agent 的一次执行，是一回事。**

| agent 概念 | 映射到现有模型 |
|---|---|
| agent 执行一个目标 | `tasks`（一次 run） |
| agent 调的每个工具 | `task_nodes`（节点 = 一次工具调用） |
| agent 发的事件流 | `task_events`（观测流，给 UI） |
| 异步工具等待 | `await_bindings` |
| 子调用（workflow-as-tool / 子 agent） | `tasks.parent_id` / `root_id`（已有，天然支持递归组合）|

**不造平行的"agent 执行"系统。agent run = task。**

---

## 缺的那一层：sessions（对话）

现有模型只描述"一次执行"（task），没有"一段对话"（session）。agent 的 LLM 上下文（planner messages:system/user/assistant/tool）**横跨多次 task**，它不属于任何一个 task——**它属于 session**。

### 表：`sessions`

```sql
CREATE TABLE sessions (
    id          BIGINT PRIMARY KEY,
    user_id     BIGINT NOT NULL,
    title       VARCHAR(255),
    workdir     VARCHAR(512),                -- CLI 用；服务端可空
    entry_type  VARCHAR(32) NOT NULL DEFAULT 'agent',  -- agent | workflow | static
    status      VARCHAR(32) NOT NULL DEFAULT 'active', -- active | archived
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);
```

- `entry_type`:agent = control loop / workflow = 人写的 WorkflowDefinition / static = DAG 一次性生成（三者都是"如何启动这次执行"）。
- agent 调 workflow-as-tool、或 workflow 节点 spawn 子 agent → 它们**共享同一个 session**（根 session），各自创建子 task（`parent_id` 链接）。
- status=archived 时不再出现在活跃列表里，但不删除数据。

### 表：`session_messages`

```sql
CREATE TABLE session_messages (
    id           BIGINT PRIMARY KEY,
    session_id   BIGINT NOT NULL REFERENCES sessions(id),
    seq          INT NOT NULL,               -- 消息序号（在 session 内单调递增）
    role         VARCHAR(16) NOT NULL,       -- system | user | assistant | tool
    content      TEXT,
    tool_calls   JSONB,                      -- assistant 消息的 tool_calls
    tool_call_id VARCHAR(64),                -- tool 消息对应的 call id
    created_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_session_messages_session ON session_messages(session_id, seq);
```

- 这是 LLM 工作记忆，**不是**给 UI 的观测流——后者是 `task_events`。两者不同，不重复。
- `seq` 保证消息的有序性，对续接时恢复上下文安全。
- `tool_calls` 存原始 tool_calls JSON，方便 LLM 继续时完全还原状态。

### 表：`tasks.session_id`（扩展现有表，不新建）

```sql
ALTER TABLE tasks ADD COLUMN session_id BIGINT REFERENCES sessions(id);
ALTER TABLE tasks ADD COLUMN session_seq INT;  -- 该 task 在 session 中的第几次 run
```

- 端到端：`sessions → tasks → task_nodes / task_events / await_bindings`。
- `session_seq` 区分"第 1 轮 run"和"第 3 轮 run"——以及 `--continue` 续接时的下一轮。
- 客户端查询：`GET /sessions/:id/messages` 拿对话，`GET /sessions/:id/tasks` 拿执行历史。

---

## 两个推论模型里天然成立

### 1. "agent 生成的 DAG 也是一个 workflow"

type-B agent 产出的 DAG → 落进 `workflows` / `workflow_versions`，执行落进 `task`。agent 生成的图**自动变成可复用 workflow**。

### 2. "静态 workflow 被 agent 当 tool 调用，也是一次 session"

agent 的 task 调一个 workflow-tool → spawn **子 task**（`parent_id` → agent task，`root_id` 锚到根 session）。一个 session 是一棵树：叶子是"纯工具调用"，分支是"调了一个子 task（workflow/子 agent）回到本 task 继续"。
**Claude Code 的递归组合在这里是天然成立的。**

`workflows`/`workflow_versions` 表不需要改——它们存储"定义"；session/task 存储"这一次被谁、怎么调用"。

---

## 统一入口图

```
客户端
  │
  ├── GET  /sessions          ← 我的历史对话列表
  ├── POST /sessions          ← 发起新对话/新执行
  ├── GET  /sessions/:id      ← 对话详情（messages + tasks）
  └── POST /sessions/:id/messages  ← 追加消息（触发下一轮 agent 执行 → 新 task）
```

无论对话是 agent（动态规划）还是 workflow（静态编译），都走同一组 API。`entry_type` 决定如何启动执行，但数据模型不变。

---

## SessionStore 端口

```go
package session

import "context"

type Store interface {
    Load(ctx context.Context, key string) (*Session, error)
    Save(ctx context.Context, s *Session) error
}

type Session struct {
    Key       string          // CLI: 工作目录；server: session id
    Title     string
    Workdir   string
    Messages  []model.Message // LLM 上下文
    UpdatedAt time.Time
}
```

- **CLI 单机**：`FileStore`（`~/.flux-agent/sessions/<key哈希>.json`），不引入 DB 依赖。
- **dream-ai 服务端**：Postgres `SessionStore`（读/写 `sessions` + `session_messages` 表），可共享给客户端。

**同一接口、同一数据形状**，从 CLI 切到服务端只需换实现——零数据迁移撕裂。

---

## 分阶段落地

| 阶段 | 落地内容 |
|---|---|
| A（当前） | `session.Store` 端口 + `FileStore`（JSON）。CLI 已有 `--continue`，不打乱。 |
| B/server 化 | Postgres `SessionStore` + `sessions` / `session_messages` 表 + `tasks.session_id`。客户端 API。 |
| 进阶 | `entry_type=static` 挂接 type-B DAGPlanner；`session_messages` 用于多 agent 共享上下文。 |
