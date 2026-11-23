package pricing

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func overrideFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEstimateArithmetic(t *testing.T) {
	catalog, err := NewCatalog(overrideFile(t, `[
		{"provider":"custom","model":"cached","input_per_million":2,"output_per_million":8,"cache_read_per_million":0.5,"cache_write_per_million":3},
		{"provider":"custom","model":"default-cache","input_per_million":2,"output_per_million":8},
		{"provider":"custom","model":"free-cache","input_per_million":2,"output_per_million":8,"cache_read_per_million":0,"cache_write_per_million":0}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		model string
		usage Usage
		want  float64
	}{
		{"uncached", "cached", Usage{PromptTokens: 1000000, CompletionTokens: 1000000}, 10},
		{"disjoint subsets", "cached", Usage{PromptTokens: 1000000, CompletionTokens: 1000000, CacheReadTokens: 400000, CacheWriteTokens: 100000}, 9.5},
		{"all cached", "cached", Usage{PromptTokens: 1000000, CacheReadTokens: 1000000}, .5},
		{"nil cache is full input", "default-cache", Usage{PromptTokens: 1000000, CacheReadTokens: 400000, CacheWriteTokens: 100000}, 2},
		{"explicit zero cache", "free-cache", Usage{PromptTokens: 1000000, CacheReadTokens: 400000, CacheWriteTokens: 600000}, 0},
		{"no tokens", "cached", Usage{}, 0},
		{"single token", "cached", Usage{CompletionTokens: 1}, .000008},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := catalog.Estimate("custom", tc.model, tc.usage)
			if err != nil || math.Abs(got-tc.want) > 1e-12 {
				t.Fatalf("got %g, %v; want %g", got, err, tc.want)
			}
		})
	}
}

func TestUnknownVersusFreeAndExactIdentity(t *testing.T) {
	c, err := NewCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Estimate("zai", "glm-4.7-flash", Usage{PromptTokens: 1000000}); got != 0 || err != nil {
		t.Fatalf("free model: %g, %v", got, err)
	}
	for _, key := range [][2]string{
		{"openai", "unknown"}, {"openai", "gpt-4o-unknown"}, {"OpenAI", "gpt-4o"},
		{"openai", "GPT-4o"}, {"openai", " gpt-4o"}, {"other", "gpt-4o"},
		{"anthropic", "anthropic/claude-sonnet-4-6"},
	} {
		if _, err := c.Estimate(key[0], key[1], Usage{}); !errors.Is(err, ErrUnknownModel) {
			t.Fatalf("%v: expected unknown, got %v", key, err)
		}
	}
	for _, c := range []*Catalog{nil, {}} {
		if _, err := c.Estimate("openai", "gpt-4o", Usage{}); !errors.Is(err, ErrUnknownModel) {
			t.Fatalf("empty catalog: %v", err)
		}
	}
}

func TestInvalidUsageAndOverflow(t *testing.T) {
	c, err := NewCatalog(overrideFile(t, `[{"provider":"huge","model":"huge","input_per_million":1e308,"output_per_million":1e308}]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, usage := range []Usage{
		{PromptTokens: -1}, {CompletionTokens: -1}, {CacheReadTokens: -1}, {CacheWriteTokens: -1},
		{PromptTokens: 1, CacheReadTokens: 2}, {PromptTokens: 1, CacheWriteTokens: 2},
		{PromptTokens: 3, CacheReadTokens: 2, CacheWriteTokens: 2},
		{PromptTokens: math.MaxInt64, CacheReadTokens: math.MaxInt64, CacheWriteTokens: math.MaxInt64},
		{PromptTokens: math.MaxInt64},
	} {
		if _, err := c.Estimate("huge", "huge", usage); err == nil {
			t.Fatalf("accepted invalid/overflow usage: %+v", usage)
		}
	}
	// Large token counts alone are valid and must not overflow integer arithmetic.
	if got, err := c.Estimate("openai", "gpt-4o", Usage{PromptTokens: math.MaxInt64, CacheReadTokens: math.MaxInt64}); err != nil || math.IsInf(got, 0) {
		t.Fatalf("large valid usage: %g, %v", got, err)
	}
	if got, err := c.Estimate("huge", "huge", Usage{PromptTokens: 1}); err != nil || math.IsInf(got, 0) {
		t.Fatalf("finite high rate with one token: %g, %v", got, err)
	}
}

func TestOverridesReplaceExactKey(t *testing.T) {
	c, err := NewCatalog(overrideFile(t, `[
		{"provider":"openai","model":"gpt-4o","input_per_million":0,"output_per_million":0},
		{"provider":"custom","model":"gpt-4o","input_per_million":3,"output_per_million":4},
		{"provider":"custom","model":"gpt-4o-extra","input_per_million":5,"output_per_million":6}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		provider string
		model    string
		want     float64
	}{{"openai", "gpt-4o", 0}, {"custom", "gpt-4o", 7}, {"custom", "gpt-4o-extra", 11}} {
		got, err := c.Estimate(tc.provider, tc.model, Usage{PromptTokens: 1000000, CompletionTokens: 1000000})
		if err != nil || got != tc.want {
			t.Fatalf("%+v: got %g, %v", tc, got, err)
		}
	}
	if _, err := c.Estimate("custom", "gpt-4o-extras", Usage{}); !errors.Is(err, ErrUnknownModel) {
		t.Fatal(err)
	}
}

func TestInvalidOverrides(t *testing.T) {
	for _, body := range []string{
		``, `{}`, `null`, `[null]`, `[] []`, `[] garbage`, `[`,
		`[{"provider":"p","model":"m","input_per_million":0}]`,
		`[{"provider":"p","model":"m","input_per_million":null,"output_per_million":0}]`,
		`[{"provider":"","model":"m","input_per_million":0,"output_per_million":0}]`,
		`[{"provider":"p","model":" ","input_per_million":0,"output_per_million":0}]`,
		`[{"provider":"p","model":"m","input_per_million":-1,"output_per_million":0}]`,
		`[{"provider":"p","model":"m","input_per_million":0,"output_per_million":-1}]`,
		`[{"provider":"p","model":"m","input_per_million":0,"output_per_million":0,"cache_read_per_million":-1}]`,
		`[{"provider":"p","model":"m","input_per_million":0,"output_per_million":0,"cache_write_per_million":-1}]`,
		`[{"provider":"p","model":"m","input_per_million":1e400,"output_per_million":0}]`,
		`[{"provider":"p","model":"m","input_per_million":NaN,"output_per_million":0}]`,
		`[{"provider":"p","model":"m","input_per_million":0,"output_per_million":0,"typo":1}]`,
		`[{"provider":"p","model":"m","input_per_million":0,"output_per_million":0},{"provider":"p","model":"m","input_per_million":1,"output_per_million":1}]`,
		strings.Repeat(" ", maxOverrideBytes+1),
	} {
		if _, err := NewCatalog(overrideFile(t, body)); err == nil {
			t.Errorf("accepted invalid override: %.100s", body)
		}
	}
	if _, err := NewCatalog(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("accepted missing override file")
	}
	if _, err := NewCatalog(overrideFile(t, `[]`)); err != nil {
		t.Fatalf("empty array should preserve built-ins: %v", err)
	}
}

func TestRateValidationRejectsNonfinite(t *testing.T) {
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		for field := range 4 {
			r := Rate{Provider: "p", Model: "m"}
			switch field {
			case 0:
				r.InputPerMillion = invalid
			case 1:
				r.OutputPerMillion = invalid
			case 2:
				r.CacheReadPerMillion = &invalid
			case 3:
				r.CacheWritePerMillion = &invalid
			}
			if err := validateRate(r); err == nil {
				t.Fatalf("accepted nonfinite/negative rate in field %d", field)
			}
		}
	}
}

func TestExplicitDeepSeekAliases(t *testing.T) {
	c, err := NewCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	usage := Usage{PromptTokens: 1000000, CompletionTokens: 1000000, CacheReadTokens: 500000}
	want, err := c.Estimate("deepseek", "deepseek-flash", usage)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"deepseek-v4-flash", "deepseek-v4-flash-vision-exp"} {
		if got, err := c.Estimate("deepseek", model, usage); err != nil || got != want {
			t.Fatalf("explicit alias %s: %g, %v", model, got, err)
		}
	}
}
