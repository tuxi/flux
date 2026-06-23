# TTS 服务化改造 PRD

日期：2026-04-29

状态：Final

关联文档：

- [TTS 服务化技术设计](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-service-technical-design.md)
- [TTS 服务化实施清单](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-service-task-breakdown.md)
- [AI Engine TTS Provider 技术设计文档](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-technical-design.md)

## 1. 背景

当前 AI Engine 在 Ubuntu 云服务器中通过 Go 进程直接执行 `edge-tts` CLI 生成语音。该方案在本地 macOS 可用，但在云端 Linux 环境出现：

```text
exec: "edge-tts": executable file not found in $PATH
```

这不是单纯的 PATH 配置问题，而是 TTS 能力与本地 Python/CLI 环境强耦合导致的架构问题。

当前 `edge-tts` 依赖 Python 虚拟环境，Linux 发行版又可能启用 PEP 668，限制系统级 pip 安装。Go worker 进程无法稳定继承 Python venv，因此只要继续由主系统直接执行 CLI，环境漂移、部署差异、权限差异都会反复影响 TTS 生成。

## 2. 问题定义

当前架构存在以下核心问题：

1. 主系统直接依赖 `edge-tts` CLI。
2. CLI 安装位置依赖 Python venv 和服务器 PATH。
3. Go worker 对 Python 运行环境不可感知。
4. TTS 执行与工作流引擎部署生命周期绑定。
5. 后续替换 Azure、OpenAI、ElevenLabs、火山等 TTS 引擎时，仍需要修改主系统执行逻辑。

因此，本次问题本质是：

```text
TTS 能力未服务化，导致运行环境不可控。
```

## 3. 产品目标

将 TTS 能力升级为独立服务能力：

```text
Go Workflow Engine
        |
        | HTTP
        v
Gin TTS Service
        |
        | HTTP
        v
Python edge-tts Worker
        |
        v
Audio File / OSS URL
```

目标包括：

1. 主系统彻底移除对 `edge-tts` CLI 的直接依赖。
2. 消除 Python venv、PEP 668、PATH 对主系统的影响。
3. TTS 提交接口响应时间小于 100ms。
4. TTS 生成异步执行，可查询任务状态。
5. 支持并发任务与失败重试。
6. 保留未来替换 TTS 引擎的扩展口。

## 4. 非目标

本期不做以下事项：

1. 不改造全部视频工作流 DSL。
2. 不引入复杂多租户配额系统。
3. 不强制接入 OSS，MVP 可以先落本地文件。
4. 不在 Python Worker 内实现业务路由策略。
5. 不把 Python Worker 直接暴露给主业务系统。
6. 不在本期完成所有付费 TTS provider 接入。

## 5. 用户与使用场景

### 5.1 AI Engine 工作流

工作流中的 `tts_submit`、`tts_wait`、`tts_speech_generate`、`tts_generate_segments` 最终应通过 TTS provider 抽象访问 Gin TTS Service，而不是执行本地 CLI。

### 5.2 运维部署

运维只需要保证：

- 主系统可以访问 Gin TTS Service。
- Gin TTS Service 可以访问 Python Worker。
- Python Worker 自己管理 `edge-tts` 运行环境。

主系统不再安装 `edge-tts`。

### 5.3 后续 provider 替换

未来如果替换为 OpenAI、Azure、ElevenLabs 或火山 TTS，可以新增 Worker 或 provider adapter，不需要修改工作流引擎执行模型。

## 6. 功能需求

## 6.1 Gin TTS Service

Gin TTS Service 是控制层，负责：

1. 提供 HTTP API。
2. 校验 `text / voice / rate / volume / pitch / format`。
3. 生成 `task_id`。
4. 管理任务状态。
5. 调度 Python Worker。
6. 查询任务结果。
7. 记录失败原因与重试次数。
8. 返回本地文件路径或 OSS URL。

Gin TTS Service 不负责：

1. 不直接执行 `edge-tts`。
2. 不依赖 Python venv。
3. 不承载具体 TTS 引擎 SDK。

## 6.2 Python edge-tts Worker

Python Worker 是执行层，负责：

1. 调用 `edge-tts`。
2. 生成音频文件。
3. 支持 voice、rate、volume、pitch、format。
4. 写入本地文件或上传 OSS。
5. 向 Gin TTS Service 返回文件路径或 URL。
6. 暴露健康检查接口。

Python Worker 不负责：

1. 不生成业务 task_id。
2. 不保存主系统工作流上下文。
3. 不处理业务 provider 策略。

## 6.3 任务状态

任务状态固定为：

| 状态 | 含义 |
| --- | --- |
| `queued` | 已创建，等待调度 |
| `processing` | Worker 正在生成 |
| `done` | 生成完成 |
| `failed` | 生成失败 |
| `canceled` | 已取消，MVP 可预留 |

## 6.4 API

### 创建 TTS 任务

```http
POST /api/v1/tts
Content-Type: application/json
```

请求：

```json
{
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
  "status": "processing"
}
```

### 查询 TTS 结果

```http
GET /api/v1/tts/result?id=tts_01HWN6T3Q2S8Q7Z6N3E0Z3J6C9
```

处理中响应：

```json
{
  "task_id": "tts_01HWN6T3Q2S8Q7Z6N3E0Z3J6C9",
  "status": "processing"
}
```

完成响应：

```json
{
  "task_id": "tts_01HWN6T3Q2S8Q7Z6N3E0Z3J6C9",
  "status": "done",
  "url": "https://cdn.example.com/audio/tts_01HWN6T3Q2S8Q7Z6N3E0Z3J6C9.mp3",
  "audio_local_path": "/data/tts/audio/tts_01HWN6T3Q2S8Q7Z6N3E0Z3J6C9.mp3",
  "duration_sec": 1.82
}
```

失败响应：

```json
{
  "task_id": "tts_01HWN6T3Q2S8Q7Z6N3E0Z3J6C9",
  "status": "failed",
  "error_code": "worker_failed",
  "error_message": "edge-tts failed"
}
```

### 健康检查

```http
GET /healthz
```

响应：

```json
{
  "status": "ok"
}
```

## 7. 非功能需求

1. 创建任务接口 P95 响应小于 100ms。
2. 支持并发提交。
3. 单任务失败后至少重试 1 次，重试次数可配置。
4. 所有任务必须可查询最终状态。
5. Worker 不可用时，创建任务应明确返回失败或进入 failed 状态。
6. 任务状态更新必须具备线程安全。
7. 日志必须包含 `task_id / voice / chars / status / latency / error_code`。

## 8. MVP 范围

MVP 采用方案 A：

```text
Gin TTS Service -> HTTP -> Python FastAPI Worker -> edge-tts
```

MVP 必须包含：

1. Gin TTS Service。
2. Python FastAPI Worker。
3. 本地文件存储。
4. `POST /api/v1/tts`。
5. `GET /api/v1/tts/result`。
6. `GET /healthz`。
7. 主系统新增 HTTP TTS provider，用于替代直接 CLI edge provider。
8. 配置项支持切换 `edge.service_url`。

MVP 可暂缓：

1. Redis 队列。
2. PostgreSQL 持久化。
3. OSS 上传。
4. 多 Worker 负载均衡。
5. 任务取消。

## 9. 验收标准

1. 主系统服务器不安装 `edge-tts` 时，TTS 工作流仍可提交任务。
2. `edge-tts` 只存在于 Python Worker 环境。
3. Gin TTS Service 创建任务响应小于 100ms。
4. 查询接口可返回 `processing / done / failed`。
5. 完成任务可返回音频路径或 URL。
6. 主系统中的 TTS provider 不再调用 `exec.Command("edge-tts")`。
7. 原商品视频 TTS 链路可通过 HTTP provider 获取音频文件。
8. Worker 停止时，任务失败原因可查询。

## 10. 结论

本次改造的核心不是修复 `edge-tts` 的安装方式，而是将 TTS 能力从本地 CLI 调用升级为标准服务能力。

最终架构确定为：

```text
主系统只依赖 HTTP TTS provider。
Gin TTS Service 管理任务。
Python Worker 独立执行 edge-tts。
```

该方案可以消除本地环境耦合，并为后续替换 TTS 引擎、接入 OSS、扩展并发和引入任务持久化保留清晰边界。
