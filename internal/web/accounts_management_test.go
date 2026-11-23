package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/accounts"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	apiintegrations "github.com/onllm-dev/onwatch/v2/internal/api_integrations"
	"github.com/onllm-dev/onwatch/v2/internal/billing"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/pricing"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

func newAccountHTTPFixture(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	for _, key := range testhome.OverrideEnv {
		t.Setenv(key, "")
	}
	t.Setenv("ONWATCH_OMP_DIR", filepath.Join(home, "optional-omp"))
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	pricePath := filepath.Join(home, "prices.json")
	if err := os.WriteFile(pricePath, []byte(`[{"provider":"zai","model":"http-priced-model","input_per_million":2,"output_per_million":4,"cache_read_per_million":0.5,"cache_write_per_million":3}]`), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := pricing.NewCatalog(pricePath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		ZaiAPIKey: "account-http-secret-default-zai", ZaiBaseURL: "https://zai.invalid/api",
		SyntheticAPIKey: "account-http-secret-default-synthetic", PollInterval: time.Minute,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := accounts.New(st, cfg, catalog, strings.Repeat("ab", 32), logger)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, nil, logger, nil, cfg)
	h.SetAccountsService(service)
	return h, st
}

func accountHTTPRequest(t *testing.T, handler http.HandlerFunc, method, target string, payload any, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, target, &body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, r)
	if w.Code != wantStatus {
		t.Fatalf("%s %s: status = %d, want %d; body = %s", method, target, w.Code, wantStatus, w.Body.String())
	}
	if !json.Valid(w.Body.Bytes()) {
		t.Fatalf("%s %s: response is not JSON: %s", method, target, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "account-http-secret-") || strings.Contains(w.Body.String(), `"credentials":`) || strings.Contains(w.Body.String(), "enc:") {
		t.Fatalf("%s %s exposed stored/submitted credentials", method, target)
	}
	return w
}

func saveAccountHTTP(t *testing.T, h *Handler, input accounts.Input) int64 {
	t.Helper()
	method := http.MethodPost
	if input.ID != 0 {
		method = http.MethodPut
	}
	w := accountHTTPRequest(t, h.Accounts, method, "/api/accounts", input, http.StatusOK)
	var result struct {
		ID      int64 `json:"id"`
		Success bool  `json:"success"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.ID <= 0 {
		t.Fatalf("save did not return an account identity: %s", w.Body.String())
	}
	if input.ID != 0 && result.ID != input.ID {
		t.Fatalf("update changed account identity: got %d, want %d", result.ID, input.ID)
	}
	return result.ID
}

func manualAccountHTTP(provider, name, mode string) accounts.Input {
	return accounts.Input{Provider: provider, Name: name, Mode: mode, BillingKind: "none", Credentials: map[string]string{"api_key": "account-http-secret-" + provider + "-" + name}}
}

func listAccountsHTTP(t *testing.T, h *Handler) []accounts.Account {
	t.Helper()
	w := accountHTTPRequest(t, h.Accounts, http.MethodGet, "/api/accounts", nil, http.StatusOK)
	var result struct {
		Accounts []accounts.Account `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Accounts
}

func usageAccountHTTP(t *testing.T, h *Handler, id int64) accounts.Usage {
	t.Helper()
	w := accountHTTPRequest(t, h.AccountUsage, http.MethodGet, fmt.Sprintf("/api/accounts/usage?account=%d&range=7d", id), nil, http.StatusOK)
	var result accounts.Usage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.AccountID != id {
		t.Fatalf("usage account identity = %d, want %d", result.AccountID, id)
	}
	return result
}

func insertAccountHTTPZai(t *testing.T, st *store.Store, id int64, at time.Time, tokens float64) {
	t.Helper()
	reset := at.Add(24 * time.Hour)
	_, err := st.ForAccount(id).InsertZaiSnapshot(&api.ZaiSnapshot{
		CapturedAt: at, TokensUsage: 1000, TokensCurrentValue: tokens,
		TokensRemaining: 1000 - tokens, TokensPercentage: int(tokens / 10), TokensNextResetTime: &reset,
		TimeUsage: 100, TimeCurrentValue: tokens / 10, TimeRemaining: 100 - tokens/10,
		TimePercentage: int(tokens / 10),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAccountsHTTPRedactsCredentialsAcrossCreateUpdateListAndErrors(t *testing.T) {
	h, _ := newAccountHTTPFixture(t)
	input := manualAccountHTTP("zai", "redacted", "quota")
	id := saveAccountHTTP(t, h, input)
	input.ID = id
	input.Credentials["api_key"] = "account-http-secret-replacement"
	saveAccountHTTP(t, h, input)
	input.Credentials["api_key"] = ""
	input.Name = "renamed"
	saveAccountHTTP(t, h, input)
	found := false
	for _, row := range listAccountsHTTP(t, h) {
		if row.ID == id {
			found = true
			if row.Name != "renamed" || !row.HasCredential {
				t.Fatalf("blank secret update lost existing credentials or rename: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("created account missing from list")
	}
	input.BaseURL = "https://account-http-secret-user:account-http-secret-password@example.invalid/"
	accountHTTPRequest(t, h.Accounts, http.MethodPut, "/api/accounts", input, http.StatusBadRequest)
	input.BaseURL = ""
	input.Provider = "synthetic"
	input.Credentials["api_key"] = "account-http-secret-rejected-provider-change"
	accountHTTPRequest(t, h.Accounts, http.MethodPut, "/api/accounts", input, http.StatusBadRequest)
	accountHTTPRequest(t, h.Accounts, http.MethodDelete, fmt.Sprintf("/api/accounts?id=%d", id), nil, http.StatusOK)
	for _, row := range listAccountsHTTP(t, h) {
		if row.ID == id {
			t.Fatal("deleted account is still listed")
		}
	}
}

func TestAccountsHTTPZaiCurrentAndHistoryRemainAccountScoped(t *testing.T) {
	h, st := newAccountHTTPFixture(t)
	a := saveAccountHTTP(t, h, manualAccountHTTP("zai", "first", "quota"))
	b := saveAccountHTTP(t, h, manualAccountHTTP("zai", "second", "quota"))
	empty := saveAccountHTTP(t, h, manualAccountHTTP("zai", "empty", "quota"))
	at := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Hour)
	insertAccountHTTPZai(t, st, 0, at.Add(time.Hour), 990)
	insertAccountHTTPZai(t, st, a, at, 110)
	insertAccountHTTPZai(t, st, a, at.Add(time.Minute), 120)
	insertAccountHTTPZai(t, st, b, at, 710)
	insertAccountHTTPZai(t, st, b, at.Add(time.Minute), 720)
	// Alternate reads to catch scope-blind response caches as well as SQL fallbacks.
	for _, tc := range []struct {
		id     int64
		values []float64
	}{{a, []float64{110, 120}}, {b, []float64{710, 720}}, {a, []float64{110, 120}}, {empty, nil}} {
		query := "?provider=zai&account=" + strconv.FormatInt(tc.id, 10)
		w := accountHTTPRequest(t, h.accountRoute((*Handler).Current), http.MethodGet, "/api/current"+query, nil, http.StatusOK)
		var current struct {
			TokensLimit struct {
				Usage float64 `json:"usage"`
			} `json:"tokensLimit"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		want := 0.0
		if len(tc.values) > 0 {
			want = tc.values[len(tc.values)-1]
		}
		if current.TokensLimit.Usage != want {
			t.Fatalf("account %d current usage = %v, want %v", tc.id, current.TokensLimit.Usage, want)
		}
		w = accountHTTPRequest(t, h.accountRoute((*Handler).History), http.MethodGet, "/api/history"+query+"&range=24h", nil, http.StatusOK)
		var history []struct {
			TokensUsage float64 `json:"tokensUsage"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil {
			t.Fatal(err)
		}
		if len(history) != len(tc.values) {
			t.Fatalf("account %d history length = %d, want %d: %s", tc.id, len(history), len(tc.values), w.Body.String())
		}
		for i, want := range tc.values {
			if history[i].TokensUsage != want {
				t.Fatalf("account %d history[%d] usage = %v, want %v", tc.id, i, history[i].TokensUsage, want)
			}
		}
	}
	usage := usageAccountHTTP(t, h, b)
	quota, ok := usage.Quota.(map[string]any)
	if !ok {
		t.Fatalf("quota payload missing: %+v", usage)
	}
	tokens, ok := quota["tokensLimit"].(map[string]any)
	if !ok || tokens["usage"] != float64(720) {
		t.Fatalf("usage endpoint returned another account's quota: %+v", quota)
	}
}

func TestAccountsHTTPRejectsInvalidDeletedAndMismatchedScopes(t *testing.T) {
	h, st := newAccountHTTPFixture(t)
	id := saveAccountHTTP(t, h, manualAccountHTTP("zai", "deleted", "quota"))
	insertAccountHTTPZai(t, st, 0, time.Now().UTC().Add(-time.Hour), 990)
	insertAccountHTTPZai(t, st, id, time.Now().UTC().Add(-time.Hour), 220)
	for _, endpoint := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/api/current", h.accountRoute((*Handler).Current)},
		{"/api/history", h.accountRoute((*Handler).History)},
	} {
		accountHTTPRequest(t, endpoint.handler, http.MethodGet, fmt.Sprintf("%s?provider=synthetic&account=%d", endpoint.path, id), nil, http.StatusBadRequest)
		for _, invalid := range []string{"0", "-1", "not-an-id", "9223372036854775808"} {
			accountHTTPRequest(t, endpoint.handler, http.MethodGet, endpoint.path+"?provider=zai&account="+invalid, nil, http.StatusBadRequest)
		}
		accountHTTPRequest(t, endpoint.handler, http.MethodGet, endpoint.path+"?provider=zai&account=9999999", nil, http.StatusNotFound)
	}
	accountHTTPRequest(t, h.Accounts, http.MethodDelete, fmt.Sprintf("/api/accounts?id=%d", id), nil, http.StatusOK)
	for _, handler := range []http.HandlerFunc{h.accountRoute((*Handler).Current), h.accountRoute((*Handler).History)} {
		accountHTTPRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/current?provider=zai&account=%d", id), nil, http.StatusNotFound)
	}
	accountHTTPRequest(t, h.AccountUsage, http.MethodGet, fmt.Sprintf("/api/accounts/usage?account=%d", id), nil, http.StatusBadRequest)
	accountHTTPRequest(t, h.AccountUsage, http.MethodGet, "/api/accounts/usage?account=9999999", nil, http.StatusBadRequest)
	stored, err := st.ForAccount(id).QueryLatestZai()
	if err != nil || stored == nil || stored.TokensCurrentValue != 220 {
		t.Fatalf("soft deletion lost account history: snapshot=%+v error=%v", stored, err)
	}
}

func TestAccountsHTTPPausePreservesQuotaAndCostModeDoesNotFallBack(t *testing.T) {
	h, st := newAccountHTTPFixture(t)
	input := manualAccountHTTP("zai", "switch-modes", "quota")
	input.ID = saveAccountHTTP(t, h, input)
	insertAccountHTTPZai(t, st, 0, time.Now().UTC().Add(-time.Hour), 990)
	insertAccountHTTPZai(t, st, input.ID, time.Now().UTC().Add(-time.Hour), 310)
	paused := false
	input.Enabled = &paused
	input.Credentials = map[string]string{"api_key": ""}
	saveAccountHTTP(t, h, input)
	assertState := func(enabled bool, mode string) {
		t.Helper()
		for _, row := range listAccountsHTTP(t, h) {
			if row.ID == input.ID {
				if row.Enabled != enabled || row.Mode != mode || !row.HasCredential {
					t.Fatalf("account state = %+v", row)
				}
				return
			}
		}
		t.Fatal("account missing")
	}
	assertState(false, "quota")
	if usage := usageAccountHTTP(t, h, input.ID); usage.Mode != "quota" || usage.Quota == nil {
		t.Fatalf("pausing erased readable quota: %+v", usage)
	}
	input.Mode = "cost"
	saveAccountHTTP(t, h, input)
	assertState(false, "cost")
	usage := usageAccountHTTP(t, h, input.ID)
	if usage.Mode != "cost" || usage.Quota != nil || usage.Cost.Amount != nil || usage.Cost.Source != "unavailable" {
		t.Fatalf("cost-only account exposed quota or invented spend: %+v", usage)
	}
	for _, endpoint := range []http.HandlerFunc{h.accountRoute((*Handler).Current), h.accountRoute((*Handler).History)} {
		accountHTTPRequest(t, endpoint, http.MethodGet, fmt.Sprintf("/api/current?provider=zai&account=%d", input.ID), nil, http.StatusBadRequest)
	}
	active := true
	input.Enabled, input.Mode = &active, "quota"
	saveAccountHTTP(t, h, input)
	assertState(true, "quota")
	if usage := usageAccountHTTP(t, h, input.ID); usage.Quota == nil {
		t.Fatal("resuming quota did not restore stored quota")
	}
}

func TestAccountsHTTPCostReportAndLocalEstimateKeepIdentityAndScope(t *testing.T) {
	h, st := newAccountHTTPFixture(t)
	local := manualAccountHTTP("zai", "local", "cost")
	local.ID = saveAccountHTTP(t, h, local)
	provider := saveAccountHTTP(t, h, manualAccountHTTP("zai", "provider-bill", "cost"))
	unknown := saveAccountHTTP(t, h, manualAccountHTTP("zai", "unknown-price", "cost"))
	otherProvider := saveAccountHTTP(t, h, manualAccountHTTP("synthetic", "local", "cost"))
	at := time.Now().UTC().Add(-time.Hour)
	addEvent := func(id int64, providerName, name, model, fingerprint string, cost *float64, when time.Time) {
		t.Helper()
		_, err := st.InsertAPIIntegrationUsageEvent(&apiintegrations.UsageEvent{
			Timestamp: when, Integration: "account-http", Provider: providerName, Account: name, AccountID: id,
			Model: model, PromptTokens: 1000000, CompletionTokens: 250000, TotalTokens: 1250000,
			CacheReadTokens: 200000, CacheWriteTokens: 100000, CostUSD: cost, Fingerprint: fingerprint,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	three, large := 3.0, 900.0
	addEvent(local.ID, "zai", "local", "http-priced-model", "local-call", nil, at)
	addEvent(provider, "zai", "provider-bill", "http-priced-model", "billed-call", &three, at)
	addEvent(unknown, "zai", "unknown-price", "http-unknown-model", "unknown-call", nil, at)
	addEvent(otherProvider, "synthetic", "local", "http-priced-model", "other-provider-call", &large, at)
	addEvent(0, "zai", "default", "http-priced-model", "default-call", &large, at)
	addEvent(local.ID, "zai", "local", "http-priced-model", "outside-range-call", &large, at.Add(-30*24*time.Hour))
	report := &billing.Report{Amount: 37.25, Currency: "USD", Scope: "organization", PeriodStart: at.Add(-24 * time.Hour).Truncate(time.Second), PeriodEnd: at.Truncate(time.Second), ObservedAt: at.Truncate(time.Second)}
	if err := st.SaveAccountBill(provider, report, "", at); err != nil {
		t.Fatal(err)
	}
	// Explicit event IDs must survive a rename, without importing another account's calls.
	local.Name = "renamed-local"
	local.Credentials = map[string]string{"api_key": ""}
	saveAccountHTTP(t, h, local)
	usage := usageAccountHTTP(t, h, local.ID)
	if usage.Provider != "zai" || usage.Name != "renamed-local" || usage.Mode != "cost" || usage.Quota != nil || usage.Cost.Source != "local_estimate" || usage.Cost.Scope != "observed calls for this account" || usage.Cost.Currency != "USD" {
		t.Fatalf("local estimate identity/scope = %+v", usage)
	}
	assertAmount := func(label string, got *float64, want float64) {
		t.Helper()
		if got == nil || math.Abs(*got-want) > 1e-9 {
			t.Fatalf("%s amount = %v, want %v", label, got, want)
		}
	}
	assertAmount("local", usage.Cost.Amount, 2.8)
	if len(usage.Daily) != 1 || len(usage.Models) != 1 {
		t.Fatalf("local breakdown includes unrelated calls: %+v", usage)
	}
	day, model := usage.Daily[0], usage.Models[0]
	assertAmount("local daily", day.Amount, 2.8)
	assertAmount("local model", model.Amount, 2.8)
	if day.Date != at.Format("2006-01-02") || day.Tokens != 1250000 || day.Requests != 1 || model.Model != "http-priced-model" || model.Requests != 1 || model.InputTokens != 1000000 || model.OutputTokens != 250000 || model.CacheReadTokens != 200000 || model.CacheWriteTokens != 100000 {
		t.Fatalf("local observed-call breakdown = %+v / %+v", day, model)
	}
	billed := usageAccountHTTP(t, h, provider)
	if billed.Provider != "zai" || billed.Name != "provider-bill" || billed.Cost.Source != "provider" || billed.Cost.Scope != "organization" || billed.Cost.Currency != "USD" || billed.Cost.PeriodStart != report.PeriodStart.Format(time.RFC3339) || billed.Cost.PeriodEnd != report.PeriodEnd.Format(time.RFC3339) || billed.Cost.ObservedAt != report.ObservedAt.Format(time.RFC3339) {
		t.Fatalf("provider report lost identity/scope/period: %+v", billed)
	}
	assertAmount("provider (not bill plus local estimate)", billed.Cost.Amount, 37.25)
	if len(billed.Daily) != 1 || len(billed.Models) != 1 || billed.Daily[0].Requests != 1 || billed.Models[0].Requests != 1 {
		t.Fatalf("provider breakdown mixed scopes: %+v", billed)
	}
	assertAmount("provider account observed calls only", billed.Daily[0].Amount, 3)
	assertAmount("provider account model calls only", billed.Models[0].Amount, 3)
	unpriced := usageAccountHTTP(t, h, unknown)
	if unpriced.Cost.Source != "local_estimate" || unpriced.Cost.Amount != nil || len(unpriced.Daily) != 1 || unpriced.Daily[0].Amount != nil || len(unpriced.Models) != 1 || unpriced.Models[0].Amount != nil {
		t.Fatalf("unknown model cost was invented or borrowed: %+v", unpriced)
	}
}
