package apiintegrations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	maxIntegrationFieldLen    = 256
	maxMetadataJSONLen        = 4096
	MaxIngestPartialLineBytes = 512 * 1024
)

func validProvider(provider string) bool {
	if provider == "" || len(provider) > maxIntegrationFieldLen {
		return false
	}
	for i, ch := range provider {
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if i > 0 && ((ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') {
			continue
		}
		return false
	}
	return true
}

// UsageEvent is the normalized API integration telemetry event stored by onWatch.
type UsageEvent struct {
	Timestamp        time.Time
	Integration      string
	Provider         string
	Account          string
	AccountID        int64
	Model            string
	RequestID        string
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	CacheWriteTokens int
	TotalTokens      int
	CostUSD          *float64
	CostSource       string
	LatencyMS        *int
	MetadataJSON     string
	SourcePath       string
	Fingerprint      string
}

// IngestState stores the persistent cursor for a tailed JSONL file.
type IngestState struct {
	SourcePath           string
	Offset               int64
	FileSize             int64
	FileModTime          time.Time
	PartialLine          string
	PartialLineBytes     int
	PartialLineOversized bool
	UpdatedAt            time.Time
}

type usageEventWire struct {
	TS               string          `json:"ts"`
	Integration      string          `json:"integration"`
	Provider         string          `json:"provider"`
	Account          string          `json:"account"`
	AccountID        int64           `json:"account_id"`
	Model            string          `json:"model"`
	RequestID        string          `json:"request_id"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	CacheReadTokens  int             `json:"cache_read_tokens"`
	CacheWriteTokens int             `json:"cache_write_tokens"`
	TotalTokens      *int            `json:"total_tokens"`
	CostUSD          *float64        `json:"cost_usd"`
	LatencyMS        *int            `json:"latency_ms"`
	Metadata         json.RawMessage `json:"metadata"`
}

// ParseUsageEventLine validates and normalizes a single JSONL event line.
func ParseUsageEventLine(line []byte, sourcePath string) (*UsageEvent, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil, fmt.Errorf("empty event line")
	}

	var wire usageEventWire
	if err := json.Unmarshal([]byte(trimmed), &wire); err != nil {
		return nil, fmt.Errorf("parse API integration usage event: %w", err)
	}

	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(wire.TS))
	if err != nil {
		return nil, fmt.Errorf("invalid ts: %w", err)
	}

	integrationName := strings.TrimSpace(wire.Integration)
	if integrationName == "" {
		return nil, fmt.Errorf("integration is required")
	}
	if len(integrationName) > maxIntegrationFieldLen {
		return nil, fmt.Errorf("integration exceeds %d characters", maxIntegrationFieldLen)
	}

	provider := strings.ToLower(strings.TrimSpace(wire.Provider))
	if !validProvider(provider) {
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}

	model := strings.TrimSpace(wire.Model)
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if len(model) > maxIntegrationFieldLen {
		return nil, fmt.Errorf("model exceeds %d characters", maxIntegrationFieldLen)
	}

	if wire.PromptTokens < 0 {
		return nil, fmt.Errorf("prompt_tokens must be >= 0")
	}
	if wire.CompletionTokens < 0 {
		return nil, fmt.Errorf("completion_tokens must be >= 0")
	}
	if wire.AccountID < 0 {
		return nil, fmt.Errorf("account_id must be >= 0")
	}
	if wire.CacheReadTokens < 0 || wire.CacheWriteTokens < 0 ||
		wire.CacheReadTokens > wire.PromptTokens || wire.CacheWriteTokens > wire.PromptTokens-wire.CacheReadTokens {
		return nil, fmt.Errorf("cache tokens must be disjoint subsets of prompt_tokens")
	}
	if wire.PromptTokens > int(^uint(0)>>1)-wire.CompletionTokens {
		return nil, fmt.Errorf("token total overflows")
	}

	totalTokens := wire.PromptTokens + wire.CompletionTokens
	if wire.TotalTokens != nil {
		if *wire.TotalTokens < 0 {
			return nil, fmt.Errorf("total_tokens must be >= 0")
		}
		totalTokens = *wire.TotalTokens
	}

	if wire.CostUSD != nil && *wire.CostUSD < 0 {
		return nil, fmt.Errorf("cost_usd must be >= 0")
	}
	if wire.LatencyMS != nil && *wire.LatencyMS < 0 {
		return nil, fmt.Errorf("latency_ms must be >= 0")
	}

	account := strings.TrimSpace(wire.Account)
	if account == "" {
		account = "default"
	}
	if len(account) > maxIntegrationFieldLen {
		return nil, fmt.Errorf("account exceeds %d characters", maxIntegrationFieldLen)
	}

	metadataJSON := ""
	if len(wire.Metadata) > 0 && string(wire.Metadata) != "null" {
		var metadata map[string]interface{}
		if err := json.Unmarshal(wire.Metadata, &metadata); err != nil {
			return nil, fmt.Errorf("metadata must be a JSON object: %w", err)
		}
		compact, err := json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("compact metadata: %w", err)
		}
		metadataJSON = string(compact)
	}
	if len(metadataJSON) > maxMetadataJSONLen {
		return nil, fmt.Errorf("metadata_json exceeds %d bytes after compaction", maxMetadataJSONLen)
	}

	event := &UsageEvent{
		Timestamp:        ts.UTC(),
		Integration:      integrationName,
		Provider:         provider,
		Account:          account,
		AccountID:        wire.AccountID,
		Model:            model,
		RequestID:        strings.TrimSpace(wire.RequestID),
		PromptTokens:     wire.PromptTokens,
		CompletionTokens: wire.CompletionTokens,
		CacheReadTokens:  wire.CacheReadTokens,
		CacheWriteTokens: wire.CacheWriteTokens,
		TotalTokens:      totalTokens,
		CostUSD:          wire.CostUSD,
		LatencyMS:        wire.LatencyMS,
		MetadataJSON:     metadataJSON,
		SourcePath:       sourcePath,
	}
	if event.CostUSD != nil {
		event.CostSource = "telemetry"
	}
	event.Fingerprint = eventFingerprint(event)
	return event, nil
}

func eventFingerprint(event *UsageEvent) string {
	h := sha256.New()
	writeHashPart(h, event.SourcePath)
	writeHashPart(h, event.Timestamp.Format(time.RFC3339Nano))
	writeHashPart(h, event.Integration)
	writeHashPart(h, event.Provider)
	writeHashPart(h, event.Account)
	writeHashPart(h, event.Model)
	writeHashPart(h, fmt.Sprintf("%d", event.PromptTokens))
	writeHashPart(h, fmt.Sprintf("%d", event.CompletionTokens))
	writeHashPart(h, fmt.Sprintf("%d", event.TotalTokens))
	if event.CacheReadTokens != 0 || event.CacheWriteTokens != 0 {
		writeHashPart(h, fmt.Sprintf("cache:%d:%d", event.CacheReadTokens, event.CacheWriteTokens))
	}
	writeHashPart(h, event.RequestID)
	return hex.EncodeToString(h.Sum(nil))
}

func writeHashPart(h interface{ Write([]byte) (int, error) }, part string) {
	_, _ = h.Write([]byte(part))
	_, _ = h.Write([]byte{0})
}
