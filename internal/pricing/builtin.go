package pricing

// Built-in USD/MTok snapshot verified 2026-11-23 against official pages:
// OpenAI: https://developers.openai.com/api/docs/pricing (Standard)
// Anthropic: https://platform.claude.com/docs/en/about-claude/pricing
// Gemini: https://ai.google.dev/gemini-api/docs/pricing (paid Standard text)
// Z.ai: https://docs.z.ai/guides/overview/pricing
// DeepSeek: https://api-docs.deepseek.com/quick_start/pricing/ (PEAK)
//
// Anthropic cache writes assume the default 5-minute lifetime, not 1 hour.
// Gemini storage and audio pricing are not represented. Gemini 2.5 Flash's
// announced December 1, 2026 reroute is NOT applied to this dated snapshot.
// DeepSeek uses published peak rates because Usage has no request timestamp;
// off-peak usage costs less. Legacy Flash names below are explicit aliases
// documented on the official pricing page, NOT prefix matches.
// No context-tiered OpenAI/Gemini models are included: aggregated Usage cannot
// determine per-request context tiers. Overrides can select a known tier.
func builtinRates() []Rate {
	price := func(v float64) *float64 { return &v }
	return []Rate{
		{Provider: "openai", Model: "gpt-4o", InputPerMillion: 2.5, OutputPerMillion: 10, CacheReadPerMillion: price(1.25)},
		{Provider: "openai", Model: "gpt-4o-mini", InputPerMillion: .15, OutputPerMillion: .6, CacheReadPerMillion: price(.075)},
		{Provider: "openai", Model: "gpt-4.1", InputPerMillion: 2, OutputPerMillion: 8, CacheReadPerMillion: price(.5)},
		{Provider: "openai", Model: "gpt-4.1-mini", InputPerMillion: .4, OutputPerMillion: 1.6, CacheReadPerMillion: price(.1)},
		{Provider: "openai", Model: "gpt-5", InputPerMillion: 1.25, OutputPerMillion: 10, CacheReadPerMillion: price(.125)},
		{Provider: "openai", Model: "gpt-5-mini", InputPerMillion: .25, OutputPerMillion: 2, CacheReadPerMillion: price(.025)},
		{Provider: "openai", Model: "gpt-5-nano", InputPerMillion: .05, OutputPerMillion: .4, CacheReadPerMillion: price(.005)},
		{Provider: "openai", Model: "gpt-5.1", InputPerMillion: 1.25, OutputPerMillion: 10, CacheReadPerMillion: price(.125)},
		{Provider: "openai", Model: "gpt-5.2", InputPerMillion: 1.75, OutputPerMillion: 14, CacheReadPerMillion: price(.175)},
		{Provider: "openai", Model: "o3", InputPerMillion: 2, OutputPerMillion: 8, CacheReadPerMillion: price(.5)},
		{Provider: "anthropic", Model: "claude-sonnet-4-6", InputPerMillion: 3, OutputPerMillion: 15, CacheReadPerMillion: price(.3), CacheWritePerMillion: price(3.75)},
		{Provider: "anthropic", Model: "claude-sonnet-4-5", InputPerMillion: 3, OutputPerMillion: 15, CacheReadPerMillion: price(.3), CacheWritePerMillion: price(3.75)},
		{Provider: "anthropic", Model: "claude-opus-4-6", InputPerMillion: 5, OutputPerMillion: 25, CacheReadPerMillion: price(.5), CacheWritePerMillion: price(6.25)},
		{Provider: "anthropic", Model: "claude-opus-4-5", InputPerMillion: 5, OutputPerMillion: 25, CacheReadPerMillion: price(.5), CacheWritePerMillion: price(6.25)},
		{Provider: "anthropic", Model: "claude-haiku-4-5", InputPerMillion: 1, OutputPerMillion: 5, CacheReadPerMillion: price(.1), CacheWritePerMillion: price(1.25)},
		{Provider: "gemini", Model: "gemini-2.5-flash", InputPerMillion: .3, OutputPerMillion: 2.5, CacheReadPerMillion: price(.03)},
		{Provider: "gemini", Model: "gemini-2.5-flash-lite", InputPerMillion: .1, OutputPerMillion: .4, CacheReadPerMillion: price(.01)},
		{Provider: "zai", Model: "glm-4.5", InputPerMillion: .6, OutputPerMillion: 2.2, CacheReadPerMillion: price(.11)},
		{Provider: "zai", Model: "glm-4.5-air", InputPerMillion: .2, OutputPerMillion: 1.1, CacheReadPerMillion: price(.03)},
		{Provider: "zai", Model: "glm-4.5-flash", InputPerMillion: 0, OutputPerMillion: 0, CacheReadPerMillion: price(0)},
		{Provider: "zai", Model: "glm-4.6", InputPerMillion: .6, OutputPerMillion: 2.2, CacheReadPerMillion: price(.11)},
		{Provider: "zai", Model: "glm-4.7", InputPerMillion: .6, OutputPerMillion: 2.2, CacheReadPerMillion: price(.11)},
		{Provider: "zai", Model: "glm-4.7-flash", InputPerMillion: 0, OutputPerMillion: 0, CacheReadPerMillion: price(0)},
		{Provider: "zai", Model: "glm-5", InputPerMillion: 1, OutputPerMillion: 3.2, CacheReadPerMillion: price(.2)},
		{Provider: "deepseek", Model: "deepseek-flash", InputPerMillion: .3, OutputPerMillion: 1.2, CacheReadPerMillion: price(.006)},
		{Provider: "deepseek", Model: "deepseek-v4-flash", InputPerMillion: .3, OutputPerMillion: 1.2, CacheReadPerMillion: price(.006)},
		{Provider: "deepseek", Model: "deepseek-v4-flash-vision-exp", InputPerMillion: .3, OutputPerMillion: 1.2, CacheReadPerMillion: price(.006)},
		{Provider: "deepseek", Model: "deepseek-v4-pro", InputPerMillion: 1.32, OutputPerMillion: 3.96, CacheReadPerMillion: price(.044)},
	}
}
