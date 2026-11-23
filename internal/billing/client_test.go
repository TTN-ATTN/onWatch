package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var testStart = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
var testNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func testClient() *Client {
	c := NewClient(nil)
	c.now = func() time.Time { return testNow }
	return c
}

func openAIBucket(day int, amount string) string {
	start := testStart.AddDate(0, 0, day)
	return fmt.Sprintf(`{"start_time":%d,"end_time":%d,"results":[{"amount":{"value":%s,"currency":"usd"}}]}`, start.Unix(), start.AddDate(0, 0, 1).Unix(), amount)
}

func TestOpenAIPaginationAndPrefix(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/gateway/v1/organization/costs" || r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Errorf("unexpected request method/path/auth")
		}
		if r.URL.Query().Get("start_time") != fmt.Sprint(testStart.Unix()) || r.URL.Query().Get("end_time") != fmt.Sprint(testStart.AddDate(0, 0, 3).Unix()) {
			t.Errorf("wrong date query: %s", r.URL.RawQuery)
		}
		if calls == 1 {
			fmt.Fprintf(w, `{"data":[%s],"has_more":true,"next_page":"opaque + /?"}`, openAIBucket(0, "1.25"))
		} else {
			if r.URL.Query().Get("page") != "opaque + /?" {
				t.Error("cursor not preserved")
			}
			fmt.Fprintf(w, `{"data":[%s],"has_more":false,"next_page":null}`, openAIBucket(1, "2.5"))
		}
	}))
	defer server.Close()
	report, err := testClient().Fetch(context.Background(), Credentials{Provider: "openai", BaseURL: server.URL + "/gateway/v1/", APIKey: "admin-secret"}, testStart.Add(time.Hour), testStart.AddDate(0, 0, 2).Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || report.Amount != 3.75 || report.Scope != "organization" || report.Currency != "USD" || len(report.Daily) != 2 || report.Daily[0].Date != "2026-11-01" || !report.PeriodStart.Equal(testStart) || !report.PeriodEnd.Equal(testStart.AddDate(0, 0, 3)) || !report.ObservedAt.Equal(testNow) {
		t.Fatalf("unexpected report: %+v calls=%d", report, calls)
	}
}

func TestAnthropicDecimalCents(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/proxy/v1/organizations/cost_report" || r.Header.Get("x-api-key") != "admin-secret" || r.Header.Get("Authorization") != "" || r.Header.Get("anthropic-version") == "" {
			t.Error("incorrect Anthropic request")
		}
		q := r.URL.Query()
		if q.Get("starting_at") != "2026-11-01T00:00:00Z" || q.Get("ending_at") != "2026-11-03T00:00:00Z" || q.Get("limit") != "31" {
			t.Error("incorrect Anthropic query")
		}
		if calls == 1 {
			fmt.Fprint(w, `{"data":[{"starting_at":"2026-11-01T00:00:00Z","ending_at":"2026-11-02T00:00:00Z","results":[{"amount":"123.45","currency":"USD"},{"amount":"0.55","currency":"USD"}]}],"has_more":true,"next_page":"next"}`)
		} else {
			if q.Get("page") != "next" {
				t.Error("missing Anthropic cursor")
			}
			fmt.Fprint(w, `{"data":[{"starting_at":"2026-11-02T00:00:00Z","ending_at":"2026-11-03T00:00:00Z","results":[]}],"has_more":false,"next_page":null}`)
		}
	}))
	defer server.Close()
	report, err := testClient().Fetch(context.Background(), Credentials{Kind: "anthropic", BaseURL: server.URL + "/proxy", APIKey: "admin-secret"}, testStart, testStart.AddDate(0, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(report.Amount-1.24) > 1e-12 || report.Scope != "organization" || calls != 2 || len(report.Daily) != 2 || report.Daily[1].Amount != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestUnsupportedNeverProbes(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, tc := range []Credentials{
		{Provider: "unknown"}, {Provider: "zai"}, {Provider: "openai-compatible"}, {Provider: "openai", Kind: "none"}, {Kind: "invented"},
	} {
		tc.BaseURL, tc.APIKey = server.URL, "secret"
		if report, err := testClient().Fetch(context.Background(), tc, testStart, testNow); !errors.Is(err, ErrUnsupported) || report != nil {
			t.Fatalf("expected unsupported for %+v: %v", tc.Provider, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unsupported adapter probed network")
	}
}

func TestOpenRouterPeriods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/api/v1/key" || r.Method != http.MethodGet {
			t.Error("incorrect OpenRouter endpoint")
		}
		fmt.Fprint(w, `{"data":{"usage":123.5,"usage_monthly":12.5,"usage_daily":1.5,"limit_remaining":999,"byok_usage":88}}`)
	}))
	defer server.Close()
	credentials := Credentials{Kind: "openrouter", BaseURL: server.URL + "/prefix/api/v1", APIKey: "secret"}
	for _, tc := range []struct {
		start, end  time.Time
		amount      float64
		unsupported bool
		daily       int
	}{
		{time.Time{}, testNow, 123.5, false, 0},
		{testStart, testNow, 12.5, false, 0},
		{dayStart(testNow), testNow, 1.5, false, 1},
		{testNow.AddDate(0, 0, -30), testNow, 0, true, 0},
		{testStart, testNow.Add(-time.Hour), 0, true, 0},
	} {
		report, err := testClient().Fetch(context.Background(), credentials, tc.start, tc.end)
		if tc.unsupported {
			if !errors.Is(err, ErrUnsupported) || report != nil {
				t.Fatalf("expected unsupported: %+v %v", report, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if report.Amount != tc.amount || report.Scope != "api_key" || !report.PeriodStart.Equal(tc.start) || !report.PeriodEnd.Equal(testNow) || len(report.Daily) != tc.daily {
			t.Fatalf("unexpected report: %+v", report)
		}
	}
}

func TestLegacyExplicitLifetimeCents(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/gateway/v1/dashboard/billing/usage" || r.URL.RawQuery != "" {
			t.Error("legacy path changed or unsupported date filtering sent")
		}
		fmt.Fprint(w, `{"object":"list","total_usage":123.45}`)
	}))
	defer server.Close()
	credentials := Credentials{Kind: "openai-compatible", BaseURL: server.URL + "/gateway/v1", APIKey: "secret"}
	report, err := testClient().Fetch(context.Background(), credentials, time.Time{}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if report.Amount != 1.2345 || !report.PeriodStart.IsZero() || report.Scope != "api_key" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := testClient().Fetch(context.Background(), credentials, testStart, testNow); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected unsupported historical range: %v", err)
	}
	if calls != 1 {
		t.Fatal("unsupported historical range probed network")
	}
}

func TestSub2APIKeyLifetimeActualCost(t *testing.T) {
	var calls atomic.Int32
	var body atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if (r.URL.Path != "/modelapi/v1/usage" && r.URL.Path != "/v1/usage") || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected Sub2API request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		fmt.Fprint(w, body.Load().(string))
	}))
	defer server.Close()
	fetch := func(base string, start time.Time) (*Report, error) {
		return testClient().Fetch(context.Background(), Credentials{Kind: "sub2api", BaseURL: base, APIKey: "secret"}, start, testNow)
	}

	// balance/remaining are the owner's shared wallet and cost is pre-multiplier:
	// only usage.total.actual_cost is this key's charged spend.
	body.Store(`{"mode":"unrestricted","isValid":true,"remaining":92068.27,"balance":92068.27,"unit":"USD","usage":{"today":{"actual_cost":1.29},"total":{"actual_cost":33.3777,"cost":40.1}}}`)
	for _, base := range []string{server.URL + "/modelapi/v1", server.URL} {
		report, err := fetch(base, time.Time{})
		if err != nil {
			t.Fatalf("%s: %v", base, err)
		}
		if report.Amount != 33.3777 || report.Currency != "USD" || report.Scope != "api_key" || !report.PeriodStart.IsZero() {
			t.Fatalf("%s: unexpected report %+v", base, report)
		}
	}
	if _, err := fetch(server.URL, testStart); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("historical range must be unsupported: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatal("unsupported historical range probed the network")
	}

	body.Store(`{"mode":"unrestricted","unit":"CNY","usage":{"total":{"actual_cost":5}}}`)
	if report, err := fetch(server.URL, time.Time{}); !errors.Is(err, ErrUnsupported) || report != nil {
		t.Fatalf("non-USD spend must be unsupported: %+v %v", report, err)
	}
	body.Store(`{"mode":"unrestricted","unit":"USD","balance":10}`)
	if report, err := fetch(server.URL, time.Time{}); err == nil || errors.Is(err, ErrUnsupported) || report != nil {
		t.Fatalf("missing usage totals must fail, never report zero spend: %+v %v", report, err)
	}
}

func TestFailuresNeverReturnPartialOrSecrets(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		unsupported bool
	}{
		{"auth", 401, "vendor body secret-key", false},
		{"forbidden", 403, "vendor body secret-key", false},
		{"missing", 404, "vendor body secret-key", true},
		{"rate limited", 429, "vendor body secret-key", false},
		{"server", 500, "vendor body secret-key", false},
		{"trailing json", 200, `{"data":[],"has_more":false} {}`, false},
		{"missing schema", 200, `{"total_usage":123}`, false},
		{"missing more", 200, `{"data":[]}`, false},
		{"missing cursor", 200, `{"data":[],"has_more":true}`, false},
		{"inconsistent cursor", 200, `{"data":[],"has_more":false,"next_page":"x"}`, false},
		{"negative", 200, fmt.Sprintf(`{"data":[%s],"has_more":false}`, openAIBucket(0, "-1")), false},
		{"overflow", 200, fmt.Sprintf(`{"data":[%s],"has_more":false}`, openAIBucket(0, "1e999")), false},
		{"missing amount", 200, `{"data":[{"start_time":1793491200,"end_time":1793577600,"results":[{}]}],"has_more":false}`, false},
		{"currency", 200, strings.ReplaceAll(fmt.Sprintf(`{"data":[%s],"has_more":false}`, openAIBucket(0, "1")), "usd", "eur"), true},
		{"oversized", 200, strings.Repeat(" ", maxResponseBytes+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			report, err := testClient().Fetch(context.Background(), Credentials{Kind: "openai", BaseURL: server.URL, APIKey: "secret-key"}, testStart, testNow)
			if err == nil || report != nil || errors.Is(err, ErrUnsupported) != tc.unsupported {
				t.Fatalf("unexpected failure: %+v %v", report, err)
			}
			if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "vendor body") {
				t.Fatalf("sensitive error: %v", err)
			}
		})
	}
}

func TestPaginationFailureDiscardsEarlierPages(t *testing.T) {
	for _, mode := range []string{"repeat", "second-auth", "duplicate", "page-limit"} {
		t.Run(mode, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "second-auth" && calls == 2 {
					w.WriteHeader(403)
					return
				}
				day := calls - 1
				cursor := fmt.Sprint(calls)
				if mode == "duplicate" {
					day = 0
				}
				if mode == "repeat" {
					cursor = "same"
				}
				fmt.Fprintf(w, `{"data":[%s],"has_more":true,"next_page":%q}`, openAIBucket(day, "1"), cursor)
			}))
			defer server.Close()
			report, err := testClient().Fetch(context.Background(), Credentials{Kind: "openai", BaseURL: server.URL, APIKey: "secret"}, testStart, testStart.AddDate(0, 0, maxPages+1))
			if err == nil || report != nil || calls > maxPages {
				t.Fatalf("partial or unbounded pagination: %+v %v calls=%d", report, err, calls)
			}
			if mode == "page-limit" && calls != maxPages {
				t.Fatalf("unexpected page limit %d", calls)
			}
		})
	}
}

func TestRedirectsDoNotLeakEitherCredentialHeader(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	for _, kind := range []string{"openai", "anthropic"} {
		report, err := testClient().Fetch(context.Background(), Credentials{Kind: kind, BaseURL: source.URL, APIKey: "secret"}, testStart, testNow)
		if err == nil || report != nil {
			t.Fatalf("redirect accepted: %+v %v", report, err)
		}
	}
	if leaked.Load() != 0 {
		t.Fatal("redirect target received a request")
	}
}

func TestContextCancellationAndTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := testClient()
	report, err := client.Fetch(ctx, Credentials{Kind: "openai", BaseURL: "http://127.0.0.1:1", APIKey: "secret"}, testStart, testNow)
	if !errors.Is(err, context.Canceled) || report != nil {
		t.Fatalf("cancellation lost: %+v %v", report, err)
	}
	if NewClient(&http.Client{}).http.Timeout != fetchTimeout || NewClient(&http.Client{Timeout: time.Millisecond}).http.Timeout != time.Millisecond {
		t.Fatal("timeout is not bounded or smaller supplied timeout lost")
	}
}

func TestBaseURLValidationAndPrefixes(t *testing.T) {
	for _, tc := range []struct{ base, kind, want string }{
		{"https://example.test/proxy", "openai", "https://example.test/proxy/v1/organization/costs"},
		{"https://example.test/proxy/v1/", "anthropic", "https://example.test/proxy/v1/organizations/cost_report"},
		{"https://example.test/a%2Fb/v1", "openai", "https://example.test/a%2Fb/v1/organization/costs"},
		{"https://example.test/gateway", "openrouter", "https://example.test/gateway/api/v1/key"},
		{"https://example.test/prefix", "openai-compatible", "https://example.test/prefix/dashboard/billing/usage"},
	} {
		u, err := adapterURL(tc.base, tc.kind)
		if err != nil || u.String() != tc.want {
			t.Fatalf("URL %q: %v %v", tc.base, u, err)
		}
	}
	for _, raw := range []string{"file:///tmp/key", "https://user:secret@example.test", "https://example.test?key=secret", "https://example.test#fragment", ":bad"} {
		if _, err := adapterURL(raw, "openai"); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe URL accepted or echoed: %v", err)
		}
	}
}

func TestRejectInvalidProviderAmounts(t *testing.T) {
	for _, amount := range []string{"NaN", "+Inf", "-1", "1e999", ""} {
		encoded, _ := json.Marshal(amount)
		if _, err := resultValue(costResult{Amount: encoded, Currency: "USD"}, "anthropic"); err == nil {
			t.Fatalf("accepted amount %q", amount)
		}
	}
	for _, body := range []string{`{"data":{}}`, `{"data":{"usage_monthly":null}}`, `{"data":{"usage_monthly":-1}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		report, err := testClient().Fetch(context.Background(), Credentials{Kind: "openrouter", BaseURL: server.URL, APIKey: "secret"}, testStart, testNow)
		server.Close()
		if err == nil || report != nil {
			t.Fatalf("accepted invalid OpenRouter response %s", body)
		}
	}
}
