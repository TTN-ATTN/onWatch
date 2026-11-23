package omp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixtureFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixtureDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE auth_credentials (id INTEGER PRIMARY KEY, provider TEXT, credential_type TEXT, data TEXT, disabled_cause TEXT)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func fixtureRow(t *testing.T, db *sql.DB, id int, provider, kind, data, disabled string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO auth_credentials VALUES (?, ?, ?, ?, ?)`, id, provider, kind, data, disabled); err != nil {
		t.Fatal(err)
	}
}

func discoverFixture(t *testing.T, dir string) []Credential {
	t.Helper()
	got, err := Discover(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMissingSourcesCreateNothing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "absent")
	if got := discoverFixture(t, dir); len(got) != 0 {
		t.Fatal("missing directory returned credentials")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("discovery created source directory")
	}
	if got := discoverFixture(t, root); len(got) != 0 {
		t.Fatal("empty directory returned credentials")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("discovery created source files")
	}
}

func TestDefaultDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONWATCH_OMP_DIR", dir)
	if DefaultDir() != dir {
		t.Fatal("override not respected")
	}
	if got := discoverFixture(t, ""); len(got) != 0 {
		t.Fatal("default directory fixture not used")
	}
}

func TestSQLiteMultipleRecordsRotationAndLegacyPrecedence(t *testing.T) {
	dir := t.TempDir()
	db := fixtureDB(t, dir)
	fixtureRow(t, db, 11, "openai-codex", "oauth", `{"access":"fixture-one","accountId":"account-one","email":"one@example.invalid"}`, "")
	fixtureRow(t, db, 12, "openai-codex", "oauth", `{"access":"fixture-two"}`, "")
	fixtureRow(t, db, 13, "anthropic", "oauth", `not-json`, "revoked")
	fixtureFile(t, dir, "auth.json", `{"openai-codex":{"type":"oauth","access":"legacy-duplicate"},"anthropic":{"type":"oauth","access":"disabled-legacy"},"deepseek":{"type":"api_key","key":"legacy-key"}}`)
	first := discoverFixture(t, dir)
	if len(first) != 3 {
		t.Fatalf("got %d identities, want 3", len(first))
	}
	if first[0].ID != "db:11" || first[0].Provider != "codex" || first[0].Mode != "quota" || first[0].Values["account_id"] != "account-one" {
		t.Fatal("incorrect SQLite identity mapping")
	}
	if first[0].Name != "openai-codex (one@example.invalid)" {
		t.Fatal("email metadata not used")
	}
	if _, err := db.Exec(`UPDATE auth_credentials SET data='{"access":"rotated"}' WHERE id=11`); err != nil {
		t.Fatal(err)
	}
	second := discoverFixture(t, dir)
	if first[0].ID != second[0].ID || second[0].Values["token"] != "rotated" {
		t.Fatal("rotation changed source identity")
	}
}

func TestYAMLMultipleAliasesAndProviderClassification(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONWATCH_FIXTURE_KEY", "environment-fixture")
	fixtureFile(t, dir, "models.yml", `providers:
  zai:
    baseUrl: https://api.z.ai/api/coding/paas/v4
    api: openai-completions
    apiKey: literal-fixture
    auth: true
    modelOverrides: {}
    models: []
  zai2:
    baseUrl: https://api.z.ai/api/paas/v4
    apiKey: ${ONWATCH_FIXTURE_KEY}
  zhipu-cn:
    baseUrl: https://open.bigmodel.cn/api/paas/v4
    apiKey: {env: ONWATCH_FIXTURE_KEY}
  deepseek-direct:
    baseUrl: https://api.deepseek.com/v1
    apiKey: env:ONWATCH_FIXTURE_KEY
  mistral-direct:
    baseUrl: https://api.mistral.ai/v1
    apiKey: literal-fixture
  dashscope-cn:
    baseUrl: https://dashscope.aliyuncs.com/compatible-mode/v1
    apiKey: literal-fixture
  newapi:
    baseUrl: https://gateway.example.invalid/v1
    apiKey: literal-fixture
  xiaomi:
    baseUrl: https://api.example.invalid/v1
    apiKey: literal-fixture
  auth-only:
    auth: true
`)
	got := discoverFixture(t, dir)
	if len(got) != 8 {
		t.Fatalf("got %d aliases, want 8", len(got))
	}
	byAlias := make(map[string]Credential)
	for _, c := range got {
		byAlias[c.SourceProvider] = c
	}
	if byAlias["zai"].Provider != "zai" || byAlias["zai"].Mode != "quota" || byAlias["zai2"].Provider != "zai" || byAlias["zai2"].Mode != "cost" {
		t.Fatal("zai aliases or API modes conflated")
	}
	if byAlias["zhipu-cn"].Values["region"] != "cn" {
		t.Fatal("CN endpoint not classified")
	}
	if byAlias["deepseek-direct"].Provider != "deepseek" || byAlias["mistral-direct"].Values["api_key"] == "" || byAlias["mistral-direct"].Values["auth_cookie"] != "" {
		t.Fatal("API key classification incorrect")
	}
	for _, alias := range []string{"dashscope-cn", "newapi", "xiaomi"} {
		if byAlias[alias].Provider != "custom" || byAlias[alias].Mode != "cost" {
			t.Fatal("custom inference alias misclassified")
		}
	}
	if byAlias["zai2"].Values["api_key"] != "environment-fixture" {
		t.Fatal("environment reference not resolved")
	}
	before := byAlias["zai2"].ID
	t.Setenv("ONWATCH_FIXTURE_KEY", "rotated-environment-fixture")
	for _, c := range discoverFixture(t, dir) {
		if c.SourceProvider == "zai2" && c.ID != before {
			t.Fatal("YAML key rotation changed ID")
		}
	}
}

func TestSQLiteAndYAMLDedupeOnlySameAliasAndKind(t *testing.T) {
	dir := t.TempDir()
	db := fixtureDB(t, dir)
	fixtureRow(t, db, 1, "deepseek", "api_key", `{"key":"current"}`, "")
	fixtureRow(t, db, 2, "anthropic", "oauth", `{"access":"subscription"}`, "")
	fixtureFile(t, dir, "models.yml", `providers:
  deepseek: {apiKey: old-inline}
  anthropic: {apiKey: separate-api-key}
  another-deepseek: {baseUrl: "https://api.deepseek.com/v1", apiKey: separate-identity}
`)
	got := discoverFixture(t, dir)
	if len(got) != 4 {
		t.Fatalf("got %d identities, want 4", len(got))
	}
	for _, c := range got {
		if c.ID == "models:deepseek" {
			t.Fatal("current DB API identity duplicated")
		}
	}
}

func TestLegacyMapsAndArrays(t *testing.T) {
	cases := []string{
		`{"api_key":{"deepseek":"fixture","zai":[{"key":"first"},{"key":"second"}]},"oauth":{"google":{"access":"access","refresh":"refresh"},"command-code":{"access":"token"}}}`,
		`{"api_key":[{"provider":"deepseek","key":"fixture"},{"provider":"zai","key":"first"},{"provider":"zai","key":"second"}],"oauth":[{"provider":"google","access":"access","refresh":"refresh"},{"provider":"command-code","access":"token"}]}`,
		`[{"provider":"deepseek","type":"api_key","key":"fixture"},{"provider":"zai","type":"api_key","key":"first"},{"provider":"zai","type":"api_key","key":"second"},{"provider":"google","type":"oauth","access":"access","refresh":"refresh"},{"provider":"command-code","type":"oauth","access":"token"}]`,
	}
	for i, body := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			dir := t.TempDir()
			fixtureFile(t, dir, "auth.json", body)
			got := discoverFixture(t, dir)
			if len(got) != 5 {
				t.Fatalf("got %d legacy identities, want 5", len(got))
			}
			for _, c := range got {
				if c.SourceProvider == "google" && (c.Provider != "gemini" || c.Values["refresh_token"] != "refresh") {
					t.Fatal("legacy OAuth mapping incorrect")
				}
			}
		})
	}
}

func TestProviderCredentialKindsDoNotBecomeSubscriptionTokens(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini", "grok", "mistral", "alibaba", "openai-codex"} {
		c, ok, err := makeCredential("fixture", provider, "api_key", "", map[string]interface{}{"key": "fixture"})
		if err != nil || !ok {
			t.Fatalf("%s: API identity unavailable", provider)
		}
		if c.Mode != "cost" || c.Values["api_key"] == "" || c.Values["token"] != "" || c.Values["cookies"] != "" || c.Values["auth_cookie"] != "" {
			t.Fatalf("%s: API identity conflated with subscription", provider)
		}
	}
	c, ok, err := makeCredential("fixture", "command-code", "api_key", "", map[string]interface{}{"key": "fixture"})
	if err != nil || !ok || c.Provider != "commandcode" || c.Mode != "quota" {
		t.Fatal("command-code alias not recognized")
	}
}

func TestMalformedSourcesAndDisabledRows(t *testing.T) {
	for _, body := range []string{`{`, `null`, `{"access":"fixture"}`, `{"key":42}`} {
		t.Run("row-"+fmt.Sprint(len(body)), func(t *testing.T) {
			dir := t.TempDir()
			db := fixtureDB(t, dir)
			fixtureRow(t, db, 1, "deepseek", "api_key", body, "")
			got, err := Discover(context.Background(), dir)
			if err == nil || got != nil {
				t.Fatal("malformed row did not fail complete discovery")
			}
			if strings.Contains(err.Error(), body) {
				t.Fatal("error exposed raw credential body")
			}
		})
	}
	dir := t.TempDir()
	db := fixtureDB(t, dir)
	fixtureRow(t, db, 1, "deepseek", "api_key", `not-json`, "disabled")
	if got := discoverFixture(t, dir); len(got) != 0 {
		t.Fatal("disabled malformed row imported")
	}
	for _, fixture := range []struct{ name, body string }{{"models.yml", "providers: ["}, {"models.yml", "providers:\n  same: {apiKey: a}\n  same: {apiKey: b}\n"}, {"auth.json", "{"}} {
		sub := t.TempDir()
		fixtureFile(t, sub, fixture.name, fixture.body)
		if got, err := Discover(context.Background(), sub); err == nil || got != nil {
			t.Fatal("malformed document accepted")
		}
	}
}

func TestCommandsAreRefusedWithoutExecution(t *testing.T) {
	for _, key := range []string{"'!touch sentinel'", "'$(touch sentinel)'", "'`touch sentinel`'", "{command: 'touch sentinel'}", "'${ONWATCH_MISSING_FIXTURE_KEY}'"} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			fixtureFile(t, dir, "models.yml", "providers:\n  custom:\n    apiKey: "+key+"\n")
			got, err := Discover(context.Background(), dir)
			if err == nil || got != nil {
				t.Fatal("unsafe or unresolved key configuration accepted")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatal("discovery executed command or created source files")
			}
		})
	}
}

func TestReadOnlyDiscoveryIncludesWALWithoutModifyingDatabase(t *testing.T) {
	dir := t.TempDir()
	db := fixtureDB(t, dir)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	fixtureRow(t, db, 1, "deepseek", "api_key", `{"key":"in-wal"}`, "")
	before := make(map[string][]byte)
	for _, name := range []string{"agent.db", "agent.db-wal"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = b
	}
	got := discoverFixture(t, dir)
	if len(got) != 1 || got[0].Values["api_key"] != "in-wal" {
		t.Fatal("uncheckpointed WAL credential omitted")
	}
	for name, b := range before {
		after, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !reflect.DeepEqual(b, after) {
			t.Fatalf("source %s modified", name)
		}
	}
}

func TestDiscoveryLimitsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := Discover(ctx, t.TempDir()); err == nil || got != nil {
		t.Fatal("canceled discovery succeeded")
	}
	dir := t.TempDir()
	fixtureFile(t, dir, "models.yml", strings.Repeat(" ", maxDocumentBytes+1))
	if got, err := Discover(context.Background(), dir); err == nil || got != nil {
		t.Fatal("oversized source accepted")
	}
	dir = t.TempDir()
	db := fixtureDB(t, dir)
	if _, err := db.Exec(`WITH RECURSIVE ids(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM ids WHERE x < ?) INSERT INTO auth_credentials SELECT x, 'deepseek', 'api_key', '{"key":"fixture"}', '' FROM ids`, maxCredentials+1); err != nil {
		t.Fatal(err)
	}
	if got, err := Discover(context.Background(), dir); err == nil || got != nil {
		t.Fatal("excess credential rows silently truncated")
	}
}

func TestInlineKeyPrecedesLegacyKeyButPreservesOAuth(t *testing.T) {
	dir := t.TempDir()
	fixtureFile(t, dir, "models.yml", "providers:\n  anthropic: {apiKey: inline-api-key}\n")
	fixtureFile(t, dir, "auth.json", `{"api_key":{"anthropic":"old-api-key"},"oauth":{"anthropic":{"access":"separate-subscription"}}}`)
	got := discoverFixture(t, dir)
	if len(got) != 2 {
		t.Fatalf("got %d identities, want API key and OAuth", len(got))
	}
	for _, c := range got {
		if c.Type == "api_key" && c.Values["api_key"] != "inline-api-key" {
			t.Fatal("legacy API key duplicated inline identity")
		}
		if c.Type == "oauth" && c.Values["token"] != "separate-subscription" {
			t.Fatal("OAuth identity lost")
		}
	}
}
