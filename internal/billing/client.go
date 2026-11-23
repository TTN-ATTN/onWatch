package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxResponseBytes = 2 << 20
	maxPages         = 100
	fetchTimeout     = 20 * time.Second
)

// Client is safe for concurrent Fetch calls. Redirects are disabled even on a
// supplied client, preventing both Authorization and x-api-key credential leaks.
type Client struct {
	http *http.Client
	now  func() time.Time
}

func NewClient(httpClient *http.Client) *Client {
	var hc http.Client
	if httpClient != nil {
		hc = *httpClient
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if hc.Timeout == 0 || hc.Timeout > fetchTimeout {
		hc.Timeout = fetchTimeout
	}
	return &Client{http: &hc, now: time.Now}
}

// Fetch performs GET requests only. Unsupported kinds never contact the server.
// OpenRouter accepts lifetime (zero start) or the current UTC day/month through
// now, not arbitrary historical ranges. Legacy openai-compatible accepts only
// lifetime. Both report the actual observation end rather than a requested past
// end. Organization reports use complete UTC-day query boundaries.
func (c *Client) Fetch(ctx context.Context, credentials Credentials, start, end time.Time) (*Report, error) {
	kind := strings.ToLower(strings.TrimSpace(credentials.Kind))
	if kind == "" || kind == "auto" {
		kind = strings.ToLower(strings.TrimSpace(credentials.Provider))
		if kind != "openai" && kind != "anthropic" && kind != "openrouter" {
			return nil, ErrUnsupported
		}
	}
	switch kind {
	case "openai", "anthropic", "openrouter", "openai-compatible":
	default:
		return nil, ErrUnsupported
	}
	if strings.TrimSpace(credentials.APIKey) == "" {
		return nil, errors.New("billing credential is missing")
	}
	now := c.now().UTC()
	start, end = start.UTC(), end.UTC()
	if end.IsZero() {
		end = now
	}
	if (!start.IsZero() && !start.Before(end)) || end.Unix() < 0 {
		return nil, errors.New("invalid billing period")
	}
	base, err := adapterURL(credentials.BaseURL, kind)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	switch kind {
	case "openrouter":
		return c.openRouter(ctx, base, credentials.APIKey, start, end, now)
	case "openai-compatible":
		return c.legacy(ctx, base, credentials.APIKey, start, end, now)
	default:
		if start.IsZero() || start.Unix() < 0 {
			return nil, fmt.Errorf("%w: organization billing requires a start date", ErrUnsupported)
		}
		start = dayStart(start)
		if !end.Equal(dayStart(end)) {
			end = dayStart(end).AddDate(0, 0, 1)
		}
		return c.organization(ctx, base, credentials.APIKey, kind, start, end)
	}
}

func adapterURL(raw, kind string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		switch kind {
		case "openai":
			raw = "https://api.openai.com/v1"
		case "anthropic":
			raw = "https://api.anthropic.com/v1"
		case "openrouter":
			raw = "https://openrouter.ai/api/v1"
		default:
			return nil, fmt.Errorf("%w: an explicit billing base URL is required", ErrUnsupported)
		}
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("invalid billing base URL")
	}
	// Append rather than resolve an absolute path, retaining reverse-proxy prefixes.
	if kind != "openai-compatible" && !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") {
		if kind == "openrouter" {
			u = u.JoinPath("api", "v1")
		} else {
			u = u.JoinPath("v1")
		}
	}
	switch kind {
	case "openai":
		return u.JoinPath("organization", "costs"), nil
	case "anthropic":
		return u.JoinPath("organizations", "cost_report"), nil
	case "openrouter":
		return u.JoinPath("key"), nil
	default:
		return u.JoinPath("dashboard", "billing", "usage"), nil
	}
}

func (c *Client) get(ctx context.Context, endpoint *url.URL, key, kind string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return errors.New("cannot create billing request")
	}
	req.Header.Set("Accept", "application/json")
	if kind == "anthropic" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Transport errors can contain URLs, headers, or even the key. Never echo.
		return errors.New("billing request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		return fmt.Errorf("%w: billing endpoint unavailable", ErrUnsupported)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("billing authorization denied; organization adapters require an admin key")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("billing HTTP status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("cannot read billing response")
	}
	if len(body) > maxResponseBytes {
		return errors.New("billing response exceeds size limit")
	}
	if json.Unmarshal(body, target) != nil {
		return errors.New("invalid billing response")
	}
	return nil
}

type costResult struct {
	// OpenAI uses amount.value in currency units, Anthropic a decimal cents string.
	Amount   json.RawMessage `json:"amount"`
	Currency string          `json:"currency"`
}

type costBucket struct {
	Start    *int64        `json:"start_time"`
	End      *int64        `json:"end_time"`
	Starting string        `json:"starting_at"`
	Ending   string        `json:"ending_at"`
	Results  *[]costResult `json:"results"`
}

type costPage struct {
	Data    *[]costBucket `json:"data"`
	HasMore *bool         `json:"has_more"`
	Next    string        `json:"next_page"`
}

// Official contracts (admin credentials; organization-wide, NOT per-key spend):
// https://developers.openai.com/docs/api-reference/usage/costs
// https://platform.claude.com/docs/en/api/beta/organization/cost_report
// https://platform.claude.com/docs/en/manage-claude/usage-cost-api
// OpenAI amount.value is USD; Anthropic amount is decimal USD CENTS.
// Anthropic's legacy cost report excludes Priority Tier charges.
func (c *Client) organization(ctx context.Context, endpoint *url.URL, key, kind string, start, end time.Time) (*Report, error) {
	q := endpoint.Query()
	q.Set("bucket_width", "1d")
	if kind == "anthropic" {
		q.Set("starting_at", start.Format(time.RFC3339))
		q.Set("ending_at", end.Format(time.RFC3339))
		q.Set("limit", "31")
	} else {
		q.Set("start_time", strconv.FormatInt(start.Unix(), 10))
		q.Set("end_time", strconv.FormatInt(end.Unix(), 10))
		q.Set("limit", "180")
	}
	r := &Report{Currency: "USD", Scope: "organization", PeriodStart: start, PeriodEnd: end}
	daily := make(map[string]float64)
	cursors := make(map[string]bool)
	buckets := make(map[int64]bool)
	for range maxPages {
		endpoint.RawQuery = q.Encode()
		var response costPage
		if err := c.get(ctx, endpoint, key, kind, &response); err != nil {
			return nil, err
		}
		if response.Data == nil || response.HasMore == nil {
			return nil, errors.New("incomplete billing response")
		}
		for _, bucket := range *response.Data {
			var bs, be time.Time
			if kind == "anthropic" {
				var err error
				bs, err = time.Parse(time.RFC3339, bucket.Starting)
				if err != nil {
					return nil, errors.New("invalid billing bucket date")
				}
				be, err = time.Parse(time.RFC3339, bucket.Ending)
				if err != nil {
					return nil, errors.New("invalid billing bucket date")
				}
			} else {
				if bucket.Start == nil || bucket.End == nil {
					return nil, errors.New("missing billing bucket date")
				}
				bs, be = time.Unix(*bucket.Start, 0).UTC(), time.Unix(*bucket.End, 0).UTC()
			}
			if bs.Before(start) || be.After(end) || !bs.Before(be) || !bs.Equal(dayStart(bs)) || !be.Equal(bs.AddDate(0, 0, 1)) || buckets[bs.Unix()] || bucket.Results == nil {
				return nil, errors.New("invalid or overlapping billing bucket")
			}
			buckets[bs.Unix()] = true
			date := bs.UTC().Format(time.DateOnly)
			if _, ok := daily[date]; !ok {
				daily[date] = 0
			}
			for _, result := range *bucket.Results {
				amount, err := resultValue(result, kind)
				if err != nil {
					return nil, err
				}
				daily[date] += amount
				r.Amount += amount
				if !validAmount(daily[date]) || !validAmount(r.Amount) {
					return nil, errors.New("invalid billing total")
				}
			}
		}
		if !*response.HasMore {
			if response.Next != "" {
				return nil, errors.New("inconsistent billing pagination")
			}
			for date, amount := range daily {
				r.Daily = append(r.Daily, DailySpend{Date: date, Amount: amount})
			}
			sort.Slice(r.Daily, func(i, j int) bool { return r.Daily[i].Date < r.Daily[j].Date })
			r.ObservedAt = c.now().UTC()
			return r, nil
		}
		if response.Next == "" || cursors[response.Next] || len(*response.Data) == 0 {
			return nil, errors.New("incomplete billing pagination")
		}
		cursors[response.Next] = true
		q.Set("page", response.Next)
	}
	return nil, errors.New("billing pagination exceeds limit")
}

func resultValue(result costResult, kind string) (float64, error) {
	var amount float64
	currency := result.Currency
	if kind == "anthropic" {
		var cents string
		if json.Unmarshal(result.Amount, &cents) != nil {
			return 0, errors.New("invalid billing amount")
		}
		value, err := strconv.ParseFloat(cents, 64)
		if err != nil || !validAmount(value) {
			return 0, errors.New("invalid billing amount")
		}
		amount = value / 100
	} else {
		var money struct {
			Value    *float64 `json:"value"`
			Currency string   `json:"currency"`
		}
		if json.Unmarshal(result.Amount, &money) != nil || money.Value == nil {
			return 0, errors.New("invalid billing amount")
		}
		amount, currency = *money.Value, money.Currency
	}
	if !strings.EqualFold(currency, "USD") {
		return 0, fmt.Errorf("%w: billing currency is not USD", ErrUnsupported)
	}
	if !validAmount(amount) {
		return 0, errors.New("invalid billing amount")
	}
	return amount, nil
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func validAmount(amount float64) bool {
	return !math.IsNaN(amount) && !math.IsInf(amount, 0) && amount >= 0
}

func currentPeriod(start, end, now time.Time) bool {
	// The endpoint has no historical end filter. Allow only a live observation,
	// accounting for the small delay between caller timestamp and HTTP request.
	return end.Sub(now) <= time.Minute && now.Sub(end) <= time.Minute && (start.IsZero() || !start.After(now))
}

// https://openrouter.ai/docs/api/api-reference/api-keys/get-current-key
// usage_* fields are USD spend charged to this API key, not remaining credits.
// BYOK external-provider costs are separate and are not included in this report.
func (c *Client) openRouter(ctx context.Context, endpoint *url.URL, key string, start, end, now time.Time) (*Report, error) {
	if !currentPeriod(start, end, now) {
		return nil, fmt.Errorf("%w: OpenRouter only exposes current periods", ErrUnsupported)
	}
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !start.IsZero() && !start.Equal(dayStart(now)) && !start.Equal(month) {
		return nil, fmt.Errorf("%w: OpenRouter only exposes current day or month", ErrUnsupported)
	}
	var response struct {
		Data *struct {
			Usage   *float64 `json:"usage"`
			Daily   *float64 `json:"usage_daily"`
			Monthly *float64 `json:"usage_monthly"`
		} `json:"data"`
	}
	if err := c.get(ctx, endpoint, key, "openrouter", &response); err != nil {
		return nil, err
	}
	if response.Data == nil {
		return nil, errors.New("missing OpenRouter billing data")
	}
	amount := response.Data.Usage
	if start.Equal(dayStart(now)) {
		amount = response.Data.Daily
	} else if start.Equal(month) {
		amount = response.Data.Monthly
	}
	if amount == nil || !validAmount(*amount) {
		return nil, errors.New("invalid OpenRouter billing amount")
	}
	r := &Report{Amount: *amount, Currency: "USD", Scope: "api_key", PeriodStart: start, PeriodEnd: now, ObservedAt: c.now().UTC()}
	if start.Equal(dayStart(now)) {
		r.Daily = []DailySpend{{Date: start.Format(time.DateOnly), Amount: *amount}}
	}
	return r, nil
}

// Explicit legacy contract: https://docs.4allapi.com/en/api/balance-query/
// total_usage is cumulative current-token spend in USD cents. The endpoint does
// not establish date-filter semantics: never assume start_date/end_date work.
// Choosing this kind asserts the configured service implements that contract;
// merely being OpenAI inference-compatible is insufficient. No schema guessing.
func (c *Client) legacy(ctx context.Context, endpoint *url.URL, key string, start, end, now time.Time) (*Report, error) {
	if !start.IsZero() || !currentPeriod(start, end, now) {
		return nil, fmt.Errorf("%w: legacy billing only exposes lifetime usage", ErrUnsupported)
	}
	var response struct {
		Total *float64 `json:"total_usage"`
	}
	if err := c.get(ctx, endpoint, key, "openai-compatible", &response); err != nil {
		return nil, err
	}
	if response.Total == nil || !validAmount(*response.Total) {
		return nil, errors.New("invalid legacy billing amount")
	}
	return &Report{Amount: *response.Total / 100, Currency: "USD", Scope: "api_key", PeriodEnd: now, ObservedAt: c.now().UTC()}, nil
}
