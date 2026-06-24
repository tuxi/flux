package planner

// 类型 B 数据流的 live 证明（gated）：真实 LLM 产出带 $from 引用的 DAG，
// 校验通过、执行后上游产出真的流到了下游。internal 包测试，便于检视 lastSpec 里的引用。
//
//	LLM_API_KEY=... LLM_BASE_URL=https://api.deepseek.com/v1 \
//	  go test ./planner/ -run TestBDataflow_LLM -v -timeout 5m
//
// 注：用 builtin 工具时上游产出是确定的（LLM 本可内联），故 prompt 明确要求用 $from——
// 本测试验证"LLM 能正确产出我们定义的引用格式且端到端解析"，而非"非用不可"。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flux/model"
	"flux/tool"
	"flux/tool/builtin"

	"flux/runtime"
)

func TestBDataflow_LLM_UsesReferences(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_API_KEY 未设置：跳过类型 B 数据流 live 测试")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	modelName := os.Getenv("LLM_MODEL")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}

	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())
	reg.Register(builtin.NewWriteFileTool(dir))

	goal := `生成一个恰好两节点的计划，演示数据流：
1) 节点 id="config"，调 merge_result，arguments={"filename":"greeting.txt","body":"hello from flux"}
   （merge_result 会把 input 原样作为 output 回显）；
2) 节点 id="write"，调 write_file，depends_on=["config"]。它的 path 必须用引用
   {"$from":"config","field":"filename"}，content 必须用引用 {"$from":"config","field":"body"}。
不要在 write 里内联 path/content 字面量，必须用 $from 引用 config 的输出。`

	p := NewDAGPlanner(model.NewOpenAICompatibleProvider(baseURL, apiKey), modelName, goal, reg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	plan, err := p.Generate(ctx)
	if err != nil {
		t.Fatalf("生成/校验失败: %v", err)
	}

	// 检视 lastSpec：write 节点必须真的用了 $from 引用 config
	var refCount int
	for _, n := range p.lastSpec.Nodes {
		for _, ref := range referencedNodes(n.Arguments) {
			t.Logf("  node %q 引用了 %q", n.ID, ref)
			if ref == "config" {
				refCount++
			}
		}
	}
	if refCount == 0 {
		t.Fatalf("LLM 没有使用 $from 引用（lastSpec=%+v）", p.lastSpec)
	}

	// 执行：config 先跑，write 解析引用后写文件
	sched := runtime.NewScheduler(NewToolInvoker(reg), NopAwait{}, NopStore{}, NopEmitter{}).
		WithMaxSteps(30)
	st := runtime.NewMemState(nil)
	res, err := sched.Run(ctx, runtime.NewStaticSource(plan), st)
	if err != nil {
		t.Fatalf("执行: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("status=%d", res.Status)
	}

	// 上游产出真的流到了下游：greeting.txt 应被创建且内容来自 config.body
	data, err := os.ReadFile(filepath.Join(dir, "greeting.txt"))
	if err != nil {
		t.Fatalf("greeting.txt 未创建（引用未正确解析为 path？）: %v", err)
	}
	if !strings.Contains(string(data), "hello from flux") {
		t.Fatalf("内容未来自 config.body 引用，得 %q", string(data))
	}
	t.Logf("✅✅ 类型 B 数据流：LLM 用 $from 引用 config 的 filename/body，运行时解析，greeting.txt 内容=%q", string(data))
}
