// Package billing reads provider-reported monetary spend without inference requests
// or local credential/statistics discovery. Unknown schemas are never inferred.
package billing

import (
	"errors"
	"time"
)

// ErrUnsupported means the adapter, currency, endpoint, or requested period is
// unavailable. Callers may fall back to independently calculated token pricing.
// Authentication and malformed-response errors do not match ErrUnsupported.
var ErrUnsupported = errors.New("provider billing unsupported")

// Credentials selects a read-only adapter. Kind is auto, openai, anthropic,
// openrouter, openai-compatible, or none. APIKey must be an organization admin
// key for OpenAI and Anthropic; their reports cover the organization, not this key.
// BaseURL is the API root, including any gateway prefix and optional /v1.
// Explicit openai-compatible opts into the legacy USD-cents, cumulative token
// usage contract; it is not a general promise of OpenAI billing compatibility.
type Credentials struct {
	Provider string
	BaseURL  string
	Kind     string
	APIKey   string
}

type DailySpend struct {
	Date   string
	Amount float64
}

// Report periods are [PeriodStart, PeriodEnd), in UTC. Zero PeriodStart means
// lifetime usage. ObservedAt is the local observation time, not a freshness claim
// about vendor accounting. Organization adapters expand queries to complete UTC
// days because Anthropic only reports daily buckets; inspect the returned period.
// Scope is organization or api_key. Reports with overlapping scopes must not be
// added together. Daily is nil when the provider supplies no daily breakdown.
type Report struct {
	Amount      float64
	Currency    string
	Scope       string
	PeriodStart time.Time
	PeriodEnd   time.Time
	ObservedAt  time.Time
	Daily       []DailySpend
}
