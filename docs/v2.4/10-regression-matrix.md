# 10 · Regression Matrix

## 1. 目标

V2.4 回归矩阵锁定 Planner 行为，而不是只验证回复文案。

必须覆盖：

- Skill selection。
- InputPlanningPolicy。
- Assumption / override。
- PlannerTrace。
- PlanCard generation。
- ReAct boundary。
- short_drama 零回退。

## 2. 测试原则

- 优先 Mock `PlannerInput`，避免依赖真实 DB。
- 对 compiled Skill catalog 使用固定 fixture。
- 对 PlannerTrace 做结构化断言，不断言完整自然语言。
- 对 PlanCard 断言 value source / assumption id / editable / cost hint。
- 对 legacy 链路保留端到端 smoke。

## 3. Skill Selection

| ID | Priority | 输入 | 上下文 | 期望 |
| --- | --- | --- | --- | --- |
| S01 | P0 | 帮我做一个赛博朋克视频 | 无 pending | selected=`video_gen` |
| S02 | P0 | 帮我做一张赛博朋克图 | 无 pending | selected=`text_to_image` |
| S03 | P0 | 帮我做一个赛博朋克 | 无 pending | ambiguous=true，ask_user 消歧 |
| S04 | P0 | 把这张改成动漫风 | 带图片 | selected=`image_to_image` |
| S05 | P0 | 把这张改成动漫风 | 无图片 | selected=`image_to_image`，Missing=`source_image` |
| S06 | P0 | 赛博朋克 | pending asks style | 延续 pending skill，不全局选择 |
| S07 | P0 | 我想做一张图 | short_drama reviewing | `goal_switch_confirmation` pending |
| S08 | P1 | 今天天气怎么样 | current task active | out_of_scope，锚回当前任务 |
| S09 | P0 | 取消 | any pending/task | no skill selection |
| S10 | P0 | 再来一版 | current result | inherit current skill |

## 4. InputPlanningPolicy

| ID | Priority | 场景 | 期望 |
| --- | --- | --- | --- |
| P01 | P0 | required + ask_user | MissingInput，NextAction=ask_user |
| P02 | P0 | required + requires_asset 无资产 | MissingInput，追问资产 |
| P03 | P0 | required + requires_asset 有资产 | ProposedSlots 绑定 asset_id |
| P04 | P0 | required + system_default | ProposedSlots，Observation，不生成 Assumption |
| P05 | P0 | required + creative_default 高置信 | ProposedSlots + confirmable Assumption |
| P06 | P0 | required + creative_default 低置信 | MissingInput，ask_user |
| P07 | P0 | required + cost_affecting_default | ProposedSlots + confirmable Assumption + cost hint |
| P08 | P1 | 多 missing：source_image + bgm_style | 只追问 source_image |
| P09 | P1 | 多 missing：bgm_style + narration_style | 可聚合轻量偏好问题 |
| P10 | P0 | system_injected 缺失 | Planner 不生成，交系统注入/launcher |

## 5. Assumption / Override

| ID | Priority | 场景 | 期望 |
| --- | --- | --- | --- |
| A01 | P0 | cyberpunk -> bgm synthwave | Assumption 有稳定 ID |
| A02 | P0 | 用户确认 PlanCard | Assumption accepted |
| A03 | P0 | 用户说不要电子乐换钢琴 | override 指向 SourceAssumptionID |
| A04 | P0 | override 后再规划同一 plan | 不再推荐 synthwave |
| A05 | P1 | 新目标重新开始 | 不自动继承旧 override |
| A06 | P2 | 用户说以后都不要电子乐 | 可进入长期偏好候选，但 V2.4 不实现 |
| A07 | P0 | cost default reason | 解释成本/耗时/质量，不解释用户偏好 |
| A08 | P1 | mode default | 不生成 Assumption |
| A09 | P1 | system default | 不生成 Assumption |
| A10 | P0 | enum 拒绝 assumption | 降级 ask_user |

## 6. PlannerTrace

| ID | Priority | 场景 | 期望 |
| --- | --- | --- | --- |
| T01 | P0 | 正常规划 | Trace 有 observation / assumption / decision / validation |
| T02 | P0 | PlannerActivity upsert | 同一 message.id replace |
| T03 | P0 | completed | status=completed |
| T04 | P0 | validator fail | status=failed，用户可见降级摘要 |
| T05 | P1 | cancel planning | status=canceled |
| T06 | P1 | expired plan | status=expired |
| T07 | P0 | payload contains token | 黑名单拦截 |
| T08 | P1 | payload contains asset_id | 白名单允许 |
| T09 | P0 | raw CoT attempted | 测试失败 |
| T10 | P2 | detail TTL expired | 不伪造重建 |
| T11 | P0 | PlannerActivity failed terminal | 失败后 upsert status=failed，不允许一直停留在 planning |

## 7. PlanCard

| ID | Priority | 场景 | 期望 |
| --- | --- | --- | --- |
| C01 | P0 | validated ActionPlan | 生成 PlanCard |
| C02 | P0 | required missing | 不生成 PlanCard |
| C03 | P0 | requires_asset missing | 不生成 PlanCard |
| C04 | P0 | creative_default | 展示 reason + editable |
| C05 | P0 | cost_default | 展示 cost hint + editable |
| C06 | P0 | system_injected | 不展示为可编辑字段 |
| C07 | P0 | client tamper payload | confirm_plan 使用服务端 Plan/ActionPlan |
| C08 | P0 | active version changed | confirm 阻止并提示重生成 |
| C09 | P1 | editable field natural language edit | 重新校验 ActionPlan |
| C10 | P1 | trace_id linked | 可懒加载 Trace detail |

## 8. ReAct Boundary

| ID | Priority | 场景 | 期望 |
| --- | --- | --- | --- |
| R01 | P0 | answer_question | 延续 pending |
| R02 | P0 | request_modification on ReviewCard | 走 V2.3 review revision |
| R03 | P0 | cancel pending | clear pending，不选 Skill |
| R04 | P0 | cancel running task | 走 cancel capability / lifecycle |
| R05 | P0 | regenerate result | inherit current plan/result |
| R06 | P1 | out_of_scope during review | 锚回 review |
| R07 | P0 | new skill during blocking task | goal_switch_confirmation |
| R08 | P0 | user confirms switch | 处理旧上下文后进入新 Skill |
| R09 | P0 | validator fail | 单轮停止，不自循环 |
| R10 | P1 | response includes explanation + card | 仍算一个主业务决策 |

## 9. short_drama 零回退

| ID | Priority | 场景 | 期望 |
| --- | --- | --- | --- |
| D01 | P0 | 做一个搞笑短剧 | PlanCard 字段不回退 |
| D02 | P0 | 信息不足 | 追问，不生成脏 PlanCard |
| D03 | P0 | 回答追问 | 延续 short_drama pending |
| D04 | P0 | confirm_plan | outbox 创建 task |
| D05 | P0 | prompt ReviewCard | 正常等待确认 |
| D06 | P0 | ReviewCard 修改 | revise_review_by_fork |
| D07 | P0 | 修改后旧 task | canceled + await binding canceled |
| D08 | P0 | 旧 ReviewCard 再提交 | stale/noop |
| D09 | P0 | ResultCard 再来一版 | fork/regenerate 正常 |
| D10 | P0 | Planner fail | 不静默 legacy fallback |

## 10. LLM Roadmap Guard

| ID | Priority | 场景 | 期望 |
| --- | --- | --- | --- |
| L01 | P1 | LLM 输出未知 Skill | rejected |
| L02 | P1 | LLM 输出 invalid enum | rejected / ask_user |
| L03 | P0 | LLM 输出 raw reasoning | 不落库不展示 |
| L04 | P2 | LLM timeout | fallback |
| L05 | P1 | LLM 尝试绕过 cost confirm | rejected |

## 11. 自动化形态

建议测试层级：

```text
unit:
  InputPlanningPolicy
  SkillSelection
  AssumptionOverride
  ActionPlanValidator

integration:
  PlannerInput -> ActionPlan -> PlanCard
  PlannerTrace -> PlannerActivity

runtime smoke:
  short_drama E2E
  image_to_image requires_asset
  goal_switch_confirmation
```

Mock fixture：

- `video_gen`
- `text_to_image`
- `image_to_image`
- `short_drama`
- disabled skill

## 12. 裁决

V2.4 通过标准：

- 以上矩阵中 P0/P1 case 全绿。
- `short_drama` 零回退全绿。
- 原始 CoT 不落库、不推送。
- PlanCard 只来自已校验 ActionPlan。
- blocking context 下不静默切 Skill。
