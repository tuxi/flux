# 07 · RAG 增强路线（第二阶段）

> **定位先行**：我们的 RAG **不是**「知识库问答」，而是「**给创作 Agent 提供可检索的创作记忆与素材经验**」。它是 Agent Runtime 的**增强能力**，不是主架构。主架构永远是 `Conversation → Agent Runtime → Skill Selector → Workflow Engine`。

## 1. 为什么第一版不做、却要先规划

- **第一版不做**：先把 `Conversation → Skill Selector → Workflow` 主干跑通（[08](08-roadmap-and-milestones.md)）。RAG 在没有主干时是空中楼阁。
- **要先规划**：架构上必须**预留挂载点**（[02 §6](02-architecture-overview.md#6-扩展点为未来预留但第一版不实现)、[04 §11](04-agent-runtime.md#11-与未来的接口预留不实现)），否则二期接入会改动 Agent Runtime 主循环。
- **底座已就绪**：PostgreSQL + **pgvector** 已在用，RAG 不需要引入新数据库或新向量服务。

## 2. 五类创作记忆（业务分类）

RAG 检索的不是「文档」，是「**能让 Agent 少瞎编、不重复踩坑、更懂你**」的经验。五类：

| 类别 | 内容 | 典型触发 | 价值 |
|------|------|---------|------|
| `skill` | 各 Skill 的能力说明、适用/不适用、参数经验 | 意图识别 / Skill 选择 | 选得更准，少选错产线 |
| `template` | 已验证效果好的模板/蓝图（小红书种草、口播、爆款结构…） | 「做个适合小红书的种草视频」 | 直接命中好模板，而非从零编 |
| `prompt_case` | 好用的 prompt 写法、各模型适合的任务、禁忌 | 生成提示词阶段 | 提示词质量稳定，少翻车 |
| `user_work` | 用户历史作品的风格/分镜/文案/timeline/是否满意 | 「按我上次那个咖啡杯视频的风格再做一个」 | 个性化、延续性 |
| `failure_case` | 失败/差评案例：输入+模型+prompt+原因+修复 | 相似任务执行前 | 规避已知坑（手部崩、Logo 变形、长字幕溢出…） |

### 2.1 失败经验库（对我们尤其重要）
每次任务失败或效果差，结构化沉淀：

```text
输入是什么 / 模型是什么 / prompt是什么 / 失败原因 / 最后怎么修复
```

积累后，Agent 遇到相似任务能主动规避，例如：
- 人物手部容易崩 → 提示词/构图规避特写手部
- 商品 Logo 容易变形 → 用 mask/固定边缘策略（我们已有 `goods_image_fixed_edge_mask` 等工具经验）
- 长字幕容易溢出 → 控制每段字幕长度
- 6 秒视频比 4 秒更稳 → 默认时长选择
- 食品类更适合近景细节 → 镜头策略

> 这类经验本质是把团队/系统的 know-how 从「散落在代码与人脑」沉淀为「可检索、可复用、可增长」的资产。

### 2.2 商品/行业知识
```text
耳机：突出降噪、续航、佩戴舒适、通勤场景
口红：突出显白、质地、妆感、氛围
咖啡杯：突出质感、生活方式、桌面美学
```
带货 Skill 生成卖点/脚本时检索此类知识，避免 LLM 每次靠空想。

### 2.3 创作规则库
App 审核规则、内容安全、平台风格、视频生成禁忌、版权风险提示等。例：用户用带水印的小红书图生成商品视频 → Agent 提示「这类素材可能有版权/平台风险，建议用本人拍摄或授权素材」。

## 3. 最小可落地数据模型

物理上 4 张表（足够支撑五类业务），用 pgvector 存 embedding：

| 表 | 作用 | 关键字段 |
|----|------|---------|
| `rag_documents` | 一条知识的元信息 | `id, category(skill/template/prompt_case/user_work/failure_case), owner_user_id?(user_work 私有), source_ref(task_id/template_id…), title, status, created_at` |
| `rag_chunks` | 文档切片 | `id, document_id, ord, text, meta_json` |
| `rag_embeddings` | 切片向量 | `chunk_id, embedding vector(dim), model` （pgvector，建 ivfflat/hnsw 索引） |
| `rag_retrieval_logs` | 检索日志（调优/评估） | `id, conversation_id, query, category_filter, hit_chunk_ids, scores, used_in_plan(bool), created_at` |

设计要点：
- **`category` 是一等过滤维度**：检索时按意图选类（带货脚本阶段查 `template+prompt_case+知识`；执行前查 `failure_case`）。
- **`user_work` 按 `owner_user_id` 隔离**：用户私有记忆不串号；其余类（skill/template/prompt_case/failure_case）可全局共享。
- **`retrieval_logs` 从第一天就记**：用于离线评估「检索有没有真的帮到 Plan」，避免 RAG 变成自我感觉良好的摆设。

## 4. 增强后的 Agent 流程

RAG 是在主循环里**插入一步检索**，注入 Plan 提示词，**不改主干形状**：

```text
用户输入
  ↓
识别 intent ──────────────┐
  ↓                      │（intent + 槽位 + 商品信息 + user_id）
  ├── retrieve(RAG) ◀─────┘     ← 第二阶段新增的唯一插入点
  │     · template/prompt_case/知识 → 注入「怎么做得更好」
  │     · failure_case            → 注入「要避开什么」
  │     · user_work(私有)         → 注入「这个用户的偏好/上次风格」
  ↓
生成 plan（提示词里带上检索结果）
  ↓
选择 workflow（Skill Selector，检索结果可影响选择）
  ↓
执行（不变）
```

对应 [04 §2 主循环](04-agent-runtime.md#2-主循环实现视角)：检索插在 `recognizeIntent` 之后、`buildPlan` 之前。Agent Runtime 之外的层（Conversation/Skill/Engine/客户端）**无需任何改动**——这正是预留挂载点的价值。

## 5. 知识从哪来（喂养闭环）

RAG 的质量取决于喂养。我们有天然的数据飞轮：

```text
每次创作(Task) ──成功且用户满意──▶ 沉淀为 user_work / template 候选
                ──失败或差评──────▶ 沉淀为 failure_case
运营在 admin 标注「爆款/优质」 ───▶ 提升为高权重 template
Skill Manifest / 商品知识 / 规则 ─▶ 人工 + 运营维护
```

- **复用现有发布中心**：admin 已有「任务发布为模板/工具资产」的能力（`task_publish` 系列）。优质作品发布时，**顺手写入 RAG template/prompt_case**。
- **失败采集复用事件流**：`task_failed` / 用户「不满意」反馈 → 异步管线抽取输入/模型/prompt/原因，落 `failure_case`。
- **嵌入异步化**：写入与 embedding 计算走异步（可复用 worker 模式），不阻塞主流程。

## 6. 明确不做（第二阶段也克制）

| 不做 | 原因 |
|------|------|
| 用户上传 PDF → 问答 | 与创作目标无关，重且偏题 |
| 全网知识库 | 维护成本高、噪声大 |
| 复杂长期记忆 / 用户画像大模型 | 先用「user_work 检索」这种轻量个性化即可 |
| 多模态视频向量检索 | 太重；先用「作品的结构化文本特征（分镜/文案/标签）」做检索 |

> RAG 第一版要**很轻**：五类知识、4 张表、一个检索插入点。重的多模态向量、长期记忆，等数据飞轮转起来再说。

## 7. 与 Skill / Conversation 的关系（边界）

- RAG **不替代** Skill Manifest：Manifest 是「技能的静态说明书」，RAG 是「动态的经验补充」。Manifest 决定「能不能选」，RAG 影响「怎么做更好」。
- RAG **不写 Conversation 状态**：它只在 Agent Runtime 内被查询，结果进入 Plan 提示词，不直接生成用户消息。
- RAG **可被关闭**：作为增强项，故障时应能降级（检索失败 → 退回无 RAG 的 Plan 生成），不阻断创作。

## 8. 建议接入顺序

```text
先做 Conversation Layer（一期）
  → 再做 Skill Selector（一期）
    → 跑通主干、积累数据与失败样本
      → 接轻量 RAG（二期）：先 failure_case + template，再 user_work + prompt_case
```

> 顺序的本质：**先有主干和数据，再有增强。** RAG 让 Agent「更懂你的模板、历史作品、失败经验和创作规则」，但它始终是配角。

---

下一篇：[08 · 路线图与里程碑](08-roadmap-and-milestones.md) —— 分阶段落地、成功指标、未来 Agent 与 Blueprint 演进。
