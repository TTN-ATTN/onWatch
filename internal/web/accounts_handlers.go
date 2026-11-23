package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/accounts"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func (h *Handler) SetAccountsService(service *accounts.Service) { h.accounts = service }

func (h *Handler) AccountsPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.accountsTmpl == nil {
		respondError(w, http.StatusInternalServerError, "could not load accounts page")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.accountsTmpl.ExecuteTemplate(w, "layout.html", map[string]any{"Title": "Accounts & Costs", "AccountsPage": true, "BasePath": h.getBasePath(), "Version": h.version}); err != nil {
		h.logger.Error("rendering accounts page failed", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (h *Handler) Accounts(w http.ResponseWriter, r *http.Request) {
	if h.accounts == nil {
		respondError(w, http.StatusServiceUnavailable, "account management is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := h.accounts.List()
		if err != nil {
			respondError(w, http.StatusInternalServerError, "could not list accounts")
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"accounts": list, "providers": accounts.Providers(), "discovery": h.accounts.Discovery()})
	case http.MethodPost, http.MethodPut:
		var input accounts.Input
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			respondError(w, http.StatusBadRequest, "invalid account payload")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			respondError(w, http.StatusBadRequest, "expected one account payload")
			return
		}
		if (r.Method == http.MethodPost && input.ID != 0) || (r.Method == http.MethodPut && input.ID <= 0) {
			respondError(w, http.StatusBadRequest, "invalid account ID for request method")
			return
		}
		id, err := h.accounts.Save(input)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		if h.responses != nil {
			h.responses.invalidateAll()
		}
		h.invalidateMenubarSnapshot()
		respondJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
	case http.MethodDelete:
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id <= 0 {
			respondError(w, http.StatusBadRequest, "invalid account ID")
			return
		}
		if err := h.accounts.Delete(id); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		if h.responses != nil {
			h.responses.invalidateAll()
		}
		h.invalidateMenubarSnapshot()
		respondJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) DiscoverAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.accounts == nil {
		respondError(w, http.StatusServiceUnavailable, "account management is unavailable")
		return
	}
	count, err := h.accounts.Discover(r.Context())
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.responses != nil {
		h.responses.invalidateAll()
	}
	respondJSON(w, http.StatusOK, map[string]any{"success": true, "imported": count, "message": h.accounts.Discovery().Message})
}

// AccountAPIKey returns one account's API key for the dashboard copy button.
// POST keeps the key out of URLs, history and link prefetch; respondJSON marks
// the response no-store, and the key itself is never logged.
func (h *Handler) AccountAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.accounts == nil {
		respondError(w, http.StatusServiceUnavailable, "account management is unavailable")
		return
	}
	var input struct {
		ID int64 `json:"id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ID <= 0 {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}
	key, err := h.accounts.APIKey(input.ID)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info("account API key copied", "account_id", input.ID)
	respondJSON(w, http.StatusOK, map[string]string{"apiKey": key})
}

func (h *Handler) AccountUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.accounts == nil {
		respondError(w, http.StatusServiceUnavailable, "account management is unavailable")
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("account"), 10, 64)
	if err != nil || id <= 0 {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}
	duration := 30 * 24 * time.Hour
	switch r.URL.Query().Get("range") {
	case "", "30d":
	case "7d":
		duration = 7 * 24 * time.Hour
	case "90d":
		duration = 90 * 24 * time.Hour
	default:
		respondError(w, http.StatusBadRequest, "range must be 7d, 30d, or 90d")
		return
	}
	usage, err := h.accounts.Usage(id, duration)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if usage.Mode == "quota" {
		row, err := h.store.GetProviderAccountByID(id)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "could not read account")
			return
		}
		scoped, err := h.forAccount(*row)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		usage.Quota = scoped.accountCurrent(r, row.Provider, id)
	}
	respondJSON(w, http.StatusOK, usage)
}

// accountRoute validates scope before any provider endpoint reads its tables.
// Existing native "all accounts" endpoints retain their aggregate contract.
func (h *Handler) accountRoute(endpoint func(*Handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scoped := h
		if value := r.URL.Query().Get("account"); h.accounts != nil && value != "" && value != "all" {
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				respondError(w, http.StatusBadRequest, "invalid account ID")
				return
			}
			row, err := h.store.GetProviderAccountByID(id)
			if err != nil {
				respondError(w, http.StatusInternalServerError, "could not read account")
				return
			}
			if row == nil || row.DeletedAt != nil {
				respondError(w, http.StatusNotFound, "account not found")
				return
			}
			if provider := r.URL.Query().Get("provider"); provider != "" && provider != row.Provider {
				respondError(w, http.StatusBadRequest, "account does not belong to requested provider")
				return
			}
			scoped, err = h.forAccount(*row)
			if err != nil {
				respondError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		endpoint(scoped, w, r)
	}
}

func nativeAccountProvider(provider string) bool {
	return provider == "codex" || provider == "minimax" || provider == "alibaba"
}

func (h *Handler) forAccount(row store.ProviderAccount) (*Handler, error) {
	settings, err := accounts.ParseSettings(row.Metadata)
	if err != nil {
		return nil, err
	}
	if settings.Mode != "quota" {
		return nil, fmt.Errorf("this account uses cost tracking; open Accounts & Costs")
	}
	if !settings.Managed && nativeAccountProvider(row.Provider) {
		return h, nil
	}
	resolved, resolveErr := h.accounts.Resolve(row)
	if resolveErr == nil {
		settings = resolved
	} // Missing credentials do not erase readable history.
	scope := row.ID
	if settings.Legacy {
		scope = 0
	}
	st := h.store.ForAccount(scope)
	cfg := accounts.RuntimeConfig(h.config, row.Provider, settings)
	// A fresh value copies dependencies only; locks and menubar caches start zeroed.
	child := &Handler{store: st, config: &cfg, accounts: h.accounts, logger: h.logger, version: h.version, responses: h.responses, pollStatus: h.pollStatus, codexPauseReporter: h.codexPauseReporter, notifier: h.notifier, sessions: h.sessions, metrics: h.metrics, updater: h.updater, agentManager: h.agentManager, minimaxAgentMgr: h.minimaxAgentMgr, alibabaAgentMgr: h.alibabaAgentMgr, zenmuxUsageFetch: h.zenmuxUsageFetch, rateLimiter: h.rateLimiter, dashboardTmpl: h.dashboardTmpl, loginTmpl: h.loginTmpl, settingsTmpl: h.settingsTmpl, accountsTmpl: h.accountsTmpl}
	switch row.Provider {
	case "synthetic":
		child.tracker = tracker.New(st, h.logger)
	case "zai":
		child.zaiTracker = tracker.NewZaiTracker(st, h.logger)
	case "anthropic":
		child.anthropicTracker = tracker.NewAnthropicTracker(st, h.logger)
	case "copilot":
		child.copilotTracker = tracker.NewCopilotTracker(st, h.logger)
	case "codex":
		child.codexTracker = tracker.NewCodexTracker(st, h.logger)
	case "antigravity":
		child.antigravityTracker = tracker.NewAntigravityTracker(st, h.logger)
	case "minimax":
		child.minimaxTracker = tracker.NewMiniMaxTracker(st, h.logger)
	case "openrouter":
		child.openrouterTracker = tracker.NewOpenRouterTracker(st, h.logger)
	case "moonshot":
		child.moonshotTracker = tracker.NewMoonshotTracker(st, h.logger)
	case "deepseek":
		child.deepseekTracker = tracker.NewDeepSeekTracker(st, h.logger)
	case "gemini":
		child.geminiTracker = tracker.NewGeminiTracker(st, h.logger)
	case "cursor":
		child.cursorTracker = tracker.NewCursorTracker(st, h.logger)
	case "grok":
		child.grokTracker = tracker.NewGrokTracker(st, h.logger)
	case "kimi":
		child.kimiTracker = tracker.NewKimiTracker(st, h.logger)
	case "opencode":
		child.opencodeTracker = tracker.NewOpenCodeTracker(st, h.logger)
	case "ollama":
		child.ollamaTracker = tracker.NewOllamaTracker(st, h.logger)
	case "muse":
		child.museTracker = tracker.NewMuseTracker(st, h.logger)
	case "commandcode":
		child.commandCodeTracker = tracker.NewCommandCodeTracker(st, h.logger)
	case "alibaba":
		child.alibabaTracker = tracker.NewAlibabaTracker(st, h.logger)
	}
	return child, nil
}

func (h *Handler) accountCurrent(r *http.Request, provider string, id int64) any {
	switch provider {
	case "synthetic":
		return h.buildSyntheticCurrent()
	case "zai":
		return h.buildZaiCurrent()
	case "anthropic":
		return h.buildAnthropicCurrent()
	case "copilot":
		return h.buildCopilotCurrent()
	case "codex":
		return h.buildCodexCurrent(id)
	case "antigravity":
		return h.buildAntigravityCurrent()
	case "minimax":
		return h.buildMiniMaxCurrent(id)
	case "openrouter":
		return h.buildOpenRouterCurrent()
	case "moonshot":
		return h.buildMoonshotCurrent()
	case "deepseek":
		return h.buildDeepSeekCurrent()
	case "gemini":
		return h.buildGeminiCurrent()
	case "cursor":
		return h.buildCursorCurrent()
	case "grok":
		return h.buildGrokCurrent()
	case "kimi":
		return h.buildKimiCurrent()
	case "mistral":
		return h.buildMistralCurrent()
	case "opencode":
		return h.buildOpenCodeCurrent()
	case "ollama":
		return h.buildOllamaCurrent()
	case "muse":
		return h.buildMuseCurrent()
	case "commandcode":
		return h.buildCommandCodeCurrent()
	case "zenmux":
		return h.buildZenmuxCurrent(r.Context())
	case "alibaba":
		return h.buildAlibabaCurrent(id)
	}
	return nil
}

func (h *Handler) availableAccountProviders() []string {
	var providers []string
	if h.config != nil {
		providers = h.config.AvailableProviders()
	}
	if h.accounts != nil {
		for _, provider := range h.accounts.QuotaProviders() {
			found := false
			for _, existing := range providers {
				if provider == existing {
					found = true
					break
				}
			}
			if !found {
				providers = append(providers, provider)
			}
		}
	}
	return providers
}

func containsProvider(providers []string, provider string) bool {
	for _, candidate := range providers {
		if candidate == provider {
			return true
		}
	}
	return false
}

type dashboardAccountOption struct {
	ID   int64
	Name string
}

// dashboardAccountOptions lists quota accounts for providers without a native
// selector. A selector appears once a managed account exists, so the first
// managed account never silently renders the empty legacy scope.
func (h *Handler) dashboardAccountOptions(provider, requested string) ([]dashboardAccountOption, int64, error) {
	if h.accounts == nil || provider == "" || provider == "both" || nativeAccountProvider(provider) {
		return nil, 0, nil
	}
	list, err := h.accounts.List()
	if err != nil {
		return nil, 0, err
	}
	options := make([]dashboardAccountOption, 0)
	managed := false
	for _, account := range list {
		if account.Provider != provider || account.Mode != "quota" {
			continue
		}
		options = append(options, dashboardAccountOption{ID: account.ID, Name: account.Name})
		managed = managed || !account.Legacy
	}
	if !managed {
		return nil, 0, nil
	}
	if requested != "" {
		id, err := strconv.ParseInt(requested, 10, 64)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid account ID")
		}
		for _, option := range options {
			if option.ID == id {
				return options, id, nil
			}
		}
		return nil, 0, fmt.Errorf("account does not belong to this provider")
	}
	return options, options[0].ID, nil
}
