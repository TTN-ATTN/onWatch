// Package pricing estimates USD token costs from observed usage without network access.
// Estimates are not invoices: they exclude tools, storage, taxes, negotiated discounts,
// batch/priority tiers, regional premiums and non-text modalities. Built-ins are a
// dated snapshot; use overrides for a different billing tier or changed prices.
package pricing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// ErrUnknownModel means no exact provider/model rate is available, not a zero cost.
var ErrUnknownModel = errors.New("unknown pricing model")

// Usage counts observed tokens. PromptTokens includes both cache subsets, which
// must be disjoint. Adapters for APIs with exclusive input counts must add their
// cache counts before calling Estimate. CompletionTokens includes thinking tokens.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Rate contains USD prices per million tokens. A nil cache price charges the full
// input rate; a pointer to zero explicitly makes that cache category free.
type Rate struct {
	Provider             string   `json:"provider"`
	Model                string   `json:"model"`
	InputPerMillion      float64  `json:"input_per_million"`
	OutputPerMillion     float64  `json:"output_per_million"`
	CacheReadPerMillion  *float64 `json:"cache_read_per_million,omitempty"`
	CacheWritePerMillion *float64 `json:"cache_write_per_million,omitempty"`
}

type modelKey struct {
	provider string
	model    string
}

// Catalog is immutable after construction and safe for concurrent estimates.
// Its zero value is an empty catalog.
type Catalog struct {
	rates map[modelKey]Rate
}

const maxOverrideBytes = 1 << 20

// NewCatalog loads built-in rates and optionally replaces/adds exact keys from a
// JSON array of Rate objects, limited to 1 MiB. Empty path uses built-ins only.
// Explicit paths must exist. Unknown fields, missing input/output prices, null
// entries, duplicate keys, trailing data and invalid rates are rejected.
func NewCatalog(overridesPath string) (*Catalog, error) {
	c := &Catalog{rates: make(map[modelKey]Rate)}
	for _, r := range builtinRates() {
		if err := validateRate(r); err != nil {
			return nil, err
		}
		c.rates[modelKey{r.Provider, r.Model}] = r
	}
	if overridesPath == "" {
		return c, nil
	}
	f, err := os.Open(overridesPath)
	if err != nil {
		return nil, fmt.Errorf("open pricing overrides: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxOverrideBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read pricing overrides: %w", err)
	}
	if len(data) > maxOverrideBytes {
		return nil, errors.New("pricing overrides exceed 1 MiB")
	}
	// Pointer fields distinguish a genuinely free rate from omitted/null prices.
	type override struct {
		Provider             string   `json:"provider"`
		Model                string   `json:"model"`
		InputPerMillion      *float64 `json:"input_per_million"`
		OutputPerMillion     *float64 `json:"output_per_million"`
		CacheReadPerMillion  *float64 `json:"cache_read_per_million,omitempty"`
		CacheWritePerMillion *float64 `json:"cache_write_per_million,omitempty"`
	}
	var entries []*override
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode pricing overrides: %w", err)
	}
	if entries == nil {
		return nil, errors.New("pricing overrides must be an array, not null")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("pricing overrides contain trailing data")
	}
	seen := make(map[modelKey]bool, len(entries))
	for i, entry := range entries {
		if entry == nil || entry.InputPerMillion == nil || entry.OutputPerMillion == nil {
			return nil, fmt.Errorf("pricing override %d requires an object with input and output prices", i)
		}
		r := Rate{entry.Provider, entry.Model, *entry.InputPerMillion, *entry.OutputPerMillion, entry.CacheReadPerMillion, entry.CacheWritePerMillion}
		if err := validateRate(r); err != nil {
			return nil, fmt.Errorf("pricing override %d: %w", i, err)
		}
		key := modelKey{r.Provider, r.Model}
		if seen[key] {
			return nil, fmt.Errorf("duplicate pricing override for %q/%q", r.Provider, r.Model)
		}
		seen[key] = true
		c.rates[key] = r
	}
	return c, nil
}

func validateRate(r Rate) error {
	if r.Provider == "" || r.Model == "" || strings.TrimSpace(r.Provider) != r.Provider || strings.TrimSpace(r.Model) != r.Model {
		return errors.New("pricing provider and model must be nonempty without surrounding whitespace")
	}
	values := [4]float64{r.InputPerMillion, r.OutputPerMillion, r.InputPerMillion, r.InputPerMillion}
	if r.CacheReadPerMillion != nil {
		values[2] = *r.CacheReadPerMillion
	}
	if r.CacheWritePerMillion != nil {
		values[3] = *r.CacheWritePerMillion
	}
	for _, v := range values {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("pricing rates must be finite and nonnegative")
		}
	}
	return nil
}

// Estimate returns the estimated USD cost for an exact, case-sensitive key.
// It never strips prefixes, infers aliases, or treats an unknown model as free.
// Usage may aggregate multiple requests; no context-length tier is inferred.
func (c *Catalog) Estimate(provider, model string, usage Usage) (float64, error) {
	if usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheWriteTokens < 0 {
		return 0, errors.New("token counts must be nonnegative")
	}
	// Subtraction avoids overflowing a sum of two large cache counts.
	if usage.CacheReadTokens > usage.PromptTokens || usage.CacheWriteTokens > usage.PromptTokens-usage.CacheReadTokens {
		return 0, errors.New("cache token subsets exceed prompt tokens")
	}
	if c == nil {
		return 0, fmt.Errorf("%w: %q/%q", ErrUnknownModel, provider, model)
	}
	r, ok := c.rates[modelKey{provider, model}]
	if !ok {
		return 0, fmt.Errorf("%w: %q/%q", ErrUnknownModel, provider, model)
	}
	read, write := r.InputPerMillion, r.InputPerMillion
	if r.CacheReadPerMillion != nil {
		read = *r.CacheReadPerMillion
	}
	if r.CacheWritePerMillion != nil {
		write = *r.CacheWritePerMillion
	}
	cost := float64(usage.PromptTokens-usage.CacheReadTokens-usage.CacheWriteTokens)/1e6*r.InputPerMillion +
		float64(usage.CompletionTokens)/1e6*r.OutputPerMillion +
		float64(usage.CacheReadTokens)/1e6*read +
		float64(usage.CacheWriteTokens)/1e6*write
	if math.IsInf(cost, 0) || math.IsNaN(cost) {
		return 0, errors.New("token cost overflows USD representation")
	}
	return cost, nil
}
