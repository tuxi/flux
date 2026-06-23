# 13 · short_drama 最小闭环验证（Skill 样例 + 端到端）

> 本文不是「写一个 YAML」，而是用 **short_drama 这一个 Skill 把 V2 Agent First 的最小闭环跑通一遍**：Manifest（10）→ 决策（15）→ 状态机（11）→ 落库（12/14）→ 进度翻译（10§6/14§5）。**如果这一个 Skill 能从「帮我做个短剧」一句话走到成片，整套架构就被证明了。**
>
> 所有字段都对齐真实工作流 `ai-engine/workflows/short_drama/short_drama_main_dsl.go`（输入、stage、两道 await 闸门、signal 名都取自其 DSL 头注释与节点定义），而非杜撰。文件落点：`ai-engine/skill/manifests/short_drama.yaml`（[10 §2](10-skill-manifest-spec.md#2-顶层结构)）。
>
> 注：09/11/15 里出现过的 `duration_sec` / `characters` 是**示意性占位**；**本文的 slots 才是 short_drama 的权威映射**（短剧没有 `input.duration`/`input.character_count`，时长由 `grid_size × shot_duration` 派生，见 §4）。

## 1. 完整 Manifest（short_drama.yaml）

```yaml
schema_version: 1
key: short_drama
intent: [short_drama]
title: 短剧
summary: 一句话生成 AI 短剧（九宫格故事板 → 逐镜生成 → 配音 → 合成）
description: >
  适合：用户想做有剧情、分镜头的 AI 短视频/短剧（都市/悬疑/搞笑/治愈等），
  从一句故事点子或一段梗概出发，自动生成故事板、逐镜画面、旁白配音并合成成片。
  不适合：单纯把一张图做成带货口播视频（用 goods_video）、让单张照片动起来
  （用 image_motion）、纯静态海报/封面（用 text_to_image）。
result_type: video
status: active
phase: p1

source:
  type: workflow
  workflow_name: short_drama          # → CreateTask 走此 workflow（[14 §4.2] Outbox 投递）

cost:
  needs_plan_confirmation: true        # 视频生成昂贵 → 必经 confirming 方案确认
  estimate: quote
  hint: "约 300 积分（随镜头数变化）"

slots:
  - key: user_prompt
    title: 故事点子
    type: text
    required: true
    maps_to: input.user_prompt
    ask: "想讲一个什么样的故事？一句话或一段梗概都行"
    extract_hint: "用户对剧情/主题/人物/走向的描述"

  - key: style
    title: 风格
    type: string                       # input.style 为自由字符串；options 仅作建议
    required: false
    maps_to: input.style
    ask: "想要什么风格？比如都市爱情、悬疑、搞笑、治愈"
    options:
      - { value: 都市爱情, label: 都市爱情 }
      - { value: 悬疑,     label: 悬疑 }
      - { value: 搞笑,     label: 搞笑 }
      - { value: 治愈,     label: 治愈 }

  - key: grid_size                      # 规模/时长档：9 格=8 镜≈48s，4 格=3 镜≈18s
    title: 规模
    type: enum
    required: false
    default: 9
    maps_to: input.grid_size
    options:
      - { value: 9, label: "标准（8 个镜头，约 48 秒）" }
      - { value: 4, label: "精简（3 个镜头，约 18 秒）" }

  - key: shot_duration
    title: 单镜时长（秒）
    type: int
    required: false
    default: 6
    min: 4
    max: 10
    maps_to: input.shot_duration

  - key: aspect_ratio
    title: 画幅
    type: enum
    required: false
    default: "9:16"
    maps_to: input.aspect_ratio
    options:
      - { value: "9:16", label: 竖屏 }
      - { value: "16:9", label: 横屏 }
      - { value: "1:1",  label: 方形 }

  - key: reference_image                # 可选：参考图模式（决定主角形象）
    title: 参考图
    type: asset
    required: false
    accept: [image]
    maps_to: input.reference_image_asset_id
    ask: "如果有主角参考图可以发我（没有也行，我来生成）"

  - key: narration_language
    title: 旁白语言
    type: enum
    required: false
    default: zh
    maps_to: input.narration_language
    options:
      - { value: zh, label: 中文 }
      - { value: en, label: 英文 }

stages:                                 # 见 §6（task event → message 翻译）
  - { match: "node:storyboard_generation",      text: "正在生成九宫格故事板" }
  - { match: "stage_changed:generating_shots",  text: "正在逐镜生成画面（共 {n} 镜）" }
  - { match: "node:voiceover_compose",          text: "正在配音" }
  - { match: "stage_changed:shots_ready",       text: "画面与配音已就绪" }
  - { match: "node:composite_video",            text: "正在合成成片" }

gates:                                  # 两道真实 await 闸门 → reviewing
  - card_type: prompt_review_card
    signal: confirm_storyboard_prompt
    title: 确认分镜脚本
  - card_type: storyboard_review_card
    signal: confirm_storyboard_image
    title: 确认九宫格画面

iteration:
  - { intent_hint: ["第N镜改成…", "某一镜换成…", "把第二幕改成夜晚"], scope: node, target_node: map_generate_shots }
  - { intent_hint: ["换个旁白", "重新配音", "换个音色"],            scope: node, target_node: voiceover_compose }
  - { intent_hint: ["换个故事板", "重画九宫格"],                    scope: node, target_node: storyboard_generation }
  - { intent_hint: ["再来一版", "换个风格", "整个重做"],            scope: whole }

examples:                               # 见 §3（IntentClassifier few-shot）
  - "帮我做个一分钟都市爱情短剧"
  - "做一条程序员转行开咖啡馆的治愈系小短剧"
  - "用这张主角图，生成一个悬疑反转的短剧"
  - "我想要一个搞笑的办公室短剧，竖屏"
```

## 2. slots 定义（逐项说明）

| key | type | required | maps_to | 缺失行为 | 备注 |
|-----|------|:---:|---------|---------|------|
| `user_prompt` | text | ✅ | `input.user_prompt` | 无默认 → **追问** | 唯一必填；剧情种子 |
| `style` | string | — | `input.style` | 抽不到不追问，交给故事板自由发挥 | options 仅建议值，实际透传字符串 |
| `grid_size` | enum | — | `input.grid_size` | default 9 | **时长档**（9→8 镜，4→3 镜） |
| `shot_duration` | int | — | `input.shot_duration` | default 6 | min4/max10 |
| `aspect_ratio` | enum | — | `input.aspect_ratio` | default 9:16 | 竖/横/方 |
| `reference_image` | asset | — | `input.reference_image_asset_id` | 无则走纯生成 | asset_id 引用（§4） |
| `narration_language` | enum | — | `input.narration_language` | default zh | 旁白语种 |

唯一硬门槛是 `user_prompt`。**其余全有默认 → 一句「帮我做个短剧」也能往下走**（少打扰原则，[10 §5.2](10-skill-manifest-spec.md#52-填充与追问语义规范)）。`model`/`fps`/`bgm_url`/`mode` 是工作流的系统默认，不暴露为 slot；`callback_token` 由系统在启动时注入（= 任务 ID，用于 signal 路由，§7），更不是 slot。

## 3. examples —— 喂给 IntentClassifier 的 few-shot

Manifest.examples 直接进 `IntentClassifier`（[15 §5 步①](15-agent-decision-engine.md#5-冷启动主流水线架构师给的那张图)）的 few-shot 池：

```text
正例（→ intent=short_drama）：
  "帮我做个一分钟都市爱情短剧"
  "做一条程序员转行开咖啡馆的治愈系小短剧"
  "用这张主角图，生成一个悬疑反转的短剧"
  "我想要一个搞笑的办公室短剧，竖屏"

边界（→ 不是 short_drama，避免误选；description 的「不适合」也起作用）：
  "用这几张图做个带货视频"        → goods_video
  "让这张照片动起来"              → image_motion
  "画一张露营主题的小红书封面"     → text_to_image
```

> 关键：正例教模型「这是短剧」，`description` 的「不适合」+ 其它 Skill 的 examples 一起，构成**消歧边界**。当输入是「用这几张图做个视频」这类模糊句，`SkillSelector` 命中多个 → 走澄清（[15 §13-B](15-agent-decision-engine.md#13-worked-traces决策视角)）。

## 4. slot → workflow input 映射

`Decide` 在 `launch` 时，按各 slot 的 `maps_to` 把已填值组装成 `LaunchIntent.Input`（[15 §3](15-agent-decision-engine.md#3-agentdecision-结构)），交给 Outbox 的 `create_task` 投递：

| slot 值 | → workflow input | 说明 |
|--------|------------------|------|
| `user_prompt="程序员转行开咖啡馆"` | `input.user_prompt` | 直传 |
| `style="都市治愈"` | `input.style` | 直传字符串 |
| `grid_size=9` | `input.grid_size` | 9 或 4 |
| `shot_duration=7` | `input.shot_duration` | |
| `aspect_ratio="9:16"` | `input.aspect_ratio` | |
| `reference_image=asset:8842` | `input.reference_image_asset_id` | **asset_id 引用**，走现有 `RegisterTaskInputAssetRefs`，不直传 URL |
| `narration_language="zh"` | `input.narration_language` | |
| —（系统注入） | `input.callback_token = <task_id>` | signal 路由令牌（[09 §2.7](09-conversation-api.md#27-用户信号卡片回应)） |
| —（系统默认） | `input.model/fps/bgm_url/mode` | 不由 slot 提供 |

**派生量（不是 input，但 Plan 要展示）**：
```text
shot_count       = grid_size - 1            # 9→8, 4→3
est_duration_sec = shot_count × shot_duration
```
所以「**一分钟**短剧」不是填某个 `duration` 字段，而是 `Decide` 的槽位推理：`SlotExtractor` 把「一分钟」解释为 `grid_size=9, shot_duration≈7`（8×7≈56s）。这正是「Agent 决定路径、用户只表达目标」的具体体现（[00](00-vision-and-positioning.md)）。

## 5. confirming 卡片结构（plan_card）

`needs_plan_confirmation:true` → 槽位齐备后进 `confirming`，推一张 `plan_card`（[11 §6](11-agent-state-machine.md#6-跃迁表from--trigger--to--副作用)、[09 §2.7](09-conversation-api.md#27-用户信号卡片回应)）。`content_json`：

```json
{
  "kind": "plan_card",
  "plan": {
    "id": "100234",
    "skill_key": "short_drama",
    "intent": "short_drama",
    "slots": {
      "user_prompt": "程序员转行开咖啡馆，从崩溃到治愈",
      "style": "都市治愈",
      "grid_size": 9,
      "shot_duration": 7,
      "aspect_ratio": "9:16",
      "narration_language": "zh"
    },
    "derived": { "keyframe_count": 9, "segment_count": 8, "shot_count": 8, "shot_duration": 7, "est_duration_sec": 56 },
    "stages": ["生成故事板", "确认脚本", "确认画面", "逐镜生成", "配音", "合成成片"],
    "estimated_cost": 320
  },
  "editable": ["style", "grid_size", "shot_duration", "aspect_ratio"],
  "actions": [
    { "signal": "confirm_plan", "label": "开始创作" },
    { "signal": "modify_plan",  "label": "调整一下" }
  ]
}
```

要点：
- 展示**派生量**（8 镜≈56s）和**预估积分**（Quote）——昂贵不可逆操作必须让用户先看清（护栏，[15 §10](15-agent-decision-engine.md#10-llm-用量与护栏)）。
- `editable` 列出可即时改的槽位；用户点「调整」发 `modify_plan` → 回 `planning` 重算 Plan/报价（[15 §4](15-agent-decision-engine.md#4-回合类型路由decide-的第一步) TurnModifyPlan）。
- 用户点「开始创作」发 `confirm_plan`（`routed_to=agent`）→ `launch`。
- **区分**：`plan_card` 是 **Agent 级方案确认**（confirming）；下文的两张 `review_card` 是**引擎级审核闸门**（reviewing），二者不同（§6）。

## 6. task event → conversation message（进度翻译）

执行期，引擎事件经 `AgentObserver.Translate`（[14 §5](14-repository-contracts.md#5-agentobserver--translator红线-3-的落点)）按 Manifest 白名单翻译为消息。**未列入的事件不出消息**（如内部 `node_complete_async`）。

### 6.1 进度（stages → 文案，transient 原地更新）
| 引擎事件 | → 用户消息 | grade |
|---------|-----------|-------|
| `node:storyboard_generation` | "正在生成九宫格故事板" | persistent（阶段切换占 seq） |
| `stage_changed:generating_shots` | "正在逐镜生成画面（共 8 镜）" | persistent |
| `node:voiceover_compose` | "正在配音" | persistent |
| `stage_changed:shots_ready` | "画面与配音已就绪" | persistent |
| `node:composite_video` | "正在合成成片" | persistent |
| 高频百分比刷新 | 同一进度卡原地更新 | transient（不占 seq、可不入库） |

### 6.2 审核闸门（await_user_action → review_card → reviewing）
short_drama 有**两道**真实闸门（DSL 头注释）：

| 引擎 `await_user_action` | card_type | 用户确认 signal | payload | 翻译为 |
|------------------------|-----------|----------------|---------|--------|
| 分镜脚本闸门 | `prompt_review_card` | `confirm_storyboard_prompt` | `{storyboard: StoryboardPrompt}`（可编辑后回传） | review_card「确认分镜脚本」，stage→reviewing |
| 九宫格画面闸门 | `storyboard_review_card` | `confirm_storyboard_image` | `{action:"accept"}` | review_card「确认九宫格画面」，携 `frames[{id,asset_id,url}]`+`grid_size`，stage→reviewing |

`storyboard_review_card` 的 `review_card.content_json`：
```json
{
  "kind": "review_card",
  "card_type": "storyboard_review_card",
  "title": "确认九宫格画面",
  "grid_size": 9,
  "frames": [ {"id":"f1","asset_id":"50001","url":"https://..."}, "...(共 9 帧)" ],
  "image_url": "https://.../montage_fallback.jpg",
  "signal": "confirm_storyboard_image",
  "actions": [ {"signal":"confirm_storyboard_image","payload":{"action":"accept"},"label":"就用这个"} ]
}
```
> 用户点确认 → `POST /signals{signal:"confirm_storyboard_image"}` → `routed_to=engine` → 取会话 `current_task_id` 作 `callback_token` → `await.HandleSignal`（[14 §5]）→ 引擎继续。**Observer 不处理入站确认**，它只把出站的 `await_user_action` 翻成卡。

### 6.3 结果
| 引擎事件 | → 用户消息 |
|---------|-----------|
| `completed`/`task_succeeded` | `result_card`（primary_file_url=成片、cover、actions=[再来一版/改某镜]），stage→completed |
| `task_failed`/`final_failed` | `error_card`（含退款告知），stage→failed |

## 7. 一条完整的 Conversation 状态流

把上面全部串起来——从一句话到成片，再到一次「改第二幕」迭代。`R` 路由：`A`=routed_to agent，`E`=routed_to engine。

```text
# 步  动作                                          stage          会话.status   持久化 / 副作用
─────────────────────────────────────────────────────────────────────────────────────────────
1  POST /conversations {first:"帮我做个一分钟都市     idle→collecting  active       建 conversation + agent_state(idle)
   治愈短剧，程序员转行开咖啡馆"}                                                    + user msg(seq1)
   ↳ Decide: intent=short_drama; skill 唯一命中;
     抽到 user_prompt+style=都市治愈; "一分钟"→
     grid_size=9,shot_duration=7; 必填齐
2  Decide ⑤⑥: needs_confirm → buildPlan+Quote=320   planning→        awaiting_user agent_state CAS;
   WS← plan_card(§5)                                 confirming                     plan v1(draft,seq2)
3  POST /signals{signal:confirm_plan} (R=A)          confirming→      running       agent_state CAS(executing);
   ↳ Decide TurnConfirmPlan → launch                 executing                      plan→confirmed;
   WS← text "好的，开始啦"                                                          Outbox.Enqueue(create_task) ── 同事务提交
4  [提交后] Outbox Worker: engine.CreateTask()        executing        running       Task 70010 建成 →
   → TaskLinks.Create(primary,70010)                                                tasklink; outbox done
5  WS← progress "正在生成九宫格故事板"                 executing        running       msg(persistent,seq3) / transient 刷新
6  引擎 await_user_action(prompt_review_card)         executing→       awaiting_user  agent_state CAS(reviewing);
   WS← review_card "确认分镜脚本"                      reviewing                      review msg(seq4),pending=seq4
7  POST /signals{confirm_storyboard_prompt,           reviewing→       running        await.HandleSignal(token=70010)
   payload:{storyboard:…}} (R=E)                      executing                       → 引擎继续；agent_state CAS(executing)
8  WS← progress（裁切/合成九宫格）                      executing        running
9  引擎 await_user_action(storyboard_review_card)      executing→       awaiting_user  CAS(reviewing); review msg(seq5)
   WS← review_card "确认九宫格画面"(frames[9])          reviewing                       pending=seq5
10 POST /signals{confirm_storyboard_image,            reviewing→       running         await.HandleSignal(token=70010)
   payload:{action:accept}} (R=E)                     executing
11 WS← "正在逐镜生成画面（共 8 镜）"                    executing        running         stage_changed:generating_shots
   → "正在配音" → "画面与配音已就绪" → "正在合成成片"                                  （逐条 persistent）
12 引擎 completed                                      executing→       completed       CAS(completed);
   WS← result_card(成片 url, actions[再来一版/改某镜])  completed                       result msg(seq6)
─────────────────────────── 迭代（fork）──────────────────────────────────────────────────────
13 POST /messages {"第二幕改成夜晚"}                   completed→       active          user msg(seq7)
   ↳ Decide TurnModifyResult: 命中 iteration          collecting
     (scope=node, target=map_generate_shots);
     PatchPreview→重跑 1 镜≈Y 积分
14 WS← plan_card「修改预览」                            collecting→      awaiting_user   plan v2(revised,seq8)
                                                       confirming
15 POST /signals{confirm_plan}(R=A) → launch(Fork)    confirming→      running         Outbox.Enqueue(create_task:
                                                       executing                       Fork{from:70010,patch:shot[1]=night})
16 [提交后] Outbox→engine fork → Task 70021            executing        running         tasklink(fork,forked_from=70010)
17 WS← progress… → result_card(版本2)                  executing→       completed       result msg
                                                       completed
```

## 8. 这条闭环验证了哪些文档

| 环节 | 验证的文档 |
|------|-----------|
| 一句话 → intent/skill/slots | [15 §5](15-agent-decision-engine.md) Decide 冷启动 + [10 §5](10-skill-manifest-spec.md) slots + examples |
| 步2/14 方案卡 + 报价 | [10 §4](10-skill-manifest-spec.md) cost + [09 §2.7](09-conversation-api.md) plan_card |
| 步3 confirm_plan(A) / 步7·10 review(E) | [09 §2.7](09-conversation-api.md) 信号双归宿 + [14 §5](14-repository-contracts.md) |
| 步4/15 Post-Commit 建任务 | [14 §4.2](14-repository-contracts.md) Outbox（红线4）+ [12 §8.2](12-data-model-ddl.md) agent_outbox |
| 步5/11 进度翻译（白名单） | [10 §6](10-skill-manifest-spec.md) stages + [14 §5](14-repository-contracts.md) Observer |
| 步6/9 reviewing 闸门 | [11 §4](11-agent-state-machine.md) 9 态 + [10 §7](10-skill-manifest-spec.md) gates |
| 全程 seq/CAS/幂等 | [12 §7](12-data-model-ddl.md) + [14 §0 红线](14-repository-contracts.md) |
| 步13–17 fork 迭代 | [10 §8](10-skill-manifest-spec.md) iteration + [15 §8](15-agent-decision-engine.md) modifyPath + 引擎 fork/patch |

**结论**：short_drama 这一个 Skill 完整穿过了「Manifest → Decide → State → UoW/Outbox → Observer → fork」全链路。**这条闭环跑通 = V2 Agent First 架构成立。** 其余 Skill（goods_video / image_motion / text_to_image…）只是换一份 Manifest，骨架不变。

## 9. 落地清单（用它做第一个验收）
1. 把本文 §1 落为 `ai-engine/skill/manifests/short_drama.yaml`，过 [10 §9](10-skill-manifest-spec.md#9-注册与校验启动期-fail-fast) 启动期校验（`workflow_name=short_drama` 存在、两个 gate signal 真实、slot `maps_to` 可解析）。
2. 用它写 [15 §11](15-agent-decision-engine.md#11-可测性这是把大脑写对的保证) 的决策快照测试：冷启动/缺 user_prompt/确认/改某镜 四条。
3. 端到端联调按 §7 的 17 步走一遍——这就是 V2 的**第一个验收用例**。

---

返回：[v2/README.md](README.md) · 相关：[10 Manifest](10-skill-manifest-spec.md) · [11 状态机](11-agent-state-machine.md) · [14 Repository](14-repository-contracts.md) · [15 Decision Engine](15-agent-decision-engine.md)
