# Await / AsyncExecution Migration Inventory

## 1. 目的

这份清单用于落实 `engine-await-runtime-v1` 中的 `P1-4 收缩外部等待型 AsyncExecution 使用范围`。

目标不是一次性移除所有 `tool.AsyncExecution`，而是把当前仓库里的 async tool 明确分成三类：

1. 应继续保留的内部异步执行型
2. 应迁移到 `submit + await` 的外部等待型
3. 已迁移到 `await`，但暂时保留为 fallback poll 的 legacy wait tool

## 2. 当前结论

截至本次盘点，仓库里的 `tool.AsyncExecution` 现状如下：

1. 真正合理保留的内部异步执行型非常少，当前明确的一条是 `merge_video`
2. 大多数 `wait tool` 都属于“外部结果等待型”，语义上更适合 `await`
3. `motion_control` 已完成试点迁移，证明这条路线可行
4. 视频、goods、图片主路径 workflow 已完成迁移，当前进入 legacy wait tool 收口阶段
5. `poll_once` 规范已落地第一批工具，fallback poll 正在从兼容调用 legacy wait tool 逐步切换到单次查询型 tool

## 3. Async 工具盘点

### 3.1 内部异步执行型

这些工具的异步语义仍然成立，不建议迁移为 `await`：

| Tool | 文件 | 当前定位 | 结论 |
| --- | --- | --- | --- |
| `merge_video` | `ai-engine/tool/builtin/merge_video.go` | 本地长耗时 ffmpeg 合并任务 | 保留 `AsyncExecution` |

说明：

- 这类工具不是“等外部世界给结果”，而是引擎自己的 worker 真正去执行计算
- 它们符合 `AsyncExecution = internal background work` 的语义

### 3.2 外部等待型，主路径迁移已完成

这些工具原本属于“submit 之后阻塞轮询 provider 结果”的主路径 wait tool，当前主路径均已迁移为：

- `submit(tool sync) -> await(node await)`

当前状态如下：

| Tool | 文件 | 当前引用 workflow | 当前状态 | 结论 |
| --- | --- | --- | --- | --- |
| `video_generate_wait` | `ai-engine/workflows/videos/video_generate_wait.go` | `text_to_video`、`image_to_video` | 主路径已迁移 | legacy wait tool 仅保留为兼容 fallback poll |
| `goods_shot_i2v_wait` | `ai-engine/workflows/goods/goods_shot_i2v_wait.go` | `goods_shot_i2v_generate` | 主路径已迁移 | legacy wait tool 仅保留为兼容 fallback poll |
| `aliyun_image_generate_wait` | `ai-engine/workflows/images/aliyun_image_generate_wait.go` | `text_to_image` | 主路径已迁移 | legacy wait tool 仅保留为兼容 fallback poll |
| `volcengine_image_generate_wait` | `ai-engine/workflows/images/volcengine_image_generate_wait.go` | `text_to_image` | 主路径已迁移 | legacy wait tool 仅保留为兼容 fallback poll |
| `aliyun_image_to_image_wait` | `ai-engine/workflows/images/aliyun_image_to_image_wait.go` | `style_transfer`、`image_to_image` | 主路径已迁移 | legacy wait tool 仅保留为兼容 fallback poll |

说明：

- 这些工具都符合“外部 provider task -> task_id -> 查询结果”的模式
- 它们已经不再作为长期主路径
- 当前保留它们，主要是为了兼容 fallback poll / replay / eventbridge 补查
- 下一步重点不再是“继续迁移 workflow”，而是“收口 legacy wait tool 语义”

### 3.3 已迁移到 await，但保留为 fallback poll

这类工具已经不再作为业务 workflow 的主路径等待点，而是作为 `fallback_poll.tool` 被调用：

| Tool | 文件 | 当前使用方式 | 结论 |
| --- | --- | --- | --- |
| `video_generate_wait` | `ai-engine/workflows/videos/video_generate_wait.go` | `text_to_video` / `image_to_video` 的 fallback poll tool | 已迁移到 `await`，保留为 fallback poll |
| `goods_shot_i2v_wait` | `ai-engine/workflows/goods/goods_shot_i2v_wait.go` | `goods_shot_i2v_generate` 的 fallback poll tool | 已迁移到 `await`，保留为 fallback poll |
| `aliyun_image_generate_wait` | `ai-engine/workflows/images/aliyun_image_generate_wait.go` | `text_to_image` 的 fallback poll tool / EventBridge 补查 tool | 已迁移到 `await`，保留为 fallback poll |
| `volcengine_image_generate_wait` | `ai-engine/workflows/images/volcengine_image_generate_wait.go` | `text_to_image` 的 fallback poll tool | 已迁移到 `await`，保留为 fallback poll |
| `aliyun_image_to_image_wait` | `ai-engine/workflows/images/aliyun_image_to_image_wait.go` | `style_transfer` / `image_to_image` 的 fallback poll tool | 已迁移到 `await`，保留为 fallback poll |
| `kling_motion_wait` | `ai-engine/workflows/motion_control/kling_motion_wait.go` | `image_to_video_with_motion` 的 fallback poll tool | 保留，但不再作为主路径 |
| `volc_motion_wait` | `ai-engine/workflows/motion_control/volc_motion_wait.go` | `image_to_video_with_motion` 的 fallback poll tool | 保留，但不再作为主路径 |

第一批 `poll_once` 已落地并开始替换 fallback 配置：

| Poll Once Tool | 当前接入位置 | 状态 |
| --- | --- | --- |
| `aliyun_image_generate_poll_once` | `text_to_image` fallback poll | 已接入 |
| `aliyun_image_to_image_poll_once` | `image_to_image` / `style_transfer` fallback poll | 已接入 |
| `volcengine_image_generate_poll_once` | `text_to_image` fallback poll | 已接入 |
| `video_generate_poll_once` | `text_to_video` / `image_to_video` fallback poll | 已接入 |
| `goods_shot_i2v_poll_once` | `goods_shot_i2v_generate` fallback poll | 已接入 |

说明：

- 这两条链路已经迁到了 `await`
- 当前 scanner 已开始优先依赖 `poll_once`，legacy wait tool 主要作为兼容层保留
- 运行时已经增加“优先解析”策略：
  - scanner
  - replay
  - EventBridge
  会先尝试将旧 wait tool 名称映射到对应的 `poll_once`
- 对尚未切换的场景，poll worker 仍可通过注入：
  - `max_retry=1`
  - `poll_interval_ms=0`
  将 legacy wait tool 降级成“单次查询”
- 兼容策略仍然保留，但已经不再是唯一实现路径

长期建议：

1. 将 legacy wait tool 拆成：
   - `provider_submit`
   - `provider_poll_once`
2. `await` 的 fallback poll 只依赖单次查询型 tool
3. 删除“节点内部自己 while-loop 等结果”的主路径语义

### 3.4 占位型 wait tool

这些工具目前只是 DSL 占位和 provider 占位实现，不属于稳定生产主路径，但它们的语义也应遵循新规范：

| Tool | 当前状态 | 说明 |
| --- | --- | --- |
| `openai_image_generate_wait` | 占位 | provider 尚未实现 |
| `kling_image_generate_wait` | 占位 | provider 尚未实现 |
| `openai_image_to_image_wait` | 占位 | provider 尚未实现 |
| `kling_image_to_image_wait` | 占位 | provider 尚未实现 |
| `volcengine_image_to_image_wait` | 占位 | provider 尚未实现 |

结论：

- 这些 provider 未来真正实现时，不应再新增“主路径 wait tool”
- 应直接按 `submit + await` 方案接入

## 4. 迁移优先级建议

### 第一优先级

1. 将剩余 fallback 场景继续切换到 `poll_once`
2. 明确 scanner / replay / EventBridge 统一优先依赖单次查询型 tool

原因：

- 当前主路径迁移已经完成
- `poll_once` 第一批已经落地
- 剩余问题主要是 legacy wait tool 的最终收口和统一使用约束
- 运行时优先策略已经落地，后续重点转为减少对旧名字兼容层的依赖

### 第二优先级

1. 将 legacy wait tool 逐步替换为 `*_poll_once`
2. 未来新增 provider 直接按 `submit + await + poll_once` 接入

原因：

- 这能避免继续扩散历史命名和历史语义
- 新 provider 可以直接落到长期模型

## 5. 开发规范

从现在开始，新增能力接入建议遵循以下规则：

### 5.0 submit 与 await 必须拆分

对于第三方异步任务接入，`submit` 和 `await` 必须拆成两个节点，不能混成一个节点。

原因：

1. `submit` 会产生真实外部副作用
2. `submit` 失败后的重试，可能再次调用第三方创建任务
3. 重复提交可能导致重复计费、重复生成、重复外部 side effect
4. `await` 的重试语义应该只是“继续等待 / 继续恢复”，而不是“重新创建任务”

强约束：

1. 调用第三方“创建任务 / 生成任务 / 下单任务”的动作，必须建模为独立的 `submit` 节点
2. 等待 webhook / signal / poll 结果的动作，必须建模为独立的 `await` 节点
3. 不允许把“submit + wait + retry”揉成一个可整体重试的单节点

### 5.1 何时使用 await

满足以下任一条件时，优先使用 `await`：

1. 需要等待 provider callback / webhook
2. 需要等待用户输入、审批、外部 signal
3. 需要等待外部系统异步结果
4. 本质上是“等待某个外部条件成立”

### 5.2 何时保留 AsyncExecution

只有以下场景建议继续使用 `tool.AsyncExecution`：

1. 引擎 worker 真正执行本地/内部长耗时任务
2. 不依赖外部回调或外部系统相关性唤醒
3. 任务结果由内部执行过程自然产出，而不是由外部事件补回

### 5.3 不再推荐的模式

不再推荐新增：

1. `submit(tool sync) -> wait(tool async)` 作为主路径
2. 节点内部高频轮询 provider 直到成功/失败
3. 通过 wait tool 承担 webhook、signal、超时、补偿轮询等全部职责
4. 将第三方任务创建与外部结果等待混成一个可整体重试的节点

### 5.4 fallback poll 的建议

当前允许 legacy wait tool 继续作为 fallback poll 使用，但仅作为过渡策略。

建议后续新接入统一采用单次查询型 poll tool：

1. tool 只做一次 provider 状态查询
2. 不在 tool 内部 sleep / retry / while-loop
3. 重试节奏由 `AwaitBinding.next_poll_at` 和 scanner 管理

## 6. 下一步实施建议

建议按下面顺序继续推进：

1. 继续将剩余 fallback 场景切换到 `poll_once`
2. 逐步让 replay / EventBridge 的补查也优先复用 `poll_once`
3. 最后统一弱化 legacy wait tool 的兼容层角色

## 7. 当前状态结论

`P1-4` 目前已经从“盘点与分类”阶段进入“legacy wait tool 收口”阶段。

更准确地说：

1. `await` 模型已经证明可行
2. 迁移标准已经清晰
3. `video_generate_wait`、`goods_shot_i2v_wait` 已完成迁移
4. `aliyun/volcengine image wait`、`aliyun_image_to_image_wait` 也已完成迁移
5. 当前已完成 `poll_once` 第一批落地
6. 下一步重点是继续推广 `poll_once`，并最终弱化 legacy wait tool 的兼容层角色
