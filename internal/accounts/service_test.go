package accounts

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apiintegrations "github.com/onllm-dev/onwatch/v2/internal/api_integrations"
	"github.com/onllm-dev/onwatch/v2/internal/billing"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/pricing"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newAccountServiceFixture(t *testing.T, discovery bool, dir string) (*Service, *store.Store) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "no-omp-installation")
	}
	t.Setenv("ONWATCH_OMP_DIR", dir)
	path := filepath.Join(t.TempDir(), "pricing.json")
	if err := os.WriteFile(path, []byte(`[
		{"provider":"openai","model":"fixture-cached","input_per_million":2,"output_per_million":8,"cache_read_per_million":0.5,"cache_write_per_million":3},
		{"provider":"openai","model":"fixture-free","input_per_million":0,"output_per_million":0}
	]`), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := pricing.NewCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(st, &config.Config{OMPDiscoveryEnabled: discovery}, catalog, strings.Repeat("42", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func saveManualAccount(t *testing.T, s *Service, name, secret string) int64 {
	t.Helper()
	id, err := s.Save(Input{Provider: "openai", Name: name, Mode: "cost", BillingKind: "none", Credentials: map[string]string{"api_key": secret}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertAccountEvent(t *testing.T, st *store.Store, at time.Time, request, account, model string, id int64, input, output, read, write int, cost *float64) {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"ts": at.UTC().Format(time.RFC3339Nano), "integration": "service-regression", "provider": "openai", "account": account,
		"account_id": id, "model": model, "request_id": request, "prompt_tokens": input, "completion_tokens": output,
		"cache_read_tokens": read, "cache_write_tokens": write, "cost_usd": cost,
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := apiintegrations.ParseUsageEventLine(line, "service-regression.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertAPIIntegrationUsageEvent(event); err != nil {
		t.Fatal(err)
	}
}

func accountUsage(t *testing.T, s *Service, id int64) *Usage {
	t.Helper()
	usage, err := s.Usage(id, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return usage
}

func requireAccountAmount(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("cost is unknown; want %.9f", want)
	}
	if math.Abs(*got-want) > 1e-12 {
		t.Fatalf("cost = %.12f; want %.12f", *got, want)
	}
}

func TestServiceManualAccountsEncryptPreserveAndIsolateCredentials(t *testing.T) {
	s, st := newAccountServiceFixture(t, false, "")
	first := saveManualAccount(t, s, "personal", "fixture-personal-secret")
	second := saveManualAccount(t, s, "work", "fixture-work-secret")
	if first == second || first == 0 || second == 0 {
		t.Fatalf("independent accounts share identity: %d, %d", first, second)
	}
	for _, tc := range []struct {
		id           int64
		name, secret string
	}{{first, "personal", "fixture-personal-secret"}, {second, "work", "fixture-work-secret"}} {
		row, err := st.GetProviderAccountByID(tc.id)
		if err != nil || row == nil {
			t.Fatalf("stored account: %v, %v", row, err)
		}
		if strings.Contains(row.Metadata, "fixture-personal-secret") || strings.Contains(row.Metadata, "fixture-work-secret") {
			t.Fatal("stored metadata contains plaintext credentials")
		}
		stored, err := ParseSettings(row.Metadata)
		if err != nil || !strings.HasPrefix(stored.Credentials["api_key"], "enc:") {
			t.Fatalf("credential was not encrypted: %v", err)
		}
		resolved, err := s.Resolve(*row)
		if err != nil || resolved.Credentials["api_key"] != tc.secret || row.Name != tc.name {
			t.Fatalf("account %d did not resolve its own credential: %v", tc.id, err)
		}
	}
	updated, err := s.Save(Input{ID: first, Provider: "openai", Name: "personal renamed", Mode: "cost", Credentials: map[string]string{"api_key": ""}})
	if err != nil || updated != first {
		t.Fatalf("blank credential edit changed identity: %d, %v", updated, err)
	}
	for _, tc := range []struct {
		id     int64
		secret string
	}{{first, "fixture-personal-secret"}, {second, "fixture-work-secret"}} {
		_, settings, err := s.Get(tc.id)
		if err != nil || settings.Credentials["api_key"] != tc.secret {
			t.Fatalf("blank edit lost or crossed account credentials: account %d, %v", tc.id, err)
		}
	}
	listed, err := s.List()
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed accounts: %v, %v", listed, err)
	}
	for _, account := range listed {
		if account.Provider != "openai" || account.Source != "manual" || !account.HasCredential || account.Mode != "cost" {
			t.Fatalf("incorrect manual account view: %+v", account)
		}
	}
	encoded, err := json.Marshal(listed)
	if err != nil || strings.Contains(string(encoded), "fixture-personal-secret") || strings.Contains(string(encoded), "fixture-work-secret") || strings.Contains(string(encoded), "enc:") {
		t.Fatal("account list exposes credential material")
	}
}

// A key added by hand that an existing account (manual or discovered) already
// tracks would be polled twice against one rate limit and double-counted.
func TestServiceRejectsCredentialAlreadyTrackedByAnotherAccount(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.yml"), []byte("providers:\n  zai:\n    baseUrl: https://api.z.ai/api/coding/paas/v4\n    apiKey: fixture-discovered-zai\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, _ := newAccountServiceFixture(t, true, dir)
	_, err := s.Save(Input{Provider: "zai", Name: "manual copy", Mode: "quota", Credentials: map[string]string{"api_key": "fixture-discovered-zai"}})
	if err == nil || !strings.Contains(err.Error(), "omp / zai") {
		t.Fatalf("duplicate of a discovered key was accepted: %v", err)
	}
	id := saveManualAccount(t, s, "personal", "fixture-unique-openai")
	if _, err := s.Save(Input{Provider: "openai", Name: "second copy", Mode: "cost", BillingKind: "none", Credentials: map[string]string{"api_key": "fixture-unique-openai"}}); err == nil {
		t.Fatal("duplicate of a manual key was accepted")
	}
	// Re-entering an account's own key while editing it is not a duplicate.
	if _, err := s.Save(Input{ID: id, Provider: "openai", Name: "personal", Mode: "cost", BillingKind: "none", Credentials: map[string]string{"api_key": "fixture-unique-openai"}}); err != nil {
		t.Fatalf("editing an account with its own key failed: %v", err)
	}
}

// One omp provider can hold several logins; a reported call names only the
// provider, so binding it to either login would misattribute its cost.
func TestServiceResolveOMPProviderRefusesAmbiguousSources(t *testing.T) {
	s, st := newAccountServiceFixture(t, false, "")
	add := func(name, alias, external string) int64 {
		id, err := st.SaveManagedProviderAccount(0, "anthropic", name, `{"managed":true,"source":"omp","source_provider":"`+alias+`","mode":"cost"}`, external)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	single := add("omp / solo", "solo", "omp:x:anthropic:solo")
	if id, provider, ok := s.ResolveOMPProvider("solo"); !ok || id != single || provider != "anthropic" {
		t.Fatalf("unique omp provider not resolved: %d %q %v", id, provider, ok)
	}
	add("omp / anthropic (a)", "anthropic", "omp:x:anthropic:auth:1")
	add("omp / anthropic (b)", "anthropic", "omp:x:anthropic:auth:2")
	if id, _, ok := s.ResolveOMPProvider("anthropic"); ok {
		t.Fatalf("ambiguous omp provider bound to account %d", id)
	}
}

func TestServiceRenameRetainsHistoryAndAccountIDLinkage(t *testing.T) {
	s, st := newAccountServiceFixture(t, false, "")
	at := time.Now().UTC().Add(-time.Hour)
	oldCost, otherCost, newCost := 1.25, 9.0, 0.75
	// A pre-registry event must become bound before its account is renamed.
	insertAccountEvent(t, st, at, "pre-registry", "before", "fixture-cached", 0, 10, 1, 0, 0, &oldCost)
	id := saveManualAccount(t, s, "before", "fixture-first")
	other := saveManualAccount(t, s, "other", "fixture-other")
	insertAccountEvent(t, st, at, "other", "other", "fixture-cached", other, 900, 90, 0, 0, &otherCost)
	updated, err := s.Save(Input{ID: id, Provider: "openai", Name: "after", Mode: "cost"})
	if err != nil || updated != id {
		t.Fatalf("rename changed ID: %d, %v", updated, err)
	}
	// Explicit account_id wins even when a producer still sends the old label.
	insertAccountEvent(t, st, at, "stale-label", "before", "fixture-cached", id, 20, 2, 0, 0, &newCost)
	reused := saveManualAccount(t, s, "before", "fixture-reused-name")
	usage := accountUsage(t, s, id)
	if usage.AccountID != id || usage.Name != "after" || usage.Provider != "openai" {
		t.Fatalf("renamed account identity: %+v", usage)
	}
	requireAccountAmount(t, usage.Cost.Amount, 2)
	if len(usage.Models) != 1 || usage.Models[0].Requests != 2 || usage.Models[0].InputTokens != 30 || usage.Models[0].OutputTokens != 3 {
		t.Fatalf("rename lost or mixed history: %+v", usage.Models)
	}
	requireAccountAmount(t, accountUsage(t, s, other).Cost.Amount, 9)
	if got := accountUsage(t, s, reused); got.Cost.Amount != nil || len(got.Models) != 0 || len(got.Daily) != 0 {
		t.Fatalf("reused name acquired the old account's history: %+v", got)
	}
	events, err := st.QueryAPIIntegrationUsageRange(at.Add(-time.Minute), at.Add(time.Minute))
	if err != nil || len(events) != 3 {
		t.Fatalf("stored history: %v, %v", events, err)
	}
	for _, event := range events {
		want := id
		if event.RequestID == "other" {
			want = other
		}
		if event.AccountID != want {
			t.Fatalf("request %q bound to %d; want %d", event.RequestID, event.AccountID, want)
		}
	}
}

func TestServiceDiscoveryAliasesAndDeletionTombstones(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yml")
	writeSource := func(secondKey string) {
		t.Helper()
		body := "providers:\n  first-zai:\n    baseUrl: https://api.z.ai/api/paas/v4\n    apiKey: fixture-source-one\n  second-zai:\n    baseUrl: https://api.z.ai/api/paas/v4\n    apiKey: " + secondKey + "\n"
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeSource("fixture-source-two")
	s, st := newAccountServiceFixture(t, true, dir)
	all, err := st.QueryAllProviderAccounts()
	if err != nil {
		t.Fatal(err)
	}
	// The store seeds native default rows; only discovered sources matter here.
	var rows []store.ProviderAccount
	for _, row := range all {
		if row.ExternalID != "" {
			rows = append(rows, row)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("source accounts: %v", rows)
	}
	ids := make(map[string]int64)
	for _, row := range rows {
		settings, err := s.Resolve(row)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"first-zai": "fixture-source-one", "second-zai": "fixture-source-two"}[settings.SourceProvider]
		if row.Provider != "zai" || want == "" || settings.Credentials["api_key"] != want || settings.Source != "omp" || row.ExternalID == "" {
			t.Fatalf("alias resolved to wrong account: %s, %s", row.Provider, settings.SourceProvider)
		}
		stored, err := ParseSettings(row.Metadata)
		if err != nil || len(stored.Credentials) != 0 || strings.Contains(row.Metadata, "fixture-source-") {
			t.Fatal("discovered credentials were persisted rather than referenced")
		}
		ids[settings.SourceProvider] = row.ID
	}
	if ids["first-zai"] == ids["second-zai"] {
		t.Fatal("same-provider aliases were merged")
	}
	deleted := ids["first-zai"]
	if err := s.Delete(deleted); err != nil {
		t.Fatal(err)
	}
	writeSource("fixture-source-two-rotated")
	if imported, err := s.Discover(context.Background()); err != nil || imported != 0 {
		t.Fatalf("rescan resurrected or duplicated source identities: %d, %v", imported, err)
	}
	listed, err := s.List()
	if err != nil || len(listed) != 1 || listed[0].ID != ids["second-zai"] || !listed[0].ReadOnly || !listed[0].HasCredential {
		t.Fatalf("deleted source is visible or surviving account changed: %+v, %v", listed, err)
	}
	_, resolved, err := s.Get(ids["second-zai"])
	if err != nil || resolved.Credentials["api_key"] != "fixture-source-two-rotated" {
		t.Fatalf("source rotation did not resolve: %v", err)
	}
	row, err := st.GetProviderAccountByID(deleted)
	if err != nil || row == nil || row.DeletedAt == nil || row.ExternalID == "" {
		t.Fatalf("source tombstone lost: %+v, %v", row, err)
	}
	if _, _, err := s.Get(deleted); err == nil {
		t.Fatal("deleted source remains retrievable")
	}
	// A fresh service must also honor the persisted tombstone, not just memory.
	restarted, err := New(st, s.config, s.pricing, s.key(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed, err = restarted.List()
	if err != nil || len(listed) != 1 || listed[0].ID != ids["second-zai"] {
		t.Fatalf("restart resurrected deleted source: %+v, %v", listed, err)
	}
}

func TestServiceLocalCostUsesExactCacheRatesWithoutOmp(t *testing.T) {
	s, st := newAccountServiceFixture(t, true, "")
	id := saveManualAccount(t, s, "local-only", "fixture-local-secret")
	at := time.Now().UTC().Add(-time.Hour)
	insertAccountEvent(t, st, at, "estimate", "local-only", "fixture-cached", id, 1000, 100, 400, 100, nil)
	reported := 0.25
	insertAccountEvent(t, st, at, "reported", "local-only", "fixture-cached", id, 1000, 100, 400, 100, &reported)
	usage := accountUsage(t, s, id)
	// (500*2 + 400*0.5 + 100*3 + 100*8)/1e6, plus the supplied call cost.
	want := 0.0023 + reported
	requireAccountAmount(t, usage.Cost.Amount, want)
	if usage.Cost.Source != "local_estimate" || usage.Cost.Currency != "USD" || usage.Mode != "cost" {
		t.Fatalf("local cost attribution: %+v", usage.Cost)
	}
	if len(usage.Models) != 1 {
		t.Fatalf("models: %+v", usage.Models)
	}
	model := usage.Models[0]
	if model.Model != "fixture-cached" || model.Requests != 2 || model.InputTokens != 2000 || model.OutputTokens != 200 || model.CacheReadTokens != 800 || model.CacheWriteTokens != 200 {
		t.Fatalf("normalized model totals: %+v", model)
	}
	requireAccountAmount(t, model.Amount, want)
	if len(usage.Daily) != 1 || usage.Daily[0].Date != at.Format("2006-01-02") || usage.Daily[0].Tokens != 2200 || usage.Daily[0].Requests != 2 {
		t.Fatalf("daily totals: %+v", usage.Daily)
	}
	requireAccountAmount(t, usage.Daily[0].Amount, want)
	if discovery := s.Discovery(); !discovery.Enabled || discovery.Available {
		t.Fatalf("missing optional source state: %+v", discovery)
	}
	if _, err := os.Stat(s.ompDir); !os.IsNotExist(err) {
		t.Fatalf("local costs created or required omp source: %v", err)
	}
}

func TestServiceUnknownCostsStayNilAndKnownZeroStaysZero(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		cost        *float64
		unknown     bool
	}{
		{name: "unknown-model", model: "fixture-unlisted", unknown: true},
		{name: "known-free-model", model: "fixture-free"},
		{name: "reported-zero-unknown-model", model: "fixture-unlisted", cost: new(float64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, st := newAccountServiceFixture(t, false, "")
			id := saveManualAccount(t, s, "costs", "fixture-cost-secret")
			insertAccountEvent(t, st, time.Now().UTC().Add(-time.Hour), tc.name, "costs", tc.model, id, 1000, 100, 0, 0, tc.cost)
			usage := accountUsage(t, s, id)
			if len(usage.Models) != 1 || len(usage.Daily) != 1 || usage.Models[0].Model != tc.model || usage.Models[0].Requests != 1 || usage.Daily[0].Tokens != 1100 {
				t.Fatalf("observed unknown/free usage lost: %+v", usage)
			}
			for _, amount := range []*float64{usage.Cost.Amount, usage.Models[0].Amount, usage.Daily[0].Amount} {
				if tc.unknown {
					if amount != nil {
						t.Fatalf("unknown price became known: %v", *amount)
					}
				} else {
					requireAccountAmount(t, amount, 0)
				}
			}
		})
	}
}

func TestServicePartialPricingDoesNotExposeKnownSubtotalAsTotal(t *testing.T) {
	s, st := newAccountServiceFixture(t, false, "")
	id := saveManualAccount(t, s, "mixed", "fixture-mixed-secret")
	at := time.Now().UTC().Add(-time.Hour)
	known := 1.5
	insertAccountEvent(t, st, at, "known", "mixed", "fixture-cached", id, 10, 2, 0, 0, &known)
	insertAccountEvent(t, st, at, "unknown", "mixed", "fixture-unlisted", id, 100, 20, 0, 0, nil)
	usage := accountUsage(t, s, id)
	if usage.Cost.Amount != nil || len(usage.Daily) != 1 || usage.Daily[0].Amount != nil || usage.Daily[0].Requests != 2 || usage.Daily[0].Tokens != 132 {
		t.Fatalf("partial cost reported as complete: %+v", usage)
	}
	if len(usage.Models) != 2 {
		t.Fatalf("model breakdown: %+v", usage.Models)
	}
	for _, model := range usage.Models {
		switch model.Model {
		case "fixture-cached":
			requireAccountAmount(t, model.Amount, known)
		case "fixture-unlisted":
			if model.Amount != nil {
				t.Fatal("unknown model acquired the known model's cost")
			}
		default:
			t.Fatalf("unexpected model: %+v", model)
		}
	}
}

func TestServiceProviderReportPreservesScopePeriodAndLocalBreakdowns(t *testing.T) {
	s, st := newAccountServiceFixture(t, false, "")
	id := saveManualAccount(t, s, "billed", "fixture-billed-secret")
	other := saveManualAccount(t, s, "unbilled", "fixture-unbilled-secret")
	now := time.Now().UTC().Truncate(time.Second)
	local := 0.75
	insertAccountEvent(t, st, now.Add(-time.Hour), "billed-local", "billed", "fixture-cached", id, 10, 2, 0, 0, &local)
	insertAccountEvent(t, st, now.Add(-time.Hour), "unbilled-local", "unbilled", "fixture-cached", other, 20, 4, 0, 0, &local)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	report := &billing.Report{Amount: 42.5, Currency: "USD", Scope: "organization", PeriodStart: start, PeriodEnd: end, ObservedAt: now}
	if err := st.SaveAccountBill(id, report, "", now); err != nil {
		t.Fatal(err)
	}
	usage := accountUsage(t, s, id)
	requireAccountAmount(t, usage.Cost.Amount, 42.5)
	if usage.Cost.Source != "provider" || usage.Cost.Scope != "organization" || usage.Cost.PeriodStart != start.Format(time.RFC3339) || usage.Cost.PeriodEnd != end.Format(time.RFC3339) || usage.Cost.ObservedAt != now.Format(time.RFC3339) {
		t.Fatalf("provider report relabeled to local scope or selected range: %+v", usage.Cost)
	}
	if len(usage.Models) != 1 || len(usage.Daily) != 1 || usage.Models[0].InputTokens != 10 || usage.Daily[0].Tokens != 12 {
		t.Fatalf("local breakdown replaced by provider total: %+v", usage)
	}
	requireAccountAmount(t, usage.Models[0].Amount, local)
	requireAccountAmount(t, usage.Daily[0].Amount, local)
	otherUsage := accountUsage(t, s, other)
	if otherUsage.Cost.Source != "local_estimate" {
		t.Fatalf("bill leaked across accounts: %+v", otherUsage.Cost)
	}
	requireAccountAmount(t, otherUsage.Cost.Amount, local)
}
