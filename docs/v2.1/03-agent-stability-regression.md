# V2.1 · 03 · Agent 稳定性与三层回归矩阵

> 建立「由简到繁」的回归梯度：任何 Agent **公共能力**变更，优先跑 Level 1/2（便宜、快），按需再跑昂贵的 Level 3。
> 上位：[00 总体方案](00-skill-integration-and-stability.md) · [v2/17 §6 路线图](../v2/17-implementation-status-and-roadmap.md)。

---

## 1. 三层回归矩阵

| 维度 | Level 1 · text_to_image | Level 2 · image_to_image | Level 3 · short_drama |
|---|---|---|---|
| 验证重点 | 基础 Agent 闭环 | 资产输入 / 上下文引用 / 连续修改 | 复杂 Workflow / 双 Review Gate / 子任务 / 长执行 |
| 单次成本 | ~8 积分 | ~10 积分 | ~300 积分 |
| 单次耗时 | 秒级~十几秒 | 秒级~十几秒 | 数分钟 |
| await 闸门 | 无 | 无 | 2 道（脚本 + 九宫格画面） |
| 子任务规模 | 小（内容审核 map） | 小（内容审核 map） | 大（map 逐镜 / tts / 合成） |
| 资产输入 | 无 | **有（source image）** | 可选（参考图） |
| 回归频率 | **每次公共改动必跑（高频）** | 每次涉及资产/上下文/迭代的改动必跑 | 大版本 / 发布前 / 涉及 Review·子任务·终态收口时（低频） |
| 自动化目标 | 决策快照测试 + 端到端冒烟（CI 友好） | 决策快照 + 端到端（含 mock 资产） | 端到端真机为主（成本高，难全自动） |

**使用原则**：
- 改 `agent/runtime`、`agent/skill`、`agent/service` 等**公共层** → 至少跑 L1 全套 + L2 资产相关用例；通过后再评估是否触发 L3。
- 改 Observer / Activity / 终态收口 / Review gate → **必跑 L3**（L1/L2 无 gate、子任务少，覆盖不到）。
- 改 Manifest 校验 / 注册纪律 → L1+L2+L3 启动期 fail-fast 各跑一遍。

## 2. 通用性回归（确认不依赖 short_drama 特判）

对**每个** Skill 逐项确认以下能力均通过同一套公共代码（无 per-skill 硬编码）：

| 能力 | 检查点 | 当前风险（缺口） |
|---|---|---|
| Manifest 加载/注册 | 启动 LoadEmbedded→Validate→Register，invalid 阻断启动 | 深度校验缺失（G9） |
| Intent 选择 | 各 Skill 正例命中、负例不误命中 | Intent 只认短剧（G3） |
| Slot 抽取 | 各 Skill 必填/默认/追问正确 | 抽取器只认短剧槽位（G4） |
| Clarify | 缺必填 → 追问对应 slot.ask | — |
| PlanCard | 展示该 Skill 的 slots + 估价 + plan_stages | 写死短剧 derived（G2） |
| confirm_plan | confirming→executing，幂等 | ✅ 通用 |
| Outbox / Task 创建 / TaskLink | Post-Commit 建 task，无孤儿/重复 | ✅ 通用 |
| Observer | 主/子任务分层正确 | ✅ 通用 |
| Activity | 同 id 原地更新；headline 正确 | headline 写死短剧（G10） |
| Result/Error | 终态唯一、互斥；图片单图正确 | 不携带源图/多图（G12） |
| Works | 进 /works，可打开作品 | route_key 需对齐（待核对） |
| Fork / iterate | 「再来一版/改一下」whole-fork | ✅ 通用（i2i 的 source 切换需 G6） |
| Conversation WS | 按 message.id upsert | ✅ 通用 |

> **回归判定**：若某能力在 L1/L2 失败而 L3 通过，往往说明该能力**被 short_drama 特判掩盖**——这正是 V2.1 要逼出的问题，应记为缺口而非「Skill bug」。

## 3. 正确性回归（每层都查）

呼应需求文档第十节 10.2：
- [ ] 一个主 Task 只产**一个**最终 ResultCard **或** ErrorCard（`observer.finalize` 守卫，`observer.go:351`）。
- [ ] 子任务终态**不**升格为独立结果消息（`isMainTaskEvent`，`observer.go:157`）。L1/L2 的内容审核 map、L3 的逐镜/tts 子任务都要验证不刷屏。
- [ ] Activity **同 id 原地更新**（`observer.applyToActivity`，`observer.go:212`），不新增重复消息。
- [ ] Agent 启动任务进入 /works（entry 元数据非空）。
- [ ] ResultCard 可打开有效作品详情。
- [ ] **失败链路不产生成功作品**；失败有 ErrorCard + 计费退款语义正确（G8 重点核验）。
- [ ] 终态幂等：`task_failed` 后又来 `task_final_failed` / recovery 重入队，不产第二张终态卡。

## 4. 恢复性回归（每层都查）

呼应需求文档 10.3（[v2/17 §5.2 / Phase A](../v2/17-implementation-status-and-roadmap.md)：WS 无 event_seq/replay，恢复靠 REST 兜底）：
- [ ] 退出聊天详情再进入：detail + GET /messages 重建（含 Activity 累积态、pending action）。
- [ ] App 切后台再回来。
- [ ] WS 断开重连：补 lastSequence 之后的新消息。
- [ ] 任务完成时客户端不在线：再上线靠 GET 兜底拿到 result_card。
- [ ] ResultCard 漏帧后 snapshot 恢复。
- [ ] Activity 更新漏帧后恢复（整窗 GET 补累积态）。

> L1/L2 因执行快，「完成时不在线」更容易触发（用户刚发就切走，回来已完成）——是恢复路径的好测试场景。L3 因长执行，更适合测「reviewing 中途断线 / 子任务进行中断线」。

## 5. 触发式回归速查（改了什么 → 跑什么）

| 改动位置 | L1 | L2 | L3 |
|---|:--:|:--:|:--:|
| `runtime.go` Intent/Slot/Plan/Confirm | ✅必 | ✅必 | ⬜按需 |
| `skill/manifest.go` 校验/注册 | ✅ | ✅ | ✅ |
| `observer.go` 进度/Activity | ✅ | ✅ | ✅必（gate/子任务） |
| `observer.go` 终态/finalize | ✅ | ✅ | ✅必 |
| `conversation_service.go` turn/confirm/fork | ✅必 | ✅必 | ✅ |
| `agent_outbox_launcher.go` / 计费 / 资产引用 | ✅ | ✅必（资产） | ✅ |
| Review gate / signal 路由 | ⬜(无 gate) | ⬜(无 gate) | ✅必 |
| 资产入口 / Asset Slot（G5–G7） | ⬜ | ✅必 | ⬜ |
| WS / 恢复协议 | ✅ | ✅ | ✅必 |

## 6. 前置说明（与缺口的关系）

本矩阵描述的是**目标态**。在 [00 §7](00-skill-integration-and-stability.md) 的缺口落地前：
- L1 受 G1（mode 注入）、G2（PlanCard）、G3（Intent）、G4（Slot）阻塞，无法跑通。
- L2 额外受 G5（资产入口）、G6（指代解析）、G7（资产引用）阻塞。
- L3 已可跑通（[v2/17 §4](../v2/17-implementation-status-and-roadmap.md) 已验证），本轮作为**回归基线**，确保 L1/L2 接入不破坏它。

因此：**先建 L3 回归基线（现状即可），再随 PR-B/PR-D 逐步点亮 L1/L2。**

## 7. 最小自动化建议

- **决策快照测试**（参考 `runtime_integration_test.go` / `conversation_service_test.go` 既有范式）：对每个 Skill 写「冷启动 / 缺必填 / 确认 / iterate」四条快照，断言 Decision（intent/slots/stage/plan/launch）。便宜、稳定、CI 必跑。
- **端到端冒烟**：L1/L2 用 mock provider（或 content_audit 关 + 最小 provider）跑「建会话→确认→completed→/works 可见」，不真烧 provider 额度。
- **L3 真机**：保留人工 + 半自动，按发布节奏跑。

---

返回：[00 总体方案](00-skill-integration-and-stability.md)
