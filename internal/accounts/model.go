// Package accounts manages independent provider credentials and polling scopes.
package accounts

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/onllm-dev/onwatch/v2/internal/config"
)

type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
}

type Provider struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Fields []Field `json:"fields"`
	Quota  bool    `json:"quota"`
}

// Settings is stored in provider_accounts.metadata. Credential values use the
// existing enc: storage format. Omp credentials are referenced, never copied.
type Settings struct {
	Managed        bool              `json:"managed,omitempty"`
	Legacy         bool              `json:"legacy,omitempty"`
	Source         string            `json:"source,omitempty"`
	SourceID       string            `json:"source_id,omitempty"`
	SourceProvider string            `json:"source_provider,omitempty"`
	SourceBaseURL  string            `json:"source_base_url,omitempty"`
	Credentials    map[string]string `json:"credentials,omitempty"`
	BaseURL        string            `json:"base_url,omitempty"`
	Mode           string            `json:"mode,omitempty"`
	BillingKind    string            `json:"billing_kind,omitempty"`
	Enabled        *bool             `json:"enabled,omitempty"`
}

func ParseSettings(raw string) (Settings, error) {
	var settings Settings
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return settings, fmt.Errorf("invalid account settings")
		}
	}
	if settings.Mode == "" {
		settings.Mode = "quota"
	}
	if settings.BillingKind == "" {
		settings.BillingKind = "auto"
	}
	if settings.Source == "" {
		settings.Source = "onwatch"
	}
	return settings, nil
}

func (s Settings) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

var providers = func() []Provider {
	key := func(name, label string) Field { return Field{Key: name, Label: label, Secret: true} }
	text := func(name, label string) Field { return Field{Key: name, Label: label} }
	return []Provider{
		{"synthetic", "Synthetic", []Field{key("api_key", "API key")}, true},
		{"zai", "Z.ai", []Field{key("api_key", "API key"), text("region", "Region (global or cn)")}, true},
		{"anthropic", "Anthropic", []Field{key("token", "OAuth token (quota)"), key("api_key", "Admin API key (billing)")}, true},
		{"codex", "Codex", []Field{key("token", "Access token"), text("account_id", "ChatGPT account ID")}, true},
		{"copilot", "GitHub Copilot", []Field{key("token", "GitHub token")}, true},
		{"antigravity", "Antigravity", []Field{key("csrf_token", "CSRF token")}, true},
		{"minimax", "MiniMax", []Field{key("api_key", "API key"), text("region", "Region (global or cn)")}, true},
		{"gemini", "Gemini", []Field{key("access_token", "OAuth access token"), key("refresh_token", "OAuth refresh token"), key("api_key", "API key (cost telemetry)")}, true},
		{"cursor", "Cursor", []Field{key("token", "Access token")}, true},
		{"grok", "Grok", []Field{key("token", "Access token"), key("api_key", "API key (cost telemetry)")}, true},
		{"kimi", "Kimi Code", []Field{key("token", "Access token")}, true},
		{"moonshot", "Moonshot", []Field{key("api_key", "API key")}, true},
		{"deepseek", "DeepSeek", []Field{key("api_key", "API key")}, true},
		{"openrouter", "OpenRouter", []Field{key("api_key", "API key")}, true},
		{"mistral", "Mistral", []Field{key("auth_cookie", "Console session cookie"), key("api_key", "API key (cost telemetry)")}, true},
		{"opencode", "OpenCode Go", []Field{key("api_key", "Usage API key"), text("workspace_id", "Workspace ID"), key("auth_cookie", "Console session cookie")}, true},
		{"ollama", "Ollama Cloud", []Field{key("api_key", "API key")}, true},
		{"muse", "Muse", []Field{key("api_key", "API key"), text("model", "Model")}, true},
		{"commandcode", "Command Code", []Field{key("api_key", "API key")}, true},
		{"zenmux", "Zenmux", []Field{key("management_api_key", "Management API key")}, true},
		{"alibaba", "Alibaba Token Plan", []Field{key("cookies", "Session cookies"), text("region", "Console region")}, true},
		{"openai", "OpenAI API", []Field{key("api_key", "Admin API key (provider billing)")}, false},
		{"custom", "Custom API", []Field{key("api_key", "API key")}, false},
	}
}()

// Providers returns the immutable provider catalog.
func Providers() []Provider { return providers }

func ProviderFor(id string) (Provider, bool) {
	for _, provider := range Providers() {
		if provider.ID == id {
			return provider, true
		}
	}
	return Provider{}, false
}

func Validate(provider, name string, settings Settings) error {
	if len(name) == 0 || len(name) > 200 || strings.ContainsAny(name, "\x00\r\n") {
		return fmt.Errorf("account name must contain 1-200 characters")
	}
	definition, known := ProviderFor(provider)
	if !known {
		return fmt.Errorf("unsupported provider")
	}
	if settings.Mode != "quota" && settings.Mode != "cost" {
		return fmt.Errorf("mode must be quota or cost")
	}
	if settings.Mode == "quota" && !definition.Quota {
		return fmt.Errorf("this provider supports cost tracking, not quota polling")
	}
	switch settings.BillingKind {
	case "auto", "openai", "anthropic", "openrouter", "openai-compatible", "sub2api", "none":
	default:
		return fmt.Errorf("unsupported billing adapter")
	}
	if settings.BaseURL != "" {
		u, err := url.Parse(settings.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("base URL must be an HTTP(S) URL without credentials, query, or fragment")
		}
	}
	if region := settings.Credentials["region"]; (provider == "zai" || provider == "minimax") && region != "" && region != "global" && region != "cn" {
		return fmt.Errorf("region must be global or cn")
	}
	for field, value := range settings.Credentials {
		valid := false
		for _, definition := range definition.Fields {
			if definition.Key == field {
				valid = true
				break
			}
		}
		if !valid || len(value) > 65536 {
			return fmt.Errorf("invalid credential field %q", field)
		}
	}
	return nil
}

// RuntimeConfig takes a snapshot and applies only this account's credentials.
// Explicit-account agents must never discover another account from tool-global
// auth files or write rotating credentials back to a coding harness.
func RuntimeConfig(base *config.Config, provider string, s Settings) config.Config {
	cfg := base.Snapshot()
	c := s.Credentials
	switch provider {
	case "synthetic":
		cfg.SyntheticAPIKey = c["api_key"]
	case "zai":
		cfg.ZaiAPIKey = c["api_key"]
		cfg.ZaiRegion = c["region"]
		cfg.ZaiBaseURL = s.BaseURL
	case "anthropic":
		cfg.AnthropicToken = c["token"]
		cfg.AnthropicAutoToken = false
		cfg.AnthropicSource = "api"
	case "codex":
		cfg.CodexToken = c["token"]
		cfg.CodexAutoToken = false
	case "copilot":
		cfg.CopilotToken = c["token"]
	case "antigravity":
		cfg.AntigravityBaseURL = s.BaseURL
		cfg.AntigravityCSRFToken = c["csrf_token"]
		cfg.AntigravityEnabled = s.IsEnabled()
	case "minimax":
		cfg.MiniMaxAPIKey = c["api_key"]
		cfg.MiniMaxRegion = c["region"]
	case "gemini":
		cfg.GeminiAccessToken = c["access_token"]
		cfg.GeminiRefreshToken = c["refresh_token"]
		cfg.GeminiAutoToken = false
		cfg.GeminiEnabled = true
	case "cursor":
		cfg.CursorToken = c["token"]
		cfg.CursorAutoToken = false
	case "grok":
		cfg.GrokToken = c["token"]
		cfg.GrokAutoToken = false
		cfg.GrokEnabled = true
	case "kimi":
		cfg.KimiToken = c["token"]
		cfg.KimiAutoToken = false
		cfg.KimiEnabled = true
	case "moonshot":
		cfg.MoonshotAPIKey = c["api_key"]
		cfg.MoonshotBaseURL = s.BaseURL
	case "deepseek":
		cfg.DeepSeekAPIKey = c["api_key"]
		cfg.DeepSeekBaseURL = s.BaseURL
	case "openrouter":
		cfg.OpenRouterAPIKey = c["api_key"]
	case "mistral":
		cfg.MistralAuthCookie = c["auth_cookie"]
		cfg.MistralAuthMode = "cookie"
		cfg.MistralEnabled = true
		cfg.MistralDisabled = false
	case "opencode":
		cfg.OpenCodeGoAPIKey = c["api_key"]
		cfg.OpenCodeGoWorkspaceID = c["workspace_id"]
		cfg.OpenCodeGoAuthCookie = c["auth_cookie"]
		cfg.OpenCodeGoBaseURL = s.BaseURL
	case "ollama":
		cfg.OllamaAPIKey = c["api_key"]
	case "muse":
		cfg.MuseAPIKey = c["api_key"]
		cfg.MuseAutoToken = false
		cfg.MuseModel = c["model"]
		cfg.MuseBaseURL = s.BaseURL
		cfg.MuseEnabled = s.IsEnabled()
		cfg.MuseDisabled = !s.IsEnabled()
	case "commandcode":
		cfg.CommandCodeAPIKey = c["api_key"]
		cfg.CommandCodeAutoToken = false
		cfg.CommandCodeBaseURL = s.BaseURL
		cfg.CommandCodeEnabled = true
		cfg.CommandCodeDisabled = false
	case "zenmux":
		cfg.ZenmuxManagementAPIKey = c["management_api_key"]
		cfg.ZenmuxDisabled = false
	case "alibaba":
		cfg.AlibabaCookies = c["cookies"]
		cfg.AlibabaRegion = c["region"]
		cfg.AlibabaEnabled = true
		cfg.AlibabaDisabled = false
	}
	return cfg
}

func ConfigCredentials(cfg *config.Config, provider string) map[string]string {
	c := cfg.Snapshot()
	switch provider {
	case "synthetic":
		return map[string]string{"api_key": c.SyntheticAPIKey}
	case "zai":
		return map[string]string{"api_key": c.ZaiAPIKey, "region": c.ZaiRegion}
	case "anthropic":
		return map[string]string{"token": c.AnthropicToken}
	case "codex":
		return map[string]string{"token": c.CodexToken}
	case "copilot":
		return map[string]string{"token": c.CopilotToken}
	case "antigravity":
		return map[string]string{"csrf_token": c.AntigravityCSRFToken}
	case "minimax":
		return map[string]string{"api_key": c.MiniMaxAPIKey, "region": c.MiniMaxRegion}
	case "gemini":
		return map[string]string{"access_token": c.GeminiAccessToken, "refresh_token": c.GeminiRefreshToken}
	case "cursor":
		return map[string]string{"token": c.CursorToken}
	case "grok":
		return map[string]string{"token": c.GrokToken}
	case "kimi":
		return map[string]string{"token": c.KimiToken}
	case "moonshot":
		return map[string]string{"api_key": c.MoonshotAPIKey}
	case "deepseek":
		return map[string]string{"api_key": c.DeepSeekAPIKey}
	case "openrouter":
		return map[string]string{"api_key": c.OpenRouterAPIKey}
	case "mistral":
		return map[string]string{"auth_cookie": c.MistralAuthCookie}
	case "opencode":
		return map[string]string{"api_key": c.OpenCodeGoAPIKey, "workspace_id": c.OpenCodeGoWorkspaceID, "auth_cookie": c.OpenCodeGoAuthCookie}
	case "ollama":
		return map[string]string{"api_key": c.OllamaAPIKey}
	case "muse":
		return map[string]string{"api_key": c.MuseAPIKey, "model": c.MuseModel}
	case "commandcode":
		return map[string]string{"api_key": c.CommandCodeAPIKey}
	case "zenmux":
		return map[string]string{"management_api_key": c.ZenmuxManagementAPIKey}
	case "alibaba":
		return map[string]string{"cookies": c.AlibabaCookies, "region": c.AlibabaRegion}
	}
	return nil
}
