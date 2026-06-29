package flux

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadLLMConfig_FromFile(t *testing.T) {
	clearLLMEnv(t)
	p := writeCfg(t, "llm:\n  api_key: \"sk-file\"\n  base_url: \"https://api.deepseek.com/v1\"\n  model: \"deepseek-v4-pro\"\n")
	cfg, err := LoadLLMConfig(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKey != "sk-file" || cfg.Model != "deepseek-v4-pro" || cfg.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestLoadLLMConfig_EnvOverridesFile(t *testing.T) {
	clearLLMEnv(t)
	p := writeCfg(t, "llm:\n  api_key: \"sk-file\"\n  model: \"deepseek-v4-pro\"\n")
	t.Setenv("LLM_API_KEY", "sk-env")
	t.Setenv("LLM_MODEL", "qwen3-coder-plus")
	cfg, err := LoadLLMConfig(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKey != "sk-env" || cfg.Model != "qwen3-coder-plus" {
		t.Fatalf("env did not override file: %+v", cfg)
	}
}

func TestLoadLLMConfig_DefaultsWhenFileMissing(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("LLM_API_KEY", "sk-env-only")
	cfg, err := LoadLLMConfig("/nonexistent/config.yaml")
	if err != nil {
		t.Fatalf("missing file should not error when env key present: %v", err)
	}
	if cfg.BaseURL != "https://api.deepseek.com/v1" || cfg.Model != "deepseek-chat" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadLLMConfig_ErrorWhenNoKey(t *testing.T) {
	clearLLMEnv(t)
	p := writeCfg(t, "llm:\n  model: \"deepseek-chat\"\n")
	if _, err := LoadLLMConfig(p); err == nil {
		t.Fatal("expected error when api_key absent, got nil")
	}
}

func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LLM_API_KEY", "LLM_BASE_URL", "LLM_MODEL"} {
		t.Setenv(k, "") // restored by t.Cleanup; empty == unset for our getenv checks
	}
}
