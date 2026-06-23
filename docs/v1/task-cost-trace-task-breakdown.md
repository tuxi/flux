# AI Engine Task Cost Trace 实施清单

日期：2026-04-25

状态：Draft

关联文档：

- [AI Engine Task Cost Trace 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-design.md)
- [AI Engine TTS Provider 技术设计文档](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-technical-design.md)

## 1. 说明

本清单按 `P0 / P1 / P2` 拆分，目标是直接作为统一成本明细能力的开发排期、联调和验收基础使用。

每个任务包含：

- 任务目标
- 核心改动点
- 主要涉及模块
- 验收标准

本清单默认前提：

- `task_cost_trace` 是统一成本明细主表
- `task` 仅负责汇总
- `node runtime` 保持运行态职责，不承担正式记账主数据角色

## 2. P0 任务

## P0-1 落地 `task_cost_trace` 数据模型与 migration

任务目标：

- 为统一成本明细建立正式持久化表结构

核心改动点：

- 新增 `task_cost_trace` 表 migration
- 增加实体模型
- 增加基础索引：
  - `task_id`
  - `node_runtime_id`
  - `resource_type`
  - `created_at`
- 增加 `idempotency_key` 唯一索引

主要涉及模块：

- `internal/model/entity/`
- `internal/repository/`
- `script/sql/` 或现有 migration 目录

验收标准：

- 本地数据库可成功建表
- 表字段与设计稿一致
- 索引可正常创建
- 支持基本插入与查询

## P0-2 建立 `task_cost_trace` Repository 与服务接口

任务目标：

- 为成本明细写入与查询提供统一基础服务

核心改动点：

- 新增 Repository 接口
- 支持按 `task_id` 查询
- 支持按 `node_runtime_id` 查询
- 支持按 `idempotency_key` 幂等写入
- 新增基础服务层：
  - `CreateCostTrace`
  - `ListTaskCostTraces`
  - `SummarizeTaskCost`

主要涉及模块：

- `internal/repository/`
- `internal/service/`

验收标准：

- 成本明细可以通过 service 正常创建
- 重复 `idempotency_key` 不会重复入账
- 可以按任务维度拿到全部成本项

## P0-3 统一资源类型与成本字段枚举

任务目标：

- 从第一天起把 `tts / llm / vlm / image_generation / video_generation` 放进统一模型

核心改动点：

- 定义 `resource_type` 常量
- 明确 `usage_unit` 常量
- 明确 `cost_status` 常量
- 在服务层统一校验合法枚举

主要涉及模块：

- `internal/model/consts/` 或现有常量目录
- `internal/service/`

验收标准：

- `vlm` 作为独立 `resource_type` 存在
- 非法资源类型会被拒绝
- 成本服务对单位和状态有统一校验

## P0-4 为 `task` 增加成本汇总字段

任务目标：

- 让任务表具备最小成本汇总能力

核心改动点：

- 为 `task` 增加字段：
  - `estimated_cost_total`
  - `actual_cost_total`
  - `cost_status`
  - `cost_version`
- 增加汇总更新逻辑

主要涉及模块：

- `internal/model/entity/task.go` 或对应任务实体
- `internal/repository/task*`
- `internal/service/`

验收标准：

- 新任务默认成本字段有合理初值
- 写入成本明细后可正确回写 `estimated_cost_total`
- 任务详情可直接展示总成本

## P0-5 接入首条 `tts` 成本明细写入链路

任务目标：

- 用当前已打通的 TTS 估算成本链路，为 `task_cost_trace` 写入第一类真实成本项

核心改动点：

- 在 `tts_generate_segments` 或统一 TTS service 收口点写入成本明细
- 写入字段至少包括：
  - `task_id`
  - `workflow_name`
  - `node_name`
  - `resource_type=tts`
  - `provider`
  - `model`
  - `usage_quantity`
  - `usage_unit=chars`
  - `estimated_cost`
- `trace_payload` 中写入：
  - `protocol`
  - `fallback_chain`
  - `subtitle_sentence_count`
  - `mode`

主要涉及模块：

- `ai-engine/pkg/tts/`
- `ai-engine/workflows/goods/`
- `internal/service/`

验收标准：

- 一次商品视频生成至少落一条 `tts` 成本明细
- 成本金额与当前 `tts_estimated_cost` 一致
- 重试场景不会重复生成重复账项

## P0-6 建立任务成本汇总回写逻辑

任务目标：

- 让 `task_cost_trace` 和 `task` 总成本形成闭环

核心改动点：

- 每次新增成本明细后触发任务级汇总更新
- 汇总逻辑至少计算：
  - `estimated_cost_total`
  - `actual_cost_total`
  - `cost_status`
- 保证任务取消、失败、成功状态下都能保留已发生的估算成本

主要涉及模块：

- `internal/service/`
- `internal/repository/`

验收标准：

- 同一任务多条成本明细可正确聚合
- `task.estimated_cost_total` 与成本明细求和一致
- 失败任务也能保留成本痕迹

## P0-7 增加基础查询与管理接口

任务目标：

- 让成本明细能被后台和排障链路直接查看

核心改动点：

- 增加任务成本明细查询接口
- 增加任务成本汇总查询接口
- 后台接口最少支持：
  - 按 `task_id` 查询
  - 按 `resource_type` 过滤
  - 返回总成本和明细列表

主要涉及模块：

- `internal/service/`
- `internal/controller/` 或后台接口层
- DTO 定义

验收标准：

- 可以通过接口查到某条任务的 `tts` 成本明细
- 可以看到任务总成本
- 接口字段口径与表结构一致

## P0-8 增加基础测试集

任务目标：

- 为成本明细链路建立最小可信测试基线

核心改动点：

- 增加 migration/实体测试
- 增加 repository 测试
- 增加服务层幂等写入测试
- 增加任务汇总测试
- 增加 `tts` 成本写入链路测试

主要涉及模块：

- `internal/service/*_test.go`
- `internal/repository/*_test.go`
- `ai-engine/pkg/tts/*_test.go`

验收标准：

- 重复写入不会重复入账
- 任务成本汇总逻辑有测试覆盖
- `tts` 成本写入链路通过测试

## 3. P1 任务

## P1-1 接入 `llm` 成本明细

任务目标：

- 将纯文本大模型调用成本纳入统一总账

核心改动点：

- 在统一 LLM 调用封装层收集：
  - `prompt_tokens`
  - `completion_tokens`
  - `total_tokens`
  - `model`
  - `provider`
- 写入 `resource_type=llm`

主要涉及模块：

- `ai-engine/pkg/llm/` 或现有 LLM 调用封装
- `internal/service/`

验收标准：

- 至少一条主工作流的文本 LLM 调用能写入成本明细
- `usage_unit=tokens`
- 任务总成本包含 `llm`

## P1-2 接入 `vlm` 成本明细

任务目标：

- 将视觉理解类调用单独记账，不与 `llm` 混口径

核心改动点：

- 在 VLM 调用封装处收集 token 与模型信息
- 写入 `resource_type=vlm`
- 明确 `trace_payload` 中的视觉输入信息摘要

主要涉及模块：

- `ai-engine/pkg/vlm/` 或相关视觉理解工具链
- `internal/service/`

验收标准：

- `vlm` 作为独立成本项入账
- 可从任务成本明细中区分 `llm` 与 `vlm`

## P1-3 接入 `image_generation` 成本明细

任务目标：

- 将图片生成成本纳入统一总账

核心改动点：

- 在图片生成 provider 收口点写入：
  - `resource_type=image_generation`
  - `usage_quantity=image_count`
  - `usage_unit=images`
- 对接 provider / model 信息

主要涉及模块：

- 图片工作流相关 provider 调用层
- `internal/service/`

验收标准：

- 至少一条图片生成链路能写入成本明细
- 可按 provider / model 统计图片成本

## P1-4 接入 `video_generation` 成本明细

任务目标：

- 将视频生成成本纳入统一总账

核心改动点：

- 先按 `jobs` 建立第一阶段成本口径
- 如 provider 明确按时长计费，再补 `seconds` 口径
- 写入 `resource_type=video_generation`

主要涉及模块：

- 视频生成 provider 收口层
- `await / callback / poll_once` 相关完成收口点
- `internal/service/`

验收标准：

- 至少一条视频生成链路能写入成本明细
- 异步 `submit + await` 场景不会重复记账

## P1-5 建立任务成本详情页或后台管理视图

任务目标：

- 让运营、排障和评审可以直接查看任务级成本总账

核心改动点：

- 增加后台任务成本详情展示
- 展示维度至少包括：
  - 总成本
  - 各资源类型成本
  - provider / model
  - usage 与 unit price

主要涉及模块：

- 管理后台接口层
- 管理后台页面或管理 API

验收标准：

- 可视化查看一条任务的全部成本项
- 能区分 `tts / llm / vlm / image / video`

## 4. P2 任务

## P2-1 引入 `actual_cost` 与账单对账能力

任务目标：

- 从预估成本演进到供应商真实成本

核心改动点：

- 定义账单回填或对账流程
- 支持按供应商账单更新 `actual_cost`
- 支持 `cost_status=actualized`

主要涉及模块：

- `internal/service/`
- 供应商账单同步层

验收标准：

- 至少一个 provider 能从估算转为真实成本
- 同一条成本明细可完成 `estimated -> actualized`

## P2-2 建立成本报表与统计聚合

任务目标：

- 支撑运营和产品的成本看数需求

核心改动点：

- 增加按天、按 workflow、按 provider 的成本聚合
- 增加按资源类型的成本占比统计

主要涉及模块：

- 统计服务
- 后台聚合接口

验收标准：

- 可输出日维度成本报表
- 可输出各资源类型成本占比

## P2-3 与用户积分 / 定价体系联动

任务目标：

- 为用户侧定价和权益策略提供基础数据

核心改动点：

- 建立成本到积分的映射策略
- 支持按任务类型或资源消耗做价格策略
- 评估与现有 billing 体系的联动方式

主要涉及模块：

- `internal/service/billing_*`
- 定价规则与报价服务

验收标准：

- 可以基于统一成本明细评估新的积分或定价模型
- 用户侧计费策略不再依赖拍脑袋估算

## 5. 推荐落地顺序

建议按照以下顺序推进：

1. `P0-1` 到 `P0-4`
先把表、实体、汇总字段和服务骨架立起来。

2. `P0-5` 到 `P0-8`
先用 `tts` 跑通第一条真实成本入账链路，并补齐测试和查询能力。

3. `P1-1` 到 `P1-4`
逐步接入 `llm / vlm / image / video`，形成完整生成成本总账。

4. `P2-1` 到 `P2-3`
再进入真实账单、报表和定价联动阶段。

## 6. 评审会需拍板事项

1. `task_cost_trace` 是否作为正式统一成本明细主表
2. `vlm` 是否确定保持独立 `resource_type`
3. `video_generation` 第一阶段是否先按 `jobs` 计量
4. `task` 是否同步新增成本汇总字段
5. 本期是否仅落 `estimated_cost`
