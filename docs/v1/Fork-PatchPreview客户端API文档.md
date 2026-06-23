
# Fork & PatchPreview 客户端 API 文档
===================================

## 概述

用户对工作流生成结果不满意时，可以通过 Fork（分叉）+ Redo（局部重做）来修正，
而不需要从头重新跑整个工作流。核心流程：

1. 先调用 PatchPreview 接口预览执行计划，了解哪些节点会复用、哪些会重新执行
2. 用户确认后，调用 Fork 接口创建新的 fork task 并执行

⚠️ Fork 会产生扣费：只有被标记为 execute 或 patch 的节点才会实际执行并扣费，
reuse 节点会复用已有结果，不额外扣费。

## API 列表

### POST /api/v1/user/works/:id/patch-preview
预览 fork 执行计划（不创建 task，不扣费）

### POST /api/v1/user/works/:id/fork
执行 fork + redo，创建新的 fork task（会扣费）

--------------------------------------------------------------------------------

## 一、Patch Preview — 预览执行计划

POST /api/v1/user/works/:id/patch-preview

### 请求体

{
"override_input": { ... },   // 可选，覆盖工作流顶层 input
"resume_spec": {             // 必填（无 resume_spec 走全量复用）
"resume_from": "node_name", // 从哪个节点开始重新执行
"patches": [ ... ]         // 可选，patch 列表
}
}

### 响应体

{
"valid": true,
"plan": {
"mode": "fork",             // fork | re_fork（对已 fork 的 task 再次 fork）
"resume_from": "enhance_prompt",
"summary": {
"execute_count": 3,       // 将重新执行的节点数（会扣费）
"reuse_count": 5,         // 复用结果的节点数（不扣费）
"patch_count": 1          // 被 patch 的节点数
},
"nodes": [
{
"name": "parse_input",
"label": "解析输入",              // DSL 定义的展示名，可用于 UI
"action": "reuse",              // execute | reuse | patch
"reason": "upstream_of_resume", // 为什么是这个 action
"reuse_kind": "node",           // node | map_items
"is_patched": false,
"is_resume_boundary": false     // 是否是 resume_from 指定的节点
},
{
"name": "enhance_prompt",
"label": "增强提示词",
"action": "execute",
"reason": "resume_boundary",
"reuse_kind": "",
"is_patched": false,
"is_resume_boundary": true
}
]
}
}

如果 valid=false，message 字段包含错误原因。

--------------------------------------------------------------------------------

## 二、Fork — 执行分叉重做

POST /api/v1/user/works/:id/fork

### 限制条件

- 只有 terminal（success/failed/canceled）或 suspended 状态的任务才能 fork
- 只能 fork 自己的任务

### 请求体

{
"override_input": { ... },
"edit_action": "edit_parsed_intent",
"edit_label": "修改提示词理解",
"resume_spec": {
"resume_from": "enhance_prompt",
"patches": [ ... ]
}
}

字段说明：
override_input  可选，覆盖工作流的顶层 input JSON
edit_action     必填，编辑动作标识，用于前端展示和追溯
edit_label      可选，用户可读的编辑描述
resume_spec     可选，fork + redo 的核心配置
resume_from   从哪个节点开始重新执行，省略则只 fork 不 redo
patches       patch 列表，用于修正上游节点的输出或 checkpoint

### 响应体

{
"task_id": 123456,
"forked_from": 123000,
"status": "pending"
}

--------------------------------------------------------------------------------

## 三、Patch 说明

Patch 用于修正上游已执行节点的运行时数据，让后续 redo 的节点拿到正确的输入。

### Patch 结构

{
"target": "node_output",
"node":   "target_node_name",
"path":   "field.sub_field",
"op":     "set",
"value":  { ... },
"label":  "人类可读的描述"
}

### Patch Target 类型

node_output     修改节点的 output（影响下游节点的 input_mapping 取值）
node_checkpoint 修改节点的 checkpoint（影响 map 的 item_states / reused_items 等）
runtime_state   修改运行时全局状态

### Patch Op 类型

set    设置值（最常用）
delete 删除字段
add    向数组追加元素
merge  合并 map

### Patch Path 说明

路径分隔规则（与 engine/patch_path.go 一致）：

- 只有 `[0]` 这种 bracket 数字形式才会被视为数组索引
- `.0` 点号后跟纯数字视为字符串 key "0"，不会自动转为数组索引
- `["key"]` bracket 字符串形式显式指定字符串 key

正确写法示例：
  "intent"                       → output["intent"]
  "results[2].caption"            → output["results"][2]["caption"] （数组索引用 [N]）
  "reused_items[\"3\"]"           → checkpoint["reused_items"]["3"] （字符串 key "3"）

常见错误：
  "results.2.caption"             → output["results"]["2"]["caption"] ❌ 2 被当成 key 而非索引
  "results[0].items.3.label"      → output["results"][0]["items"]["3"]["label"] ❌ 3 被当成 key

--------------------------------------------------------------------------------

## 四、使用场景示例

### 场景 1: 修改上游节点输出，从中间节点重跑

用户在生成视频后不满意意图理解结果，修改了 intent 内容，希望从 enhance_prompt
节点开始重新执行。parse_input 和 reconstruct_intent 在上游会被复用。

POST /api/v1/user/works/123000/fork

{
"edit_action": "edit_parsed_intent",
"edit_label": "修改提示词理解",
"resume_spec": {
"resume_from": "enhance_prompt",
"patches": [
{
"target": "node_output",
"node": "reconstruct_intent",
"path": "intent",
"op": "set",
"value": {
"scene": "海边日落",
"motion": "缓慢横移",
"style": "电影感"
},
"label": "修正 intent 内容"
}
]
}
}

效果：
parse_input           → reuse（不扣费）
reconstruct_intent    → reuse，但 output 被 patch 覆盖
enhance_prompt        → execute（扣费，开始重做）
...后续节点           → execute（扣费）

### 场景 2: 修正 map 内部某一项的 checkpoint

用户对 map 生成的 5 张图中第 2 张的分析结果不满意，修正后只重跑那一项。

POST /api/v1/user/works/123000/fork

{
"edit_action": "fix_map_result",
"edit_label": "修正第2张图分析",
"resume_spec": {
"resume_from": "map_results_extract",
"patches": [
{
"target": "node_checkpoint",
"node": "map_images_prepare",
"path": "results.1.vlm_result.caption",
"op": "set",
"value": "一个女孩站在海边",
"label": "修正 index=1 的 caption"
},
{
"target": "node_checkpoint",
"node": "map_images_prepare",
"path": "reused_items.1",
"op": "set",
"value": false,
"label": "标记 index=1 不复用"
}
]
}
}

效果：map 中 index=1 的 lane 重新执行，其他 lane 复用。

### 场景 3: 只覆盖输入，不 patch 任何节点

用户只想用新的 input 重新跑整个流程，不需要 patch。

POST /api/v1/user/works/123000/fork

{
"edit_action": "retry_with_new_input",
"edit_label": "用新 prompt 重新生成",
"override_input": {
"prompt": "一只猫坐在窗台上",
"style": "水彩画"
}
}

### 场景 4: 先预览再决定

POST /api/v1/user/works/123000/patch-preview

请求体与 fork 完全一致（去掉 edit_action / edit_label），客户端可以先拿到
execute_count 告知用户「此操作将重新执行 3 个节点，可能会产生扣费」。
