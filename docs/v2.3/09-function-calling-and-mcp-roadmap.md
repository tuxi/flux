# 09 · Function Calling And MCP Roadmap

## 1. 当前不实施

第一版只建立内部 Capability Runtime，不实施 Function Calling，也不暴露 MCP。

原因：

- 内部对象生命周期尚未稳定。
- Task/await/activity 的 superseded 语义还未落地。
- 现在暴露外部协议会固化错误边界。
- Capability 会产生真实副作用，必须先完成策略、幂等、事务和回归。

## 2. Function Calling 未来接入

未来 LLM 可以基于当前 ActiveObject 的 CapabilityDescriptor schema 输出结构化 `CapabilityCall`。

但必须保留服务端强校验：

```text
LLM proposed call
  -> registered capability lookup
  -> target revalidation
  -> input schema validation
  -> policy check
  -> idempotency check
  -> invoker
```

LLM 不能构造未注册 capability，不能绕过 CapabilityPolicy。

## 3. Function Calling 可见范围

给 LLM 的工具列表必须是每回合动态裁剪后的：

```json
[
  {
    "name": "revise_review_by_fork",
    "target": {"type": "review_artifact", "message_id": "..."},
    "input_schema": {"feedback": "string"}
  }
]
```

禁止提供全局：

```text
cancel_task
modify_any_plan
fork_any_task
```

## 4. MCP 未来边界

当内部模型稳定后，可以把以下结构包装成 MCP Resources/Tools：

- `ActiveObject`
- `CapabilityDescriptor`
- `CapabilityCall`
- `CapabilityResult`

可能形态：

```text
resource: conversation/{id}/active_objects
tool: capability.invoke
```

但 MCP 层仍只能调用内部 CapabilityInvoker，不能直接访问 task repository 或 await binding repository。

## 5. 外部 Agent 安全

未来若外部 Agent 可调用能力，必须新增：

- capability scope
- user delegation token
- approval policy
- audit log
- rate limit
- replay/idempotency contract
- dry-run/preview 模式

Review 修订这类会取消运行并创建新版 Plan 的能力，默认不应开放给外部 Agent 自动执行，除非用户明确授权。

