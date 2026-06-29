# 🔥 Flux `plan_workflow` 功能需求：解锁视频/媒体剪辑场景

> 提交日期：2026-06-29 | 源自：CodeAgent 用户实际体验

---

## 📋 背景

当前 `plan_workflow` 在纯文本/代码场景表现优秀（并行文件读取、多源抓取、日志诊断等），DAG 编排模型天然适合 **视频剪辑 pipeline**（ffmpeg 滤镜图本身就是 DAG）。但在实际测试中发现三个瓶颈，补齐后可覆盖 **80% 的视频后期场景**（短视频转码、批量渲染、多格式导出）。

---

## 🎯 需求 1：可配置节点超时

### 现状
所有节点硬超时 **60 秒**，超时即 kill。

### 问题
视频渲染/转码天然长耗时。1 分钟 1080p 就需要 15-50s 不等，更长的素材直接没法跑。用户无法表达「这个节点允许跑 5 分钟」。

### 建议方案
在 goal 中支持自然语言声明超时，或 DAG 引擎自动推断：

```
# 方式 A：goal 里自然声明
"节点A需要 300 秒超时，节点B默认 60 秒"

# 方式 B：引擎根据命令特征自动推断
# 检测到 ffmpeg/渲染类命令 → 自动给 300s
```

### 优先级
🔴 **P0** — 不解决则所有长任务不可用。

---

## 🎯 需求 2：文件产物作为一等公民传递

### 现状
节点间只能通过 **stdout 文本** 传数据，无法传二进制文件。

### 问题
视频剪辑的中间产物是 `.mp4`/`.mov` 等二进制文件。当前只能变通：节点写磁盘 → stdout 传路径 → 下游读路径。这种方式丢失了 DAG 的优雅性，且没有自动清理机制。

### 建议方案
节点声明 `output_files`，下游 `input_files` 自动路由：

```yaml
# DAG 引擎自动生成类似结构
node_trim:
  command: "ffmpeg -i input.mp4 -ss 10 -t 30 clip.mp4"
  outputs: ["clip.mp4"]     # ← 新增：一等公民

node_watermark:
  depends_on: [node_trim]
  inputs: ["clip.mp4"]       # ← 自动从上游路由
  command: "ffmpeg -i clip.mp4 -i logo.png ... output.mp4"
```

### 优先级
🟡 **P1** — 当前能绕过（路径传参），但体验打折扣。

---

## 🎯 需求 3：硬件加速感知

### 现状
命令盲跑，不感知运行环境。

### 问题
Mac 上 ffmpeg 不加 `-hwaccel videotoolbox` 只能用 CPU 软编码，速度差 **3-10 倍**。用户需要在每个命令里手写加速参数，不写就慢。

### 建议方案
DAG 引擎在编译阶段注入环境信息，或提供能力标记：

```
# 方式 A：引擎自动注入环境变量
$FLUX_HWACCEL=videotoolbox    # Apple Silicon
$FLUX_HWACCEL=cuda            # NVIDIA GPU
$FLUX_HWACCEL=none            # 无 GPU

# 方式 B：系统能力声明 + 命令模板
system_info:
  gpu: "apple_silicon"
  hwaccel_flags: "-hwaccel videotoolbox -q:v 75"
```

### 优先级
🟢 **P2** — 锦上添花，但做视频的都会拍大腿。

---

## 📐 优先级汇总

| # | 需求 | 优先级 | 影响面 |
|---|------|--------|--------|
| 1 | 可配置超时 | 🔴 P0 | 所有长任务（视频/大模型推理/大数据处理） |
| 2 | 文件产物传递 | 🟡 P1 | 媒体/构建产物类 pipeline |
| 3 | 硬件加速感知 | 🟢 P2 | 视频渲染/ML 推理性能 |

---

## 🧪 期望效果（需求落地后）

```
用户输入：
  "把这个 3 分钟视频并行导出抖音竖版、YouTube横版、GIF动图三个版本，
   自动用 GPU 加速，5 分钟内完成"

Flux 自动生成 DAG：
  场景检测 ─→ 智能裁剪（竖版）──┐
  场景检测 ─→ 智能裁剪（横版）──┼──→ 合并报告
  场景检测 ─→ GIF 关键帧  ──────┘

引擎行为：
  - 每节点超时 300s（自动识别视频命令）
  - videotoolbox 硬件加速自动注入
  - 中间产物作为路径自动路由
```

---

## 📎 附录：已验证可用的工具链

```
macOS 环境：
  ffmpeg    ✅ /opt/homebrew/bin/ffmpeg
  ffprobe   ✅ /opt/homebrew/bin/ffprobe
  sips      ✅ /usr/bin/sips (macOS 内置图像处理)
```

---

> 以上需求基于 `plan_workflow` 在代码审查、文件分析、多源聚合等场景的实际测试提炼。三个能力补齐后，plan_workflow 将从「文本/代码 DAG 引擎」升级为「通用媒体处理编排引擎」，覆盖的场景会从开发者工具扩展到创作者工具，用户群拓宽一个数量级。
