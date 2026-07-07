package storage

import (
	"context"
	"time"
)

// LogEntry represents a single recorded LLM request.
type LogEntry struct {
	ID               string
	Ts               time.Time
	Model            string
	Provider         string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          float64
	LatencyMs        int64
	StatusCode       int
	KeyID            string
	Error            string
}

// StatsSummary is aggregated totals for a time period.
type StatsSummary struct {
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	ErrorRate        float64 `json:"error_rate"`
}

// TimeseriesBucket is one time bucket for charts.
type TimeseriesBucket struct {
	Ts           time.Time `json:"ts"`
	Requests     int64     `json:"requests"`
	TotalTokens  int64     `json:"total_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	AvgLatencyMs float64   `json:"avg_latency_ms"`
}

// ModelStat is per-model breakdown.
type ModelStat struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Requests     int64   `json:"requests"`
	TotalTokens  int64   `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

// LogFilter controls what LogStore.List returns.
type LogFilter struct {
	Limit  int
	Offset int
}

// LogStore persists and queries request logs.
type LogStore interface {
	Insert(ctx context.Context, entry LogEntry) error
	List(ctx context.Context, f LogFilter) ([]LogEntry, error)
	Summary(ctx context.Context, since time.Time) (StatsSummary, error)
	Timeseries(ctx context.Context, since time.Time, bucketHours int) ([]TimeseriesBucket, error)
	ModelStats(ctx context.Context, since time.Time) ([]ModelStat, error)
}
