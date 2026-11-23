package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/pricing"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// An omp event names omp's provider (e.g. "zai2"), not onWatch's. The account
// resolver must rebind it to the discovered account and its provider before
// pricing, otherwise the call is unpriced and attached to no account.
func TestAPIIntegrationsIngestAgent_OMPEventBindsAccountBeforePricing(t *testing.T) {
	t.Parallel()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	accountID, err := st.SaveManagedProviderAccount(0, "zai", "omp / zai2", `{"managed":true,"source":"omp","source_provider":"zai2","mode":"cost"}`, "omp:test:zai:models:zai2")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := pricing.NewCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "omp-2026-11.jsonl")
	lines := `{"ts":"2026-11-23T05:00:00Z","integration":"omp","provider":"zai2","account":"zai2","omp_provider":"zai2","model":"glm-4.6","request_id":"bound","prompt_tokens":1000000,"completion_tokens":0}
{"ts":"2026-11-23T05:00:01Z","integration":"omp","provider":"unknown-alias","account":"unknown-alias","omp_provider":"unknown-alias","model":"glm-4.6","request_id":"unbound","prompt_tokens":10,"completion_tokens":0}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, slog.Default())
	ag.SetPricingCatalog(catalog)
	ag.SetOMPAccountResolver(func(alias string) (int64, string, bool) {
		if alias == "zai2" {
			return accountID, "zai", true
		}
		return 0, "", false
	})
	if err := ag.scanFile(path); err != nil {
		t.Fatal(err)
	}
	events, err := st.QueryAPIIntegrationUsageRange(time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC), time.Date(2026, 11, 23, 6, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 2 {
		t.Fatalf("events: %+v, %v", events, err)
	}
	bound, unbound := events[0], events[1]
	if bound.AccountID != accountID || bound.Provider != "zai" || bound.CostUSD == nil || *bound.CostUSD != 0.6 || bound.CostSource != "local_estimate" {
		t.Fatalf("omp event not bound and priced as its discovered account: %+v cost=%v", bound, bound.CostUSD)
	}
	if unbound.AccountID != 0 || unbound.Provider != "unknown-alias" || unbound.CostUSD != nil {
		t.Fatalf("unresolved omp provider was rebound or priced: %+v", unbound)
	}
}
