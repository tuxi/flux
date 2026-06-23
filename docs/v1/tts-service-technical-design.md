# TTS 服务化技术设计

日期：2026-04-29

状态：Final

关联文档：

- [TTS 服务化改造 PRD](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-service-prd.md)
- [TTS 服务化实施清单](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-service-task-breakdown.md)

## 1. 当前代码基线分析

当前仓库已经具备 TTS provider 抽象：

- [provider.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/pkg/tts/provider.go:5)
- [types.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/pkg/tts/types.go:40)
- [service.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/pkg/tts/service.go:17)
- [edge_provider.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/pkg/tts/providers/edge_provider.go:18)

当前服务启动时仍会创建本地 `EdgeProvider`：

- [server.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/server/server.go:102)

该 provider 内部仍执行 CLI：

```go
cmd := exec.CommandContext(ctx, p.commandPath, args...)
```

因此，仓库不需要从零设计 TTS 抽象，本次服务化改造应复用现有 `tts.Provider / tts.AsyncProvider / tts.Service`，重点新增：

1. 独立 Gin TTS Service。
2. 独立 Python Worker。
3. 主系统 HTTP Edge Provider。
4. 配置项从 `edge.command` 迁移到 `edge.service_url`。

## 2. 总体架构

```text
ai-engine main process
  |
  | tts.Provider HTTP client
  v
Gin TTS Service
  |
  | in-memory queue/state for MVP
  v
worker dispatcher
  |
  | HTTP
  v
Python FastAPI edge-tts Worker
  |
  v
local audio file / OSS URL
```

职责边界：

| 模块 | 职责 | 不负责 |
| --- | --- | --- |
| AI Engine | 工作流编排、provider 路由、成本与 trace | 执行 edge-tts |
| HTTP Edge Provider | 把现有 provider 请求转成 HTTP 任务 | 管理 Worker 环境 |
| Gin TTS Service | API、任务、状态、调度、重试 | 调用 edge-tts CLI |
| Python Worker | 调用 edge-tts 生成音频 | 业务 task 路由 |
| 存储层 | 保存音频文件或 URL | 决策 TTS 策略 |

## 3. 模块设计

## 3.1 主系统 HTTP Edge Provider

新增 provider：

```text
ai-engine/pkg/tts/providers/edge_service_provider.go
```

实现：

```go
type EdgeServiceProvider struct {
    baseURL string
    client  *http.Client
}
```

实现接口：

```go
func (p *EdgeServiceProvider) Name() tts.ProviderName
func (p *EdgeServiceProvider) Synthesize(ctx context.Context, req tts.SynthesizeRequest) (*tts.SynthesizeResult, error)
func (p *EdgeServiceProvider) SubmitSynthesize(ctx context.Context, req tts.SubmitSynthesizeRequest) (*tts.SubmitSynthesizeResult, error)
func (p *EdgeServiceProvider) WaitSynthesize(ctx context.Context, req tts.WaitSynthesizeRequest) (*tts.SynthesizeResult, error)
```

短期兼容策略：

- `Synthesize` 内部可以 `SubmitSynthesize + WaitSynthesize`。
- `SubmitSynthesize` 调用 Gin TTS Service `POST /api/v1/tts`。
- `WaitSynthesize` 轮询 `GET /api/v1/tts/result?id=...`。

这样现有 `tts.Service` 无需大改即可复用。

## 3.2 Gin TTS Service

新增目录：

```text
services/tts-service/
  cmd/server/main.go
  internal/api/
  internal/task/
  internal/worker/
  internal/storage/
  config.example.yaml
```

MVP 使用内存状态存储：

```go
type TaskStatus string

const (
    TaskStatusQueued     TaskStatus = "queued"
    TaskStatusProcessing TaskStatus = "processing"
    TaskStatusDone       TaskStatus = "done"
    TaskStatusFailed     TaskStatus = "failed"
    TaskStatusCanceled   TaskStatus = "canceled"
)

type Task struct {
    ID             string
    Text           string
    Voice          string
    Rate           string
    Volume         string
    Pitch          string
    Format         string
    Status         TaskStatus
    URL            string
    AudioLocalPath string
    DurationSec    float64
    ErrorCode      string
    ErrorMessage   string
    RetryCount     int
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

MVP 调度模型：

```text
POST /api/v1/tts
  -> validate request
  -> create task
  -> store task as processing
  -> run goroutine to call worker
  -> return task_id immediately
```

生产演进：

- 将内存 map 替换为 Redis hash 或 PostgreSQL。
- 将 goroutine 替换为 Redis Stream / queue。
- 支持多实例 Gin TTS Service 水平扩展。

## 3.3 Python FastAPI Worker

新增目录：

```text
services/tts-worker/
  app/main.py
  requirements.txt
  README.md
```

API：

```http
POST /api/v1/synthesize
GET /healthz
```

请求：

```json
{
  "task_id": "tts_01HWN6T3Q2S8Q7Z6N3E0Z3J6C9",
  "text": "你好世界",
  "voice": "zh-CN-XiaoxiaoNeural",
  "rate": "+0%",
  "volume": "+0%",
  "pitch": "+0Hz",
  "format": "mp3"
}
```

响应：

```json
{
  "task_id": "tts_01HWN6T3Q2S8Q7Z6N3E0Z3J6C9",
  "url": "",
  "audio_local_path": "/data/tts/audio/tts_01HWN6T3Q2S8Q7Z6N3E0Z3J6C9.mp3",
  "duration_sec": 1.82
}
```

Worker 内部执行：

```text
edge_tts.Communicate(text, voice, rate, volume, pitch).save(output_path)
```

注意：

- Python Worker 允许使用 venv。
- `edge-tts` 安装和版本锁定只在 Worker 镜像内处理。
- 主系统镜像不安装 Python TTS 依赖。

## 4. API 契约

## 4.1 Gin TTS Service API

### 创建任务

```http
POST /api/v1/tts
```

请求字段：

| 字段 | 类型 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `text` | string | 是 | 无 | 合成文本 |
| `voice` | string | 否 | `zh-CN-XiaoxiaoNeural` | 音色 |
| `rate` | string | 否 | `+0%` | 语速 |
| `volume` | string | 否 | `+0%` | 音量 |
| `pitch` | string | 否 | `+0Hz` | 音调 |
| `format` | string | 否 | `mp3` | 音频格式 |

校验规则：

- `text` trim 后不能为空。
- `text` 长度 MVP 限制为 5000 字符。
- `voice` 不能为空时必须是普通字符串，不允许包含路径分隔符。
- `format` MVP 只允许 `mp3`。
- `rate / volume / pitch` 保持 edge-tts 兼容格式，非法时由 Worker 返回结构化错误。

### 查询结果

```http
GET /api/v1/tts/result?id={task_id}
```

响应字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `task_id` | string | 任务 ID |
| `status` | string | 任务状态 |
| `url` | string | OSS/CDN URL，可为空 |
| `audio_local_path` | string | 本地文件路径 |
| `duration_sec` | number | 音频时长 |
| `error_code` | string | 失败错误码 |
| `error_message` | string | 失败原因 |

## 4.2 Worker API

Worker API 不对主系统开放，只对 Gin TTS Service 开放。

```http
POST /api/v1/synthesize
```

Worker 成功返回 200，失败返回 4xx/5xx 加结构化错误：

```json
{
  "error_code": "edge_tts_failed",
  "error_message": "edge-tts failed"
}
```

## 5. 配置设计

主系统配置扩展：

```yaml
tts:
  enabled: true
  edge:
    service_url: "http://tts-service:8088"
    submit_timeout_ms: 1000
    wait_timeout_ms: 90000
    poll_interval_ms: 1000
```

兼容策略：

- 若 `edge.service_url` 非空，主系统使用 `EdgeServiceProvider`。
- 若 `edge.service_url` 为空且 `edge.command` 非空，开发环境可临时使用旧 CLI provider。
- 生产配置必须使用 `edge.service_url`。

Gin TTS Service 配置：

```yaml
server:
  port: 8088

worker:
  base_url: "http://tts-worker:8090"
  timeout_ms: 120000
  retry_times: 1

storage:
  type: "local"
  local_dir: "/data/tts/audio"
  public_base_url: ""
```

Python Worker 配置：

```yaml
server:
  port: 8090

storage:
  local_dir: "/data/tts/audio"
```

## 6. 并发与重试

MVP：

- Gin TTS Service 使用 buffered channel 限制并发。
- 默认最大并发为 4。
- 每个任务失败后重试 1 次。
- 重试只覆盖 Worker 调用失败或 edge-tts 执行失败。

生产：

- 使用 Redis Stream 或 RabbitMQ 做任务队列。
- 支持多 Worker 消费。
- 引入任务租约与超时回收。

## 7. 文件存储

MVP：

```text
/data/tts/audio/{task_id}.mp3
```

返回：

- `audio_local_path` 必填。
- `url` 在本地模式可为空。

生产：

- Worker 或 Gin TTS Service 上传 OSS。
- 任务结果保存 `url`。
- 本地文件作为临时文件，按 TTL 清理。

## 8. 可观测性

Gin TTS Service 日志字段：

- `task_id`
- `voice`
- `chars`
- `status`
- `worker_latency_ms`
- `retry_count`
- `error_code`
- `error_message`

主系统 provider 日志字段：

- `task_id`
- `provider=edge`
- `protocol=async`
- `submission_status`
- `fallback_chain`

Worker 日志字段：

- `task_id`
- `voice`
- `chars`
- `output_path`
- `latency_ms`
- `edge_tts_error`

## 9. 迁移策略

### 阶段 1：文档与契约冻结

完成 PRD、技术设计、实施清单。

### 阶段 2：新增独立服务

实现 Gin TTS Service 与 Python Worker，主系统暂不切流。

### 阶段 3：主系统接入 HTTP Provider

新增 `EdgeServiceProvider`，通过配置切换。

### 阶段 4：生产环境禁用 CLI

生产配置移除 `edge.command`，主系统镜像不安装 `edge-tts`。

### 阶段 5：状态持久化与 OSS

引入 Redis/PostgreSQL 与 OSS。

## 10. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| Gin TTS Service 重启后内存任务丢失 | MVP 查询不到旧任务 | MVP 接受，生产引入 Redis/PostgreSQL |
| Worker 生成慢 | 查询等待时间变长 | 异步状态查询，不阻塞提交 |
| 本地文件跨容器不可见 | 主系统无法读取文件 | 生产使用共享卷或 OSS |
| edge-tts 上游不稳定 | 任务失败 | 重试、失败状态、后续接入付费 provider |
| 多实例状态不一致 | 查询错实例无任务 | 生产使用共享状态存储 |

## 11. 技术结论

本次技术方案确定为：

1. 主系统保留现有 `tts.Provider` 抽象。
2. 新增 HTTP Edge Provider 替代 CLI Edge Provider。
3. Gin TTS Service 只做任务控制和调度。
4. Python Worker 独立管理 `edge-tts` 环境。
5. MVP 用本地文件和内存状态，生产演进到 OSS 与 Redis/PostgreSQL。

该方案改动边界清晰，可以在不大改现有工作流 DSL 的前提下，先消除生产环境中最危险的 CLI/Python 耦合。
