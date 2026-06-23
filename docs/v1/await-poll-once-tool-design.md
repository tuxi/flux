# Await Fallback Poll / Poll Once Tool 设计

日期：2026-04-24

状态：Draft

关联文档：

- [Await Runtime V1 PRD](./engine-await-runtime-v1-prd.md)
- [Await / AsyncExecution Migration Inventory](./await-async-migration-inventory.md)
- [Provider 回调注册模型规范](./provider-callback-registration-model.md)

## 1. 背景

当前 `await` 主模型已经落地：

1. `submit -> await`
2. `AwaitBinding`
3. webhook / signal / replay
4. fallback poll scanner

当前 scanner 的兼容实现是：

1. 继续复用 legacy wait tool
2. 通过注入：
   - `max_retry=1`
   - `poll_interval_ms=0`
3. 把原本会在节点内部长轮询的 wait tool 降级成“单次查询”

这个方案已经能工作，但它只是过渡方案，不适合作为长期终态。

## 2. 目标

本设计目标：

1. 为 `await` 的 fallback poll 定义统一的 `poll_once` tool 规范
2. 明确 scanner 只依赖“单次查询型 tool”
3. 弱化 legacy wait tool 的主路径角色
4. 为后续 provider 接入提供统一范式

## 3. 非目标

本期不做：

1. 不一次性重写所有 legacy wait tool
2. 不强制现有 scanner 立刻停止兼容 legacy wait tool
3. 不改变 `await` 的 DSL 形态
4. 不把 provider submit 与 poll 合并成单节点

## 4. 当前问题

当前 fallback poll 依赖 legacy wait tool 的方式存在几个问题：

1. tool 名称仍然叫 `wait`
2. tool 的自然语义仍然像“内部阻塞轮询”
3. scanner 需要靠额外输入把它压成单次查询
4. 新人阅读代码时，容易误以为 wait tool 仍然是主执行路径

所以现在真正缺的不是功能，而是**语义收口**。

## 5. 设计原则

### 5.1 scanner 只负责调度，不负责实现 provider 查询逻辑

scanner 的职责是：

1. 找到 `AwaitBinding`
2. 判断是否 due
3. 选择 poll tool
4. 命中终态后走 `CompleteAwaitNode`

它不应该承载 provider-specific 的请求细节。

### 5.2 poll tool 只做一次查询

`poll_once` tool 的职责应该是：

1. 根据 task id / provider task id 查询一次外部状态
2. 返回：
   - 成功终态结果
   - 失败终态错误
   - 非终态状态

它不应该：

1. sleep
2. while-loop
3. 自己计算退避节奏
4. 自己决定下一次何时 poll

### 5.3 重试节奏统一交给 AwaitBinding

poll 的节奏统一由：

1. `AwaitBinding.next_poll_at`
2. `AwaitBinding.poll_attempts`
3. scanner 的回退策略

控制，而不是由 tool 自己控制。

## 6. 推荐接口规范

建议新增统一语义约定：

- tool 名称建议使用：`*_poll_once`

例如：

1. `video_generate_poll_once`
2. `goods_shot_i2v_poll_once`
3. `aliyun_image_generate_poll_once`
4. `volcengine_image_generate_poll_once`
5. `aliyun_image_to_image_poll_once`

### 6.1 输入规范

最小输入建议：

```json
{
  "api_task_id": "string",
  "provider_task_id": "string",
  "api_provider": "string",
  "model": "string"
}
```

要求：

1. 至少支持本 provider 所需的 task 识别字段
2. 允许带 `model`、`api_provider` 这类辅助字段
3. 不再通过 `max_retry`、`poll_interval_ms` 控制查询语义

### 6.2 输出规范

统一建议输出三种语义：

1. 成功终态
2. 失败终态
3. 非终态

实现上建议：

- `tool.Result.Success=true`
- `Data.status` 明确给出状态

例如：

```json
{
  "status": "running"
}
```

```json
{
  "status": "succeeded",
  "image_url": "https://...",
  "width": 1024,
  "height": 1024,
  "provider_task_id": "xxx",
  "api_provider": "aliyun"
}
```

```json
{
  "status": "failed",
  "error_message": "provider task failed"
}
```

## 7. 与 scanner 的配合方式

scanner 拿到 `poll_once` 的结果后，统一做：

1. 若 `status=running/pending`
   - 更新 `poll_attempts`
   - 更新 `next_poll_at`
   - 发 `await_poll_miss`

2. 若 `status=succeeded`
   - 合成 provider payload 或直接归一化结果
   - `CompleteAwaitNode`

3. 若 `status=failed`
   - `CompleteAwaitNode` with error

这意味着：

1. poll tool 不再控制完成链
2. scanner 继续作为 runtime 调度层
3. 完成入口仍然统一收敛到 `CompleteAwaitNode`

## 8. 与 legacy wait tool 的兼容策略

过渡期允许两种类型并存：

### 8.1 兼容型 legacy wait tool

例如：

1. `video_generate_wait`
2. `goods_shot_i2v_wait`
3. `aliyun_image_generate_wait`
4. `volcengine_image_generate_wait`
5. `aliyun_image_to_image_wait`

这类 tool 当前仍可被 scanner 调用，但仅限兼容模式：

1. 注入单次查询参数
2. 不再作为 workflow 主路径

### 8.2 标准型 poll_once tool

后续 provider 应优先补：

1. `*_poll_once`
2. scanner 优先依赖它
3. legacy wait tool 逐步只剩兼容层或被删除

### 8.3 运行时优先策略

为避免历史 binding / 历史 DSL 配置立即失效，运行时当前增加了一层“优先解析”策略：

1. scanner
2. replay 的 `poll_and_replay`
3. EventBridge 成功事件后的补查

在读取 `AwaitBinding.FallbackPollTool` 时，会先尝试把旧 wait tool 名称映射到对应的 `poll_once`。

例如：

1. `aliyun_image_generate_wait -> aliyun_image_generate_poll_once`
2. `aliyun_image_to_image_wait -> aliyun_image_to_image_poll_once`
3. `volcengine_image_generate_wait -> volcengine_image_generate_poll_once`
4. `video_generate_wait -> video_generate_poll_once`
5. `goods_shot_i2v_wait -> goods_shot_i2v_poll_once`

这意味着：

1. 新 binding 会优先直接写 `poll_once`
2. 旧 binding 即使仍保存 legacy wait tool 名称，运行时也会优先命中新 `poll_once`
3. `poll_once` 推广不要求所有历史配置一次性重写

所以当前的真实策略不是“只在 DSL 层切换”，而是：

**DSL 优先写 `poll_once`，运行时也优先解析到 `poll_once`。**

## 9. 推荐迁移顺序

建议按业务优先级收口：

1. `aliyun_image_generate_wait -> aliyun_image_generate_poll_once`
2. `volcengine_image_generate_wait -> volcengine_image_generate_poll_once`
3. `aliyun_image_to_image_wait -> aliyun_image_to_image_poll_once`
4. `video_generate_wait -> video_generate_poll_once`
5. `goods_shot_i2v_wait -> goods_shot_i2v_poll_once`

`kling_motion_wait`、`volc_motion_wait` 可随后按相同模式处理。

## 10. 开发规范建议

从现在开始，新增 provider 接入建议遵循：

1. 任务创建必须使用 `submit`
2. 等待恢复必须使用 `await`
3. fallback 查询必须使用 `poll_once`
4. 禁止新增“内部 while-loop + sleep”的主路径 wait tool

## 11. 成功标准

这份规范落地后的成功标志：

1. 新接入 provider 不再新增 legacy wait tool 主路径
2. fallback poll 的语义从“兼容复用 wait tool”收口为“标准 poll_once tool”
3. scanner / replay / eventbridge 等补查逻辑都依赖统一的单次查询约定
4. `await` runtime 的长期模型更稳定、更容易理解
