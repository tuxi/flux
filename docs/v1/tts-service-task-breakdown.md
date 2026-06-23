# TTS 服务化实施清单

日期：2026-04-29

状态：Final

关联文档：

- [TTS 服务化改造 PRD](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-service-prd.md)
- [TTS 服务化技术设计](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-service-technical-design.md)

## 1. 目标

本清单用于指导 TTS 服务化落地。核心目标是把主系统从 `edge-tts` CLI 依赖中解耦出来，形成：

```text
AI Engine -> Gin TTS Service -> Python edge-tts Worker
```

## 2. P0 MVP

## P0-1 冻结 API 契约

任务目标：

- 确认 Gin TTS Service 与 Python Worker 的 HTTP API。

核心改动：

- 确认 `POST /api/v1/tts`。
- 确认 `GET /api/v1/tts/result`。
- 确认 Worker `POST /api/v1/synthesize`。
- 确认状态枚举和错误结构。

涉及模块：

- `ai-engine/docs/tts-service-prd.md`
- `ai-engine/docs/tts-service-technical-design.md`

验收标准：

- 请求/响应字段明确。
- 错误码格式明确。
- 主系统、Gin TTS Service、Worker 职责边界明确。

## P0-2 实现 Python edge-tts Worker

任务目标：

- 将 `edge-tts` 执行环境从主系统迁移到 Worker。

核心改动：

- 新增 `services/tts-worker/`。
- 使用 FastAPI 暴露 `POST /api/v1/synthesize`。
- 使用 `edge_tts.Communicate(...).save(...)` 生成音频。
- 输出本地文件路径。
- 增加 `GET /healthz`。

涉及模块：

- `services/tts-worker/app/main.py`
- `services/tts-worker/requirements.txt`
- `services/tts-worker/README.md`

验收标准：

- Worker 独立启动后可生成 mp3。
- Worker 可以通过 venv 安装 edge-tts。
- 主系统无需安装 Python 依赖。

## P0-3 实现 Gin TTS Service

任务目标：

- 提供 TTS 任务控制层。

核心改动：

- 新增 `services/tts-service/`。
- 实现 `POST /api/v1/tts`。
- 实现 `GET /api/v1/tts/result`。
- 实现内存任务状态存储。
- 实现异步 goroutine 调度 Worker。
- 实现基础重试。

涉及模块：

- `services/tts-service/cmd/server/main.go`
- `services/tts-service/internal/api/`
- `services/tts-service/internal/task/`
- `services/tts-service/internal/worker/`

验收标准：

- 创建任务接口 100ms 内返回。
- 查询接口可看到 `processing / done / failed`。
- Worker 失败时状态变为 `failed`，并保留错误信息。

## P0-4 新增主系统 HTTP Edge Provider

任务目标：

- 用 HTTP provider 替代主系统直接执行 `edge-tts` CLI。

核心改动：

- 新增 `EdgeServiceProvider`。
- `SubmitSynthesize` 调用 Gin TTS Service 创建任务。
- `WaitSynthesize` 轮询任务结果。
- `Synthesize` 兼容同步调用路径。

涉及模块：

- `ai-engine/pkg/tts/providers/edge_service_provider.go`
- `ai-engine/pkg/tts/providers/edge_provider.go`
- `ai-engine/server/server.go`

验收标准：

- 配置 `edge.service_url` 后，主系统不再执行 `edge-tts` CLI。
- 原 TTS 工具可通过现有 `tts.Service` 拿到音频文件。
- `provider=edge`、`protocol=async` 可在结果中体现。

## P0-5 扩展配置

任务目标：

- 支持生产环境通过配置切换到 TTS Service。

核心改动：

- 扩展 `config.TTSEdge`。
- 更新 `config.example.yaml`。
- 保留 `command` 作为开发兼容项。

涉及模块：

- `config/config.go`
- `config/config.example.yaml`
- `ai-engine/server/server.go`

验收标准：

- `edge.service_url` 非空时使用 HTTP provider。
- `edge.service_url` 为空时可继续使用旧 CLI provider。
- 生产部署文档明确推荐 `service_url`。

## P0-6 本地联调

任务目标：

- 验证端到端链路。

核心步骤：

1. 启动 Python Worker。
2. 启动 Gin TTS Service。
3. 调用 `POST /api/v1/tts`。
4. 轮询 `GET /api/v1/tts/result`。
5. 主系统配置 `edge.service_url`。
6. 运行 TTS 工具或商品视频 TTS 节点。

验收标准：

- 主系统机器未安装 `edge-tts` 仍可生成音频。
- 音频文件存在且可被后续 ffprobe 读取时长。
- 失败任务可以查询错误。

## 3. P1 生产化

## P1-1 引入 Redis 状态与队列

任务目标：

- 支持 Gin TTS Service 多实例部署。

核心改动：

- 用 Redis hash 保存任务状态。
- 用 Redis stream/list 保存任务队列。
- Worker dispatcher 消费队列。

验收标准：

- Service 重启后任务状态不丢失。
- 多实例查询同一任务结果一致。

## P1-2 接入 OSS

任务目标：

- 生产环境返回稳定 URL。

核心改动：

- 支持上传 OSS/S3/COS。
- 保存 `url`。
- 本地文件按 TTL 清理。

验收标准：

- 查询结果返回可访问 URL。
- 主系统不依赖跨容器本地路径。

## P1-3 增强可观测性

任务目标：

- 支持线上排障和 SLA 分析。

核心改动：

- 结构化日志。
- 指标：提交数、成功数、失败数、耗时、重试数。
- 按 `voice / worker / error_code` 聚合。

验收标准：

- 可以定位单个 `task_id` 的完整链路。
- 可以统计 Worker 失败率和平均耗时。

## P1-4 Docker 与部署编排

任务目标：

- 提供标准部署方式。

核心改动：

- 为 `tts-service` 增加 Dockerfile。
- 为 `tts-worker` 增加 Dockerfile。
- 更新 compose 示例。
- 更新部署文档。

验收标准：

- 一条 compose 命令可启动 TTS service 和 worker。
- 主系统 compose 不再安装 `edge-tts`。

## 4. P2 扩展能力

## P2-1 多 Worker 负载均衡

任务目标：

- 支持高并发 TTS 生成。

核心改动：

- Worker 注册或静态列表。
- 失败摘除。
- 简单轮询或最少连接调度。

验收标准：

- 多 Worker 并行处理任务。
- 单 Worker 失败不影响全部任务。

## P2-2 接入更多 TTS 引擎

任务目标：

- 让服务层支持替换 TTS 引擎。

核心改动：

- Worker 增加 engine adapter。
- 支持 `engine=edge|openai|azure|elevenlabs|volcengine`。
- Gin TTS Service 透传 engine 字段或按策略选择。

验收标准：

- 新增引擎不需要修改主系统工作流。
- 不同引擎结果结构一致。

## P2-3 任务取消与超时回收

任务目标：

- 避免长时间卡住的任务占用资源。

核心改动：

- 增加 `POST /api/v1/tts/cancel`。
- 增加任务租约。
- 超时任务自动 failed。

验收标准：

- 可以取消未完成任务。
- 超时任务最终可查询到 failed。

## 5. 推荐落地顺序

1. P0-1 冻结 API 契约。
2. P0-2 实现 Python Worker。
3. P0-3 实现 Gin TTS Service。
4. P0-4 主系统接入 HTTP Edge Provider。
5. P0-5 扩展配置。
6. P0-6 本地联调。
7. P1-1 引入 Redis。
8. P1-2 接入 OSS。

## 6. 发布检查

上线前必须确认：

1. 主系统镜像不依赖 `edge-tts`。
2. 生产配置使用 `tts.edge.service_url`。
3. Worker 镜像内能执行 edge-tts。
4. Gin TTS Service 到 Worker 网络连通。
5. 音频文件目录或 OSS 权限正确。
6. 失败任务可查询错误原因。
7. 商品视频 TTS 链路至少跑通一个完整样例。
