package store

import (
	"context"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// All accounts deliberately reuse timestamps and quota names. Account B is
// inserted last and has the largest utilization, exposing unscoped MAX/peak reads.
func TestProviderAccountIsolation(t *testing.T) {
	at := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	t.Run("cursor", func(t *testing.T) {
		root, err := New(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		stores := []*Store{root, root.ForAccount(71), root.ForAccount(72)}
		for i, s := range stores {
			value := float64(10 + i*30)
			_, err := s.InsertCursorSnapshot(&api.CursorSnapshot{CapturedAt: at, Quotas: []api.CursorQuota{{Name: "shared", Used: value, Limit: 100, Utilization: value}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateCursorCycle("shared", at.Add(-time.Hour), nil); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			want := float64(10 + i*30)
			latest, err := s.QueryLatestCursor()
			if err != nil {
				t.Fatal(err)
			}
			if latest == nil || len(latest.Quotas) != 1 || latest.Quotas[0].Utilization != want {
				t.Fatalf("account %d latest = %+v", i, latest)
			}
			for _, limit := range []int{0, 1} {
				history, err := s.QueryCursorRange(at.Add(-time.Minute), at.Add(time.Minute), limit)
				if err != nil {
					t.Fatal(err)
				}
				if len(history) != 1 || len(history[0].Quotas) != 1 || history[0].Quotas[0].Utilization != want {
					t.Fatalf("account %d range = %+v", i, history)
				}
			}
			quotas, err := s.QueryCursorLatestPerQuota()
			if err != nil {
				t.Fatal(err)
			}
			if len(quotas) != 1 || quotas[0].Utilization != want {
				t.Fatalf("account %d quotas = %+v", i, quotas)
			}
			series, err := s.QueryCursorUtilizationSeries("shared", at.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(series) != 1 || series[0].Utilization != want {
				t.Fatalf("account %d series = %+v", i, series)
			}
			overview, err := s.QueryCursorCycleOverview("shared", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(overview) != 1 || len(overview[0].CrossQuotas) != 1 || overview[0].CrossQuotas[0].Percent != want {
				t.Fatalf("account %d overview = %+v", i, overview)
			}
			if err := s.CloseCursorCycle("shared", at.Add(time.Hour), want, want); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			history, err := s.QueryCursorCycleHistory("shared")
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 || history[0].PeakUtilization != float64(10+i*30) {
				t.Fatalf("account %d cycles = %+v", i, history)
			}
		}
	})
	t.Run("opencode", func(t *testing.T) {
		root, err := New(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		stores := []*Store{root, root.ForAccount(71), root.ForAccount(72)}
		for i, s := range stores {
			value := float64(10 + i*30)
			_, err := s.InsertOpenCodeSnapshot(&api.OpenCodeSnapshot{CapturedAt: at, Quotas: []api.OpenCodeQuota{{Name: "shared", Used: value, Limit: 100, Utilization: value}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateOpenCodeCycle("shared", at.Add(-time.Hour), nil); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			want := float64(10 + i*30)
			latest, err := s.QueryLatestOpenCode()
			if err != nil {
				t.Fatal(err)
			}
			if latest == nil || len(latest.Quotas) != 1 || latest.Quotas[0].Utilization != want {
				t.Fatalf("account %d latest = %+v", i, latest)
			}
			for _, limit := range []int{0, 1} {
				history, err := s.QueryOpenCodeRange(at.Add(-time.Minute), at.Add(time.Minute), limit)
				if err != nil {
					t.Fatal(err)
				}
				if len(history) != 1 || len(history[0].Quotas) != 1 || history[0].Quotas[0].Utilization != want {
					t.Fatalf("account %d range = %+v", i, history)
				}
			}
			quotas, err := s.QueryOpenCodeLatestPerQuota()
			if err != nil {
				t.Fatal(err)
			}
			if len(quotas) != 1 || quotas[0].Utilization != want {
				t.Fatalf("account %d quotas = %+v", i, quotas)
			}
			series, err := s.QueryOpenCodeUtilizationSeries("shared", at.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(series) != 1 || series[0].Utilization != want {
				t.Fatalf("account %d series = %+v", i, series)
			}
			overview, err := s.QueryOpenCodeCycleOverview("shared", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(overview) != 1 || len(overview[0].CrossQuotas) != 1 || overview[0].CrossQuotas[0].Percent != want {
				t.Fatalf("account %d overview = %+v", i, overview)
			}
			if err := s.CloseOpenCodeCycle("shared", at.Add(time.Hour), want, want); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			history, err := s.QueryOpenCodeCycleHistory("shared")
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 || history[0].PeakUtilization != float64(10+i*30) {
				t.Fatalf("account %d cycles = %+v", i, history)
			}
		}
	})
	t.Run("ollama", func(t *testing.T) {
		root, err := New(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		stores := []*Store{root, root.ForAccount(71), root.ForAccount(72)}
		for i, s := range stores {
			value := float64(10 + i*30)
			_, err := s.InsertOllamaSnapshot(&api.OllamaSnapshot{CapturedAt: at, Quotas: []api.OllamaQuota{{Name: "shared", Used: value, Limit: 100, Utilization: value}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateOllamaCycle("shared", at.Add(-time.Hour), nil); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			want := float64(10 + i*30)
			latest, err := s.QueryLatestOllama()
			if err != nil {
				t.Fatal(err)
			}
			if latest == nil || len(latest.Quotas) != 1 || latest.Quotas[0].Utilization != want {
				t.Fatalf("account %d latest = %+v", i, latest)
			}
			for _, limit := range []int{0, 1} {
				history, err := s.QueryOllamaRange(at.Add(-time.Minute), at.Add(time.Minute), limit)
				if err != nil {
					t.Fatal(err)
				}
				if len(history) != 1 || len(history[0].Quotas) != 1 || history[0].Quotas[0].Utilization != want {
					t.Fatalf("account %d range = %+v", i, history)
				}
			}
			quotas, err := s.QueryOllamaLatestPerQuota()
			if err != nil {
				t.Fatal(err)
			}
			if len(quotas) != 1 || quotas[0].Utilization != want {
				t.Fatalf("account %d quotas = %+v", i, quotas)
			}
			series, err := s.QueryOllamaUtilizationSeries("shared", at.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(series) != 1 || series[0].Utilization != want {
				t.Fatalf("account %d series = %+v", i, series)
			}
			overview, err := s.QueryOllamaCycleOverview("shared", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(overview) != 1 || len(overview[0].CrossQuotas) != 1 || overview[0].CrossQuotas[0].Percent != want {
				t.Fatalf("account %d overview = %+v", i, overview)
			}
			if err := s.CloseOllamaCycle("shared", at.Add(time.Hour), want, want); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			history, err := s.QueryOllamaCycleHistory("shared")
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 || history[0].PeakUtilization != float64(10+i*30) {
				t.Fatalf("account %d cycles = %+v", i, history)
			}
		}
	})
	t.Run("muse", func(t *testing.T) {
		root, err := New(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		stores := []*Store{root, root.ForAccount(71), root.ForAccount(72)}
		for i, s := range stores {
			value := float64(10 + i*30)
			_, err := s.InsertMuseSnapshot(&api.MuseSnapshot{CapturedAt: at, Quotas: []api.MuseQuota{{Name: "shared", Used: value, Limit: 100, Utilization: value}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateMuseCycle("shared", at.Add(-time.Hour), nil); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			want := float64(10 + i*30)
			latest, err := s.QueryLatestMuse()
			if err != nil {
				t.Fatal(err)
			}
			if latest == nil || len(latest.Quotas) != 1 || latest.Quotas[0].Utilization != want {
				t.Fatalf("account %d latest = %+v", i, latest)
			}
			for _, limit := range []int{0, 1} {
				history, err := s.QueryMuseRange(at.Add(-time.Minute), at.Add(time.Minute), limit)
				if err != nil {
					t.Fatal(err)
				}
				if len(history) != 1 || len(history[0].Quotas) != 1 || history[0].Quotas[0].Utilization != want {
					t.Fatalf("account %d range = %+v", i, history)
				}
			}
			quotas, err := s.QueryMuseLatestPerQuota()
			if err != nil {
				t.Fatal(err)
			}
			if len(quotas) != 1 || quotas[0].Utilization != want {
				t.Fatalf("account %d quotas = %+v", i, quotas)
			}
			series, err := s.QueryMuseUtilizationSeries("shared", at.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(series) != 1 || series[0].Utilization != want {
				t.Fatalf("account %d series = %+v", i, series)
			}
			overview, err := s.QueryMuseCycleOverview("shared", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(overview) != 1 || len(overview[0].CrossQuotas) != 1 || overview[0].CrossQuotas[0].Percent != want {
				t.Fatalf("account %d overview = %+v", i, overview)
			}
			if err := s.CloseMuseCycle("shared", at.Add(time.Hour), want, want); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			history, err := s.QueryMuseCycleHistory("shared")
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 || history[0].PeakUtilization != float64(10+i*30) {
				t.Fatalf("account %d cycles = %+v", i, history)
			}
		}
	})
	t.Run("commandcode", func(t *testing.T) {
		root, err := New(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		stores := []*Store{root, root.ForAccount(71), root.ForAccount(72)}
		for i, s := range stores {
			value := float64(10 + i*30)
			_, err := s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{CapturedAt: at, Quotas: []api.CommandCodeQuota{{Name: "shared", Used: value, Limit: 100, Utilization: value}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateCommandCodeCycle("shared", at.Add(-time.Hour), nil); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			want := float64(10 + i*30)
			latest, err := s.QueryLatestCommandCode()
			if err != nil {
				t.Fatal(err)
			}
			if latest == nil || len(latest.Quotas) != 1 || latest.Quotas[0].Utilization != want {
				t.Fatalf("account %d latest = %+v", i, latest)
			}
			for _, limit := range []int{0, 1} {
				history, err := s.QueryCommandCodeRange(at.Add(-time.Minute), at.Add(time.Minute), limit)
				if err != nil {
					t.Fatal(err)
				}
				if len(history) != 1 || len(history[0].Quotas) != 1 || history[0].Quotas[0].Utilization != want {
					t.Fatalf("account %d range = %+v", i, history)
				}
			}
			quotas, err := s.QueryCommandCodeLatestPerQuota()
			if err != nil {
				t.Fatal(err)
			}
			if len(quotas) != 1 || quotas[0].Utilization != want {
				t.Fatalf("account %d quotas = %+v", i, quotas)
			}
			series, err := s.QueryCommandCodeUtilizationSeries("shared", at.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(series) != 1 || series[0].Utilization != want {
				t.Fatalf("account %d series = %+v", i, series)
			}
			overview, err := s.QueryCommandCodeCycleOverview("shared", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(overview) != 1 || len(overview[0].CrossQuotas) != 1 || overview[0].CrossQuotas[0].Percent != want {
				t.Fatalf("account %d overview = %+v", i, overview)
			}
			if err := s.CloseCommandCodeCycle("shared", at.Add(time.Hour), want, want); err != nil {
				t.Fatal(err)
			}
		}
		for i, s := range stores {
			history, err := s.QueryCommandCodeCycleHistory("shared")
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 || history[0].PeakUtilization != float64(10+i*30) {
				t.Fatalf("account %d cycles = %+v", i, history)
			}
		}
	})
}

func TestGrokScopedArgumentsAndCycleIDs(t *testing.T) {
	root, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	at := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	a, b := root.ForAccount(71), root.ForAccount(72)
	var bCycle int64
	for i, s := range []*Store{a, b} {
		if _, err := s.InsertGrokSnapshot(&api.GrokSnapshot{AccountID: 999, CapturedAt: at, Quotas: []api.GrokQuota{{Name: "shared", Utilization: float64(i + 1)}}}); err != nil {
			t.Fatal(err)
		}
		id, err := s.InsertGrokResetCycle(&GrokResetCycle{AccountID: 999, QuotaName: "shared", CycleStart: at})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			bCycle = id
		}
	}
	latest, err := a.QueryLatestGrok(72)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.AccountID != 71 || latest.Quotas[0].Utilization != 1 {
		t.Fatalf("scoped latest = %+v", latest)
	}
	sampled, err := a.QueryGrokRangeSampled(72, at.Add(-time.Minute), at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(sampled) != 1 || sampled[0].AccountID != 71 {
		t.Fatalf("sampled = %+v", sampled)
	}
	if err := a.UpdateGrokResetCycleEnd(bCycle, at.Add(time.Hour), 99, 99); err != nil {
		t.Fatal(err)
	}
	active, err := b.QueryActiveGrokResetCycle(71, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if active == nil || active.ID != bCycle {
		t.Fatalf("foreign cycle was closed: %+v", active)
	}
	legacy, err := root.QueryLatestGrok(71)
	if err != nil {
		t.Fatal(err)
	}
	if legacy == nil || legacy.AccountID != 71 {
		t.Fatalf("root explicit account = %+v", legacy)
	}
}

func TestKimiScopedArgumentsAndCycleIDs(t *testing.T) {
	root, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	at := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	a, b := root.ForAccount(71), root.ForAccount(72)
	var bCycle int64
	for i, s := range []*Store{a, b} {
		if _, err := s.InsertKimiSnapshot(&api.KimiSnapshot{AccountID: 999, CapturedAt: at, Quotas: []api.KimiQuota{{Name: "shared", Utilization: float64(i + 1)}}}); err != nil {
			t.Fatal(err)
		}
		id, err := s.InsertKimiResetCycle(&KimiResetCycle{AccountID: 999, QuotaName: "shared", CycleStart: at})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			bCycle = id
		}
	}
	latest, err := a.QueryLatestKimi(72)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.AccountID != 71 || latest.Quotas[0].Utilization != 1 {
		t.Fatalf("scoped latest = %+v", latest)
	}
	sampled, err := a.QueryKimiRangeSampled(72, at.Add(-time.Minute), at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(sampled) != 1 || sampled[0].AccountID != 71 {
		t.Fatalf("sampled = %+v", sampled)
	}
	if err := a.UpdateKimiResetCycleEnd(bCycle, at.Add(time.Hour), 99, 99); err != nil {
		t.Fatal(err)
	}
	active, err := b.QueryActiveKimiResetCycle(71, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if active == nil || active.ID != bCycle {
		t.Fatalf("foreign cycle was closed: %+v", active)
	}
	legacy, err := root.QueryLatestKimi(71)
	if err != nil {
		t.Fatal(err)
	}
	if legacy == nil || legacy.AccountID != 71 {
		t.Fatalf("root explicit account = %+v", legacy)
	}
}

func TestMistralAccountIdentityAndBillingIsolation(t *testing.T) {
	root, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx := context.Background()
	at := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	stores := []*Store{root, root.ForAccount(71), root.ForAccount(72)}
	for i, s := range stores {
		amount := float64(i + 1)
		q := api.MistralQuota{Name: "api_included", Used: amount, Limit: 100, Utilization: amount, CapturedAt: at}
		if err := s.SaveMistral(ctx, &api.MistralSnapshot{Identity: "same-identity", CapturedAt: at, Quotas: []api.MistralQuota{q}, Billing: &api.MistralBilling{Amount: &amount, Currency: "USD", CapturedAt: at}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.TrackMistral(ctx, "same-identity", q); err != nil {
			t.Fatal(err)
		}
	}
	for i, s := range stores {
		want := float64(i + 1)
		snap, err := s.LatestMistral(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if snap == nil || len(snap.Quotas) != 1 || snap.Quotas[0].Used != want || snap.Billing == nil || *snap.Billing.Amount != want {
			t.Fatalf("account %d latest = %+v", i, snap)
		}
		history, err := s.MistralHistory(ctx, at.Add(-time.Minute), at.Add(time.Minute), 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || history[0].Quotas[0].Used != want {
			t.Fatalf("account %d history = %+v", i, history)
		}
		cycles, err := s.MistralCycles(ctx, "api_included", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(cycles) != 1 || cycles[0].PeakUtilization != want {
			t.Fatalf("account %d cycles = %+v", i, cycles)
		}
	}
}

func TestZenmuxScopedSampledAndRecentHistory(t *testing.T) {
	root, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	at := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	stores := []*Store{root, root.ForAccount(71), root.ForAccount(72)}
	ids := make([]int64, len(stores))
	for i, s := range stores {
		ids[i], err = s.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: at})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, s := range stores {
		latest, err := s.QueryLatestZenmux()
		if err != nil {
			t.Fatal(err)
		}
		if latest == nil || latest.ID != ids[i] {
			t.Fatalf("account %d latest = %+v", i, latest)
		}
		recent, err := s.QueryRecentZenmux(context.Background(), at.Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if len(recent) != 1 || recent[0].ID != ids[i] {
			t.Fatalf("account %d recent = %+v", i, recent)
		}
		sampled, err := s.QueryZenmuxChartRange(at.Add(-time.Minute), at.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if len(sampled) != 1 || sampled[0].ID != ids[i] {
			t.Fatalf("account %d sampled = %+v", i, sampled)
		}
	}
}
