package accounts

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/pricing"
)

type Cost struct {
	Amount      *float64 `json:"amount"`
	Currency    string   `json:"currency"`
	Source      string   `json:"source"`
	Scope       string   `json:"scope"`
	PeriodStart string   `json:"periodStart"`
	PeriodEnd   string   `json:"periodEnd"`
	ObservedAt  string   `json:"observedAt"`
	Status      string   `json:"status"`
}

type Daily struct {
	Date     string   `json:"date"`
	Amount   *float64 `json:"amount"`
	Tokens   int64    `json:"tokens"`
	Requests int64    `json:"requests"`
}

type Model struct {
	Model            string   `json:"model"`
	Amount           *float64 `json:"amount"`
	InputTokens      int64    `json:"inputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	CacheReadTokens  int64    `json:"cacheReadTokens"`
	CacheWriteTokens int64    `json:"cacheWriteTokens"`
	Requests         int64    `json:"requests"`
}

type Usage struct {
	AccountID int64   `json:"accountId"`
	Provider  string  `json:"provider"`
	Name      string  `json:"name"`
	Mode      string  `json:"mode"`
	Quota     any     `json:"quota"`
	Cost      Cost    `json:"cost"`
	Daily     []Daily `json:"daily"`
	Models    []Model `json:"models"`
	Message   string  `json:"message"`
}

// Usage aggregates observed calls for one stable account ID. Provider totals
// retain their own period/scope; local daily/model breakdowns are never presented
// as a reconciliation of an organization-wide invoice.
func (s *Service) Usage(id int64, duration time.Duration) (*Usage, error) {
	if duration <= 0 || duration > 90*24*time.Hour {
		return nil, fmt.Errorf("usage range must be between 1 and 90 days")
	}
	row, err := s.store.GetProviderAccountByID(id)
	if err != nil {
		return nil, err
	}
	if row == nil || row.DeletedAt != nil {
		return nil, fmt.Errorf("account not found")
	}
	settings, err := ParseSettings(row.Metadata)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	start := now.Add(-duration)
	usage := &Usage{AccountID: id, Provider: row.Provider, Name: row.Name, Mode: settings.Mode, Daily: []Daily{}, Models: []Model{}, Cost: Cost{Currency: "USD", Source: "unavailable", Scope: "observed calls for this account", PeriodStart: start.Format(time.RFC3339), PeriodEnd: now.Format(time.RFC3339), Status: "No observed token usage or provider bill is available."}}
	groups, err := s.store.QueryAccountUsageGroups(id, row.Provider, row.Name, start, now)
	if err != nil {
		return nil, err
	}
	type dayTotal struct {
		row     Daily
		amount  float64
		unknown bool
	}
	type modelTotal struct {
		row     Model
		amount  float64
		unknown bool
	}
	days := map[string]*dayTotal{}
	models := map[string]*modelTotal{}
	var total, knownSubtotal float64
	var requests, unknownRequests int64
	for _, group := range groups {
		amount := group.ReportedCost
		unknown := false
		if group.PricedRequests < group.Requests {
			cost, priceErr := s.pricing.Estimate(row.Provider, group.Model, pricing.Usage{PromptTokens: group.UnpricedPromptTokens, CompletionTokens: group.UnpricedCompletionTokens, CacheReadTokens: group.UnpricedCacheReadTokens, CacheWriteTokens: group.UnpricedCacheWriteTokens})
			if priceErr != nil && row.Provider == "custom" && settings.SourceProvider != "" {
				cost, priceErr = s.pricing.Estimate(settings.SourceProvider, group.Model, pricing.Usage{PromptTokens: group.UnpricedPromptTokens, CompletionTokens: group.UnpricedCompletionTokens, CacheReadTokens: group.UnpricedCacheReadTokens, CacheWriteTokens: group.UnpricedCacheWriteTokens})
			}
			if priceErr != nil {
				unknown = true
				unknownRequests += group.Requests - group.PricedRequests
			} else {
				amount += cost
			}
		}
		day := days[group.Date]
		if day == nil {
			day = &dayTotal{row: Daily{Date: group.Date}}
			days[group.Date] = day
		}
		day.amount += amount
		day.unknown = day.unknown || unknown
		day.row.Tokens += group.PromptTokens + group.CompletionTokens
		day.row.Requests += group.Requests
		model := models[group.Model]
		if model == nil {
			model = &modelTotal{row: Model{Model: group.Model}}
			models[group.Model] = model
		}
		model.amount += amount
		model.unknown = model.unknown || unknown
		model.row.InputTokens += group.PromptTokens
		model.row.OutputTokens += group.CompletionTokens
		model.row.CacheReadTokens += group.CacheReadTokens
		model.row.CacheWriteTokens += group.CacheWriteTokens
		model.row.Requests += group.Requests
		total += amount
		knownSubtotal += amount
		requests += group.Requests
	}
	for _, day := range days {
		if !day.unknown {
			amount := day.amount
			day.row.Amount = &amount
		}
		usage.Daily = append(usage.Daily, day.row)
	}
	for _, model := range models {
		if !model.unknown {
			amount := model.amount
			model.row.Amount = &amount
		}
		usage.Models = append(usage.Models, model.row)
	}
	sort.Slice(usage.Daily, func(i, j int) bool { return usage.Daily[i].Date < usage.Daily[j].Date })
	sort.Slice(usage.Models, func(i, j int) bool { return usage.Models[i].Model < usage.Models[j].Model })
	if requests > 0 {
		usage.Cost.Source = "local_estimate"
		usage.Cost.ObservedAt = now.Format(time.RFC3339)
		usage.Cost.Status = "Observed API calls with onWatch-native pricing or supplied per-call costs; not an invoice."
		if unknownRequests == 0 {
			usage.Cost.Amount = &total
		} else {
			usage.Cost.Status = fmt.Sprintf("Incomplete pricing: %d requests have no exact model rate. Known subtotal: $%.6f; total remains unknown.", unknownRequests, knownSubtotal)
		}
	}
	report, checked, billingMessage, err := s.store.QueryAccountBill(id)
	if err != nil {
		return nil, err
	}
	if report != nil {
		amount := report.Amount
		usage.Cost = Cost{Amount: &amount, Currency: report.Currency, Source: "provider", Scope: report.Scope, PeriodEnd: report.PeriodEnd.Format(time.RFC3339), ObservedAt: report.ObservedAt.Format(time.RFC3339), Status: "Provider-reported spend. Daily/model tables below show observed calls only and may not reconcile with this bill."}
		if report.PeriodStart.IsZero() {
			usage.Cost.PeriodStart = "Lifetime"
		} else {
			usage.Cost.PeriodStart = report.PeriodStart.Format(time.RFC3339)
		}
		if billingMessage != "" || time.Since(report.ObservedAt) > 15*time.Minute {
			usage.Cost.Status = "Last available provider report; it may be stale. " + usage.Cost.Status
		}
	}
	if !checked.IsZero() && billingMessage != "" {
		usage.Message = billingMessage
	}
	if requests == 0 && report == nil {
		usage.Message = strings.TrimSpace(usage.Message + " No observed calls are available. Record API response usage in the API Integrations JSONL feed; an API key alone cannot reconstruct token usage.")
	}
	s.mu.RLock()
	quotaError := s.quotaErrors[id]
	s.mu.RUnlock()
	if quotaError != "" {
		usage.Message = strings.TrimSpace(usage.Message + " " + quotaError)
	}
	return usage, nil
}
