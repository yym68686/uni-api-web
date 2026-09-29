package main

import (
	"encoding/json"
	"time"
)

// Facts never include API secrets, prompts, response text or client addresses.
// Millisecond timestamps are UTC. Token input includes cache read/write tokens.
type Fact struct {
	TraceID             string          `json:"trace_id,omitempty"`
	Stage               string          `json:"stage,omitempty"`
	TraceDetail         json.RawMessage `json:"trace_detail,omitempty"`
	TransportTiming     json.RawMessage `json:"transport_timing,omitempty"`
	FailureReason       string          `json:"failure_reason,omitempty"`
	TerminalKind        string          `json:"terminal_kind,omitempty"`
	ResponseCompleted   *bool           `json:"response_completed,omitempty"`
	Schema              int             `json:"schema"`
	SourceID            string          `json:"source_id"`
	EventID             string          `json:"event_id"`
	Kind                string          `json:"kind"`
	InstanceID          string          `json:"instance_id"`
	RequestID           string          `json:"request_id"`
	AttemptID           string          `json:"attempt_id"`
	AtMS                int64           `json:"at_ms"`
	StartedMS           int64           `json:"started_ms"`
	KeyID               string          `json:"key_id"`
	Provider            string          `json:"provider"`
	Model               string          `json:"model"`
	UpstreamModel       string          `json:"upstream_model"`
	Endpoint            string          `json:"endpoint"`
	Stream              bool            `json:"stream"`
	Outcome             string          `json:"outcome"`
	Status              int             `json:"status"`
	DurationMS          *float64        `json:"duration_ms"`
	DispatchMS          *float64        `json:"dispatch_ms"`
	FirstOutputMS       *float64        `json:"first_output_ms"`
	ResponseCreatedMS   *float64        `json:"response_created_ms"`
	FirstTextMS         *float64        `json:"first_text_ms"`
	InputTokens         *int64          `json:"input_tokens"`
	OutputTokens        *int64          `json:"output_tokens"`
	CacheReadTokens     *int64          `json:"cache_read_tokens"`
	CacheWriteTokens    *int64          `json:"cache_write_tokens"`
	CacheWrite1hTokens  *int64          `json:"cache_write_1h_tokens"`
	ActualCostUSD       *float64        `json:"actual_cost_usd"`
	UpstreamBase        string          `json:"upstream_base,omitempty"`
	UpstreamKeyHash     string          `json:"upstream_key_hash,omitempty"`
	BillingRequestIDs   []string        `json:"billing_request_ids,omitempty"`
	UpstreamErrorSHA256 string          `json:"upstream_error_sha256,omitempty"`
}
type LiveInstance struct {
	InstanceID   string        `json:"instance_id"`
	AtMS         int64         `json:"at_ms"`
	StartedMS    int64         `json:"started_ms"`
	Requests     int64         `json:"requests"`
	Channels     []LiveChannel `json:"channels"`
	QueueDepth   uint64        `json:"queue_depth"`
	Dropped      uint64        `json:"dropped"`
	Exported     uint64        `json:"exported"`
	LastExportMS int64         `json:"last_export_ms"`
}
type LiveChannel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Endpoint string `json:"endpoint"`
	Stream   bool   `json:"stream"`
	Count    int64  `json:"count"`
}
type Price struct {
	Model              string    `json:"model"`
	Input              float64   `json:"input"`
	Output             float64   `json:"output"`
	CacheRead          float64   `json:"cache_read"`
	CacheWrite         float64   `json:"cache_write"`
	CacheWrite1h       float64   `json:"cache_write_1h"`
	ChargeCacheWrite   *bool     `json:"charge_cache_write,omitempty"`
	LongContextPremium *bool     `json:"long_context_premium,omitempty"`
	SalePercent        *float64  `json:"sale_percent,omitempty"`
	Source             string    `json:"source"`
	Verified           bool      `json:"verified"`
	EffectiveAt        time.Time `json:"effective_at"`
}
type Config struct {
	DatabaseMemoryLimitMB                                                                int
	Address, DataDir, Upstream, SourceID, Timezone, S3Endpoint, S3Bucket, S3Prefix       string
	StateEndpoint, StateBucket, StatePrefix, StateAccessKey, StateSecretKey              string
	ControlDatabaseURL, ControlMasterKey, AdminUsername, AdminPassword, BootstrapSources string
	PublicOrigin                                                                         string
	InsecureCookie                                                                       bool
	RequireInitialImport                                                                 bool
	Poll                                                                                 time.Duration
}
