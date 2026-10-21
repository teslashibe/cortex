package cortex

import "time"

// Consolidation tracks one saved, bounded chunk of work.
type Consolidation struct {
	ID             string              `json:"id"`
	UserID         string              `json:"user_id"`
	Status         string              `json:"status"`
	ObservationIDs []string            `json:"observation_ids"`
	Model          string              `json:"model"`
	PromptVersion  string              `json:"prompt_version"`
	StartedAt      time.Time           `json:"started_at"`
	CompletedAt    *time.Time          `json:"completed_at,omitempty"`
	Counts         ConsolidationCounts `json:"counts"`
	ErrorMessage   string              `json:"error_message,omitempty"`
}

type ConsolidationCounts struct {
	ObservationsProcessed int `json:"observations_processed"`
	MemoriesCreated       int `json:"memories_created"`
	MemoriesReinforced    int `json:"memories_reinforced"`
	TokensUsed            int `json:"tokens_used"`
}

type Signal struct {
	ID          string
	UserID      string
	MemoryID    string
	SignalType  string
	SignalValue float64
	Metadata    map[string]any
}
