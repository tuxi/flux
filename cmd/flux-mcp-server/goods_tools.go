package main

import (
	"context"

	"flux/tool"
)

func registerGoodsTools(reg *tool.Registry) {
	for _, t := range goodsVideoTools() {
		reg.Register(t)
	}
}

func goodsVideoTools() []tool.Tool {
	return []tool.Tool{
		&mockTool{
			name: "goods_video_param_validate_v2",
			desc: "校验商品视频生成参数。输入: product_name, product_description, duration, aspect_ratio, target_platform。输出: is_valid(bool), normalized_params(object)",
		},
		&mockTool{
			name: "normalize_assets",
			desc: "规范化输入的商品图片资源。输入: product_images, product_image_asset_ids。输出: product_images(array), primary_image(object)",
		},
		&mockTool{
			name: "classify_product_images",
			desc: "分析商品图片质量并分类。输入: product_images。输出: high_quality_images(array), needs_cleaning(bool)",
		},
		&mockTool{
			name: "generate_creative_brief",
			desc: "生成商品视频创意简报。输入: product_name, product_description, selling_points, target_platform。输出: creative_brief(object, 含style/duration/shots)",
		},
		&mockTool{
			name: "generate_goods_script_pro",
			desc: "生成商品视频分镜脚本和TTS文案。输入: creative_brief, visual_profile, duration, mode。输出: video_script(array), shot_coverage_plan(array), voiceover_text(string)",
		},
		&mockTool{
			name: "image_to_video_submit",
			desc: "提交图片生成视频任务（异步）。输入: image_url, prompt, duration, aspect_ratio。输出: provider_task_id(string), estimated_time(int)",
		},
		&mockTool{
			name: "video_generate_wait",
			desc: "等待视频生成任务完成（异步轮询）。输入: provider_task_id。输出: video_url(string), video_duration(float), status(string)",
		},
		&mockTool{
			name: "tts_generate_segments",
			desc: "生成TTS语音片段。输入: voiceover_text, voice_type。输出: audio_urls(array), voiceover_plan(array), segments(array)",
		},
		&mockTool{
			name: "video_assemble",
			desc: "合成最终视频（视频轨+音频轨+字幕）。输入: video_urls(array), audio_urls(array), subtitle_plan(array)。输出: final_video_url(string), duration(float)",
		},
	}
}

type mockTool struct {
	name string
	desc string
}

func (m *mockTool) Name() string                  { return m.name }
func (m *mockTool) Description() string           { return m.desc }
func (m *mockTool) InputSchema() tool.DataSchema  { return tool.DataSchema{} }
func (m *mockTool) OutputSchema() tool.DataSchema { return tool.DataSchema{} }
func (m *mockTool) Mode() tool.ExecutionMode      { return tool.SyncExecution }
func (m *mockTool) Execute(_ context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	// Mock: 返回输入作为输出，让 DAG 的数据流引用正常工作
	return tool.Success(input), nil
}
