# 重做 - 节点参数编辑需求文档

## 概述

用户在作品详情页点击「重新生成」进入局部重做流程。当前 Step 2（编辑节点参数）只是一个自由文本表单，无法满足实际使用需求。本文档定义两种用户角色的节点编辑方式，以及对后端 API 的依赖。

## 两种用户角色

### 角色 A：普通用户（修改节点 Output）

**场景**：用户生成带货视频，工作流有 5 段分镜，其中第 2 段不满意。用户选择从分镜提示词构建节点（如 `shot_prompt_builder`）开始重做。

**需求**：直接查看和修改该节点**上一次的输出内容**。例如 `shot_prompt_builder` 的 output 中有 `results[1].caption`，用户将其从"女孩在海边奔跑"改为"女孩在夕阳下漫步"，然后重跑，后续节点自动使用修正后的值。

**为什么修改 Output 最有效**：工作流 DSL 中下游节点的 input 通过表达式（input_mapping）从上游节点 output 取值，格式如 `$.nodes.shot_prompt_builder.output.results.1.caption`。修改上游 output 后，下游节点自动拿到新值，无需逐个修改 input 表达式。

**交互**：
```
选节点 → 展示节点 output JSON 树 → 用户点击字段修改值 → 生成 patch → 预览 → 执行
```

Patch 生成规则：每个被修改的字段映射为一个 PatchEntry：
```json
{
  "target": "node_output",
  "node": "shot_prompt_builder",
  "path": "results.1.caption",
  "op": "set",
  "value": "女孩在夕阳下漫步",
  "label": "修改第2段分镜提示词"
}
```

### 角色 B：专业用户/管理员（查看 ResolvedInput 定位问题）

**场景**：开发调试阶段，某个节点的 input 取值不符合预期，管理员需要查看该节点**实际解析后的 input 值**（resolvedInput），追溯到上游节点，精确定位是哪个上游节点的 output 有问题，然后对上游节点做 patch。

**需求**：查看节点的 `resolvedInput` 结构，理解数据流。`resolvedInput` 展示了 input_mapping 表达式解析后的真实 key-value，管理员据此判断数据来源和正确性。

**交互**：
```
选节点 → 展示 resolvedInput + output → 管理员分析 → 选择上游节点 → 修改其 output → 预览 → 执行
```

## API 依赖

### 已有 API：GET ai/runs/{runID}/nodes/{nodeName}

WorkflowKit 包中已定义 `RunInspectorApi.getRunNodeDetail(runID:nodeName:)`，返回 `RunNodeDetailResponseDTO`。

**核心字段**（`RunNodeDetailDTO`）：

| 字段 | 类型 | 用途 | 对应角色 |
|------|------|------|----------|
| `output` | `[String: JSONValue]?` | 节点的输出数据，用户可直接修改 | 角色 A（普通用户） |
| `resolvedInput` | `[String: JSONValue]?` | input_mapping 解析后的实际输入值 | 角色 B（管理员） |
| `checkpoint` | `[String: JSONValue]?` | 节点 checkpoint 数据（map 场景） | 角色 B |
| `name` | `String` | 节点名称 | 两者 |
| `state` | `String` | 节点状态 | 两者 |
| `action` | `String` | execute / reuse / patch | 两者 |

### 前端使用方式

1. **Step 1**：调用 `POST /user/works/:id/patch-preview`（空 body），获取所有节点名称列表，用户选择 resume_from 节点
2. **Step 2（新）**：调用 `GET ai/runs/{runID}/nodes/{nodeName}` 获取选中节点 + 相关上游节点的 detail：
   - 展示 `output` JSON 树 → 普通用户点击字段直接修改值
   - 展示 `resolvedInput` → 管理员查看数据来源
   - 每个修改自动生成对应的 PatchEntry
3. **Step 3**：调用 `POST /user/works/:id/patch-preview`（带 resume_spec + patches），预览执行计划
4. **Step 4**：调用 `POST /user/works/:id/fork`，执行重做

### Patch 与 Node Detail 的关系

```
output 字段路径                           Patch path
────────────────────────────────────────────────────────────
output.intent                        →  path: "intent"
output.results[2].caption            →  path: "results.2.caption"
output.shots[0].prompt               →  path: "shots.0.prompt"
checkpoint.reused_items["3"]         →  path: "reused_items.3"
```

## 需要后端确认

1. **runID 与 work/task ID 的关系**：`GET ai/runs/{runID}/nodes/{nodeName}` 中的 `runID` 是否等同 `/user/works/:id/patch-preview` 中的 `id`？如果不同，前端如何从 work ID 获取 runID？

2. **鉴权**：`ai/runs/{runID}/nodes/{nodeName}` 当前可能是 Run Inspector（内部/admin 工具）的 API，是否能用于普通用户侧请求？是否需要新建一个用户侧的 node detail 端点（如 `GET /user/works/:id/nodes/:nodeName`）？

3. **Node detail 数据可用性**：对于已完成的任务，所有节点的 `output`、`resolvedInput`、`checkpoint` 是否都有持久化数据？reuse 的节点是否也能返回 output？

4. **批量获取节点 output**：当前 `GET ai/runs/{runID}/nodes/{nodeName}` 是单节点查询。如果用户选择从某个节点重做，前端可能需要展示该节点及上游相关节点的 output（供管理员追溯数据流），是否需要支持批量查询？

## 前端实现计划（待后端确认后）

1. Step 2 页面改为双 Tab 布局：「修改输出」（普通用户）和「查看输入」（管理员）
2. 「修改输出」Tab：展示 `output` 的 JSON 树，支持点击叶子节点直接编辑值
3. 「查看输入」Tab：展示 `resolvedInput` 的 JSON 树（只读），帮助定位数据来源
4. 编辑界面复用 WorkflowKit 中已有的 `JSONTreeNode` 渲染组件
