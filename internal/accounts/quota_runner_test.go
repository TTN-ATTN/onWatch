package accounts

import (
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestQuotaRunnerRejectsUnrelatedCredentials(t *testing.T) {
	for _, tc := range []struct{ provider, key, required string }{
		{"anthropic", "api_key", "token"},
		{"gemini", "api_key", "access_token"},
		{"gemini", "refresh_token", "access_token"},
		{"grok", "api_key", "token"},
		{"mistral", "api_key", "auth_cookie"},
		{"alibaba", "api_key", "cookies"},
		{"zenmux", "api_key", "management_api_key"},
	} {
		t.Run(tc.provider+"/"+tc.key, func(t *testing.T) {
			_, err := newQuotaRunner(42, tc.provider, Settings{Credentials: map[string]string{tc.key: "not-a-quota-credential"}}, &store.Store{}, &config.Config{}, nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.required) {
				t.Fatalf("expected explicit %s requirement, got %v", tc.required, err)
			}
		})
	}
}

func TestQuotaRunnerRejectsImplicitSources(t *testing.T) {
	for _, tc := range []struct {
		provider string
		settings Settings
		want     string
	}{
		{"antigravity", Settings{Credentials: map[string]string{"csrf_token": "explicit"}}, "explicit language-server"},
		{"muse", Settings{Enabled: boolPointer(false), Credentials: map[string]string{"api_key": "explicit"}}, "disabled"},
		{"grok", Settings{BaseURL: "https://proxy.example/prefix", Credentials: map[string]string{"token": "explicit"}}, "does not support a custom"},
		{"alibaba", Settings{BaseURL: "https://proxy.example/prefix", Credentials: map[string]string{"cookies": "explicit"}}, "does not support a custom"},
		{"openai", Settings{}, "does not support quota"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			_, err := newQuotaRunner(42, tc.provider, tc.settings, &store.Store{}, &config.Config{}, nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func boolPointer(v bool) *bool { return &v }

func TestQuotaZaiURLPreservesExplicitMonitoringPrefixes(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://api.z.ai/api/coding/paas/v4", "https://api.z.ai/api/monitor/usage/quota/limit"},
		{"https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/monitor/usage/quota/limit"},
		{"https://proxy.example/team/zai/monitor/usage/quota/limit", "https://proxy.example/team/zai/monitor/usage/quota/limit"},
	} {
		got, err := quotaZaiURL(tc.base, "")
		if err != nil || got != tc.want {
			t.Fatalf("quotaZaiURL(%q) = %q, %v", tc.base, got, err)
		}
	}
	if _, err := quotaZaiURL("https://proxy.example/team/coding/paas/v4", ""); err == nil {
		t.Fatal("unknown inference proxy must require explicit monitoring endpoint")
	}
}
