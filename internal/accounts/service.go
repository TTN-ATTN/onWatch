package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/billing"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/omp"
	"github.com/onllm-dev/onwatch/v2/internal/pricing"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// Service is optional integration around the existing provider account registry.
// Manual accounts, provider bills, and local pricing work without omp files.
type Service struct {
	store         *store.Store
	config        *config.Config
	pricing       *pricing.Catalog
	billing       *billing.Client
	keyMu         sync.RWMutex
	encryptionKey string
	logger        *slog.Logger
	wake          chan struct{}
	billWake      chan struct{}
	updateMu      sync.Mutex
	discoverMu    sync.Mutex
	mu            sync.RWMutex
	sources       map[string]omp.Credential
	discovery     Discovery
	quotaErrors   map[int64]string
	ompDir        string
}

type Discovery struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Message   string `json:"message"`
}

type Account struct {
	ID            int64  `json:"id"`
	Provider      string `json:"provider"`
	Name          string `json:"name"`
	Source        string `json:"source"`
	Enabled       bool   `json:"enabled"`
	Mode          string `json:"mode"`
	BaseURL       string `json:"baseUrl"`
	BillingKind   string `json:"billingKind"`
	HasCredential bool   `json:"hasCredential"`
	Legacy        bool   `json:"legacy"`
	ReadOnly      bool   `json:"readOnly"`
}

type Input struct {
	ID          int64             `json:"id"`
	Provider    string            `json:"provider"`
	Name        string            `json:"name"`
	Credentials map[string]string `json:"credentials"`
	BaseURL     string            `json:"baseUrl"`
	Mode        string            `json:"mode"`
	BillingKind string            `json:"billingKind"`
	Enabled     *bool             `json:"enabled"`
}

// New prepares account management. encryptionKey is the same AES key used for
// other dashboard secrets; RotateEncryptionKey must follow password changes.
func New(st *store.Store, cfg *config.Config, catalog *pricing.Catalog, encryptionKey string, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if st == nil || cfg == nil || catalog == nil || encryptionKey == "" {
		return nil, fmt.Errorf("account storage, configuration, pricing, and encryption key are required")
	}
	s := &Service{store: st, config: cfg, pricing: catalog, billing: billing.NewClient(nil), encryptionKey: encryptionKey, logger: logger, wake: make(chan struct{}, 1), billWake: make(chan struct{}, 1), sources: make(map[string]omp.Credential), quotaErrors: make(map[int64]string), ompDir: omp.DefaultDir()}
	s.discovery.Enabled = cfg.Snapshot().OMPDiscoveryEnabled
	if !s.discovery.Enabled {
		s.discovery.Message = "Optional omp discovery is disabled (ONWATCH_OMP_ENABLED=false)."
	}
	if err := s.seedConfigured(); err != nil {
		return nil, err
	}
	if s.discovery.Enabled {
		if _, err := s.Discover(context.Background()); err != nil {
			logger.Warn("optional omp credential discovery unavailable", "error", err)
		}
	}
	return s, nil
}

func (s *Service) seedConfigured() error {
	all, err := s.store.QueryAllProviderAccounts()
	if err != nil {
		return err
	}
	for _, provider := range s.config.AvailableProviders() {
		if provider == "codex" || provider == "minimax" || provider == "alibaba" {
			continue
		}
		if _, known := ProviderFor(provider); !known {
			continue
		}
		found := false
		for _, account := range all {
			settings, err := ParseSettings(account.Metadata)
			if err != nil {
				return err
			}
			if account.Provider == provider && (account.Name == "default" || settings.Legacy) {
				found = true
				break
			}
		}
		if found {
			continue
		}
		metadata, _ := json.Marshal(Settings{Legacy: true, Source: "onwatch", Mode: "quota", BillingKind: "auto"})
		if _, err := s.store.SaveManagedProviderAccount(0, provider, "default", string(metadata), ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Discovery() Discovery { s.mu.RLock(); defer s.mu.RUnlock(); return s.discovery }

func (s *Service) Signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
	select {
	case s.billWake <- struct{}{}:
	default:
	}
}

func (s *Service) key() string { s.keyMu.RLock(); defer s.keyMu.RUnlock(); return s.encryptionKey }

// RotateEncryptionKey re-encrypts every stored manual credential, including
// soft-deleted rows that may be restored, in one transaction. omp credentials
// are references and are never stored.
func (s *Service) RotateEncryptionKey(oldKey, newKey string) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if newKey == "" {
		return fmt.Errorf("new encryption key is empty")
	}
	rows, err := s.store.QueryAllProviderAccounts()
	if err != nil {
		return err
	}
	updates := make(map[int64]string)
	for _, row := range rows {
		settings, err := ParseSettings(row.Metadata)
		if err != nil {
			return fmt.Errorf("account %d has invalid settings", row.ID)
		}
		if !settings.Managed || settings.Source == "omp" || len(settings.Credentials) == 0 {
			continue
		}
		for field, value := range settings.Credentials {
			plain, err := notify.DecryptFromStorage(value, oldKey)
			if err != nil {
				return fmt.Errorf("account %d credentials could not be decrypted", row.ID)
			}
			encrypted, err := notify.EncryptForStorage(plain, newKey)
			if err != nil {
				return fmt.Errorf("account %d credentials could not be encrypted", row.ID)
			}
			settings.Credentials[field] = encrypted
		}
		data, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		updates[row.ID] = string(data)
	}
	if err := s.store.ReplaceProviderAccountMetadata(updates); err != nil {
		return err
	}
	s.keyMu.Lock()
	s.encryptionKey = newKey
	s.keyMu.Unlock()
	s.Signal()
	return nil
}

func secretField(provider, field string) bool {
	definition, ok := ProviderFor(provider)
	if !ok {
		return false
	}
	for _, candidate := range definition.Fields {
		if candidate.Key == field {
			return candidate.Secret
		}
	}
	return false
}

func secretDigest(provider, value string) [32]byte {
	return sha256.Sum256([]byte(provider + "\x00" + value))
}

// knownSecrets fingerprints credentials onWatch already polls, in memory only,
// so an omp import of the same key cannot double-spend a provider rate limit.
func (s *Service) knownSecrets(rows []store.ProviderAccount) map[[32]byte]bool {
	seen := make(map[[32]byte]bool)
	for _, provider := range Providers() {
		for field, value := range ConfigCredentials(s.config, provider.ID) {
			if value != "" && secretField(provider.ID, field) {
				seen[secretDigest(provider.ID, value)] = true
			}
		}
	}
	for _, row := range rows {
		settings, err := ParseSettings(row.Metadata)
		if err != nil || row.DeletedAt != nil || !settings.Managed || settings.Source == "omp" || !settings.IsEnabled() {
			continue
		}
		resolved, err := s.Resolve(row)
		if err != nil {
			continue
		}
		for field, value := range resolved.Credentials {
			if value != "" && secretField(row.Provider, field) {
				seen[secretDigest(row.Provider, value)] = true
			}
		}
	}
	return seen
}

func markSeen(seen map[[32]byte]bool, credential omp.Credential) bool {
	duplicate := false
	for field, value := range credential.Values {
		if value == "" || !secretField(credential.Provider, field) {
			continue
		}
		digest := secretDigest(credential.Provider, value)
		duplicate = duplicate || seen[digest]
		seen[digest] = true
	}
	return duplicate
}

func (s *Service) Discover(ctx context.Context) (int, error) {
	s.discoverMu.Lock()
	defer s.discoverMu.Unlock()
	if !s.Discovery().Enabled {
		return 0, fmt.Errorf("omp discovery is disabled")
	}
	credentials, err := omp.Discover(ctx, s.ompDir)
	if err != nil {
		s.mu.Lock()
		s.discovery.Available = false
		s.discovery.Message = err.Error()
		s.mu.Unlock()
		return 0, err
	}
	all, err := s.store.QueryAllProviderAccounts()
	if err != nil {
		return 0, err
	}
	pathHash := sha256.Sum256([]byte(s.ompDir))
	prefix := "omp:" + hex.EncodeToString(pathHash[:8]) + ":"
	sources := make(map[string]omp.Credential, len(credentials))
	seen := s.knownSecrets(all)
	externalID := func(credential omp.Credential) string { return prefix + credential.Provider + ":" + credential.ID }
	existing := make(map[string]bool, len(all))
	for _, account := range all {
		if account.ExternalID != "" {
			existing[account.ExternalID] = true
		}
	}
	// Existing imports claim their keys first, so a later alias of the same key
	// is imported paused instead of polling one rate limit twice.
	for _, credential := range credentials {
		if existing[externalID(credential)] {
			markSeen(seen, credential)
		}
	}
	imported := 0
	for _, credential := range credentials {
		sources[credential.ID] = credential
		external := externalID(credential)
		if existing[external] {
			for _, account := range all {
				if account.ExternalID != external || account.DeletedAt != nil {
					continue
				}
				settings, err := ParseSettings(account.Metadata)
				if err != nil {
					return imported, err
				}
				if settings.SourceBaseURL != credential.BaseURL {
					if settings.BaseURL == settings.SourceBaseURL {
						settings.BaseURL = credential.BaseURL
					}
					settings.SourceBaseURL = credential.BaseURL
					metadata, err := json.Marshal(settings)
					if err != nil {
						return imported, err
					}
					if err := s.store.UpdateProviderAccountMetadata(account.ID, string(metadata)); err != nil {
						return imported, err
					}
				}
			}
			continue // Includes tombstones: explicit removal wins over rediscovery.
		}
		name := "omp / " + credential.Name
		for len(name) > 180 {
			_, size := utf8.DecodeLastRuneInString(name)
			name = name[:len(name)-size]
		}
		for _, account := range all {
			if account.Provider == credential.Provider && account.Name == name {
				digest := sha256.Sum256([]byte(external))
				name += " / " + hex.EncodeToString(digest[:4])
				break
			}
		}
		settings := Settings{Managed: true, Source: "omp", SourceID: credential.ID, SourceProvider: credential.SourceProvider, SourceBaseURL: credential.BaseURL, BaseURL: credential.BaseURL, Mode: credential.Mode, BillingKind: "auto"}
		if settings.Mode == "" {
			settings.Mode = "cost"
		}
		// Muse polls spend prompts; a duplicate key would double rate-limit use.
		duplicate := markSeen(seen, credential)
		if credential.Provider == "muse" || duplicate {
			disabled := false
			settings.Enabled = &disabled
		}
		if err := Validate(credential.Provider, name, settings); err != nil {
			return imported, err
		}
		metadata, _ := json.Marshal(settings)
		id, err := s.store.SaveManagedProviderAccount(0, credential.Provider, name, string(metadata), external)
		if err != nil {
			return imported, err
		}
		all = append(all, store.ProviderAccount{ID: id, Provider: credential.Provider, Name: name, Metadata: string(metadata), ExternalID: external})
		existing[external] = true
		imported++
	}
	s.mu.Lock()
	s.sources = sources
	s.discovery.Available = len(credentials) > 0
	if len(credentials) == 0 {
		s.discovery.Message = "No omp credentials found. Manual accounts and local pricing remain available."
	} else {
		s.discovery.Message = fmt.Sprintf("%d optional omp credential sources available. Credentials are read-only.", len(credentials))
	}
	s.mu.Unlock()
	s.Signal()
	return imported, nil
}

func (s *Service) Resolve(account store.ProviderAccount) (Settings, error) {
	settings, err := ParseSettings(account.Metadata)
	if err != nil {
		return settings, err
	}
	if settings.Source == "omp" {
		s.mu.RLock()
		source, ok := s.sources[settings.SourceID]
		available := s.discovery.Available
		s.mu.RUnlock()
		if !ok || !available {
			return settings, fmt.Errorf("optional omp credential source is unavailable")
		}
		settings.Credentials = source.Values
		return settings, nil
	}
	if settings.Legacy && !settings.Managed {
		settings.Credentials = ConfigCredentials(s.config, account.Provider)
		return settings, nil
	}
	if !settings.Managed {
		// Existing MiniMax/Alibaba accounts retain their established metadata.
		var legacy map[string]json.RawMessage
		if json.Unmarshal([]byte(account.Metadata), &legacy) == nil {
			settings.Credentials = make(map[string]string)
			for _, field := range []string{"api_key", "region", "cookies"} {
				var value string
				if json.Unmarshal(legacy[field], &value) == nil {
					settings.Credentials[field] = value
				}
			}
		}
		return settings, nil
	}
	credentials := make(map[string]string, len(settings.Credentials))
	for field, value := range settings.Credentials {
		plain, err := notify.DecryptFromStorage(value, s.key())
		if err != nil {
			return settings, fmt.Errorf("cannot decrypt account credentials")
		}
		credentials[field] = plain
	}
	settings.Credentials = credentials
	return settings, nil
}

func hasCredential(values map[string]string) bool {
	for _, field := range []string{"api_key", "token", "access_token", "refresh_token", "csrf_token", "auth_cookie", "cookies", "management_api_key"} {
		if values[field] != "" {
			return true
		}
	}
	return false
}

// ResolveOMPProvider maps the omp provider name on a reported call to the one
// active account discovered from it. Ambiguous names (one omp provider holding
// several logins) stay unbound rather than guessing which login was used.
func (s *Service) ResolveOMPProvider(ompProvider string) (int64, string, bool) {
	rows, err := s.store.QueryAllProviderAccounts()
	if err != nil {
		return 0, "", false
	}
	var id int64
	var provider string
	for _, row := range rows {
		if row.DeletedAt != nil {
			continue
		}
		settings, err := ParseSettings(row.Metadata)
		if err != nil || settings.Source != "omp" || settings.SourceProvider != ompProvider {
			continue
		}
		if id != 0 {
			return 0, "", false
		}
		id, provider = row.ID, row.Provider
	}
	return id, provider, id != 0
}

func (s *Service) List() ([]Account, error) {
	rows, err := s.store.QueryAllProviderAccounts()
	if err != nil {
		return nil, err
	}
	result := make([]Account, 0, len(rows))
	for _, row := range rows {
		if row.DeletedAt != nil {
			continue
		}
		settings, err := ParseSettings(row.Metadata)
		if err != nil {
			return nil, err
		}
		resolved, resolveErr := s.Resolve(row)
		credentialed := resolveErr == nil && hasCredential(resolved.Credentials)
		// Store-seeded native placeholders (Codex id 1, MiniMax, Alibaba) are not
		// accounts until their provider is configured or they hold credentials.
		if !settings.Managed && !settings.Legacy && !credentialed && !s.config.HasProvider(row.Provider) {
			continue
		}
		result = append(result, Account{ID: row.ID, Provider: row.Provider, Name: row.Name, Source: settings.Source, Enabled: settings.IsEnabled(), Mode: settings.Mode, BaseURL: settings.BaseURL, BillingKind: settings.BillingKind, HasCredential: credentialed, Legacy: settings.Legacy, ReadOnly: settings.Source == "omp" || (!settings.Managed && !settings.Legacy)})
	}
	return result, nil
}

func (s *Service) Get(id int64) (*store.ProviderAccount, Settings, error) {
	row, err := s.store.GetProviderAccountByID(id)
	if err != nil {
		return nil, Settings{}, err
	}
	if row == nil || row.DeletedAt != nil {
		return nil, Settings{}, fmt.Errorf("account not found")
	}
	settings, err := s.Resolve(*row)
	return row, settings, err
}

func (s *Service) Save(input Input) (int64, error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.Name = strings.TrimSpace(input.Name)
	settings := Settings{Managed: true, Source: "manual", Mode: input.Mode, BillingKind: input.BillingKind, Credentials: map[string]string{}}
	var row *store.ProviderAccount
	var err error
	if input.ID != 0 {
		row, err = s.store.GetProviderAccountByID(input.ID)
		if err != nil {
			return 0, err
		}
		if row == nil || row.DeletedAt != nil || row.Provider != input.Provider {
			return 0, fmt.Errorf("account not found for provider")
		}
		settings, err = ParseSettings(row.Metadata)
		if err != nil {
			return 0, err
		}
		if settings.Source != "omp" {
			settings, err = s.Resolve(*row)
			if err != nil {
				return 0, err
			}
		}
	}
	before := settings
	if settings.Credentials == nil {
		settings.Credentials = map[string]string{}
	}
	if settings.Source == "omp" && len(input.Credentials) > 0 {
		return 0, fmt.Errorf("omp credentials are read-only; create a manual account to override them")
	}
	changedCredential := false
	for field, value := range input.Credentials {
		if value == "" {
			continue
		} // Blank password fields preserve an existing secret.
		settings.Credentials[field] = value
		changedCredential = true
	}
	if input.Mode != "" {
		settings.Mode = input.Mode
	}
	if settings.Mode == "" {
		settings.Mode = "quota"
	}
	if input.BillingKind != "" {
		settings.BillingKind = input.BillingKind
	}
	if settings.BillingKind == "" {
		settings.BillingKind = "auto"
	}
	settings.BaseURL = strings.TrimSpace(input.BaseURL)
	if input.Enabled != nil {
		settings.Enabled = input.Enabled
	}
	if changedCredential {
		settings.Managed = true
		settings.Source = "manual"
	}
	if err := Validate(input.Provider, input.Name, settings); err != nil {
		return 0, err
	}
	if settings.Managed && settings.Source != "omp" && !hasCredential(settings.Credentials) {
		return 0, fmt.Errorf("credentials are required")
	}
	if changedCredential {
		owner, err := s.credentialOwner(input.Provider, settings.Credentials, input.ID)
		if err != nil {
			return 0, err
		}
		if owner != "" {
			return 0, fmt.Errorf("this credential is already tracked by account %q; edit or resume that account instead", owner)
		}
	}
	if input.Provider == "muse" && settings.Mode == "quota" && settings.IsEnabled() && input.Enabled == nil {
		return 0, fmt.Errorf("Muse quota polling spends one prompt per poll; explicitly enable it to opt in")
	}
	external := ""
	if row != nil {
		external = row.ExternalID
		if err := s.store.BindAPIIntegrationAccount(row.Provider, row.Name, row.ID); err != nil {
			return 0, err
		}
	}
	var metadata []byte
	if !settings.Managed && row != nil {
		var original map[string]any
		if row.Metadata != "" {
			if err := json.Unmarshal([]byte(row.Metadata), &original); err != nil {
				return 0, fmt.Errorf("invalid existing account metadata")
			}
		}
		if original == nil {
			original = map[string]any{}
		}
		original["enabled"] = settings.Enabled
		original["mode"] = settings.Mode
		original["billing_kind"] = settings.BillingKind
		original["base_url"] = settings.BaseURL
		metadata, err = json.Marshal(original)
	} else {
		stored := settings
		stored.Credentials = make(map[string]string, len(settings.Credentials))
		for field, value := range settings.Credentials {
			if settings.Source == "omp" {
				break
			}
			encrypted, encryptErr := notify.EncryptForStorage(value, s.key())
			if encryptErr != nil {
				return 0, fmt.Errorf("cannot encrypt account credentials")
			}
			stored.Credentials[field] = encrypted
		}
		metadata, err = json.Marshal(stored)
	}
	if err != nil {
		return 0, err
	}
	id, err := s.store.SaveManagedProviderAccount(input.ID, input.Provider, input.Name, string(metadata), external)
	if err != nil {
		return 0, fmt.Errorf("could not save account: %w", err)
	}
	if err := s.store.BindAPIIntegrationAccount(input.Provider, input.Name, id); err != nil {
		return 0, err
	}
	if row != nil && (changedCredential || before.BillingKind != settings.BillingKind || before.BaseURL != settings.BaseURL || before.Mode != settings.Mode) {
		if err := s.store.ClearAccountBill(id); err != nil {
			return 0, err
		}
	}
	s.Signal()
	return id, nil
}

// credentialOwner names another active account of the same provider holding
// any of these secrets. One key maps to one account, so it is never polled
// twice against the same provider rate limit or double-counted in costs.
func (s *Service) credentialOwner(provider string, credentials map[string]string, exclude int64) (string, error) {
	wanted := make(map[[32]byte]bool)
	for field, value := range credentials {
		if value != "" && secretField(provider, field) {
			wanted[secretDigest(provider, value)] = true
		}
	}
	if len(wanted) == 0 {
		return "", nil
	}
	rows, err := s.store.QueryAllProviderAccounts()
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.ID == exclude || row.DeletedAt != nil || row.Provider != provider {
			continue
		}
		resolved, err := s.Resolve(row)
		if err != nil {
			continue // An unavailable source holds no live credential.
		}
		for field, value := range resolved.Credentials {
			if value != "" && secretField(provider, field) && wanted[secretDigest(provider, value)] {
				return row.Name, nil
			}
		}
	}
	return "", nil
}

func (s *Service) Delete(id int64) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	row, err := s.store.GetProviderAccountByID(id)
	if err != nil {
		return err
	}
	if row == nil || row.DeletedAt != nil {
		return fmt.Errorf("account not found")
	}
	if err := s.store.MarkProviderAccountDeletedByID(id); err != nil {
		return err
	}
	s.Signal()
	return nil
}

// ShouldPollLegacy lets existing default/profile managers honor per-account
// pause, cost mode, deletion and an explicit cutover to managed credentials.
func (s *Service) ShouldPollLegacy(provider string, id int64) bool {
	if s == nil {
		return true
	}
	if id != 0 {
		row, err := s.store.GetProviderAccountByID(id)
		if err != nil || row == nil {
			return false
		}
		if row.DeletedAt != nil {
			return false
		}
		settings, err := ParseSettings(row.Metadata)
		return err == nil && !settings.Managed && settings.IsEnabled() && settings.Mode == "quota"
	}
	rows, err := s.store.QueryProviderAccounts(provider)
	if err != nil {
		return false
	}
	for _, row := range rows {
		settings, err := ParseSettings(row.Metadata)
		if err != nil {
			return false
		}
		if settings.Legacy {
			return row.DeletedAt == nil && !settings.Managed && settings.IsEnabled() && settings.Mode == "quota"
		}
	}
	return true
}

// QuotaProviders lists providers that only managed accounts make available.
// Legacy and native rows are already covered by configuration; counting the
// store's seeded native placeholders would add tabs for unused providers.
func (s *Service) QuotaProviders() []string {
	rows, err := s.store.QueryAllProviderAccounts()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, row := range rows {
		if row.DeletedAt != nil {
			continue
		}
		settings, err := ParseSettings(row.Metadata)
		if err != nil || settings.Mode != "quota" || !settings.Managed || settings.Legacy {
			continue
		}
		if definition, ok := ProviderFor(row.Provider); ok && definition.Quota {
			set[row.Provider] = true
		}
	}
	result := make([]string, 0, len(set))
	for provider := range set {
		result = append(result, provider)
	}
	sort.Strings(result)
	return result
}

func (s *Service) Run(ctx context.Context) error {
	manager := agent.NewAgentManager(s.logger)
	defer manager.StopAll()
	billingCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	billingDone := make(chan struct{})
	go func() { defer close(billingDone); s.runBilling(billingCtx) }()
	defer func() { cancel(); <-billingDone }()
	fingerprints := map[int64][32]byte{}
	s.reconcile(ctx, manager, fingerprints)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.wake:
			s.reconcile(ctx, manager, fingerprints)
		case <-ticker.C:
			if s.Discovery().Enabled {
				if _, err := s.Discover(ctx); err != nil {
					s.logger.Debug("optional credential discovery unavailable", "error", err)
				}
			}
			s.reconcile(ctx, manager, fingerprints)
		}
	}
}

func (s *Service) reconcile(ctx context.Context, manager *agent.AgentManager, fingerprints map[int64][32]byte) {
	rows, err := s.store.QueryAllProviderAccounts()
	if err != nil {
		s.logger.Error("account reload failed", "error", err)
		return
	}
	wanted := map[int64]bool{}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		settings, err := ParseSettings(row.Metadata)
		if err != nil || row.DeletedAt != nil || !settings.Managed || !settings.IsEnabled() || settings.Mode != "quota" {
			continue
		}
		resolved, err := s.Resolve(row)
		if err != nil {
			s.setQuotaError(row.ID, err.Error())
			continue
		}
		encoded, _ := json.Marshal(resolved)
		fingerprint := sha256.Sum256(encoded)
		wanted[row.ID] = true
		if previous, ok := fingerprints[row.ID]; ok && previous == fingerprint && manager.IsRunning(fmt.Sprint(row.ID)) {
			continue
		}
		key := fmt.Sprint(row.ID)
		manager.Stop(key)
		account := row
		accountSettings := resolved
		manager.RegisterFactory(key, func() (agent.AgentRunner, error) {
			scope := account.ID
			if accountSettings.Legacy {
				scope = 0
			}
			st := s.store.ForAccount(scope)
			n := notify.New(st, s.logger)
			n.SetEncryptionKey(s.key())
			n.Reload()
			n.ConfigureSMTP()
			n.ConfigurePush()
			n.ConfigureWebhook()
			runner, err := newQuotaRunner(account.ID, account.Provider, accountSettings, st, s.config, s.logger.With("account_id", account.ID), n)
			if err != nil {
				return nil, err
			}
			if polling, ok := runner.(interface{ SetPollingCheck(func() bool) }); ok {
				polling.SetPollingCheck(func() bool { return s.providerPollingEnabled(account.Provider) })
			}
			return runner, nil
		})
		if err := manager.Start(key); err != nil {
			s.setQuotaError(row.ID, err.Error())
			continue
		}
		fingerprints[row.ID] = fingerprint
		s.setQuotaError(row.ID, "")
	}
	for id := range fingerprints {
		if !wanted[id] {
			manager.Stop(fmt.Sprint(id))
			delete(fingerprints, id)
		}
	}
}

func (s *Service) setQuotaError(id int64, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if message == "" {
		delete(s.quotaErrors, id)
	} else {
		s.quotaErrors[id] = message
	}
}

func (s *Service) providerPollingEnabled(provider string) bool {
	raw, err := s.store.GetSetting("provider_visibility")
	if err != nil {
		return false
	}
	var visibility map[string]map[string]bool
	if raw == "" || json.Unmarshal([]byte(raw), &visibility) != nil {
		return true
	}
	value, exists := visibility[provider]["polling"]
	return !exists || value
}

func (s *Service) runBilling(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		s.pollBills(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.billWake:
		case <-ticker.C:
		}
	}
}

func (s *Service) pollBills(ctx context.Context) {
	rows, err := s.store.QueryAllProviderAccounts()
	if err != nil {
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		settings, err := ParseSettings(row.Metadata)
		if err != nil || row.DeletedAt != nil || !settings.IsEnabled() || settings.Mode != "cost" {
			continue
		}
		_, checked, _, err := s.store.QueryAccountBill(row.ID)
		if err != nil {
			continue
		}
		if time.Since(checked) < 5*time.Minute {
			continue
		}
		resolved, err := s.Resolve(row)
		now := time.Now().UTC()
		if err != nil {
			_ = s.store.SaveAccountBill(row.ID, nil, err.Error(), now)
			continue
		}
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		if resolved.BillingKind == "openai-compatible" {
			start = time.Time{}
		}
		key := resolved.Credentials["api_key"]
		if key == "" {
			_ = s.store.SaveAccountBill(row.ID, nil, "No billing API key is configured; using observed usage and onWatch pricing.", now)
			continue
		}
		report, err := s.billing.Fetch(ctx, billing.Credentials{Provider: row.Provider, BaseURL: resolved.BaseURL, Kind: resolved.BillingKind, APIKey: key}, start, now)
		message := ""
		if errors.Is(err, billing.ErrUnsupported) {
			message = "Provider billing is unsupported for this endpoint; using observed usage and onWatch pricing."
		} else if err != nil {
			message = err.Error()
		}
		if err := s.store.SaveAccountBill(row.ID, report, message, now); err != nil {
			s.logger.Error("saving account bill failed", "account_id", row.ID, "error", err)
		}
	}
}
