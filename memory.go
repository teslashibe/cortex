package cortex

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"time"
)

type Kind string

const (
	KindFact         Kind = "fact"
	KindBehavior     Kind = "behavior"
	KindPreference   Kind = "preference"
	KindRelationship Kind = "relationship"
	KindProject      Kind = "project"
	KindRoutine      Kind = "routine"
)

func (k Kind) Valid() bool {
	switch k {
	case KindFact, KindBehavior, KindPreference, KindRelationship, KindProject, KindRoutine:
		return true
	}
	return false
}

type OpKind string

const (
	OpInsert     OpKind = "insert"
	OpReinforce  OpKind = "reinforce"
	OpContradict OpKind = "contradict"
)

type Observation struct {
	ID             string         `json:"id"`
	UserID         string         `json:"user_id"`
	Source         string         `json:"source"`
	Content        string         `json:"content"`
	Refs           map[string]any `json:"refs"`
	Salience       float64        `json:"salience"`
	At             time.Time      `json:"at"`
	IdempotencyKey string         `json:"idempotency_key"`
	ToolName       string         `json:"tool_name,omitempty"`
	IntentID       string         `json:"intent_id,omitempty"`
	OutcomeClass   string         `json:"outcome_class,omitempty"`
	TurnIndex      *int           `json:"turn_index,omitempty"`
}

type Memory struct {
	ID              string     `json:"id"`
	UserID          string     `json:"user_id"`
	Kind            Kind       `json:"kind"`
	Summary         string     `json:"summary"`
	Confidence      float64    `json:"confidence"`
	ReinforcedCount int        `json:"reinforced_count"`
	BornAt          time.Time  `json:"born_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	ContradictedBy  string     `json:"contradicted_by,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	Sources         []string   `json:"sources"`
	ArchivedAt      *time.Time `json:"archived_at,omitempty"`
	Pinned          bool       `json:"pinned"`
	Utility         float64    `json:"utility"`
	OutcomeCount    int        `json:"outcome_count"`
}

type Op struct {
	Kind    OpKind
	Memory  Memory
	OldID   string
	Reason  string
	Sources []string
}

type Hit struct {
	Memory Memory
	Score  float64
}

func ObservationID(sender, text, createdAt, conversationUUID string) string {
	h := sha256.Sum256([]byte(sender + "\x00" + text + "\x00" + createdAt + "\x00" + conversationUUID))
	return hex.EncodeToString(h[:])[:16]
}

// sensitiveSummaryRE matches memory summaries that should be filtered from
// public-tier profile reads (legal / health / financial / security).
// Conservative: false positives acceptable, false negatives leak.
var sensitiveSummaryRE = regexp.MustCompile(`(?i)\b(landlord|lawsuit|tax court|irs|deficienc|peptide|bpc-?157|tesamorelin|tirzepatide|nad\b|glutathione|wallet|ledger|1password|seed phrase|passport|ssn|salary|forbearance|mortgage|hpa|hpv|herpes|menopause|diagnos|injur|hrv|dexa|inbody)\b`)
