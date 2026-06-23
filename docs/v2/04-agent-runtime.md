# 04 · Agent Runtime 设计

> Agent Runtime 是 V2 唯一的「大脑」。**所有 LLM 调用、所有决策、所有翻译都在这一层。** 它把「用户目标」变成「引擎可执行的 Task」，再把「引擎的执行」翻译回「用户语言」。

## 1. 职责清单

| # | 能力 | 输入 | 输出 | 第一版做到 |
|---|------|------|------|-----------|
| 1 | 意图识别 Intent Recognition | 用户消息 + 会话上下文 | `intent`（命中某 Skill 域）| ✅ |
| 2 | 槽位补全 Slot Filling | 意图 + 已知信息 + 附件 | 缺失槽位 → 追问 / 默认值 | ✅ |
| 3 | 计划生成 Plan Generation | 意图 + 槽位 | 结构化 Plan（写入会话） | ✅ |
| 4 | 技能选择 Skill Selection | 意图 + 槽位 | 选定 Skill + 组装 input | ✅ |
| 5 | 任务启动 Task Launch | Skill + input | 创建 Task + TaskLink | ✅（复用现有接口） |
| 6 | 进度翻译 Progress Translation | TaskEvent 流 | 用户语言的 progress/卡片消息 | ✅ |
| 7 | 迭代修改 Iteration | 修改意图 + 目标作品 | fork/patch 新任务 | ✅（复用 fork/patch） |
| 8 | 失败兜底 Recovery | 失败事件 | 用户友好的解释 + 可行动建议 | ✅ |

> RAG 检索增强、多 Agent 路由是**第二阶段**插在「②槽位 / ③计划」之前/之中（见 [07](07-rag-roadmap.md) / [08](08-roadmap-and-milestones.md)），第一版不做。

## 2. 主循环（实现视角）

Agent Runtime 是**无状态的处理器**（状态在 Conversation 层）。它被两类事件唤醒：

```text
唤醒源 A：用户消息/信号（来自 Conversation API）
唤醒源 B：引擎 TaskEvent（来自 EventBus 订阅）

handleUserTurn(conv, message):
    ctx     = loadContext(conv)            # 历史消息 + 当前 Plan + 关联任务摘要
    if isModificationOfExistingWork(message, ctx):
        return handleIteration(conv, message, ctx)     # → §7（fork/patch）
    intent  = recognizeIntent(message, ctx)            # → §3
    skill   = selectSkill(intent, ctx)                 # → §5
    if skill == nil:
        return replyGracefulFallback(conv, intent)     # 坦诚降级
    slots   = fillSlots(skill, message, ctx)           # → §4
    if slots.hasMissingRequired():
        return askClarify(conv, slots.firstBlockingGap())   # 写 clarify 消息，会话转 awaiting_user
    plan    = buildPlan(conv, intent, skill, slots)    # → §4.5，落 conversation_plans
    if skill.needsPlanConfirmation():                  # 昂贵/复杂技能 → 闸门
        return presentPlanCard(conv, plan)             # 写 plan_card，等 confirm_plan signal
    return act(conv, plan)                              # → §5.4 启动任务

handleEngineEvent(taskEvent):
    conv    = lookupConversationByTask(taskEvent.TaskID)   # 经 TaskLink
    msg     = translate(taskEvent, conv.currentPlan)       # → §6 进度翻译
    if msg != nil: appendMessageAndPush(conv, msg)
    if taskEvent is awaitUserAction:                       # 引擎闸门 → 会话审核卡
        appendReviewCard(conv, taskEvent.payload)
    if taskEvent is terminal:                              # 成功/失败
        appendResultOrErrorCard(conv, taskEvent)
```

两条唤醒路径共享同一套「读会话上下文 → 决策 → 写消息」骨架，区别只在输入源。

## 3. 意图识别

### 3.1 方法
用 LLM 的**结构化输出 / function-calling**，把用户消息分类到「已注册 Skill 的意图域」之一，外加若干元意图：

```text
意图空间 = { 已注册 Skill 的 intent } ∪ { modify, ask_capability, smalltalk, unknown }
```

```json
// LLM 返回（结构化）
{
  "intent": "short_drama",
  "confidence": 0.92,
  "extracted_slots": {"duration_sec": 60, "style_hint": "都市"},
  "is_modification": false,
  "referenced_work": null
}
```

### 3.2 关键约束
- **闭世界**：意图只能落在「已注册 Skill」或元意图里。LLM 不得发明能力。Skill 清单作为候选注入提示词（来自 [05](05-skill-layer.md) 的 Skill Manifest 摘要）。
- **附件即信号**：用户传了商品图 → 强先验「带货/图生视频」；传了人脸 → 「照片动起来」。附件类型参与意图判定。
- **低置信兜底**：`confidence` 低或 `unknown` → 反问澄清（「你是想做视频、图片，还是 Logo 呀？」）而非乱猜。

## 4. 槽位补全（追问策略）

槽位 = 执行某个 Skill 所需的一项信息。每个 Skill 在 Manifest 里声明它的 **slots**（必填/选填/默认值/可选项/取值校验），见 [05](05-skill-layer.md#3-skill-manifest-规格)。

### 4.1 三段式填充
```text
对每个 slot:
  1. 抽取：从用户消息 + 附件 + 会话历史里抽（LLM extracted_slots）
  2. 默认：抽不到但 Manifest 有 default → 用默认（记入 defaults_applied，方案卡里告知）
  3. 追问：必填且无默认且抽不到 → 进入「待问队列」
```

### 4.2 追问的「少打扰」原则
- **能默认就默认**：比例、音色、模型这类有合理默认的，先默认，方案卡里用「可调整」露出，不打断。
- **批量而非逐条**：一次 `clarify` 尽量覆盖「真正阻塞」的 1–2 个关键缺口（如短剧的「风格」「角色数」），不要一问一答挤牙膏。
- **给选项优于开放问**：能枚举就给 `options`（点击即填），减少打字。
- **风格题留给作品**：很多「风格」其实问不清，不如先用默认快速出一版，再让用户基于成品迭代（「不喜欢？换个风格」走 fork）。这与竞品「先出再改」的体感一致（[01](01-competitive-analysis.md)）。

### 4.3 追问的产物
一条 `clarify` 消息（[03 §4.1](03-conversation-layer.md#41-clarify追问示例)），会话转 `awaiting_user`。用户回答（文本或点选项）经 `messages` 或 `signals` 回来 → 回到主循环重新 `fillSlots`。

### 4.4 槽位 → Workflow input 映射
Manifest 声明每个 slot 如何映射到 Workflow 的 `input.*` 路径。`act()` 时按映射组装 `RunWorkflowReq.Input`：

```text
slot "duration_sec" → input.duration
slot "style"        → input.style_preset
slot "characters"   → input.character_count
（asset slots）     → input.product_images / input.face_image …（asset_id 引用，复用现有 RegisterTaskInputAssetRefs）
```

### 4.5 Plan 生成
槽位齐备后，生成 `conversation_plans` 记录：意图、skill_key、slots_json、stages_json（取自 Manifest 的可见阶段）、estimated_cost（调现有 **Quote** 接口预估），status=`draft`。Plan 是「方案卡」的数据源，也是后续进度翻译时「阶段名」的来源。

## 5. 技能选择与任务启动

### 5.1 Skill Selector
输入意图 + 槽位，从 Skill 注册表选出具体 Skill。第一版策略（足够用）：

```text
1. 意图 → Skill 候选集（Manifest 的 intent 标签）
2. 用槽位/附件二次判别（如带货：有无参考视频 → goods_video_pro vs simple）
3. 命中唯一 → 选定；多候选 → 让 LLM 在候选内裁决（携带各 Manifest 的 description）；零候选 → 降级
```

> 选择逻辑**只在已注册 Skill 内**做 ranking，绝不凭空生成。详见 [05 §4](05-skill-layer.md#4-skill-selector-选择策略)。

### 5.2 多 Skill 的编排（第一版克制）
第一版**一个 Plan 一般对应一个主 Skill**（短剧/带货/Logo…本身已是完整产线）。若确需串联（如「先抠图再生成视频」），优先选择**已经把这些步骤打包好的 Workflow**（我们的工作流本就是大颗粒产线），而不是让 Agent 即时编排多个 Skill。即时编排留给 Blueprint 阶段（[08](08-roadmap-and-milestones.md#blueprint-演进)）。

### 5.3 报价闸门
昂贵 Skill（视频生成类）在启动前，方案卡里展示 `estimated_cost`（Quote），并要求 `confirm_plan`。轻量 Skill（文生图）可配置为**免确认直跑**（Manifest 的 `needs_plan_confirmation=false`），减少摩擦。

### 5.4 启动任务（复用现有路径）
```text
act(conv, plan):
    input = assembleInput(plan.slots)        # §4.4
    task  = createTask(skill.workflowName, input, user)   # = CreateTaskFromWorkflow 等价逻辑
    enqueue(task)                                          # 现有 Enqueue
    createTaskLink(conv, task, plan, relation="primary")  # 03 §2.4
    conv.status = running
    # 之后由 handleEngineEvent 接管进度
```

> 这里**完全等价于现有 `RunWorkflow` / `CreateTaskFromWorkflow`**，只是发起者从「用户点按钮」变成「Agent 决策」。计费冻结、asset 注册、权限校验全部沿用，不开后门。

## 6. 进度翻译（Translate, don't Forward）

引擎的 `TaskEvent` 必须被翻译成用户语言。翻译是**确定性映射为主、LLM 润色为辅**。

### 6.1 翻译表（核心：确定性映射）
每个 Skill 在 Manifest 里给「节点/阶段 → 用户文案」的映射。例：短剧（呼应 `short_drama_main_dsl.go` 的 stage 事件）：

| 引擎事件（节点/stage） | 翻译成（progress 消息） |
|----------------------|------------------------|
| `stage_changed: storyboard` | 正在规划剧情与分镜… |
| `await_user_action: storyboard_review_card` | （转为 `review_card`）分镜画好了，看看要不要调整 👇 |
| `stage_changed: generating_shots` | 正在逐镜生成画面（第 i/N 幕）… |
| `voiceover_compose` | 正在配音… |
| `stage_changed: composite_video` | 正在合成成片… |
| `task_succeeded` | （转为 `result_card`）成片好啦 🎬 |

- **进度百分比**：复用现有 `overall_progress = (node_index + node_progress) / node_total`，直接驱动 `progress` 消息的 `percent`，无需重算。
- **节流**：高频进度走 `transient`（只推 WS 不入库）；阶段切换/审核/结果走 `persistent` 入库可回放（[03 §5.2](03-conversation-layer.md#52-服务端下行帧)）。

### 6.2 LLM 润色（可选、第二位）
对「失败原因」「方案解释」「成品点评」等需要灵活措辞的，调 LLM 把结构化信息润色成自然话术。**进度刷屏不走 LLM**（成本/延迟不可接受），只用确定性模板。

### 6.3 没有映射的事件
默认**不展示**给用户（如内部缓存命中、provider 轮询、心跳）。翻译层是「白名单展示」而非「全量透传」——这是把引擎细节挡在用户之外的关键。

## 7. 迭代式修改 = 复用 fork/patch

「再做一个 / 换风格 / 第二幕改成夜晚 / 换个音色」是创作常态，也是我们相对纯 LLM 产品的**结构性优势**。

```text
handleIteration(conv, message, ctx):
    target = resolveTargetWork(message, ctx)        # 默认指最近成品；可指定历史版本
    edit   = parseEdit(message)                     # {scope: whole|node, node?, patch}
    preview = PatchPreview(target.task, edit.patch)  # 现有接口：预估重跑哪些节点 + 代价
    presentModifyCard(conv, preview)                 # [修改预览卡] 将重做 X 个节点，约 Y 积分
    on confirm:
        newTask = Fork(target.task, edit.patch)      # 现有 Fork/Redo
        createTaskLink(conv, newTask, relation="fork", forked_from=target.task)
        # 进度/结果照常翻译 → 交付新版本
```

要点：
- **粒度**：能定位到 DAG 单节点的修改（如某一幕、某段配音）就只 patch 那个节点，复用其余节点结果——比整段重生成省得多。`PatchPreview` 让「会重跑什么、花多少」对用户透明（呼应 [01](01-competitive-analysis.md#3-潜能激发我们被低估的结构性优势)）。
- **版本树**：每次 fork 生成新 TaskLink（`relation=fork`），构成会话内可切换的版本历史。
- **意图判别**：`is_modification` 由意图识别给出；含「再/换/改/把…改成」等 + 存在最近成品 → 走此路径，否则当新创作。

## 8. 用户信号的两种归宿

用户对卡片的回应（`POST /signals`）有两种处理路径，对用户**表现一致**：

| 卡片来源 | signal 例 | Agent 处理 |
|---------|----------|-----------|
| **引擎 await 闸门** | `confirm_storyboard_prompt` / `confirm_storyboard_image` | 转发到现有 `await.HandleSignal`（payload 透传），引擎继续执行 |
| **会话级决策** | `confirm_plan` / 选择某 Skill / 确认迭代 | Agent 自己推进主循环（act / fork），不碰引擎 await |

Agent Runtime 依据 `ref_message_id` 对应卡片的 `signal` 元数据判断走哪条路。两条路最终都让会话从 `awaiting_user` 前进。

## 9. 失败兜底

```text
on task_failed / task_final_failed:
    reasonUser = mapErrorToUser(event.Error, skill)     # 把技术错误翻成人话
    actions    = suggestRecovery(event, skill)          # 重试 / 换模型 / 换素材 / 改方案
    appendErrorCard(conv, reasonUser, actions)
    # 计费：引擎侧已有失败退款（task_points_refunded），Agent 只需如实告知
```

- 不暴露堆栈/provider 报错原文。
- 给**可点击的下一步**（重试 = resume/fork，换方案 = 重选 Skill）。
- 退款由引擎既有逻辑处理（`task_points_refunded` 事件），Agent 翻译为「已退还 X 积分」。

## 10. LLM 用量与护栏

| 场景 | 是否调 LLM | 备注 |
|------|-----------|------|
| 意图识别 | 是（结构化输出） | 注入 Skill 候选摘要，闭世界 |
| 槽位抽取 | 是（可与意图同一次调用） | 合并调用省一跳 |
| 追问话术 | 模板优先，必要时 LLM | 选项化追问基本不需要 LLM |
| Plan 解释/成品点评 | LLM | 短，可缓存风格 |
| 进度翻译 | **否**（确定性模板） | 高频，禁止 LLM |
| 失败原因润色 | LLM | 低频 |

护栏（呼应 [02 §7 架构不变量](02-architecture-overview.md#7-架构不变量review-时对照)）：
1. 闭世界：只选已注册 Skill。
2. 钱有闸门：昂贵操作先报价、用户确认。
3. 进度不过 LLM：确定性翻译。
4. 人在环路：修改必须用户确认，不自动重做。
5. 复用而非绕过：建任务/发 signal/fork 全走现有计费与权限。

## 11. 与未来的接口（预留，不实现）

- **RAG 注入点**：`recognizeIntent` 后、`buildPlan` 前插入 `retrieve(intent, slots, user)`，把模板/历史作品/失败经验注入 Plan 提示词（[07](07-rag-roadmap.md)）。
- **Multi-Agent**：`selectSkill` 升级为「Router → 专家 Agent」，专家 Agent 复用本 Runtime 骨架（[08](08-roadmap-and-milestones.md)）。
- **Blueprint**：`buildPlan` 的产物从「单 Skill 计划」升级为「可执行 Blueprint（多 Skill DAG）」，`act` 相应地编译并提交（[08](08-roadmap-and-milestones.md#blueprint-演进)）。

---

下一篇：[05 · Skill 层设计](05-skill-layer.md) —— Workflow as Skill：Manifest 规格、Selector、现有能力映射。
