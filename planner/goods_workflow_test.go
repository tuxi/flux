package planner_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tuxi/flux/planner"
	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/tool"
	"github.com/tuxi/flux/tool/builtin"
)

// TestGoodsVideoDAG_DAGPlanner 验证 DAGPlanner 在拥有商品视频工具目录时，
// 能否生成合法的 DAG。
//
// 这是 Agent-Driven DAG 生成的 M1 验证：
//
//	LLM_API_KEY=... go test ./planner/ -run TestGoodsVideoDAG -v -timeout 5m
func TestGoodsVideoDAG_DAGPlanner(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_API_KEY 未设置：跳过")
	}

	// 模拟 DreamAI 的工具目录：精选 10 个代表性的商品视频工具
	goodsReg := tool.NewRegistry()

	// 参数校验
	goodsReg.Register(&mockTool{
		name:        "goods_video_param_validate_v2",
		description: "校验商品视频生成参数。输入：product_name, product_description, duration, aspect_ratio, target_platform。输出：is_valid, normalized_params",
	})
	// 素材规范化
	goodsReg.Register(&mockTool{
		name:        "normalize_assets",
		description: "规范化输入的商品图片资源。输入：product_images, product_image_asset_ids。输出：product_images, primary_image",
	})
	// 商品图片分析
	goodsReg.Register(&mockTool{
		name:        "classify_product_images",
		description: "分析商品图片质量并分类。输入：product_images。输出：high_quality_images, needs_cleaning",
	})
	// 创意简报
	goodsReg.Register(&mockTool{
		name:        "generate_creative_brief",
		description: "生成商品视频创意简报。输入：product_name, product_description, selling_points, target_platform。输出：creative_brief (JSON)",
	})
	// 分镜脚本
	goodsReg.Register(&mockTool{
		name:        "generate_goods_script_pro",
		description: "生成商品视频分镜脚本。输入：creative_brief, visual_profile, duration, mode。输出：video_script, shot_coverage_plan, voiceover_text",
	})
	// 提交图片生成
	goodsReg.Register(&mockTool{
		name:        "image_to_video_submit",
		description: "提交图片生成视频任务（异步）。输入：image_url, prompt, duration, aspect_ratio。输出：provider_task_id, estimated_time",
	})
	// 查询视频生成结果
	goodsReg.Register(&mockTool{
		name:        "video_generate_wait",
		description: "等待视频生成任务完成（异步轮询）。输入：provider_task_id。输出：video_url, video_duration, status",
	})
	// TTS 语音合成
	goodsReg.Register(&mockTool{
		name:        "tts_generate_segments",
		description: "生成 TTS 语音片段。输入：voiceover_text, voice_type。输出：audio_urls, voiceover_plan, segments",
	})
	// 视频合成
	goodsReg.Register(&mockTool{
		name:        "video_assemble",
		description: "合成最终视频（视频轨+音频轨+字幕）。输入：video_urls, audio_urls, subtitle_plan。输出：final_video_url, duration",
	})
	// 通用工具
	goodsReg.Register(builtin.NewMergeResultTool())
	goodsReg.Register(builtin.NewShellTool("."))

	// 构造 provider
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	provider := provider(baseURL, apiKey)
	modelName := os.Getenv("LLM_MODEL")
	if modelName == "" {
		modelName = "deepseek-v4-pro"
	}

	goal := `为商品 "红色连衣裙" 生成一个 15 秒的带货展示视频。要求：
1. 先校验参数并规范化图片素材
2. 分析商品图片质量
3. 生成创意简报和分镜脚本（包含 TTS 语音）
4. 提交视频生成，等待完成后合成最终视频
请合理安排依赖关系，可以并行的地方尽量并行。`

	p := planner.NewDAGPlanner(provider, modelName, goal, goodsReg)
	p.MaxRepairs = 3

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	plan, err := p.Generate(ctx)
	if err != nil {
		t.Fatalf("DAG 生成失败: %v", err)
	}

	// 验证生成的 DAG
	if len(plan.Nodes) < 3 {
		t.Fatalf("期望至少 3 个节点，实际 %d", len(plan.Nodes))
	}

	t.Logf("✅ DAGPlanner 生成了 %d 个节点:", len(plan.Nodes))
	for _, n := range plan.Nodes {
		t.Logf("  %s tool=%s depends_on=%v", n.Name, n.ToolName, n.DependsOn)
	}

	// 执行 DAG（用空输入，验证结构即可）
	state := runtime.NewMemState(map[string]any{})
	sched := runtime.NewScheduler(
		planner.NewToolInvoker(goodsReg),
		planner.NopAwait{},
		planner.NopStore{},
		nil,
	).WithMaxSteps(100)

	res, err := sched.Run(ctx, runtime.NewStaticSource(plan), state)
	if err != nil {
		t.Logf("⚠️ DAG 执行有错误（预期，因为使用了 mock 工具）: %v", err)
	}
	if res.Status == runtime.StatusCompleted {
		t.Log("✅ DAG 完整执行通过")
	}
}

// mockTool 是一个只有元数据的 tool.Tool，用于测试 DAGPlanner。
type mockTool struct {
	name        string
	description string
}

func (m *mockTool) Name() string                    { return m.name }
func (m *mockTool) Description() string             { return m.description }
func (m *mockTool) InputSchema() tool.DataSchema     { return tool.DataSchema{} }
func (m *mockTool) OutputSchema() tool.DataSchema    { return tool.DataSchema{} }
func (m *mockTool) Mode() tool.ExecutionMode         { return tool.SyncExecution }
func (m *mockTool) Execute(_ context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	return tool.Success(input), nil
}
