package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/billing"
)

func (s *Store) createAccountTables() error {
	for _, column := range []struct{ name, definition string }{
		{"account_id", "account_id INTEGER NOT NULL DEFAULT 0"},
		{"cache_read_tokens", "cache_read_tokens INTEGER NOT NULL DEFAULT 0"},
		{"cache_write_tokens", "cache_write_tokens INTEGER NOT NULL DEFAULT 0"},
		{"cost_source", "cost_source TEXT NOT NULL DEFAULT ''"},
	} {
		has, err := s.tableHasColumn("api_integration_usage_events", column.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := s.db.Exec("ALTER TABLE api_integration_usage_events ADD COLUMN " + column.definition); err != nil {
				return err
			}
		}
	}
	_, err := s.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_api_usage_account ON api_integration_usage_events(account_id, captured_at);
		CREATE TABLE IF NOT EXISTS account_billing_reports (
			account_id INTEGER NOT NULL, period_start TEXT NOT NULL, scope TEXT NOT NULL,
			observed_at TEXT NOT NULL, report_json TEXT NOT NULL,
			PRIMARY KEY(account_id, period_start, scope)
		);
		CREATE TABLE IF NOT EXISTS account_billing_status (
			account_id INTEGER PRIMARY KEY, checked_at TEXT NOT NULL, last_error TEXT NOT NULL DEFAULT ''
		);
	`)
	return err
}

// QueryAllProviderAccounts includes soft-deleted records, so discovery cannot
// resurrect a source that the user explicitly removed.
func (s *Store) QueryAllProviderAccounts() ([]ProviderAccount, error) {
	rows, err := s.db.Query(`SELECT id, provider, name, created_at, COALESCE(metadata,''), deleted_at, COALESCE(external_id,'') FROM provider_accounts ORDER BY provider, id LIMIT 513`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]ProviderAccount, 0)
	for rows.Next() {
		var account ProviderAccount
		var created string
		var deleted sql.NullString
		if err := rows.Scan(&account.ID, &account.Provider, &account.Name, &created, &account.Metadata, &deleted, &account.ExternalID); err != nil {
			return nil, err
		}
		account.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if deleted.Valid {
			t, err := time.Parse(time.RFC3339Nano, deleted.String)
			if err != nil {
				return nil, err
			}
			account.DeletedAt = &t
		}
		accounts = append(accounts, account)
		if len(accounts) > 512 {
			return nil, fmt.Errorf("account limit exceeded (512)")
		}
	}
	return accounts, rows.Err()
}

// SaveManagedProviderAccount atomically changes identity labels and metadata.
// A deliberate create restores the same named soft-deleted account and history.
func (s *Store) SaveManagedProviderAccount(id int64, provider, name, metadata, externalID string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if id == 0 {
		err := tx.QueryRow(`SELECT id FROM provider_accounts WHERE provider = ? AND name = ?`, provider, name).Scan(&id)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		if err == nil {
			var deleted sql.NullString
			if err := tx.QueryRow(`SELECT deleted_at FROM provider_accounts WHERE id = ?`, id).Scan(&deleted); err != nil {
				return 0, err
			}
			if !deleted.Valid {
				return 0, fmt.Errorf("an account with that provider and name already exists")
			}
		}
		if id == 0 {
			result, err := tx.Exec(`INSERT INTO provider_accounts(provider,name,created_at,metadata,external_id) VALUES(?,?,?,?,?)`, provider, name, time.Now().UTC().Format(time.RFC3339Nano), metadata, externalID)
			if err != nil {
				return 0, err
			}
			id, err = result.LastInsertId()
			if err != nil {
				return 0, err
			}
		}
	}
	result, err := tx.Exec(`UPDATE provider_accounts SET name = ?, metadata = ?, external_id = ?, deleted_at = NULL WHERE id = ? AND provider = ?`, name, metadata, externalID, id, provider)
	if err != nil {
		return 0, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return 0, fmt.Errorf("account not found")
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) SaveAccountBill(accountID int64, report *billing.Report, message string, checkedAt time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if report != nil {
		data, err := json.Marshal(report)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO account_billing_reports(account_id,period_start,scope,observed_at,report_json) VALUES(?,?,?,?,?) ON CONFLICT(account_id,period_start,scope) DO UPDATE SET observed_at=excluded.observed_at, report_json=excluded.report_json`, accountID, report.PeriodStart.UTC().Format(time.RFC3339Nano), report.Scope, report.ObservedAt.UTC().Format(time.RFC3339Nano), string(data)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO account_billing_status(account_id,checked_at,last_error) VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET checked_at=excluded.checked_at,last_error=excluded.last_error`, accountID, checkedAt.UTC().Format(time.RFC3339Nano), message); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) QueryAccountBill(accountID int64) (*billing.Report, time.Time, string, error) {
	var raw string
	err := s.db.QueryRow(`SELECT report_json FROM account_billing_reports WHERE account_id = ? ORDER BY observed_at DESC LIMIT 1`, accountID).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return nil, time.Time{}, "", err
	}
	var report *billing.Report
	if raw != "" {
		report = &billing.Report{}
		if err := json.Unmarshal([]byte(raw), report); err != nil {
			return nil, time.Time{}, "", err
		}
	}
	var checked, message string
	err = s.db.QueryRow(`SELECT checked_at,last_error FROM account_billing_status WHERE account_id = ?`, accountID).Scan(&checked, &message)
	if err != nil && err != sql.ErrNoRows {
		return nil, time.Time{}, "", err
	}
	at, _ := time.Parse(time.RFC3339Nano, checked)
	return report, at, message, nil
}

type AccountUsageGroup struct {
	Date, Model                                                                                       string
	Requests, PricedRequests                                                                          int64
	PromptTokens, CompletionTokens, CacheReadTokens, CacheWriteTokens                                 int64
	UnpricedPromptTokens, UnpricedCompletionTokens, UnpricedCacheReadTokens, UnpricedCacheWriteTokens int64
	ReportedCost                                                                                      float64
}

// QueryAccountUsageGroups keeps aggregation in SQLite instead of loading calls.
// Unbound older events match the exact provider/account name, never all keys of
// a provider. Explicit IDs survive account renames and prevent duplicate names.
func (s *Store) QueryAccountUsageGroups(accountID int64, provider, name string, start, end time.Time) ([]AccountUsageGroup, error) {
	rows, err := s.db.Query(`SELECT substr(captured_at,1,10), model, COUNT(*), COUNT(cost_usd),
		SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens),
		COALESCE(SUM(CASE WHEN cost_usd IS NULL THEN prompt_tokens ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN cost_usd IS NULL THEN completion_tokens ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN cost_usd IS NULL THEN cache_read_tokens ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN cost_usd IS NULL THEN cache_write_tokens ELSE 0 END),0),
		COALESCE(SUM(cost_usd),0)
		FROM api_integration_usage_events WHERE captured_at >= ? AND captured_at < ?
		AND provider = ? AND (account_id = ? OR (account_id = 0 AND account_name = ?))
		GROUP BY 1,2 ORDER BY 1,2 LIMIT 5001`, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), provider, accountID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]AccountUsageGroup, 0)
	for rows.Next() {
		var group AccountUsageGroup
		if err := rows.Scan(&group.Date, &group.Model, &group.Requests, &group.PricedRequests, &group.PromptTokens, &group.CompletionTokens, &group.CacheReadTokens, &group.CacheWriteTokens, &group.UnpricedPromptTokens, &group.UnpricedCompletionTokens, &group.UnpricedCacheReadTokens, &group.UnpricedCacheWriteTokens, &group.ReportedCost); err != nil {
			return nil, err
		}
		groups = append(groups, group)
		if len(groups) > 5000 {
			return nil, fmt.Errorf("too many model/day groups; shorten the usage period")
		}
	}
	return groups, rows.Err()
}

func (s *Store) BindAPIIntegrationAccount(provider, name string, accountID int64) error {
	_, err := s.db.Exec(`UPDATE api_integration_usage_events SET account_id = ? WHERE account_id = 0 AND provider = ? AND account_name = ?`, accountID, provider, name)
	return err
}

// ReplaceProviderAccountMetadata atomically rewrites account settings, used to
// re-encrypt every credential with a new key without a partially-keyed state.
func (s *Store) ReplaceProviderAccountMetadata(updates map[int64]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, metadata := range updates {
		if _, err := tx.Exec(`UPDATE provider_accounts SET metadata = ? WHERE id = ?`, metadata, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
