package cortex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type SegmentInput struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type SegmentSpan struct {
	StartRune int `json:"start_rune"`
	EndRune   int `json:"end_rune"`
	Tokens    int `json:"tokens"`
}

type SegmentedText struct {
	ID      string        `json:"id"`
	Windows []SegmentSpan `json:"windows"`
}

// WindowSegmenter uses the embedding provider's exact tokenizer, including
// special tokens. Its identity must change with tokenizer or window policy.
// Returned offsets refer to the original Unicode text, never a decoded copy.
// The embedding provider must independently reject inputs beyond its limit.
type WindowSegmenter interface {
	ID() string
	MaxTokens() int
	Split(context.Context, []SegmentInput) ([]SegmentedText, error)
}

func (e *Engine) windowModelKey() string {
	model := e.embedder.Model()
	if e.segmenter != nil {
		model += "|segments=" + contentHash(fmt.Sprintf("%s:%d", e.segmenter.ID(), e.segmenter.MaxTokens()))
	}
	return model
}

func (e *Engine) splitObservations(ctx context.Context, obs []Observation) ([]ObservationWindow, error) {
	if e.segmenter == nil {
		return observationWindows(obs)
	}
	if strings.TrimSpace(e.segmenter.ID()) == "" || len(e.segmenter.ID()) > 512 || e.segmenter.MaxTokens() < 1 || e.segmenter.MaxTokens() > 65536 {
		return nil, errors.New("invalid segmenter contract")
	}
	out := []ObservationWindow{}
	minimum := 0
	ids := map[string]bool{}
	nonempty := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if !utf8.ValidString(o.Content) || o.ID == "" || ids[o.ID] {
			return nil, errors.New("invalid segmentation input")
		}
		ids[o.ID] = true
		if o.Content == "" {
			continue
		}
		nonempty = append(nonempty, o)
		minimum += (utf8.RuneCountInString(o.Content) + ObservationWindowSize - 1) / ObservationWindowSize
		if minimum > MaxObservationWindows {
			return nil, ErrWindowCapacity
		}
	}
	obs = nonempty
	for start := 0; start < len(obs); start += 32 {
		batch := obs[start:min(start+32, len(obs))]
		inputs := make([]SegmentInput, len(batch))
		byID := map[string]Observation{}
		for i, o := range batch {
			if !utf8.ValidString(o.Content) || o.ID == "" {
				return nil, errors.New("invalid source for segmentation")
			}
			if _, ok := byID[o.ID]; ok {
				return nil, errors.New("duplicate segmentation input")
			}
			byID[o.ID] = o
			inputs[i] = SegmentInput{o.ID, o.Content}
		}
		results, err := e.segmenter.Split(ctx, inputs)
		if err != nil {
			return nil, errors.New("source segmentation failed")
		}
		if len(results) != len(inputs) {
			return nil, errors.New("segmentation count mismatch")
		}
		seen := map[string]bool{}
		grouped := map[string][]ObservationWindow{}
		for _, result := range results {
			o, ok := byID[result.ID]
			if !ok || seen[result.ID] {
				return nil, errors.New("invalid segmentation result ID")
			}
			seen[result.ID] = true
			runes := []rune(o.Content)
			if len(runes) == 0 || len(result.Windows) == 0 {
				return nil, errors.New("empty segmentation")
			}
			previousStart, previousEnd := -1, 0
			for i, w := range result.Windows {
				if len(out)+len(grouped[o.ID]) >= MaxObservationWindows {
					return nil, ErrWindowCapacity
				}
				if w.StartRune < 0 || w.EndRune > len(runes) || w.EndRune <= w.StartRune || w.EndRune-w.StartRune > ObservationWindowSize || w.Tokens < 1 || w.Tokens > e.segmenter.MaxTokens() || w.StartRune <= previousStart || w.StartRune > previousEnd || w.EndRune <= previousEnd || (i == 0 && w.StartRune != 0) {
					return nil, errors.New("invalid or incomplete segmentation")
				}
				grouped[o.ID] = append(grouped[o.ID], ObservationWindow{o.ID, w.StartRune, w.EndRune, string(runes[w.StartRune:w.EndRune])})
				previousStart, previousEnd = w.StartRune, w.EndRune
			}
			if previousEnd != len(runes) {
				return nil, errors.New("segmentation does not cover source")
			}
		}
		for _, o := range batch {
			if len(out)+len(grouped[o.ID]) > MaxObservationWindows {
				return nil, ErrWindowCapacity
			}
			out = append(out, grouped[o.ID]...)
		}
	}
	return out, nil
}
