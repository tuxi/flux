# Agent-Driven DAG 生成 — 实施计划

> 目标：用 Agent + DAGPlanner 替换手写 Workflow DSL（如 goods_video_pro_dsl.go）。

## 现状

- ✅ 所有工具已实现完整的 `tool.Tool` 接口（Name + Description + InputSchema + Execute）
- ✅ DreamAI 有 50+ 注册工具（图片/视频/TTS/LLM/存储等）
- ✅ DAGPlanner 已验证可生成合法 DAG
- ✅ Scheduler 可执行 async DAG
- ❌ DAGPlanner 当前只看到 2 个工具（shell + merge）
- ❌ 39K 行 DSL 还在手写

## 三步走

### Step 1 — 让 DAGPlanner 看到所有工具

**内容**：将 DreamAI 的 toolRegistry 暴露给 DAGPlanner。

**做法**：在 DreamAI 侧启动时，基于已有的 `toolRegistry` 创建一个 `FluxWorkflowTool`（或 MCP endpoint），让 Agent 能看到全部工具。

```go
// DreamAI 启动时
goodsToolReg := tool.NewRegistry()
// ... 注册所有工具（已有代码） ...

// 为 Agent 暴露 plan_workflow 能力
wt := flux.NewWorkflowTool(flux.WorkflowToolConfig{
    Provider:   llmProvider,
    ModelName:  "deepseek-v4-pro",
    ToolReg:    goodsToolReg,  // ← 全部 50+ 工具
    WFStore:    pgWorkflowStore,
    AwaitStore: pgAwaitStore,
})
```

**预期效果**：Agent 说 "生成一个 15 秒商品展示视频" → DAGPlanner 能看到 `goods_video_param_validate`、`generate_creative_brief`、`image_to_video_submit` 等全部工具 → 自动生成完整 DAG。

### Step 2 — 效果对比

**内容**：同一商品，手写 DSL 生成视频 vs Agent 生成 DAG 生成视频。

**对比维度**：
- 视频质量（人工评分）
- 生成时间
- 重试次数
- API 调用量/成本

**pass 标准**：Agent 生成的视频质量不低于手写 DSL 的 80%，成本不超过 1.5x。

### Step 3 — 删除 DSL

**内容**：效果验证通过后，逐步删除手写 DSL 文件。

**第一批**（低风险）：`clean_product_image_workflow_dsl.go`、`analyze_product_image_workflow_dsl.go`
**第二批**（中等）：`goods_shot_i2v_generate_dsl.go`
**第三批**（核心）：`goods_video_pro_dsl.go`

## 需要的代码改动

### A. DreamAI 侧 — 暴露 plan_workflow endpoint

在 `ai-engine/server/server.go` 的 `toolRegistry` 初始化完成后，创建 `FluxWorkflowTool`。

### B. DAGPlanner — 支持更大的工具目录

当前 DAGPlanner 的工具目录直接拼在 user prompt 里（`toolCatalog()`）。工具数量到 50+ 时，token 成本很高。需要：
- 工具目录按目标筛选（只传相关工具）
- 或使用 MCP tools/list 动态获取

### C. Async 工具的 Poll/Resume 集成

DAGPlanner 生成的 DAG 包含 async 节点（如 `video_generate_submit` → `video_generate_wait`）。这些工具内部调用了 `AwaitController.Begin()`。DreamAI 的 Notify handler 需要能匹配 DAGPlanner 生成的 async 节点。

## 里程碑

```
M1  DAGPlanner 看到全部 DreamAI 工具
    → Agent 说 "生成商品视频" → DAGPlanner 生成 15+ 节点 DAG
    → 通过 FR5 校验

M2  DAG 在 DreamAI 环境中执行
    → Async 节点正确 poll/resume
    → 产出一个完整视频

M3  效果对比：手写 DSL vs Agent DAG
    → 质量、时间、成本三维度对比

M4  第一批 DSL 删除
```
