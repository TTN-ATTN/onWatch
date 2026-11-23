// Package omp discovers optional coding-agent credentials without invoking omp.
// It never refreshes credentials or reads usage, statistics, or pricing data.
package omp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v2"
	_ "modernc.org/sqlite"
)

const (
	maxDocumentBytes = 4 << 20
	maxDatabaseBytes = 64 << 20
	maxCredentials   = 4096
	discoveryTimeout = 5 * time.Second
)

// Credential is an in-memory snapshot. Persist ID, not Values: secrets can rotate.
type Credential struct {
	ID, Provider, Name, BaseURL, Type, Mode, SourceProvider string
	Values                                                  map[string]string
}

// DefaultDir does not inspect or create the directory. ONWATCH_OMP_DIR is the
// explicit opt-in override; otherwise use omp's conventional agent directory.
func DefaultDir() string {
	if dir := os.Getenv("ONWATCH_OMP_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".omp", "agent")
}

// Discover returns a complete snapshot or an error, never partial credentials.
// SQLite records take precedence over legacy records for the same source alias.
func Discover(ctx context.Context, dir string) ([]Credential, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dir == "" {
		dir = DefaultDir()
	}
	if dir == "" {
		return nil, nil
	}
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.IsDir() {
		return nil, errors.New("omp credential directory is not readable")
	}
	credentials, aliases, err := readDatabase(ctx, dir)
	if err != nil {
		return nil, err
	}
	models, err := readModels(ctx, filepath.Join(dir, "models.yml"))
	if err != nil {
		return nil, err
	}
	// An inline API key and an OAuth identity are distinct. Prefer current DB
	// records only for the same alias and kind, preserving every DB identity.
	current := make(map[string]bool)
	for _, c := range credentials {
		current[c.SourceProvider+"\x00"+c.Type] = true
	}
	filtered := models[:0]
	for _, c := range models {
		if !current[c.SourceProvider+"\x00"+c.Type] {
			filtered = append(filtered, c)
		}
	}
	models = filtered
	credentials = append(credentials, models...)
	for _, c := range models {
		aliases[c.SourceProvider+"\x00"+c.Type] = true
	}
	legacy, err := readLegacy(ctx, filepath.Join(dir, "auth.json"), aliases)
	if err != nil {
		return nil, err
	}
	credentials = append(credentials, legacy...)
	if len(credentials) > maxCredentials {
		return nil, errors.New("omp credential count exceeds limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].ID < credentials[j].ID })
	return credentials, nil
}

func readFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("omp credential source is not a regular readable file")
	}
	if info.Size() > limit {
		return nil, errors.New("omp credential source exceeds size limit")
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("omp credential source is not readable")
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("omp credential source is not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, errors.New("cannot read omp credential source")
	}
	if int64(len(b)) > limit {
		return nil, errors.New("omp credential source exceeds size limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b, nil
}

func readDatabase(ctx context.Context, dir string) ([]Credential, map[string]bool, error) {
	aliases := make(map[string]bool)
	path, err := filepath.Abs(filepath.Join(dir, "agent.db"))
	if err != nil {
		return nil, nil, errors.New("invalid omp credential directory")
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, aliases, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxDatabaseBytes {
		return nil, nil, errors.New("omp credential database is unreadable or exceeds size limit")
	}
	wal, err := os.Stat(path + "-wal")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, errors.New("omp credential WAL is unreadable")
	}
	if err == nil && (!wal.Mode().IsRegular() || wal.Size() > maxDatabaseBytes) {
		return nil, nil, errors.New("omp credential WAL exceeds size limit")
	}
	// mode=ro cannot create a missing DB or modify its contents. SQLite may
	// maintain transient WAL shared-memory locks; no credentials are copied.
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(100)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, nil, errors.New("cannot open omp credential database")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, errors.New("cannot connect to omp credential database")
	}
	defer conn.Close()
	var table string
	err = conn.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='auth_credentials'").Scan(&table)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, aliases, nil
	}
	if err != nil {
		return nil, nil, errors.New("cannot inspect omp credential database")
	}
	rows, err := conn.QueryContext(ctx, "SELECT id, provider, credential_type, data, disabled_cause FROM auth_credentials ORDER BY id LIMIT ?", maxCredentials+1)
	if err != nil {
		return nil, nil, errors.New("cannot read omp credential database")
	}
	defer rows.Close()
	var out []Credential
	count := 0
	for rows.Next() {
		count++
		if count > maxCredentials {
			return nil, nil, errors.New("omp credential count exceeds limit")
		}
		var id int64
		var provider, kind string
		var data, disabled sql.NullString
		if err := rows.Scan(&id, &provider, &kind, &data, &disabled); err != nil {
			return nil, nil, errors.New("invalid omp credential row")
		}
		aliases[provider] = true // Disabled current records must not revive legacy tokens.
		if disabled.Valid && strings.TrimSpace(disabled.String) != "" {
			continue
		}
		if len(data.String) > maxDocumentBytes {
			return nil, nil, errors.New("omp credential row exceeds size limit")
		}
		var value map[string]interface{}
		if !data.Valid || json.Unmarshal([]byte(data.String), &value) != nil || value == nil {
			return nil, nil, errors.New("invalid omp credential JSON")
		}
		credential, ok, err := makeCredential("db:"+strconv.FormatInt(id, 10), provider, kind, "", value)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, errors.New("omp credential row has no usable credential")
		}
		out = append(out, credential)
	}
	if rows.Err() != nil {
		return nil, nil, errors.New("cannot finish reading omp credential database")
	}
	return out, aliases, nil
}

func readModels(ctx context.Context, path string) ([]Credential, error) {
	b, err := readFile(ctx, path, maxDocumentBytes)
	if err != nil || b == nil {
		return nil, err
	}
	var root map[string]interface{}
	if yaml.UnmarshalStrict(b, &root) != nil {
		return nil, errors.New("invalid omp models YAML")
	}
	providers, ok := stringMap(root["providers"])
	if !ok && root["providers"] != nil {
		return nil, errors.New("invalid omp models providers")
	}
	if len(providers) > maxCredentials {
		return nil, errors.New("omp provider count exceeds limit")
	}
	var out []Credential
	for alias, raw := range providers {
		entry, ok := stringMap(raw)
		if !ok {
			return nil, errors.New("invalid omp model provider")
		}
		if disabled, _ := entry["disabled"].(bool); disabled {
			continue
		}
		key, err := resolveKey(entry["apiKey"])
		if err != nil {
			return nil, err
		}
		if key == "" {
			continue
		} // No inline key: auth may be supplied by agent.db.
		baseURL := stringValue(entry, "baseUrl", "baseURL", "base_url")
		c, ok, err := makeCredential("models:"+url.PathEscape(alias), alias, "api_key", baseURL, map[string]interface{}{"api_key": key})
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func stringMap(value interface{}) (map[string]interface{}, bool) {
	switch v := value.(type) {
	case map[string]interface{}:
		return v, true
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, value := range v {
			s, ok := key.(string)
			if !ok {
				return nil, false
			}
			out[s] = value
		}
		return out, true
	default:
		return nil, false
	}
}

func stringValue(data map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if s, ok := data[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// resolveKey handles literals and environment references only, never commands.
func resolveKey(raw interface{}) (string, error) {
	if raw == nil {
		return "", nil
	}
	if value, ok := raw.(string); ok {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "!") || strings.Contains(value, "$(") || strings.Contains(value, "`") {
			return "", errors.New("omp credential commands are not supported")
		}
		name := ""
		switch {
		case strings.HasPrefix(value, "env:"):
			name = strings.TrimSpace(strings.TrimPrefix(value, "env:"))
		case strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}"):
			name = value[2 : len(value)-1]
		case strings.HasPrefix(value, "$"):
			name = value[1:]
		default:
			if resolved, exists := os.LookupEnv(value); exists {
				if resolved == "" {
					return "", errors.New("omp credential environment reference is unavailable")
				}
				return resolved, nil
			}
			return value, nil
		}
		if name == "" || strings.ContainsAny(name, " \t\r\n{}():-/$") {
			return "", errors.New("invalid omp credential environment reference")
		}
		resolved, exists := os.LookupEnv(name)
		if !exists || resolved == "" {
			return "", errors.New("omp credential environment reference is unavailable")
		}
		return resolved, nil
	}
	if value, ok := stringMap(raw); ok {
		if len(value) == 1 {
			if name, ok := value["env"].(string); ok {
				return resolveKey("env:" + name)
			}
			if literal, ok := value["value"].(string); ok {
				return resolveKey(literal)
			}
		}
	}
	return "", errors.New("unsupported omp credential key configuration")
}
