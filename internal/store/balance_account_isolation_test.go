package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestBalanceProviderAccountIsolation(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for _, provider := range []string{"openrouter", "moonshot", "deepseek"} {
		t.Run(provider, func(t *testing.T) {
			root, err := New(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			stores := []*Store{root, root.ForAccount(101), root.ForAccount(202)}
			for i, s := range stores {
				value := float64(i + 1)
				var insertErr error
				switch provider {
				case "openrouter":
					_, insertErr = s.InsertOpenRouterSnapshot(&api.OpenRouterSnapshot{CapturedAt: base, Usage: value})
				case "moonshot":
					_, insertErr = s.InsertMoonshotSnapshot(&api.MoonshotSnapshot{CapturedAt: base, AvailableBalance: value})
				case "deepseek":
					_, insertErr = s.InsertDeepSeekSnapshot(&api.DeepSeekSnapshot{CapturedAt: base, Currency: "USD", TotalBalance: value})
				}
				if insertErr != nil {
					t.Fatal(insertErr)
				}
				var cycleErr error
				switch provider {
				case "openrouter":
					_, cycleErr = s.CreateOpenRouterCycle("usage", base)
				case "moonshot":
					_, cycleErr = s.CreateMoonshotCycle("usage", base)
				case "deepseek":
					_, cycleErr = s.CreateDeepSeekCycle("usage", "USD", base)
				}
				if cycleErr != nil {
					t.Fatal(cycleErr)
				}
			}
			// The newest-N subquery and SQL sampling must filter before limiting/ranking.
			for i, s := range stores {
				want := float64(i + 1)
				switch provider {
				case "openrouter":
					latest, err := s.QueryLatestOpenRouter()
					if err != nil || latest == nil || latest.Usage != want {
						t.Fatalf("latest: %+v, %v", latest, err)
					}
					rows, err := s.QueryOpenRouterRange(base.Add(-time.Hour), base.Add(time.Hour), 1)
					if err != nil || len(rows) != 1 || rows[0].Usage != want {
						t.Fatalf("range: %+v, %v", rows, err)
					}
					rows, err = s.QueryOpenRouterRangeSampled(base.Add(-time.Hour), base.Add(time.Hour))
					if err != nil || len(rows) != 1 || rows[0].Usage != want {
						t.Fatalf("sampled: %+v, %v", rows, err)
					}
				case "moonshot":
					latest, err := s.QueryLatestMoonshot()
					if err != nil || latest == nil || latest.AvailableBalance != want {
						t.Fatalf("latest: %+v, %v", latest, err)
					}
					rows, err := s.QueryMoonshotRange(base.Add(-time.Hour), base.Add(time.Hour), 1)
					if err != nil || len(rows) != 1 || rows[0].AvailableBalance != want {
						t.Fatalf("range: %+v, %v", rows, err)
					}
					rows, err = s.QueryMoonshotRangeSampled(base.Add(-time.Hour), base.Add(time.Hour))
					if err != nil || len(rows) != 1 || rows[0].AvailableBalance != want {
						t.Fatalf("sampled: %+v, %v", rows, err)
					}
				case "deepseek":
					latest, err := s.QueryLatestDeepSeek()
					if err != nil || latest == nil || latest.TotalBalance != want {
						t.Fatalf("latest: %+v, %v", latest, err)
					}
					rows, err := s.QueryDeepSeekRange(base.Add(-time.Hour), base.Add(time.Hour), 1)
					if err != nil || len(rows) != 1 || rows[0].TotalBalance != want {
						t.Fatalf("range: %+v, %v", rows, err)
					}
					rows, err = s.QueryDeepSeekRangeSampled(base.Add(-time.Hour), base.Add(time.Hour))
					if err != nil || len(rows) != 1 || rows[0].TotalBalance != want {
						t.Fatalf("sampled: %+v, %v", rows, err)
					}
				}
			}
			for i, s := range stores {
				var updateErr, closeErr error
				switch provider {
				case "openrouter":
					updateErr = s.UpdateOpenRouterCycle("usage", float64(i+10), 1)
					closeErr = s.CloseOpenRouterCycle("usage", base.Add(time.Hour), float64(i+20), 2)
				case "moonshot":
					updateErr = s.UpdateMoonshotCycle("usage", float64(i+10), 1)
					closeErr = s.CloseMoonshotCycle("usage", base.Add(time.Hour), float64(i+20), 2)
				case "deepseek":
					updateErr = s.UpdateDeepSeekCycle("usage", "USD", float64(i+10), 1)
					closeErr = s.CloseDeepSeekCycle("usage", "USD", base.Add(time.Hour), float64(i+20), 2)
				}
				if updateErr != nil || closeErr != nil {
					t.Fatalf("mutations: %v, %v", updateErr, closeErr)
				}
				// Closing this scope must not close any scope not yet visited.
				for j := i + 1; j < len(stores); j++ {
					var active any
					var err error
					switch provider {
					case "openrouter":
						c, e := stores[j].QueryActiveOpenRouterCycle("usage")
						err = e
						if c != nil {
							active = c
						}
					case "moonshot":
						c, e := stores[j].QueryActiveMoonshotCycle("usage")
						err = e
						if c != nil {
							active = c
						}
					case "deepseek":
						c, e := stores[j].QueryActiveDeepSeekCycle("usage", "USD")
						err = e
						if c != nil {
							active = c
						}
					}
					if err != nil || active == nil {
						t.Fatalf("other account cycle: %v, %v", active, err)
					}
				}
			}
			for i, s := range stores {
				var peaks []float64
				var err error
				switch provider {
				case "openrouter":
					rows, e := s.QueryOpenRouterCycleHistory("usage", 1)
					err = e
					for _, r := range rows {
						peaks = append(peaks, r.PeakUsage)
					}
				case "moonshot":
					rows, e := s.QueryMoonshotCycleHistory("usage", 1)
					err = e
					for _, r := range rows {
						peaks = append(peaks, r.PeakUsage)
					}
				case "deepseek":
					rows, e := s.QueryDeepSeekCycleHistory("usage", "USD", 1)
					err = e
					for _, r := range rows {
						peaks = append(peaks, r.PeakUsage)
					}
				}
				if err != nil || len(peaks) != 1 || peaks[0] != float64(i+20) {
					t.Fatal(fmt.Sprintf("history: %v, %v", peaks, err))
				}
			}
		})
	}
}

func TestGeminiAndAntigravityAccountIsolation(t *testing.T) {
	t.Parallel()
	root, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	stores := []*Store{root, root.ForAccount(101), root.ForAccount(202)}
	for i, s := range stores {
		usage := float64((i + 1) * 10)
		_, err := s.InsertGeminiSnapshot(&api.GeminiSnapshot{CapturedAt: base, Quotas: []api.GeminiQuota{{ModelID: "shared", UsagePercent: usage, RemainingFraction: 1 - usage/100}, {ModelID: fmt.Sprintf("only-%d", i), UsagePercent: usage}}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.InsertAntigravitySnapshot(&api.AntigravitySnapshot{CapturedAt: base, Models: []api.AntigravityModelQuota{{ModelID: "claude-shared", Label: "Claude", RemainingFraction: 1 - usage/100, RemainingPercent: 100 - usage}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CreateGeminiCycle("shared", base.Add(-time.Hour), nil); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CreateAntigravityCycle("claude-shared", base.Add(-time.Hour), nil); err != nil {
			t.Fatal(err)
		}
		if err = s.SaveGeminiTokens(fmt.Sprintf("access-%d", i), "refresh", 123); err != nil {
			t.Fatal(err)
		}
	}
	for i, s := range stores {
		usage := float64((i + 1) * 10)
		g, err := s.QueryLatestGemini()
		if err != nil || g == nil || len(g.Quotas) != 2 {
			t.Fatalf("latest Gemini: %+v, %v", g, err)
		}
		ids, err := s.QueryAllGeminiModelIDs()
		if err != nil || len(ids) != 2 || ids[0] != fmt.Sprintf("only-%d", i) {
			t.Fatalf("model IDs: %v, %v", ids, err)
		}
		gs, err := s.QueryGeminiRangeSampled(base.Add(-time.Hour), base.Add(time.Hour))
		if err != nil || len(gs) != 1 || gs[0].ID != g.ID {
			t.Fatalf("Gemini sampled: %+v, %v", gs, err)
		}
		series, err := s.QueryGeminiUsageSeries("shared", base)
		if err != nil || len(series) != 1 || series[0].RemainingFraction != 1-usage/100 {
			t.Fatalf("Gemini series: %v, %v", series, err)
		}
		overview, err := s.QueryGeminiCycleOverview("shared", 10)
		if err != nil || len(overview) != 1 || len(overview[0].CrossQuotas) != 2 {
			t.Fatalf("overview: %+v, %v", overview, err)
		}
		for _, q := range overview[0].CrossQuotas {
			if q.Value != usage {
				t.Fatalf("foreign peak quota: %+v", q)
			}
		}
		a, err := s.QueryAntigravitySnapshotAtOrBefore(base)
		if err != nil || a == nil || len(a.Models) != 1 || a.Models[0].RemainingPercent != 100-usage {
			t.Fatalf("at or before: %+v, %v", a, err)
		}
		as, err := s.QueryAntigravityRangeSampled(base.Add(-time.Hour), base.Add(time.Hour))
		if err != nil || len(as) != 1 || as[0].ID != a.ID {
			t.Fatalf("Antigravity sampled: %+v, %v", as, err)
		}
		count, _, _, err := s.QueryAntigravityRangeStats(base.Add(-time.Hour), base.Add(time.Hour))
		if err != nil || count != 1 {
			t.Fatalf("stats: %d, %v", count, err)
		}
		points, err := s.QueryAntigravityUsageSeries("claude-shared", base)
		if err != nil || len(points) != 1 || points[0].RemainingFraction != 1-usage/100 {
			t.Fatalf("Antigravity series: %v, %v", points, err)
		}
		access, _, _, err := s.LoadGeminiTokens()
		if err != nil || access != fmt.Sprintf("access-%d", i) {
			t.Fatalf("tokens: %q, %v", access, err)
		}
		if err = s.UpdateGeminiCycleResetTime("shared", &base); err != nil {
			t.Fatal(err)
		}
		if err = s.CloseGeminiCycle("shared", base.Add(time.Hour), usage, 1); err != nil {
			t.Fatal(err)
		}
		if err = s.UpdateAntigravityCycle("claude-shared", usage/100, 0.1); err != nil {
			t.Fatal(err)
		}
		if err = s.CloseAntigravityCycle("claude-shared", base.Add(time.Hour), usage/100, 0.1); err != nil {
			t.Fatal(err)
		}
	}
	for i, s := range stores {
		g, err := s.QueryGeminiCycleHistory("shared")
		if err != nil || len(g) != 1 || g[0].PeakUsage != float64((i+1)*10) {
			t.Fatalf("Gemini history: %+v, %v", g, err)
		}
		a, err := s.QueryAntigravityCycleHistory("claude-shared")
		if err != nil || len(a) != 1 || a[0].PeakUsage != float64(i+1)/10 {
			t.Fatalf("Antigravity history: %+v, %v", a, err)
		}
	}
}
