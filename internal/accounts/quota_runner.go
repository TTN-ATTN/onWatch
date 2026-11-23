package accounts

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// newQuotaRunner captures explicit credentials once. No credential refresh or
// coding-harness discovery callbacks are installed; rebuilding is the caller's job.
func newQuotaRunner(accountID int64, provider string, settings Settings, st *store.Store, cfg *config.Config, logger *slog.Logger, notifier *notify.NotificationEngine) (agent.AgentRunner, error) {
	definition, ok := ProviderFor(provider)
	if !ok || !definition.Quota {
		return nil, fmt.Errorf("%s does not support quota polling", provider)
	}
	if !settings.IsEnabled() {
		return nil, fmt.Errorf("%s account is disabled", provider)
	}
	if cfg == nil || st == nil {
		return nil, fmt.Errorf("%s quota polling requires configuration and a scoped store", provider)
	}
	if logger == nil {
		logger = slog.Default()
	}
	c := settings.Credentials
	required := "api_key"
	switch provider {
	case "anthropic", "codex", "copilot", "cursor", "grok", "kimi":
		required = "token"
	case "gemini":
		required = "access_token"
	case "antigravity":
		required = "csrf_token"
	case "mistral":
		required = "auth_cookie"
	case "zenmux":
		required = "management_api_key"
	case "alibaba":
		required = "cookies"
	case "opencode":
		if strings.TrimSpace(c["api_key"]) == "" {
			if strings.TrimSpace(c["workspace_id"]) == "" || strings.TrimSpace(c["auth_cookie"]) == "" {
				return nil, fmt.Errorf("opencode quota polling requires api_key or workspace_id and auth_cookie")
			}
			required = "auth_cookie"
		}
	}
	if strings.TrimSpace(c[required]) == "" {
		return nil, fmt.Errorf("%s quota polling requires %s", provider, required)
	}
	base := strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid quota base URL")
		}
		// These clients have multiple fixed origins or no exported endpoint option.
		switch provider {
		case "grok", "mistral", "zenmux", "alibaba":
			return nil, fmt.Errorf("%s quota client does not support a custom base URL", provider)
		}
	}
	if provider == "antigravity" && base == "" {
		return nil, fmt.Errorf("antigravity quota polling requires an explicit language-server base URL")
	}
	runtime := RuntimeConfig(cfg, provider, settings)
	sessionProvider := provider
	if provider == "codex" || provider == "alibaba" {
		sessionProvider = fmt.Sprintf("%s:%d", provider, accountID)
	}
	sm := agent.NewSessionManager(st, sessionProvider, runtime.SessionIdleTimeout, logger)
	interval := runtime.PollInterval
	endpoint := func(path string) string {
		if strings.HasSuffix(base, path) {
			return base
		}
		return base + path
	}
	var runner agent.AgentRunner
	switch provider {
	case "synthetic":
		var opts []api.Option
		if base != "" {
			opts = append(opts, api.WithBaseURL(endpoint("/v2/quotas")))
		}
		runner = agent.New(api.NewClient(c["api_key"], logger, opts...), st, tracker.New(st, logger), interval, logger, sm)
	case "zai":
		u, err := quotaZaiURL(base, c["region"])
		if err != nil {
			return nil, err
		}
		runner = agent.NewZaiAgent(api.NewZaiClient(c["api_key"], logger, api.WithZaiBaseURL(u)), st, tracker.NewZaiTracker(st, logger), interval, logger, sm)
	case "anthropic":
		var opts []api.AnthropicOption
		if base != "" {
			opts = append(opts, api.WithAnthropicBaseURL(endpoint("/api/oauth/usage")))
		}
		runner = agent.NewAnthropicAgent(api.NewAnthropicClient(c["token"], logger, opts...), st, tracker.NewAnthropicTracker(st, logger), interval, logger, sm)
	case "codex":
		var opts []api.CodexOption
		if base != "" {
			opts = append(opts, api.WithCodexBaseURL(endpoint("/backend-api/wham/usage")))
		}
		client := api.NewCodexClient(c["token"], logger, opts...)
		client.SetAccountID(c["account_id"])
		runner = agent.NewCodexAgentWithAccount(client, st, tracker.NewCodexTracker(st, logger), interval, logger, sm, accountID)
	case "copilot":
		var opts []api.CopilotOption
		if base != "" {
			opts = append(opts, api.WithCopilotBaseURL(endpoint("/copilot_internal/user")))
		}
		runner = agent.NewCopilotAgent(api.NewCopilotClient(c["token"], logger, opts...), st, tracker.NewCopilotTracker(st, logger), interval, logger, sm)
	case "antigravity":
		client := api.NewAntigravityClient(logger, api.WithAntigravityConnection(&api.AntigravityConnection{BaseURL: base, CSRFToken: c["csrf_token"]}))
		runner = agent.NewAntigravityAgent(client, st, tracker.NewAntigravityTracker(st, logger), interval, logger, sm, agent.WithAntigravityManualConfig(base, c["csrf_token"]))
	case "minimax":
		if base == "" {
			base = "https://api.minimax.io"
			if c["region"] == "cn" {
				base = "https://www.minimaxi.com"
			}
		}
		runner = agent.NewMiniMaxAgentWithAccount(api.NewMiniMaxClient(c["api_key"], logger, api.WithMiniMaxBaseURL(endpoint("/v1/api/openplatform/coding_plan/remains"))), st, tracker.NewMiniMaxTracker(st, logger), interval, logger, sm, accountID)
	case "gemini":
		var opts []api.GeminiOption
		if base != "" {
			opts = append(opts, api.WithGeminiBaseURL(base))
		}
		runner = agent.NewGeminiAgent(api.NewGeminiClient(c["access_token"], logger, opts...), st, tracker.NewGeminiTracker(st, logger), interval, logger, sm)
	case "cursor":
		var opts []api.CursorOption
		if base != "" {
			opts = append(opts, api.WithCursorBaseURL(base), api.WithCursorWebBaseURL(base))
		}
		runner = agent.NewCursorAgent(api.NewCursorClient(c["token"], logger, opts...), st, tracker.NewCursorTracker(st, logger), interval, logger, sm)
	case "grok":
		runner = agent.NewGrokAgent(api.NewGrokClient(c["token"], logger, api.WithGrokExplicitCredentials()), st, tracker.NewGrokTracker(st, logger), interval, logger, sm)
	case "kimi":
		var opts []api.KimiOption
		if base != "" {
			opts = append(opts, api.WithKimiBaseURL(base))
		}
		runner = agent.NewKimiAgent(api.NewKimiClient(c["token"], logger, opts...), st, tracker.NewKimiTracker(st, logger), interval, logger, sm)
	case "moonshot":
		var opts []api.MoonshotOption
		if base != "" {
			opts = append(opts, api.WithMoonshotBaseURL(base))
		}
		runner = agent.NewMoonshotAgent(api.NewMoonshotClient(c["api_key"], logger, opts...), st, tracker.NewMoonshotTracker(st, logger), interval, logger, sm)
	case "deepseek":
		var opts []api.DeepSeekOption
		if base != "" {
			opts = append(opts, api.WithDeepSeekBaseURL(base))
		}
		runner = agent.NewDeepSeekAgent(api.NewDeepSeekClient(c["api_key"], logger, opts...), st, tracker.NewDeepSeekTracker(st, logger), interval, logger, sm)
	case "openrouter":
		var opts []api.OpenRouterOption
		if base != "" {
			opts = append(opts, api.WithOpenRouterBaseURL(base))
		}
		runner = agent.NewOpenRouterAgent(api.NewOpenRouterClient(c["api_key"], logger, opts...), st, tracker.NewOpenRouterTracker(st, logger), interval, logger, sm)
	case "mistral":
		runtime.MistralAuthMode = "manual"
		runner = agent.NewMistralAgent(st, &runtime, logger)
	case "opencode":
		var opts []api.OpenCodeClientOption
		if base != "" {
			opts = append(opts, api.WithOpenCodeBaseURL(base))
		}
		runner = agent.NewOpenCodeAgent(api.NewOpenCodeClient(logger, opts...), st, tracker.NewOpenCodeTracker(st, logger), &runtime, interval, logger, sm)
	case "ollama":
		var opts []api.OllamaClientOption
		if base != "" {
			opts = append(opts, api.WithOllamaBaseURL(base))
		}
		runner = agent.NewOllamaAgent(api.NewOllamaClient(c["api_key"], logger, opts...), st, tracker.NewOllamaTracker(st, logger), &runtime, interval, logger, sm)
	case "muse":
		var opts []api.MuseClientOption
		if base != "" {
			opts = append(opts, api.WithMuseBaseURL(base))
		}
		runner = agent.NewMuseAgent(api.NewMuseClient(c["api_key"], c["model"], logger, opts...), st, tracker.NewMuseTracker(st, logger), interval, logger, sm)
	case "commandcode":
		var opts []api.CommandCodeClientOption
		if base != "" {
			opts = append(opts, api.WithCommandCodeBaseURL(base))
		}
		runner = agent.NewCommandCodeAgent(api.NewCommandCodeClient(c["api_key"], logger, opts...), st, tracker.NewCommandCodeTracker(st, logger), interval, logger, sm)
	case "zenmux":
		// Do not put a typed nil notifier into the agent's interface.
		if notifier == nil {
			runner = agent.NewZenmuxAgent(api.NewZenmuxClient(c["management_api_key"]), st, interval, logger, nil)
		} else {
			runner = agent.NewZenmuxAgent(api.NewZenmuxClient(c["management_api_key"]), st, interval, logger, notifier)
		}
	case "alibaba":
		runner = agent.NewAlibabaAgentWithAccount(api.NewAlibabaClient(c["cookies"], logger, api.WithAlibabaRegion(c["region"])), st, tracker.NewAlibabaTracker(st, logger), interval, logger, sm, accountID)
	default:
		return nil, fmt.Errorf("%s quota polling is unavailable", provider)
	}
	if n, ok := runner.(interface {
		SetNotifier(*notify.NotificationEngine)
	}); ok {
		n.SetNotifier(notifier)
	}
	return runner, nil
}

// Unknown proxy prefixes must identify the monitoring endpoint explicitly:
// silently stripping an inference path can route credentials to the wrong API.
func quotaZaiURL(base, region string) (string, error) {
	const path = "/monitor/usage/quota/limit"
	if base == "" {
		if region == "cn" {
			return "https://open.bigmodel.cn/api" + path, nil
		}
		return "https://api.z.ai/api" + path, nil
	}
	if strings.HasSuffix(base, path) {
		return base, nil
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid Z.ai quota URL")
	}
	if (u.Host == "api.z.ai" || u.Host == "open.bigmodel.cn") && (u.Path == "" || u.Path == "/api" || u.Path == "/api/paas/v4" || u.Path == "/api/coding/paas/v4") {
		u.Path = "/api" + path
		return u.String(), nil
	}
	return "", fmt.Errorf("custom Z.ai quota base URL must include %s (proxy prefixes are preserved)", path)
}
