package cortex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/teslashibe/cortex/llm"
	"github.com/teslashibe/cortex/prompts"
)

const (
	DefaultChunkSize                = 200
	MaxObservationsPerConsolidation = DefaultChunkSize * 100
	MaxContextBytes                 = 1024 * 1024
	MaxSummaryCharacters            = 512
	ConsolidationTimeout            = 30 * time.Minute
	ModelTimeout                    = 2 * time.Minute
)

// Model is the inference boundary; production uses llm.Client.
type Model interface {
	Complete(context.Context, []llm.Message) (llm.Response, error)
	Model() string
}

func consolidationMessages(model, userID string, observations []Observation, memories []Memory) ([]llm.Message, error) {
	observations, memories, _, _ = consolidationAliases(observations, memories)
	// Historical source IDs cannot support a new operation. Hide only those
	// IDs from inference so the model cannot copy them from an existing memory;
	// retain the full provenance in storage and preserve all other context.
	type memoryContext struct {
		Memory
		Sources []string `json:"sources,omitempty"`
	}
	existing := make([]memoryContext, len(memories))
	for i, memory := range memories {
		existing[i] = memoryContext{Memory: memory}
	}
	body, err := json.Marshal(struct {
		UserID       string          `json:"user_id"`
		Observations []Observation   `json:"observations"`
		Memories     []memoryContext `json:"memories"`
	}{userID, observations, existing})
	if err != nil {
		return nil, fmt.Errorf("encode consolidation context: %w", err)
	}
	if len(body) > MaxContextBytes {
		return nil, fmt.Errorf("consolidation context exceeds %d bytes; content was not truncated", MaxContextBytes)
	}
	messages := []llm.Message{{Role: "system", Content: prompts.System}, {Role: "user", Content: prompts.Consolidation + "\n" + string(body)}}
	if err := llm.ValidateRequest(model, messages); err != nil {
		return nil, fmt.Errorf("consolidation request: %w", err)
	}
	return messages, nil
}

// Aliases are request-local, disjoint from real identifiers, and never stored.
// Models may return either an alias or an exact real identifier for compatibility
// with existing Model implementations. Both resolve through the same allowlist.
func consolidationAliases(observations []Observation, memories []Memory) ([]Observation, []Memory, map[string]string, map[string]string) {
	used := map[string]bool{}
	for _, o := range observations {
		used[o.ID] = true
	}
	for _, m := range memories {
		used[m.ID] = true
	}
	alias := func(prefix string, n int) string {
		v := fmt.Sprintf("%s%d", prefix, n+1)
		for used[v] {
			v += "_"
		}
		used[v] = true
		return v
	}
	obs := append([]Observation(nil), observations...)
	mem := append([]Memory(nil), memories...)
	sources, targets := map[string]string{}, map[string]string{}
	for i, o := range observations {
		id := alias("s", i)
		obs[i].ID = id
		obs[i].IdempotencyKey = ""
		sources[id] = o.ID
		sources[o.ID] = o.ID
	}
	for i, m := range memories {
		id := alias("m", i)
		mem[i].ID = id
		targets[id] = m.ID
		targets[m.ID] = m.ID
	}
	return obs, mem, sources, targets
}

func resolveConsolidationAliases(ops []Op, observations []Observation, memories []Memory) ([]Op, error) {
	_, _, sources, targets := consolidationAliases(observations, memories)
	out := append([]Op(nil), ops...)
	for i, op := range ops {
		out[i].Sources = make([]string, len(op.Sources))
		seen := map[string]bool{}
		for j, ref := range op.Sources {
			id, ok := sources[ref]
			if !ok || seen[id] {
				return nil, fmt.Errorf("operation %d has an unavailable or duplicate source reference", i)
			}
			out[i].Sources[j] = id
			seen[id] = true
		}
		if op.OldID != "" {
			id, ok := targets[op.OldID]
			if !ok {
				return nil, fmt.Errorf("operation %d has an unavailable memory reference", i)
			}
			out[i].OldID = id
		}
	}
	return out, nil
}

func chunkObservations(obs []Observation, size int, byDay bool) [][]Observation {
	if size <= 0 {
		size = DefaultChunkSize
	}
	var chunks [][]Observation
	for start := 0; start < len(obs); {
		end := start + 1
		for end < len(obs) && end-start < size && (!byDay || obs[end].At.UTC().Format(time.DateOnly) == obs[start].At.UTC().Format(time.DateOnly)) {
			end++
		}
		chunks = append(chunks, obs[start:end])
		start = end
	}
	return chunks
}

type rawOp struct {
	Kind   OpKind `json:"kind"`
	Memory *struct {
		Kind       Kind     `json:"kind"`
		Summary    string   `json:"summary"`
		Confidence *float64 `json:"confidence"`
	} `json:"memory"`
	OldID   *string  `json:"old_id"`
	Sources []string `json:"sources"`
	Reason  string   `json:"reason"`
}

// parseOps accepts exactly one JSON envelope. Any malformed operation rejects
// the entire response; only an explicit ops: [] is a successful no-op.
func parseOps(text string) ([]Op, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("invalid UTF-8 response")
	}
	if err := checkJSON(json.NewDecoder(strings.NewReader(text))); err != nil {
		return nil, err
	}
	var raw struct {
		Ops []rawOp `json:"ops"`
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode operations: %w", err)
	}
	if raw.Ops == nil {
		return nil, fmt.Errorf("ops must be an explicit array")
	}
	out := make([]Op, 0, len(raw.Ops))
	for i, r := range raw.Ops {
		op := Op{Kind: r.Kind, Reason: r.Reason, Sources: r.Sources}
		if r.OldID != nil {
			op.OldID = *r.OldID
		}
		if r.Memory != nil {
			if r.Memory.Confidence == nil {
				return nil, fmt.Errorf("operation %d: confidence required", i)
			}
			op.Memory = Memory{Kind: r.Memory.Kind, Summary: r.Memory.Summary, Confidence: *r.Memory.Confidence}
		}
		if (r.Kind == OpInsert && r.OldID != nil) || (r.Kind == OpReinforce && r.Memory != nil) {
			return nil, fmt.Errorf("operation %d: fields do not match kind", i)
		}
		if err := validateOp(op); err != nil {
			return nil, fmt.Errorf("operation %d: %w", i, err)
		}
		out = append(out, op)
	}
	return out, nil
}

func validateOp(op Op) error {
	unit := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }
	switch op.Kind {
	case OpInsert, OpContradict:
		if !op.Memory.Kind.Valid() {
			return fmt.Errorf("invalid memory kind")
		}
		if strings.TrimSpace(op.Memory.Summary) == "" {
			return fmt.Errorf("empty memory summary")
		}
		if n := utf8.RuneCountInString(op.Memory.Summary); n > MaxSummaryCharacters {
			return fmt.Errorf("memory summary has %d characters; maximum is %d", n, MaxSummaryCharacters)
		}
		if !unit(op.Memory.Confidence) {
			return fmt.Errorf("invalid memory confidence")
		}
		if op.Kind == OpInsert && op.OldID != "" {
			return fmt.Errorf("unexpected memory operation fields")
		}
		if op.Kind == OpContradict && op.OldID == "" {
			return fmt.Errorf("old_id required")
		}
	case OpReinforce:
		if op.OldID == "" || op.Memory.Kind != "" || op.Memory.Summary != "" {
			return fmt.Errorf("invalid reinforcement")
		}
	default:
		return fmt.Errorf("unknown operation kind %q", op.Kind)
	}
	if len(op.Sources) == 0 {
		return fmt.Errorf("sources required for fact mutations")
	}
	seen := make(map[string]bool, len(op.Sources))
	for _, id := range op.Sources {
		if id == "" || seen[id] {
			return fmt.Errorf("empty or duplicate source ID")
		}
		seen[id] = true
	}
	return nil
}

// checkJSON rejects duplicate keys (which encoding/json otherwise silently
// overwrites), null fields, excessive nesting, and trailing JSON values.
func checkJSON(d *json.Decoder) error {
	var value func(int) error
	value = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("JSON nesting too deep")
		}
		t, err := d.Token()
		if err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if t == nil {
			return fmt.Errorf("null fields are not allowed")
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := k.(string)
				if !ok || keys[key] {
					return fmt.Errorf("invalid or duplicate JSON key %v", k)
				}
				switch key {
				case "ops", "kind", "memory", "summary", "confidence", "old_id", "sources", "reason":
				default:
					return fmt.Errorf("unknown JSON field %q", key)
				}
				keys[key] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}
