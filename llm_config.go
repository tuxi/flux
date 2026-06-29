package flux

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tuxi/flux/model"
)

// LLMConfig 是 standalone 二进制（如 flux-mcp-server）用来构建 DAGPlanner 所需 LLM
// provider 的配置。
//
// 设计边界（重要）：flux 作为**库**嵌入时（code-agent / dream-ai），LLM provider 由
// host 注入（WorkflowToolConfig.Provider），**不读这里**——避免库与 host 各持一套配置
// 造成不一致。只有当 flux 作为**独立 MCP server** 被 Claude Code 等客户端调用时，没有
// host 注入凭证，才由二进制自己 LoadLLMConfig 读文件 + 环境变量来自给。
//
// 仅支持 OpenAI 兼容端点（DeepSeek / Qwen / Moonshot / 自托管 / Claude-via-gateway）。
type LLMConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
}

// llmConfigFile 对应 config.yaml 顶层的 `llm:` 块。
type llmConfigFile struct {
	LLM LLMConfig `yaml:"llm"`
}

// LoadLLMConfig 从 config.yaml 的 `llm:` 块加载 LLM 配置，并用环境变量覆盖（便于 CI/容器
// 不改文件就能注入密钥）。优先级：环境变量 > 文件 > 默认值。
//
// path 为空或文件不存在时不报错——退回纯环境变量 + 默认（base_url=DeepSeek、model=deepseek-chat）。
// 这样部署可以只设 LLM_API_KEY 环境变量，连文件都不需要。
func LoadLLMConfig(path string) (LLMConfig, error) {
	var cfg LLMConfig

	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var f llmConfigFile
			if err := yaml.Unmarshal(data, &f); err != nil {
				return cfg, fmt.Errorf("parse %s: %w", path, err)
			}
			cfg = f.LLM
		} else if !os.IsNotExist(err) {
			return cfg, fmt.Errorf("read %s: %w", path, err)
		}
	}

	// 环境变量覆盖（与历史行为兼容）。
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		cfg.Model = v
	}

	// 默认值。
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "deepseek-chat"
	}

	if cfg.APIKey == "" {
		return cfg, fmt.Errorf("LLM api_key 未配置：请在 %q 的 llm.api_key 设置，或导出环境变量 LLM_API_KEY", pathOrDefault(path))
	}
	return cfg, nil
}

// NewProvider 用配置构建一个 OpenAI 兼容 provider（满足 model.Completer）。
func (c LLMConfig) NewProvider(timeout time.Duration) *model.OpenAICompatibleProvider {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	// 走构造函数：它会 TrimRight base_url 的尾斜杠，避免 "…//chat/completions"。
	p := model.NewOpenAICompatibleProvider(c.BaseURL, c.APIKey)
	p.HTTPClient = &http.Client{Timeout: timeout}
	return p
}

func pathOrDefault(path string) string {
	if path == "" {
		return "config.yaml"
	}
	return path
}
