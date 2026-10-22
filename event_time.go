package cortex

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA dates available in standalone release binaries.
)

// EventDate retains the exact source span and the rule used to resolve it.
// Resolved means the date expression was parsed, not that the event occurred.
type EventDate struct {
	Candidate   Candidate `json:"candidate"`
	Text        string    `json:"text"`
	Date        string    `json:"date,omitempty"`
	Rule        string    `json:"rule,omitempty"`
	Resolved    bool      `json:"resolved"`
	ObservedAt  time.Time `json:"observed_at"`
	Timezone    string    `json:"timezone"`
	ContentHash string    `json:"content_hash"`
}

var daysAgo = regexp.MustCompile(`^([0-9]{1,5}) days? ago$`)

// ResolveDateExpression supports explicit ISO/English dates, today, yesterday,
// N days ago and last <weekday>. Last weekday means strictly before the anchor.
// Ambiguous week/month expressions and dates without a year remain unresolved.
func ResolveDateExpression(expression string, anchor time.Time, zone string) (string, string, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return "", "", errors.New("explicit IANA timezone required")
	}
	text := strings.TrimSpace(expression)
	for _, layout := range []string{"2006-01-02", "January 2, 2006", "Jan 2, 2006", "2 January 2006", "2 Jan 2006"} {
		if d, err := time.ParseInLocation(layout, text, loc); err == nil && d.Year() >= 1 && d.Year() <= 9999 {
			return d.Format("2006-01-02"), "explicit_date", nil
		}
	}
	if anchor.IsZero() {
		return "", "", nil
	}
	local := anchor.In(loc)
	// Do civil-date arithmetic in UTC after selecting the anchor's local date.
	// Some zones change offset at midnight or skip a date entirely.
	d := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	text = strings.ToLower(text)
	delta := 0
	rule := ""
	switch text {
	case "today":
		rule = "anchor_day"
	case "yesterday":
		delta = -1
		rule = "previous_day"
	}
	if match := daysAgo.FindStringSubmatch(text); match != nil {
		n, _ := strconv.Atoi(match[1])
		delta = -n
		rule = "calendar_days_ago"
	}
	if strings.HasPrefix(text, "last ") {
		for weekday := time.Sunday; weekday <= time.Saturday; weekday++ {
			if text == "last "+strings.ToLower(weekday.String()) {
				delta = -((int(d.Weekday()) - int(weekday) + 7) % 7)
				if delta == 0 {
					delta = -7
				}
				rule = "previous_weekday"
			}
		}
	}
	if rule == "" {
		return "", "", nil
	}
	d = d.AddDate(0, 0, delta)
	if d.Year() < 1 || d.Year() > 9999 {
		return "", "", nil
	}
	return d.Format("2006-01-02"), rule, nil
}

// CalendarDaysBetween subtracts civil dates, independently of DST day length.
func CalendarDaysBetween(from, to string) (int, error) {
	a, err := time.Parse("2006-01-02", from)
	if err != nil {
		return 0, err
	}
	b, err := time.Parse("2006-01-02", to)
	if err != nil {
		return 0, err
	}
	return int((b.Unix() - a.Unix()) / 86400), nil
}

func (e *Engine) ResolveEventDates(ctx context.Context, user, zone string, candidates []Candidate) ([]EventDate, error) {
	out := []EventDate{}
	if strings.TrimSpace(user) == "" || len(candidates) < 1 || len(candidates) > 128 {
		return nil, errors.New("user and 1..128 date candidates required")
	}
	if _, _, err := ResolveDateExpression("", time.Time{}, zone); err != nil {
		return nil, err
	}
	obs, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return nil, err
	}
	byID := map[string]Observation{}
	for _, o := range obs {
		byID[o.ID] = o
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		o, ok := byID[c.ObservationID]
		runes := []rune(o.Content)
		if !ok || c.ID == "" || seen[c.ID] || c.StartRune < 0 || c.EndRune <= c.StartRune || c.EndRune > len(runes) || c.EndRune-c.StartRune > 128 {
			return nil, errors.New("invalid date candidate or unavailable source")
		}
		seen[c.ID] = true
		text := string(runes[c.StartRune:c.EndRune])
		date, rule, err := ResolveDateExpression(text, o.At, zone)
		if err != nil {
			return nil, err
		}
		out = append(out, EventDate{c, text, date, rule, date != "", o.At, zone, contentHash(o.Content)})
	}
	return out, nil
}
