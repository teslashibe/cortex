package cortex

import (
	"math"
	"strings"
	"time"
)

// recallScore ranks current memories without recording speculative feedback.
func recallScore(query string, m Memory, now time.Time) float64 {
	q := strings.ToLower(strings.TrimSpace(query))
	s := strings.ToLower(m.Summary)
	if q == "" {
		return m.Confidence
	}
	overlap := 0.0
	if strings.Contains(s, q) {
		overlap = 1.0
	} else {
		for _, w := range strings.Fields(q) {
			if len(w) > 2 && strings.Contains(s, w) {
				overlap += 1.0 / float64(len(strings.Fields(q)))
			}
		}
	}
	ageDays := math.Max(now.Sub(m.LastSeenAt).Hours()/24, 0)
	recency := math.Exp(-ageDays / 90.0)
	reinforce := math.Log(1+float64(m.ReinforcedCount)) / 4.0
	return 0.5*m.Confidence + 0.3*overlap + 0.1*recency + 0.1*math.Min(reinforce, 1.0)
}
