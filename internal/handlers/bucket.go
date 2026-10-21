package handlers

import "time"

// defaultTargetPoints is how many points query_weather aims for when the model
// does not choose a bucket. The API's own default aims for ~750, which suits a
// chart but not a model's context: four series at that size were ~45k tokens
// in one answer, and re-sending them with a follow-up question tripped the
// Anthropic input-token rate limit. Fifty points still show the shape of a day,
// a week, or a year; the model can ask for a finer bucket when it needs one.
const defaultTargetPoints = 50

// bucketLadder is the set of buckets the API accepts, finest first.
var bucketLadder = []struct {
	name string
	d    time.Duration
}{
	{"5m", 5 * time.Minute},
	{"15m", 15 * time.Minute},
	{"30m", 30 * time.Minute},
	{"1h", time.Hour},
	{"3h", 3 * time.Hour},
	{"6h", 6 * time.Hour},
	{"12h", 12 * time.Hour},
	{"1d", 24 * time.Hour},
	{"1w", 7 * 24 * time.Hour},
}

// rangeSpan approximates how long a named range is. "all" is open-ended, so it
// is treated as several years, which lands on the coarsest bucket.
func rangeSpan(name string, now time.Time) (time.Duration, bool) {
	switch name {
	case "", "24h":
		return 24 * time.Hour, true // the tool's documented default range
	case "12h":
		return 12 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	case "90d":
		return 90 * 24 * time.Hour, true
	case "ytd":
		return now.Sub(time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())), true
	case "last_12mo", "prev_year":
		return 365 * 24 * time.Hour, true
	case "all":
		return 5 * 365 * 24 * time.Hour, true
	}
	return 0, false
}

// defaultBucket picks the finest bucket that keeps a query at or under
// defaultTargetPoints. It returns "" when the span cannot be worked out, which
// leaves the choice to the API.
func defaultBucket(rangeName, start, end string, now time.Time) string {
	var span time.Duration
	if start != "" {
		s, err := time.Parse(time.RFC3339, start)
		if err != nil {
			return ""
		}
		e := now
		if end != "" {
			if e, err = time.Parse(time.RFC3339, end); err != nil {
				return ""
			}
		}
		span = e.Sub(s)
	} else {
		var ok bool
		if span, ok = rangeSpan(rangeName, now); !ok {
			return ""
		}
	}
	if span <= 0 {
		return ""
	}

	for _, b := range bucketLadder {
		if span/b.d <= defaultTargetPoints {
			return b.name
		}
	}
	return bucketLadder[len(bucketLadder)-1].name
}
