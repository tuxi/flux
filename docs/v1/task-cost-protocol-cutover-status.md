# Task Cost Protocol Cutover Status

## 结论

当前 `task_cost_trace` 主链已经正式切到“显式 usage 协议优先”的模式。

对于以下五类核心资源：

- `tts`
- `llm`
- `vlm`
- `image_generation`
- `video_generation`

Engine 在 `runDAG -> finalizeNode` 成功后，只读取节点通过可选 usage 协议返回的 `usage_facts`，不再从节点业务输出中猜字段生成成本事实。

## 当前状态

### 主路径

1. 工具实现可选 usage 协议
2. Engine 成功节点收口时调用协议
3. usage facts 经 schema 校验和标准化
4. 写入 `task_cost_traces`
5. 同步刷新 `task` 成本汇总

### 节点副本

显式 usage facts 会落到：

- `node_runtime.checkpoint["usage_facts"]`

正式账本落到：

- `task_cost_traces`

## 已完成切换的核心资源

### TTS

- `tts_speech_generate`
- `tts_generate_segments`

### LLM

- `image_prompt_enhance`
- `generate_goods_script_pro`
- `generate_creative_brief`
- `build_visual_product_profile`
- `generate_plan_intent`
- `generate_creative_director`
- `generate_day_plans`
- `generate_today_brief`
- `generate_deliverable`
- `enhance_video_prompt`
- `enhance_text_video_prompt`
- `parse_text_video_intent`
- `reconstruct_image_video_intent`

### VLM

- `analyze_product_image`
- `vlm_grounding_analyze`

### Image Generation

- `aliyun_image_generate_wait`
- `aliyun_image_generate_poll_once`
- `volcengine_image_generate_wait`
- `volcengine_image_generate_poll_once`
- `volcengine_seedream_image_generate_wait`
- `volcengine_seedream_image_generate_poll_once`
- `image_provider_result_merge`
- `image_to_image` 完成态链路通过嵌入父工具继承协议

### Video Generation

- `goods_shot_i2v_submit`
- `goods_shot_i2v_wait`
- `goods_shot_i2v_poll_once`

其中：

- `submit` 阶段可输出 usage facts
- 但 `billable=false`、`billable_stage=submit`
- 正式入账只认 `completed`

## 已收缩的兼容层

旧的字段猜测型 extractor 已从生产主链移除。

这意味着：

- 新增工具若未实现 usage 协议，将不会自动记账
- 成本漏记会直接暴露到具体工具
- 不再由 recorder 通过业务输出字段进行隐式推断

## 对后续开发的要求

1. 新增会产生资源消耗的工具时，优先实现可选 usage 协议
2. 工具只输出 usage facts，不直接写账，不输出权威价格
3. 价格计算由后续 `PricingResolver` 统一处理
4. 若某工具没有成本记录，优先检查其是否实现 usage 协议

## 剩余兼容事项

当前仍可能存在少量历史文档中提到旧 extractor/fallback 的描述，这些文档口径后续需要继续更新，但不再影响当前生产代码主链。
