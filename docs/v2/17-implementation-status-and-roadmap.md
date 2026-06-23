# 17 · 实现现状审计与后续路线图（Agent First 阶段性收口）

> **本文是 Agent First 当前唯一的阶段性状态总览（single source of truth）。** 当 00–16 与本文冲突时，**以本文 + 真实代码为准**。
>
> 审计基准：当前 `feat/agent-first-v2` 分支代码 + 多轮真实联调（含 short_drama 完整成片 + /works 验证）。审计时间：2026-06。
>
> **阶段性结论**：现在不是「Agent 架构还没做完」，而是 **short_drama Agent First MVP 主体已经成立**，应从「架构搭建期」转入「稳定 / 体验 / 通用性验证期」。本轮只对账文档、不写新业务代码。

---

## 0. 一句话现状

| 问题 | 回答 |
|---|---|
| 当前完成了多少？ | 单 Skill（short_drama）的**对话→计划→执行→审核→成片→作品→迭代**全链路已跑通并经真实测试 |
| 能上线 short_drama MVP 吗？ | **接近可以**，但需先过 Phase A 的真机稳定性清单（见 §6）——尤其 WS 真机、断线恢复、失败/积分语义 |
| 客户端 / 服务端还缺什么？ | 服务端缺：WS 恢复增强、Activity children、Manifest 深度校验。客户端缺：真机回归、断线恢复、作品跳转闭环 |
| 接第二个 Skill 前必须做什么？ | Manifest 工程化（深度校验 + 注册纪律）+ 复核 Rule Runtime 上限（见 §5 Phase C） |
| 现在**不应该**做什么？ | LLM fallback、RAG、多 Agent、节点级 patch、第二 Skill 的并行开工——全部 P2，单 Skill 阶段勿过早引入 |

---

## 1. 当前架构快照（真实执行链路）

```text
iOS（会话式 UI）
  │  REST: /ai/agent/conversations/*        WS: /ai/ws（与 task event 同一条 socket）
  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ handler/conversation_handler.go   REST 入口 + DTO（handler/dto.go）      │
├─────────────────────────────────────────────────────────────────────┤
│ service/conversation_service.go   规范回合（canonical turn）            │
│   · 构造 ConversationContext = AgentState + RecentWindow + Input + Plan │
│   · advanceTurn：append 消息(seq, 幂等) → CAS 状态 → pending 跟踪         │
│   · confirmPlan：写 Outbox（不在事务内建 task）                          │
│   · UnitOfWork（repository/query/uow.go）：一次事务，6 个 tx 绑定仓储     │
├─────────────────────────────────────────────────────────────────────┤
│ runtime/runtime.go  AgentRuntime.Respond / ConfirmPlan（决策大脑）       │
│   · intent: rule_intent.go（带上下文）→ ports.IntentClassifier          │
│   · slot:   rule_slots.go → ports.SlotExtractor                        │
│   · 产出 Decision：PlanCard / Clarify / Text / Activity / Launch         │
├─────────────────────────────────────────────────────────────────────┤
│ skill/  Registry + LoadEmbedded + Validate（manifests/short_drama.yaml） │
└─────────────────────────────────────────────────────────────────────┘
  │ confirm_plan 后：Outbox（repository/query/outbox.go）
  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ worker/outbox_worker.go  Post-Commit 消费 → TaskLauncher                │
│ server/agent_outbox_launcher.go  建 engine Task（带 works 元数据）+ 入队  │
└─────────────────────────────────────────────────────────────────────┘
  │ ★ 零改动边界 ★（Agent 只走「建 Task → 监听 TaskEvent → 发 signal」）
  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Workflow Engine（现有·不动）  short_drama DAG · await/signal · TaskEvent │
└─────────────────────────────────────────────────────────────────────┘
  │ TaskEvent（EventBus）
  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ observer/observer.go  唯一桥（红线 3）                                   │
│   · 主/子任务分层（isMainTaskEvent）                                     │
│   · pipeline_stage → Activity 步进 + ReviewCard gate                    │
│   · 主任务终态 → finalize()：Activity 收尾 + Result/Error（幂等+互斥）     │
└─────────────────────────────────────────────────────────────────────┘
  │ MessageNotifier（server/agent_ws.go）→ WSHub.Publish(conversation:<id>)
  ▼
iOS：按 message.id upsert（同一条 activity 原地替换）；断线靠 GET 兜底
```

**依赖方向**：`handler → service → {runtime, repository, skill}`；`runtime → service`（仅 Decision/Context 类型）；`observer → {repository, skill, activity, engine/domain}`（单向，agent 可依赖 engine domain，engine 永不依赖 agent）。

**各层 LOC**（`ai-engine/agent`，约 4.9k 行非测试代码）：service 702 · runtime 472 · observer 448 · repository/query ~1.1k · domain 228 · handler 516 · skill 289 · activity 278 · worker 220。

---

## 2. 实现状态矩阵

图例：✅ Implemented · 🟡 Partial（最小实现，有明确边界）· 📄 Documented Only · ⛔ Not Started · ⏸ Deferred（有意延后）

| 领域 | 能力 | 状态 | 真实位置 | 已知边界 |
|---|---|---|---|---|
| **Conversation** | 创建 / 详情 / 列表 / 重命名 / 归档 | ✅ | `handler/conversation_handler.go:36`、`service` | — |
| | 消息 append + sequence（事务内递增，红线 1） | ✅ | `repository/query/message.go`、`conversation.go` NextSequence | — |
| | 幂等（client_msg_id / pending_message_id） | ✅ | `message.go` FindByClientMsgID；service advanceTurn | — |
| **Context** | ConversationContext = State+RecentWindow+Input+Plan | ✅ | `service/conversation_service.go`（struct + advanceTurn 构造） | RecentWindow=最近 10 条 |
| | AgentState 工作记忆 + CAS（红线 2） | ✅ | `repository/query/agent_state.go` CompareAndSwap | 9 状态机 |
| **Runtime** | smalltalk / identity / help（meta intent） | ✅ | `runtime.go:262` metaReply；`rule_intent.go` | 规则短语匹配 |
| | 意图识别（带上下文） | 🟡 | `rule_intent.go:35` Classify、`:89` contextSuggestsShortDrama | **纯规则**；单 Skill 够用，多 Skill 会触顶（§5.4） |
| | 槽位抽取 + 默认值 + asked-slot 归属 | 🟡 | `rule_slots.go`；`runtime.go` clarify/iterate | 规则抽取；时长/题材/比例有限枚举 |
| | 模糊输入追问（clarify gate） | ✅ | `runtime.go:366` clarify、`planOrClarify` | — |
| | PlanCard + pending_message_id | ✅ | `runtime.go:391` buildPlanCard（keyframe/segment 字段） | 估价为占位（`*40`），未接真实 Quote |
| | confirm_plan → executing | ✅ | `service` confirmPlan；`runtime.go:174` ConfirmPlan | — |
| | iterate（改参 / 改一下 / 再来一版） | ✅ | `runtime.go:79` iterate、`mergeSlots` | 见 Fork |
| | cancel（清 pending 回 idle） | ✅ | `runtime.go:275` cancel | — |
| | 兜底文案不复读 | 🟡 | `runtime.go:316` contextualFallback | 仅对「上一条」去重，交错时仍可能两两重复（§5.4） |
| **Skill** | Manifest 加载 / 注册（启动 fail-fast） | ✅ | `skill/loader.go` LoadEmbedded、`registry.go` Register | embed 内嵌 yaml |
| | Manifest 校验 | 🟡 | `skill/manifest.go:114` Validate | **仅查** key/intent/slot.maps_to/required；**未查** workflow 存在 / gate signal / route·mode / activity step 唯一 / result contract（§4 P1） |
| | short_drama Manifest | ✅ | `skill/manifests/short_drama.yaml` | 唯一 Skill |
| **Execution** | Outbox（Post-Commit，红线 4） | ✅ | `repository/query/outbox.go`、`worker/outbox_worker.go` | 重试 + 退避 + 终止失败 |
| | Task 创建（带 works 元数据） | ✅ | `server/agent_outbox_launcher.go` | entry_type/route/mode/title 已贯通 |
| | TaskLink（primary / fork） | ✅ | `repository/query/task_link.go` | — |
| | Fork（再来一版 / 改一下） | 🟡 | service confirmPlan（isFork + forked_from） | **完整重生成**；节点级 patch ⏸（§5、Phase D） |
| **Observer** | 主/子任务事件分层 | ✅ | `observer.go` isMainTaskEvent | 子任务终态被忽略（不产卡、不进 children） |
| | 终态幂等 + 互斥 | ✅ | `observer.go` finalize（terminal stage 守卫 + CAS） | 单 run 正确；跨 run 迟到旧终态靠 stage 守卫，精确收口 ⏸ |
| | ReviewCard gate（pipeline card_type） | ✅ | `observer.go` handleReviewGate、handlePipeline | — |
| **Activity** | 持久可变过程消息 | ✅ | `activity/activity.go`、`content.go`；`message.go` Find/UpdateActivity | 唯一可原地更新的消息 |
| | 主步骤步进 + WS 推送 | ✅ | `observer.go` applyToActivity / finalize | — |
| | children / 子任务进度（completed/total） | ⛔→⏸ | — | **下一阶段体验增强**（§5 Phase B），非当前 bug |
| **Result** | ResultCard / ErrorCard（终态唯一） | ✅ | `observer.go` handleSuccess/handleFailure | — |
| | 作品归类（进入 /works） | ✅ | launcher + manifest source（route/mode/title） | 历史数据已回填 |
| **WS** | conversation 订阅（ownership 校验 + ack） | ✅ | `websocket/hub.go`、`server/agent_ws.go` | — |
| | message 推送（type=message，整条） | ✅ | `agent_ws.go` conversationWSNotifier | 仅引擎异步消息；同步回合消息走 REST 响应 |
| | 恢复：subscribe 带 after_sequence / ack 带 last_sequence | 📄 | 文档 09 描述，**代码未实现** | 客户端用 GET /messages 兜底 |
| | agent_state 帧 / event_seq / replay | ⛔ | — | 客户端用 detail + snapshot 兜底（§5.2） |
| **Client** | PlanCard / ReviewCard / Activity / pending action / WS upsert | ✅ | （客户端，已联调） | 真机回归未完（Phase A） |
| **Advanced** | Hybrid LLM intent fallback | 🟡/⏸ | `runtime/hybrid_intent.go`（外壳在，**默认关**） | 接第二 Skill 后再评估开启 |
| | LLM SlotExtractor / context summary / RAG | ⛔/⏸ | — | P2 |
| | 第二个 Skill | ⛔ | — | Phase C |
| | 节点级 patch / 单镜重跑 / 资产复用 | ⛔/⏸ | — | Phase D |

---

## 3. 文档 ↔ 实现差异表（已偏离原始设计的点）

> 下列差异已在对应原始文档就地加「⚠️ 实现校正」标注；完整结论以本文为准。

| 主题 | 原始文档描述 | 当前真实实现 | 受影响文档 |
|---|---|---|---|
| **消息可变性** | Message 不可变、append-only（11 §表） | 普通消息 append-only；**activity 是唯一允许原地更新的可变消息**（占固定 sequence，content_json 原地改） | 11、16（16 已写明） |
| **WS 恢复协议** | subscribe 可带 `after_sequence`；ack 带 `last_sequence`；可选 `state` 帧（agent_state） | 订阅只认 `{action,type,id}`；ack 无 last_sequence；**无 state 帧 / event_seq / replay**；恢复靠 REST（GET /messages?after_sequence + GET detail） | 09（§WS）、16 §6/§7 |
| **WS 推送范围** | （隐含全部消息走 WS） | **只有引擎异步消息**（review/result/error/activity）走 WS；同步回合消息（user/agent text、plan_card、clarify、确认时的 activity）在 REST 响应里返回 | 09 |
| **Observer 终态** | 最小实现：任意 task terminal event 都可能映射最终卡 | **只有主任务**（`TaskID==RootTaskID`）终态产 Result/Error；子任务终态不得升格为消息；终态幂等 + 互斥 | 16 §5（已写明） |
| **意图分类输入** | 15 设计为 `Classify(ctx, ConversationContext, msg)` | 已落地为 `Classify(ctx, *ConversationContext)`（msg 即 `c.Input`，折叠进 Context）；**与设计一致**，仅签名简化 | 15（轻微） |
| **PlanCard 时长字段** | `shot_count` | `keyframe_count / segment_count / shot_duration / est_duration_sec`（`shot_count` 保留为别名） | 13（如有引用） |

---

## 4. 已验证通过的完整 MVP 链路（short_drama）

真实联调已覆盖（以 short_drama 为唯一验收 Skill）：

```text
闲聊 / 问能力（你是谁 / 你能做什么）        ✅ smalltalk·identity·help，不误启动
  → 多轮上下文收集短剧需求（含碎片/接话）    ✅ ConversationContext + asked-slot 归属
  → 模糊请求追问故事点子                     ✅ clarify gate
  → PlanCard（keyframe/segment/估价）         ✅ + pending_message_id
  → 用户调整 / 确认                          ✅ iterate / confirm_plan
  → Outbox → 创建 Task（带 works 元数据）     ✅ Post-Commit，无孤儿任务
  → Activity 实时增长                        ✅ 确认即建、原地更新、WS 推
  → 确认分镜脚本（prompt_review_card）        ✅ gate 1
  → 确认九宫格画面（storyboard_review_card）  ✅ gate 2
  → 逐镜生成 / 配音 / 合成（大量子任务）      ✅ 子任务事件不再刷屏（分层红线）
  → ResultCard（成片）或 ErrorCard           ✅ 终态唯一、互斥
  → 作品进入 /works                          ✅ entry 元数据贯通 + 历史回填
  → 再来一版 / 改一下（Fork）                ✅ 完整重生成
  → Conversation WS + 客户端 upsert          ✅ 按 message.id 原地替换
```

**仍未完成 / 不稳定**：
- 真机 WS 长连接稳定性、断线重连恢复——尚未系统回归（Phase A）。
- Activity 只有主步骤，子任务进度（3/8）暂缺（Phase B）。
- 冷启动闲聊在用户消息交错时，兜底文案仍可能两两重复（§5.4）。
- 失败链路的积分退还语义需端到端确认（Phase A）。

---

## 5. 主要技术债与风险

### 5.1 客户端共享 WebSocket 回调串联（技术债）
当前 ConversationWSClient 与 TaskManager 复用单条 socket，靠闭包串联 onReceive。
**风险**：多模块覆盖、重复注册、生命周期泄漏。
**方向**：升级为 `WebSocketEventRouter / EventBus`，按 topic 分发，统一订阅生命周期。

### 5.2 WS 无 event_seq / replay（技术债）
恢复目前完全靠 REST 兜底：`lastSequence` 补新增、整窗 GET 补 activity 累积态、detail 补 agent_state。
**方向（未来增强，非阻塞）**：`agent_state` 帧 → 订阅即推快照 → 帧级 `event_seq` → resume/replay。

### 5.3 Activity 只有主步骤、无 children
子任务终态已**正确禁止**生成消息，但目前被**忽略**，尚未汇入 activity children。
**定性**：这是**下一阶段体验增强（Phase B），不是当前 bug**。

### 5.4 Rule Runtime 的上限
规则 + ConversationContext 足以支撑 short_drama 单 Skill MVP。扩到多 Skill 后会暴露：意图冲突、话题切换、碎片输入归属、槽位抽取复杂度。
**纪律**：**第二 Skill 前**重新评估 Hybrid LLM fallback（外壳已在 `hybrid_intent.go`，默认关）。当前阶段勿开。

### 5.5 Manifest = 关键运行契约
Manifest 配错可致：Agent 错误路由 / 作品不进 /works / 错误扣费 / 审核 signal 失效 / Activity 不更新。
当前 Validate 仅做基础校验。**接第二 Skill 前必须补深度校验**（§Phase C）。

### 5.6 跨 run 终态收口精度
单 run 的终态幂等/互斥已正确（stage 守卫 + CAS）。多次 fork 并发时，「迟到的旧 run 终态」仅靠 stage 守卫不够精确。
**方向**：在 `agent_conversation_task_links` 记 `final_status`，按 task 收口（已在 16 §5 标注）。

---

## 6. 后续路线图（阶段化，不再零散 Gap）

### Phase A — short_drama MVP 稳定与客户端闭环【上线前必做】
**目标**：完整链路真机稳定运行。
**重点**：
- 客户端 ReviewCard / Activity / ResultCard 渲染与交互
- WS 断线重连 + snapshot 恢复（按 message.id upsert）
- pending_message_id 与客户端本地 action 状态一致
- ResultCard 跳转有效作品详情；作品可从会话与 /works 双入口打开
- 失败链路 + 积分退还语义端到端正确
- 主任务终态幂等/互斥真机复验

**验收标准**：连续多轮「成功 / 失败 / 退出重进 / 断网重连」回归——无重复消息、无错误按钮态、无不可恢复阻塞态、作品可打开。

### Phase B — Activity children 与执行体验升级
**目标**：接近 Claude Code / Codex 的「可展开、持续增长的执行过程块」。
**重点**：子任务映射到 activity children；Map/Loop/SubWorkflow 进度聚合（completed/total）；展开详情；失败步骤定位；activity step matcher 用真实事件校准；过程文案优化。

### Phase C — Skill Registry 工程化与第二 Skill
**目标**：证明架构不只支持 short_drama。
**重点**：
- Manifest 深度校验：key 唯一 · workflow 存在 · slot.maps_to 合法 · gate signal 存在 · route/mode 合法 · activity step 唯一且 matcher 合法 · result contract 合法
- 注册纪律：**无 Manifest 的 Workflow 不允许被 Agent 自动调用**；启动 `LoadEmbedded → Validate → (Compile) → Register`；inactive/invalid 不进 Registry
- 建议补字段：`skill_version`、output contract、availability/rollout（后续）
- 选**结构明显不同、流程相对简单**的第二个 Skill（**不要**直接上最复杂的 goods_video），验证 Intent/Selection/Works/Activity/Result 的通用性

### Phase D — LLM / RAG / 高级迭代
前三阶段稳定后再推进：Hybrid LLM intent/slot fallback → RAG → 上下文压缩 → 节点级 patch / 单镜重跑 → 多 Agent。
**纪律**：**先接第二 Skill，再评估开 LLM fallback**；当前 Fork=完整重生成，维持为 MVP 行为。

---

## 7. 关键结论

1. **架构方向正确且主体成立**：与 Claude Code / Codex 同一套底座（对话驱动 + 可增长过程块 + 人在环路审核），short_drama 端到端已验证。
2. **现在该做稳定与体验，不是加能力**：Phase A 是上线门槛；Phase B 是体验台阶；Phase C 才回到「能力扩展」。
3. **第二 Skill 是通用性的真正考验**：在它之前必须完成 Manifest 深度校验与注册纪律，否则会把 short_drama 的隐性约定固化成债。
4. **现在不应该做**：LLM fallback、RAG、多 Agent、节点级 patch、第二 Skill 并行开工——全部 P2/Phase D，单 Skill 阶段引入只会放大不可控复杂度。

---

返回：[v2/README.md](README.md) · 关联：[09 API](09-conversation-api.md) · [14 边界](14-repository-contracts.md) · [15 大脑](15-agent-decision-engine.md) · [16 Activity](16-activity-stream.md)
