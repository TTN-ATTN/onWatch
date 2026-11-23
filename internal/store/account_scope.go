package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// ForAccount returns an immutable view sharing this store's connection pool.
// Scope zero retains pre-account history. Only the owning root may be closed.
func (s *Store) ForAccount(id int64) *Store {
	return &Store{db: s.db, accountID: id, credentialsReadOnly: true}
}

func (s *Store) accountIDOr(fallback int64) int64 {
	if s.accountID != 0 {
		return s.accountID
	}
	return fallback
}

// migrateAccountScope runs at the end of every schema migration. Column and
// index additions are idempotent; rebuilding inline UNIQUE constraints is
// one-time. All table inspection happens before the transaction because an
// in-memory store has a single connection.
func (s *Store) migrateAccountScope() error {
	hasSettings, err := s.tableExists("settings")
	if err != nil {
		return err
	}
	version := ""
	if hasSettings {
		if version, err = s.GetSetting("account_scope_v1"); err != nil {
			return err
		}
	}
	// Identifiers below are compile-time constants, never user input. Child
	// value tables remain isolated through globally unique snapshot IDs.
	tables := []string{
		"quota_snapshots", "reset_cycles", "sessions", "system_alerts",
		"zai_snapshots", "zai_reset_cycles", "zai_hourly_usage", "notification_log",
		"anthropic_snapshots", "anthropic_reset_cycles", "copilot_snapshots", "copilot_reset_cycles",
		"antigravity_snapshots", "antigravity_reset_cycles", "gemini_snapshots", "gemini_reset_cycles",
		"openrouter_snapshots", "openrouter_reset_cycles", "moonshot_snapshots", "moonshot_reset_cycles",
		"deepseek_snapshots", "deepseek_reset_cycles", "cursor_snapshots", "cursor_reset_cycles",
		"opencode_snapshots", "opencode_reset_cycles", "ollama_snapshots", "ollama_reset_cycles",
		"muse_snapshots", "muse_reset_cycles", "zenmux_snapshots", "commandcode_reset_cycles",
		"mistral_snapshots", "mistral_cycles",
	}
	present := make(map[string]bool, len(tables)+1)
	missing := make([]string, 0, len(tables))
	for _, table := range append(tables, "commandcode_snapshots") {
		exists, err := s.tableExists(table)
		if err != nil {
			return err
		}
		present[table] = exists
		if !exists || table == "commandcode_snapshots" {
			continue
		}
		has, err := s.tableHasColumn(table, "account_id")
		if err != nil {
			return err
		}
		if !has {
			missing = append(missing, table)
		}
	}
	commandScope := true
	if present["commandcode_snapshots"] {
		if commandScope, err = s.tableHasColumn("commandcode_snapshots", "scope_account_id"); err != nil {
			return err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range missing {
		if _, err := tx.Exec("ALTER TABLE " + table + " ADD COLUMN account_id INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("scope %s: %w", table, err)
		}
	}
	if !commandScope {
		if _, err := tx.Exec("ALTER TABLE commandcode_snapshots ADD COLUMN scope_account_id INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if version != "done" && hasSettings && present["zai_hourly_usage"] && present["notification_log"] && present["antigravity_reset_cycles"] && present["mistral_cycles"] {
		// SQLite's inline UNIQUE constraints must be rebuilt, not shadowed by an
		// additional index; otherwise a second account would still replace rows.
		for _, statement := range []string{
			`CREATE TABLE zai_hourly_usage_accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL DEFAULT 0, provider TEXT NOT NULL DEFAULT 'zai', hour TEXT NOT NULL, model_calls INTEGER, tokens_used INTEGER, network_searches INTEGER, web_reads INTEGER, zreads INTEGER, fetched_at TEXT NOT NULL, UNIQUE(account_id, hour))`,
			`INSERT INTO zai_hourly_usage_accounts SELECT id, account_id, provider, hour, model_calls, tokens_used, network_searches, web_reads, zreads, fetched_at FROM zai_hourly_usage`,
			`DROP TABLE zai_hourly_usage`,
			`ALTER TABLE zai_hourly_usage_accounts RENAME TO zai_hourly_usage`,
			`CREATE INDEX idx_zai_hourly_hour ON zai_hourly_usage(account_id, hour)`,
			`CREATE TABLE notification_log_accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL DEFAULT 0, provider TEXT NOT NULL DEFAULT 'legacy', quota_key TEXT NOT NULL, notification_type TEXT NOT NULL, sent_at TEXT NOT NULL, utilization REAL, UNIQUE(account_id, provider, quota_key, notification_type))`,
			`INSERT INTO notification_log_accounts SELECT id, account_id, provider, quota_key, notification_type, sent_at, utilization FROM notification_log`,
			`DROP TABLE notification_log`,
			`ALTER TABLE notification_log_accounts RENAME TO notification_log`,
			`DROP INDEX IF EXISTS idx_antigravity_cycles_model_active_unique`,
			`CREATE UNIQUE INDEX idx_antigravity_cycles_model_active_unique ON antigravity_reset_cycles(account_id, model_id) WHERE cycle_end IS NULL`,
			`DROP INDEX IF EXISTS idx_mistral_active_cycle`,
			`CREATE UNIQUE INDEX idx_mistral_active_cycle ON mistral_cycles(account_id, identity, quota_name) WHERE cycle_end IS NULL`,
			`INSERT OR REPLACE INTO settings(key,value) VALUES ('account_scope_v1','done')`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("migrate account constraints: %w", err)
			}
		}
	}
	for _, table := range []string{"quota_snapshots", "zai_snapshots", "anthropic_snapshots", "copilot_snapshots", "antigravity_snapshots", "gemini_snapshots", "openrouter_snapshots", "moonshot_snapshots", "deepseek_snapshots", "cursor_snapshots", "opencode_snapshots", "ollama_snapshots", "muse_snapshots", "zenmux_snapshots", "mistral_snapshots"} {
		if !present[table] {
			continue
		}
		if err := execAccountIndex(tx, "CREATE INDEX IF NOT EXISTS idx_account_"+table+" ON "+table+"(account_id, captured_at)"); err != nil {
			return err
		}
	}
	for _, item := range []struct{ table, key string }{
		{"reset_cycles", "quota_type"}, {"zai_reset_cycles", "quota_type"}, {"anthropic_reset_cycles", "quota_name"}, {"copilot_reset_cycles", "quota_name"},
		{"antigravity_reset_cycles", "model_id"}, {"gemini_reset_cycles", "model_id"}, {"openrouter_reset_cycles", "quota_type"}, {"moonshot_reset_cycles", "quota_type"},
		{"deepseek_reset_cycles", "quota_type, currency"}, {"cursor_reset_cycles", "quota_name"}, {"opencode_reset_cycles", "quota_name"}, {"ollama_reset_cycles", "quota_name"},
		{"muse_reset_cycles", "quota_name"}, {"commandcode_reset_cycles", "quota_name"}, {"mistral_cycles", "identity, quota_name"},
	} {
		if !present[item.table] {
			continue
		}
		if err := execAccountIndex(tx, "CREATE INDEX IF NOT EXISTS idx_account_"+item.table+" ON "+item.table+"(account_id, "+item.key+", cycle_start)"); err != nil {
			return err
		}
	}
	for _, item := range []struct{ table, statement string }{
		{"commandcode_snapshots", `CREATE INDEX IF NOT EXISTS idx_account_commandcode_snapshots ON commandcode_snapshots(scope_account_id, captured_at)`},
		{"sessions", `CREATE INDEX IF NOT EXISTS idx_account_sessions ON sessions(account_id, provider, started_at)`},
		{"mistral_snapshots", `CREATE INDEX IF NOT EXISTS idx_account_mistral_identity ON mistral_snapshots(account_id, identity, captured_at)`},
	} {
		if !present[item.table] {
			continue
		}
		if err := execAccountIndex(tx, item.statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// execAccountIndex tolerates partial schemas in migration tests, matching the
// existing migrations' "no such table" leniency. Real schemas have every column.
func execAccountIndex(tx *sql.Tx, statement string) error {
	if _, err := tx.Exec(statement); err != nil && !strings.Contains(err.Error(), "no such column") {
		return err
	}
	return nil
}

func (s *Store) tableExists(table string) (bool, error) {
	var name string
	err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}
