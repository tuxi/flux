# 12 · 数据模型：DDL + GORM Entity（数据模型锁定）

> 架构已基本定型（[02](02-architecture-overview.md)）、状态机已定型（[11](11-agent-state-machine.md)），**下一步锁定数据模型**。本文给出 5 张新表的 PostgreSQL DDL 与 GORM Entity，作为可直接落库的权威定义。端到端最小闭环验证（含 `short_drama.yaml` 完整样例）见 [13](13-short-drama-skill-walkthrough.md)。
>
> 一条红线：**不动引擎任何既有表**（`tasks` / `task_events` / `await_bindings` / `workflow_*` 原样不变）。新表只通过 `agent_conversation_task_links.task_id` 单向引用 `tasks.id`。
>
> **表命名空间（重要）**：所有会话层表与索引都加 `agent_` 前缀——`agent_conversations` / `agent_conversation_messages` / `agent_conversation_plans` / `agent_conversation_task_links` / `agent_states` / `agent_outbox`。原因：生产 PostgreSQL 可能与其它服务（如聊天）**共库**，通用名 `conversations`/`conversation_messages` 会与其同名表撞库 → AutoMigrate 合并 schema → 插入命中对方 NOT NULL 列而失败。**以 entity 代码的 `TableName()` 为准。**

## 0. 约定（对齐现有 house style）

参照现有 `ai-engine/domain/entity/*.go`（如 `TaskModel`、`AwaitBindingModel`）：

| 约定 | 做法 |
|------|------|
| 包与文件 | 新建 `ai-engine/agent/entity/`，文件 `conversation.go` / `agent_state.go`；与 `domain/entity` 同款风格 |
| 命名 | PO 结构体后缀 `Model`，`func (XxxModel) TableName() string` 返回 snake_case 复数 |
| 主键 | `ID int64 \`gorm:"primaryKey"\``，**snowflake**，由仓储 `Create` 前用 `utils.GenSnowflakeID()` 赋值（与 `repository/query/task.go` 一致），不用自增 |
| JSON 列 | `datatypes.JSON \`gorm:"type:jsonb"\``（import `gorm.io/datatypes`） |
| 可空 | 指针类型 `*int64` / `*string` / `*time.Time` |
| 时间戳 | `CreatedAt time.Time` / `UpdatedAt time.Time`（GORM 自动维护），DB 用 `timestamptz` |
| 字符串 | `gorm:"type:varchar(N)"`；长文本 `type:text` |
| ID 在 JSON | 对客户端序列化为 string（[09 §0.3](09-conversation-api.md#03-id-序列化)）；DB 内仍 bigint |

> 迁移方式按项目惯例：GORM `AutoMigrate` 可据 tag 建表与单列索引；**复合/部分唯一索引** AutoMigrate 不会建，需用下文显式 SQL（放迁移脚本）。

## 1. ER 总览

```text
                          ┌────────────────────┐
                          │   agent_conversations     │ (1)
                          │  id, user_id, status│
                          └──────────┬─────────┘
              ┌──────────────────────┼───────────────────────┬─────────────────────┐
              │ (N)                  │ (1:1)                  │ (N)                 │ (N versions)
   ┌──────────▼──────────┐ ┌─────────▼─────────┐ ┌────────────▼──────────┐ ┌────────▼──────────┐
   │ agent_conversation_messages│ │   agent_states    │ │ agent_conversation_task_links│ │ agent_conversation_plans │
   │ id, seq, role, kind  │ │ conversation_id PK │ │ id, task_id, relation  │ │ id, version, slots │
   └──────────────────────┘ │ stage, slots(jsonb)│ └───────────┬───────────┘ └───────────────────┘
                            └───────────────────┘             │ task_id (单向引用)
                                                              ▼
                                                   ┌──────────────────────┐
                                                   │  tasks (现有, 不改)    │
                                                   └──────────────────────┘
```

## 2. `agent_conversations`

### DDL
```sql
CREATE TABLE agent_conversations (
    id                   BIGINT       PRIMARY KEY,
    user_id              BIGINT       NOT NULL,
    title                VARCHAR(200) NOT NULL DEFAULT '',
    status               VARCHAR(20)  NOT NULL DEFAULT 'active',  -- active|awaiting_user|running|completed|archived
    entry                VARCHAR(20)  NOT NULL DEFAULT 'home',    -- home|suggestion|marketplace|work_continue
    intent               VARCHAR(64)  NOT NULL DEFAULT '',        -- 冗余自 agent_state，便于列表过滤
    current_plan_id      BIGINT,
    context_task_id      BIGINT,                                  -- work_continue 的来源作品
    last_sequence        BIGINT       NOT NULL DEFAULT 0,         -- 会话内消息单调序号分配器
    last_message_preview VARCHAR(200) NOT NULL DEFAULT '',        -- 列表页直出
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    archived_at          TIMESTAMPTZ
);
-- 列表页主查询：我的会话按更新倒序、可按状态过滤
CREATE INDEX idx_agent_conversations_user_status_updated ON agent_conversations (user_id, status, updated_at DESC);
```

### GORM Entity
```go
package entity

import (
    "time"
    "gorm.io/datatypes"
)

type ConversationModel struct {
    ID     int64  `gorm:"primaryKey"`
    UserID int64  `gorm:"not null;index:idx_agent_conversations_user_status_updated,priority:1"`

    Title  string `gorm:"type:varchar(200);not null;default:''"`
    Status string `gorm:"type:varchar(20);not null;default:'active';index:idx_agent_conversations_user_status_updated,priority:2"`
    Entry  string `gorm:"type:varchar(20);not null;default:'home'"`
    Intent string `gorm:"type:varchar(64);not null;default:''"`

    CurrentPlanID *int64
    ContextTaskID *int64

    LastSequence       int64  `gorm:"not null;default:0"`
    LastMessagePreview string `gorm:"type:varchar(200);not null;default:''"`

    CreatedAt  time.Time `gorm:"index:idx_agent_conversations_user_status_updated,priority:3,sort:desc"`
    UpdatedAt  time.Time
    ArchivedAt *time.Time
}

func (ConversationModel) TableName() string { return "agent_conversations" }
```

> `last_sequence` 是**该会话消息序号的分配器**（见 §7.2），不是「最后一条的序号副本」——两者数值相等，但语义是「下一个 +1 从这里取」。

## 3. `agent_conversation_messages`

### DDL
```sql
CREATE TABLE agent_conversation_messages (
    id              BIGINT       PRIMARY KEY,
    conversation_id BIGINT       NOT NULL,
    sequence        BIGINT       NOT NULL,                  -- 会话内单调递增，断线增量游标
    role            VARCHAR(16)  NOT NULL,                  -- user|agent|system
    kind            VARCHAR(24)  NOT NULL,                  -- text|clarify|plan_card|progress|review_card|result_card|error_card|system
    text            TEXT         NOT NULL DEFAULT '',
    content_json    JSONB,                                  -- 结构化卡片载荷（options/plan/frames/result...）
    task_id         BIGINT,                                 -- 进度/结果类消息关联的引擎任务
    reply_to        BIGINT,                                 -- 回应的目标消息（如 signal 对应的卡）
    client_msg_id   VARCHAR(64),                            -- 幂等键
    grade           VARCHAR(16)  NOT NULL DEFAULT 'persistent', -- persistent|transient
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- 拉取/补偿：按会话 + sequence 升序；并保证序号唯一
CREATE UNIQUE INDEX uq_agent_messages_conv_seq ON agent_conversation_messages (conversation_id, sequence);
-- 幂等：同会话同 client_msg_id 只一条（仅对非空）
CREATE UNIQUE INDEX uq_agent_messages_conv_clientmsg ON agent_conversation_messages (conversation_id, client_msg_id)
    WHERE client_msg_id IS NOT NULL;
-- 按任务回溯该任务产生的消息
CREATE INDEX idx_agent_messages_task ON agent_conversation_messages (task_id) WHERE task_id IS NOT NULL;
```

### GORM Entity
```go
type ConversationMessageModel struct {
    ID             int64  `gorm:"primaryKey"`
    ConversationID int64  `gorm:"not null;uniqueIndex:uq_agent_messages_conv_seq,priority:1"`
    Sequence       int64  `gorm:"not null;uniqueIndex:uq_agent_messages_conv_seq,priority:2"`

    Role        string         `gorm:"type:varchar(16);not null"`
    Kind        string         `gorm:"type:varchar(24);not null"`
    Text        string         `gorm:"type:text;not null;default:''"`
    ContentJSON datatypes.JSON `gorm:"type:jsonb"`

    TaskID      *int64  `gorm:"index:idx_agent_messages_task"`
    ReplyTo     *int64
    ClientMsgID *string `gorm:"type:varchar(64)"` // 部分唯一索引用 SQL 建，见 DDL

    Grade     string    `gorm:"type:varchar(16);not null;default:'persistent'"`
    CreatedAt time.Time
}

func (ConversationMessageModel) TableName() string { return "agent_conversation_messages" }
```

> `grade='transient'` 的高频进度消息**可不入库**（[09 §3.4](09-conversation-api.md#34-服务端下行帧)）；若入库则按 `task_id` 原地覆盖，不参与 `sequence` 补偿。里程碑消息 `grade='persistent'`，占用 `sequence`，可被 [09 §2.5](09-conversation-api.md#25-拉消息首次进入--断线增量) 补回。

`content_json` 形状随 `kind` 而定（与 [03 §4](03-conversation-layer.md#4-消息类型kind) 一致），举例：
```jsonc
// kind=clarify
{ "slot": "characters", "ask": "想要几个角色？", "options": [{"value":2,"label":"2 个"}], "signal": "answer_slot" }
// kind=plan_card     → content_json.plan = §4 plan 对象 + {"signal":"confirm_plan"}
// kind=review_card   → { "card_type":"storyboard_review_card", "signal":"confirm_storyboard_image", "frames":[...] }
// kind=result_card   → { "task_id":"70010", "primary_file_url":"...", "actions":["再来一版","改一下"] }
```

## 4. `agent_conversation_plans`

### DDL
```sql
CREATE TABLE agent_conversation_plans (
    id              BIGINT       PRIMARY KEY,
    conversation_id BIGINT       NOT NULL,
    version         INT          NOT NULL DEFAULT 1,        -- 会话内计划版本（改参数→+1）
    intent          VARCHAR(64)  NOT NULL DEFAULT '',
    skill_key       VARCHAR(64)  NOT NULL DEFAULT '',
    slots_json      JSONB,                                  -- 定稿槽位：{filled, defaults_applied, missing_required, missing_optional}
    stages_json     JSONB,                                  -- ["规划剧情","生成分镜",...] 进度阶段名
    estimated_cost  BIGINT       NOT NULL DEFAULT 0,        -- 预估积分（整数）
    status          VARCHAR(16)  NOT NULL DEFAULT 'draft',  -- draft|confirmed|executing|done|revised
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_agent_plans_conv_version ON agent_conversation_plans (conversation_id, version);
```

### GORM Entity
```go
type ConversationPlanModel struct {
    ID             int64 `gorm:"primaryKey"`
    ConversationID int64 `gorm:"not null;uniqueIndex:uq_agent_plans_conv_version,priority:1"`
    Version        int   `gorm:"not null;default:1;uniqueIndex:uq_agent_plans_conv_version,priority:2"`

    Intent    string         `gorm:"type:varchar(64);not null;default:''"`
    SkillKey  string         `gorm:"type:varchar(64);not null;default:''"`
    SlotsJSON datatypes.JSON `gorm:"type:jsonb"`
    StagesJSON datatypes.JSON `gorm:"type:jsonb"`

    EstimatedCost int64  `gorm:"not null;default:0"`
    Status        string `gorm:"type:varchar(16);not null;default:'draft'"`

    CreatedAt time.Time
    UpdatedAt time.Time
}

func (ConversationPlanModel) TableName() string { return "agent_conversation_plans" }
```

> `slots_json` 是 **AgentState 草稿槽位齐备后的快照**（[11 §2](11-agent-state-machine.md#2-agentstate-结构) 的「草稿→定稿」）。`agent_conversations.current_plan_id` 指向当前生效版本。

## 5. `agent_conversation_task_links`

### DDL
```sql
CREATE TABLE agent_conversation_task_links (
    id                  BIGINT      PRIMARY KEY,
    conversation_id     BIGINT      NOT NULL,
    task_id             BIGINT      NOT NULL,                 -- 引擎 tasks.id（单向引用，无外键约束）
    plan_id             BIGINT,
    relation            VARCHAR(16) NOT NULL DEFAULT 'primary', -- primary|fork|subtask
    forked_from_task_id BIGINT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_agent_task_links_conv_task ON agent_conversation_task_links (conversation_id, task_id);
CREATE INDEX idx_agent_task_links_task ON agent_conversation_task_links (task_id);
```

### GORM Entity
```go
type ConversationTaskLinkModel struct {
    ID             int64 `gorm:"primaryKey"`
    ConversationID int64 `gorm:"not null;uniqueIndex:uq_agent_task_links_conv_task,priority:1"`
    TaskID         int64 `gorm:"not null;uniqueIndex:uq_agent_task_links_conv_task,priority:2;index:idx_agent_task_links_task"`

    PlanID           *int64
    Relation         string `gorm:"type:varchar(16);not null;default:'primary'"`
    ForkedFromTaskID *int64

    CreatedAt time.Time
}

func (ConversationTaskLinkModel) TableName() string { return "agent_conversation_task_links" }
```

> 不设 DB 外键到 `tasks`（保持与引擎解耦，避免跨层约束）。引用完整性在仓储层保证：建 link 前 task 已建。`relation=fork` + `forked_from_task_id` 让会话能表达「版本 2 来自版本 1」（[04 §7](04-agent-runtime.md#7-迭代式修改--复用-forkpatch)）。

## 6. `agent_states`（1:1 会话，[11](11-agent-state-machine.md) 的落库）

### DDL
```sql
CREATE TABLE agent_states (
    conversation_id    BIGINT      PRIMARY KEY,                -- 1:1
    stage              VARCHAR(20) NOT NULL DEFAULT 'idle',    -- 9 值，见文档 11 §4
    pending_message_id BIGINT,                                 -- 阻塞态待回应卡
    intent             VARCHAR(64) NOT NULL DEFAULT '',
    goal               VARCHAR(500) NOT NULL DEFAULT '',
    skill_key          VARCHAR(64) NOT NULL DEFAULT '',
    collected_slots    JSONB,                                  -- 草稿槽位（Plan 之前）
    missing_slots      JSONB,                                  -- string[]
    defaults_applied   JSONB,
    current_plan_id    BIGINT,
    current_task_id    BIGINT,
    last_message_seq   BIGINT      NOT NULL DEFAULT 0,         -- 已处理消息游标，防重
    version            INT         NOT NULL DEFAULT 0,         -- 乐观锁
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 主动唤醒/提醒：批量找卡在阻塞态太久的会话
CREATE INDEX idx_agent_states_stage_updated ON agent_states (stage, updated_at);
```

### GORM Entity
```go
type AgentStateModel struct {
    ConversationID   int64  `gorm:"primaryKey"` // 1:1，不自增
    Stage            string `gorm:"type:varchar(20);not null;default:'idle';index:idx_agent_states_stage_updated,priority:1"`
    PendingMessageID *int64

    Intent   string `gorm:"type:varchar(64);not null;default:''"`
    Goal     string `gorm:"type:varchar(500);not null;default:''"`
    SkillKey string `gorm:"type:varchar(64);not null;default:''"`

    CollectedSlots  datatypes.JSON `gorm:"type:jsonb"`
    MissingSlots    datatypes.JSON `gorm:"type:jsonb"`
    DefaultsApplied datatypes.JSON `gorm:"type:jsonb"`

    CurrentPlanID *int64
    CurrentTaskID *int64

    LastMessageSeq int64     `gorm:"not null;default:0"`
    Version        int       `gorm:"not null;default:0"`
    UpdatedAt      time.Time `gorm:"index:idx_agent_states_stage_updated,priority:2"`
}

func (AgentStateModel) TableName() string { return "agent_states" }
```

## 7. 横切关注点（实现要点）

### 7.1 ID 生成
所有新表 PK 在仓储 `Create` 前赋值 `utils.GenSnowflakeID()`，与 `repository/query/task.go` 同款；**不用 GORM autoIncrement**。`agent_states` 主键直接等于 `conversation_id`，不另生成。

### 7.2 会话内 `sequence` 分配（强一致）
`agent_conversation_messages.sequence` 必须会话内单调且唯一。分配在写消息的同一事务内完成：
```sql
-- 单事务内：占号 + 插消息 + 更新预览
UPDATE agent_conversations SET last_sequence = last_sequence + 1, updated_at = now()
  WHERE id = $conv RETURNING last_sequence;          -- 行锁，拿到本条 seq
INSERT INTO agent_conversation_messages (id, conversation_id, sequence, ...) VALUES (...);
```
`uq_agent_messages_conv_seq` 兜底防并发重号。**WS 下行帧的 `sequence` 即此值**，客户端据此断线增量恢复（[09 §3.5](09-conversation-api.md#35-断线恢复闭环与任务页一致)）。

### 7.3 幂等
写消息/信号带 `client_msg_id`；`uq_agent_messages_conv_clientmsg` 保证重复提交不产生第二条，仓储遇唯一冲突时返回首条（[09 §0.5](09-conversation-api.md#05-幂等)）。

### 7.4 AgentState 乐观锁（CAS）
用户消息与引擎事件可能几乎同时改 AgentState。更新走 CAS：
```sql
UPDATE agent_states SET stage=$new, ..., version=version+1, updated_at=now()
  WHERE conversation_id=$conv AND version=$expected;   -- 影响 0 行 → 重读重试
```
对应 [11 §8](11-agent-state-machine.md#8-恢复语义过一天回来) 的「无状态处理器 + rehydrate」。

### 7.5 status 一致性
每次 AgentState 跃迁后，用纯函数把 `agent_states.stage` 映射写回 `agent_conversations.status`（[11 §7](11-agent-state-machine.md#7-agentstatestage--agent_conversationstatus-映射)），二者同事务更新。列表页只读 `agent_conversations`，无需 join `agent_states`。

### 7.6 归属隔离
所有读写按 `agent_conversations.user_id == 当前用户` 校验（与 `tasks` 归属校验同源，见 [WS 恢复规范](../../../docs/workflow_ws_subscription_recovery_spec.md) 对越权的处理）。

## 8. 辅助表（审计 + 副作用出箱）

### 8.1 `agent_state_transitions`（可选·审计黑匣子）
```sql
CREATE TABLE agent_state_transitions (
    id              BIGINT      PRIMARY KEY,
    conversation_id BIGINT      NOT NULL,
    from_stage      VARCHAR(20) NOT NULL,
    to_stage        VARCHAR(20) NOT NULL,
    trigger         VARCHAR(64) NOT NULL,        -- user_message|signal:confirm_plan|event:task_succeeded...
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_state_transitions_conv ON agent_state_transitions (conversation_id, created_at);
```
第一版 MAY 省略；保留则可回答「Agent 为什么卡在这」（[11 §3.2](11-agent-state-machine.md#32-agent_state_transitions可选审计日志)）。

### 8.2 `agent_outbox`（推荐·副作用出箱）
承载红线 4：跨服务副作用（建任务/通知/webhook…）在 UoW 事务内**只入箱**，与本地写一起原子提交；提交后由 Outbox Worker 投递。Pattern 与时序见 [14 §4.2](14-repository-contracts.md#42-post-commit-创建任务--outbox已拍板)。

```sql
CREATE TABLE agent_outbox (
    id              BIGINT       PRIMARY KEY,
    conversation_id BIGINT       NOT NULL,
    type            VARCHAR(40)  NOT NULL,                  -- create_task | notify | webhook | audit ...
    payload         JSONB        NOT NULL,                  -- 投递所需参数（如 LaunchIntent: plan_id/skill/input）
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',-- pending|processing|done|failed
    dedup_key       VARCHAR(128),                           -- 投递幂等键（至少一次→防重复建任务）
    attempts        INT          NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_retry_at   TIMESTAMPTZ,
    worker_id       VARCHAR(64),                            -- 抢占者，防多 Worker 重复消费
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- Worker 抢一批待投递：按 status + next_retry_at
CREATE INDEX idx_outbox_dispatch ON agent_outbox (status, next_retry_at);
-- 幂等：同一 dedup_key 只投一次
CREATE UNIQUE INDEX uq_outbox_dedup ON agent_outbox (dedup_key) WHERE dedup_key IS NOT NULL;
```

```go
type AgentOutboxModel struct {
    ID             int64          `gorm:"primaryKey"`
    ConversationID int64          `gorm:"not null"`
    Type           string         `gorm:"type:varchar(40);not null"`
    Payload        datatypes.JSON `gorm:"type:jsonb;not null"`
    Status         string         `gorm:"type:varchar(16);not null;default:'pending';index:idx_outbox_dispatch,priority:1"`
    DedupKey       *string        `gorm:"type:varchar(128)"` // 部分唯一索引用 SQL 建
    Attempts       int            `gorm:"not null;default:0"`
    LastError      *string        `gorm:"type:text"`
    NextRetryAt    *time.Time     `gorm:"index:idx_outbox_dispatch,priority:2"`
    WorkerID       *string        `gorm:"type:varchar(64)"`
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

func (AgentOutboxModel) TableName() string { return "agent_outbox" }
```

> 第一版即建议落地（[14 §4.2](14-repository-contracts.md#42-post-commit-创建任务--outbox已拍板)）：Agent 副作用会越来越多，统一走出箱后「事务内入箱、提交后投递、失败可重试」一劳永逸。Worker 形态建议复用现有 `AwaitPollWorker`/`RecoveryScanner` 框架。

## 9. 迁移与注册清单
1. 新建 `ai-engine/agent/entity/` 放上述 5 核心 + `agent_outbox`（+ 可选 `agent_state_transitions`）共 6–7 个 `*Model`。
2. 加入项目的 `AutoMigrate` 列表（建表 + 单列索引）。
3. 复合/部分唯一索引（`uq_agent_messages_conv_seq` / `uq_agent_messages_conv_clientmsg` / `uq_agent_plans_conv_version` / `uq_agent_task_links_conv_task` / `uq_outbox_dedup` / 部分索引）用迁移 SQL 显式建。
4. 仓储层（`ai-engine/agent/repository/`）：Conversation/Message/Plan/TaskLink/AgentState/Outbox 共 6 个 Repository，封装 §7.2–§7.4 的事务/CAS 语义（[14](14-repository-contracts.md)）。
5. 不改任何引擎既有表与迁移。

## 10. 各文档对本表的消费关系
| 表 | 主要消费方 |
|----|-----------|
| agent_conversations | [09](09-conversation-api.md) 列表/详情、[11 §7](11-agent-state-machine.md) status 投影 |
| agent_conversation_messages | [09 §2.5/§3](09-conversation-api.md)（拉取/WS）、[06](06-client-architecture.md) Feed 渲染 |
| agent_conversation_plans | [04 §4.5/§5](04-agent-runtime.md)（Plan/报价）、[09 §2.7](09-conversation-api.md) plan_card |
| agent_conversation_task_links | [04 §5/§7](04-agent-runtime.md)（启动/fork）、引擎 `tasks` |
| agent_states | [11](11-agent-state-machine.md) 全篇、[09 §2.3](09-conversation-api.md) 详情 |

## 11. 待定决策
- **transient 进度是否入库**：建议「阶段切换入库（persistent，占 sequence）、百分比刷新不入库」，与现有 TaskEvent 分层一致；本表 `grade` 列已为两种策略留口。
- **plan 是否需要软删除/历史保留**：当前用 `version` 多版本累积，不软删；若历史版本过多再议归档。
- **agent_state 是否合并进 agent_conversations**：本文按独立表（利于 `stage` 索引扫描阻塞会话）；若运营/扫描需求弱，可后续合并为 `agent_conversations.agent_state jsonb`，对上层透明。

---

返回：[v2/README.md](README.md) · 相关：[03 数据模型](03-conversation-layer.md) · [09 API](09-conversation-api.md) · [11 状态机](11-agent-state-machine.md) · 规范：[10 Skill Manifest](10-skill-manifest-spec.md) · 端到端样例：[13 short_drama 闭环](13-short-drama-skill-walkthrough.md)
