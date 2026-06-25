# Flux v2 Skill 模型

> 写作时间：2026-06-24。研究来源：Claude Code SKILL.md、Codex Skills、Cursor Rules (.mdc)、MCP Prompts/Resources、Gemini Gems。
> 核心原则：**Flux 不发明 Skill 格式，只实现 Skill Runtime。** SKILL.md 是正在形成的跨平台事实标准。

---

## 1. 为什么不自己发明

| 如果自己发明 | 半年后会发生 |
|---|---|
| `type Skill interface { Definition(); Invoke() }` | 「为什么 Claude Code 的 skill 拿不进来？」 |
| 自定义 YAML DSL | 「为什么 github 上的 skill repo 不能直接用？」 |
| Go struct 定义 | 「为什么 prompt.md / tool.md / memory.md 都得重新转换？」 |

**这是 MCP 当年的教训**——各家自己搞，后来才被迫兼容。这次 Skill 赛道，社区正在自然收敛到一个格式，Flux 直接站在那个格式上。

---

## 2. SKILL.md：正在收敛的跨平台格式

### 2.1 它是什么

一个文件夹 + 一个 `SKILL.md` 文件 = 一个 Skill。

```
my-skill/
├── SKILL.md          # 必需：YAML 前置元数据 + Markdown 指令
├── scripts/          # 可选：自动化脚本
├── references/       # 可选：长文档、API schema
└── assets/           # 可选：模板、配置文件
```

**跨平台路径约定（2026 事实标准）：**

| 平台 | 路径 |
|---|---|
| Claude Code | `~/.claude/skills/` |
| Codex CLI | `~/.agents/skills/` |
| Google Antigravity | `~/.gemini/antigravity/skills/` |
| Cursor | `~/.cursor/skills/` |
| GitHub Copilot | `~/.github/skills/` |
| **Flux / DreamAI** | `~/.flux/skills/` 或 `<project>/skills/` |

**同一份 SKILL.md 不加修改即可跨平台运行。**

### 2.2 格式

```markdown
---
name: generate_video
description: Generate 10-30 second product marketing videos.
---

# Generate Video

## Purpose
Generate product marketing videos from images and descriptions.

## When to Use
- E-commerce product promotion
- Short-form video ads

## Required Inputs
- image: product image URL
- description: product description text

## Outputs
- video_url: final video URL
- cover_url: thumbnail image URL

## Implementation
workflow: workflow.yaml
```

### 2.3 调用方式

- **显式**：`/generate_video` 或用自然语言匹配 `description`
- **隐式**：Agent 读取 skill 的 `description` 字段自动匹配用户意图
- **禁用隐式**：`allow_implicit_invocation: false` in metadata

---

## 3. Flux 怎么用 SKILL.md

### 3.1 原则：存储 = SKILL.md，执行 = Flux Runtime

```
skills/
  generate_video/
    SKILL.md           ← 平台标准格式，可移植
    workflow.yaml       ← Flux 专属：DAG 定义
  code_review/
    SKILL.md            ← 同一格式
    prompt.md           ← Flux 专属：agent system prompt
  short_drama/
    SKILL.md
    workflow.yaml
```

**Agent 看到的永远是 `name + description + inputs`（SKILL.md 标准化部分）。**
**底层怎么跑（workflow / tool / agent loop）是 Flux Runtime 的事，不暴露给 planner。**

### 3.2 通过 `implementation` 字段区分执行类型

```markdown
---
name: generate_video
description: Generate product marketing videos
implementation: workflow       ← Flux 理解这个字段
workflow: workflow.yaml
---
```

```markdown
---
name: web_search
description: Search the web
implementation: tool
tool: web_search_tool
---
```

```markdown
---
name: code_fix
description: Fix compile errors
implementation: agent
goal: fix compile errors and ensure tests pass
---
```

**三种实现，planner 只看到一种：`name + description + inputs`。**

### 3.3 Skill Runtime 架构

```
Agent / Planner
      ↓
   看一眼 SKILL.md（name + description + inputs = ToolDefinition）
      ↓
 Skill Runtime（选择执行器）
      ↓
   ┌───────┼───────┐
   ↓       ↓       ↓
Tool    Workflow   Agent
(直接执行) (engine.Run) (PlanSource)
   ↓       ↓       ↓
   └───────┼───────┘
           ↓
        Engine ← 同一内核
```

---

## 4. Skill 的三态闭环

> 这是 DreamAI 最有价值的能力：**动态生成 → 稳定固化 → 成为新原子 → 继续组合。**

```
1. 动态探索
   Agent 接到目标 → Planner 动态生成 DAG → 跑通
        ↓
2. 稳定固化
   这条 DAG 跑了好几次，效果不错 → 存入 workflow.yaml + 生成 SKILL.md
        ↓
3. 成为新原子
   skill://generate_video 注册进 Skill Registry
        ↓
4. 更高层组合
   skill://short_drama 使用 skill://generate_video 作为子步骤
        ↓
   回到 1，在更高抽象层继续探索
```

**Skill 不是终点，是资产层。** 工作流从一次性的 LLM 决策，逐渐收敛成可复用、可组合、可进化的能力原子。

---

## 5. Skill 与现有 Flux 概念的关系

| Flux 概念 | 在 Skill 模型中的位置 |
|---|---|
| `tool.Tool` | ToolSkill 的执行体 |
| `ToolDefinition`（stage C） | SKILL.md → 解析成的外部接口 |
| `workflow.Compile` | WorkflowSkill 的加载器 |
| `engine.Run` / `RunWithResult` | WorkflowSkill 的执行器 |
| `LLMPlanner` / `PlanSource` | AgentSkill 的执行器 |
| `tasks.parent_id` / `root_id` | Skill 嵌套调用的递归组合（已经是 DB 模型） |
| `session.Store` | Skill 执行的对话上下文 |
| MCP server（stage B） | 暴露 `skills/list`，让外部客户端看到 Skill 菜单 |
| `async / await / AwaitBinding` | engine 内部实现细节，对 planner 透明 |

---

## 6. 与外部生态的互操作

### 6.1 直接加载外部 Skill

```bash
# 从 GitHub 加载社区 skill
git clone https://github.com/awesome-skills/python ~/.flux/skills/python

# agent 立即可见
```

**不需要转换格式。** SKILL.md 本身已是跨平台标准。

### 6.2 MCP 暴露

Flux 的 MCP server 通过 `prompts/list` 暴露 Skill 菜单：

```json
{
  "prompts": [
    {
      "name": "generate_video",
      "description": "Generate product marketing videos",
      "arguments": [
        {"name": "image", "required": true},
        {"name": "description", "required": true}
      ]
    }
  ]
}
```

**MCP `prompts/list` 天然适合做 Skill 目录。** MCP 的 prompts 就是"用户显式选择的参数化模板"——与 Skill 的语义完全一致。

### 6.3 Skill 也是 Tool

Skill 同时通过 `tools/list` 暴露为 Tool——因为 `WorkflowSkill` 包装成 `tool.Tool` 后，对 planner 来说和 `web_search` 没有区别（都是 `ToolDefinition`）。

---

## 7. 分阶段落地

| 阶段 | 内容 | 依赖 |
|---|---|---|
| **S0** | 文档 + 研究（本文） | ✅ 完成 |
| **S1** | `skill.Loader`：从 SKILL.md 文件夹解析出 `SkillSpec` | 无 |
| **S2** | `skill.Runtime`：根据 `implementation` 字段选择执行器 | S1 |
| **S3** | `WorkflowSkill` 包装成 `tool.Tool`（planner 可见） | S2 + B 引擎 |
| **S4** | `AgentSkill` / `ToolSkill` | S2 |
| **S5** | MCP server 暴露 Skill 工具（`tools/list` 含 skill.Registry 的 ExecutableSkill） | ✅ 完成（2026-06-25） |
| **S6** | 动态 DAG → SKILL.md 自动生成 + 进化闭环 | ✅ 完成（2026-06-25） |

> S1–S2 是架构基础（~100 行），不依赖任何 B 的工作。S3 依赖 B-M1a（已验证引擎全链路）。S4–S6 渐进。

---

## 8. 参考

- [Codex Skills — OpenAI Developers](https://developers.openai.com/codex/skills) — SKILL.md 格式规范
- [Claude Code Skills](https://docs.anthropic.com/en/docs/claude-code/skills) — 同一格式的 Claude 实现
- [MCP Specification — Prompts](https://modelcontextprotocol.io/specification/2025-11-25/server) — `prompts/list` 协议
- [Cursor Rules](https://cursor.com/docs/rules) — `.mdc` 规则格式
- [feiskyer/codex-settings](https://github.com/feiskyer/codex-settings) — 社区 skill 仓库
- [obra/superpowers](https://github.com/obra/superpowers) — 跨平台 skill 库
