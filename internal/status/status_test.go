package status

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestExpiresAt(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 11, 4, 14, 20, 0, 0, msk) // среда
	at := time.Date(2026, 11, 20, 18, 0, 0, 0, msk)
	for id, want := range map[string]time.Time{
		"":               {},
		"thirty_minutes": time.Date(2026, 11, 4, 14, 50, 0, 0, msk),
		"one_hour":       time.Date(2026, 11, 4, 15, 20, 0, 0, msk),
		"two_hours":      time.Date(2026, 11, 4, 16, 20, 0, 0, msk),
		"four_hours":     time.Date(2026, 11, 4, 18, 20, 0, 0, msk),
		"today":          time.Date(2026, 11, 4, 23, 59, 59, 999e6, msk),
		"this_week":      time.Date(2026, 11, 8, 23, 59, 59, 999e6, msk), // воскресенье
		"tomorrow":       time.Date(2026, 11, 5, 9, 0, 0, 0, msk),
		DateAndTime:      at,
	} {
		if got := ExpiresAt(id, now, at); !got.Equal(want) {
			t.Errorf("ExpiresAt(%q) = %v, want %v", id, got, want)
		}
	}
	// В воскресенье «на этой неделе» — до конца этого же воскресенья.
	sunday := time.Date(2026, 11, 8, 10, 0, 0, 0, msk)
	if got := ExpiresAt("this_week", sunday, at); !got.Equal(time.Date(2026, 11, 8, 23, 59, 59, 999e6, msk)) {
		t.Errorf("this_week on Sunday = %v", got)
	}
}

func TestCurrentAndUntil(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 11, 4, 14, 20, 0, 0, msk)
	u := &model.User{}
	if Current(u, now) != nil {
		t.Error("status for a user without one")
	}
	u.SetCustomStatus(New("", "Думаю", "one_hour", now, time.Time{}))
	cs := Current(u, now)
	if cs == nil || cs.Emoji != "speech_balloon" || cs.Text != "Думаю" {
		t.Fatalf("Current = %+v", cs)
	}
	if got := Until(cs.Duration, cs.ExpiresAt, now); got != "до 15:20" {
		t.Errorf("Until = %q", got)
	}
	if Current(u, now.Add(2*time.Hour)) != nil {
		t.Error("expired status still shown")
	}
	u.SetCustomStatus(New("palm_tree", "В отпуске", "", now, time.Time{}))
	if cs := Current(u, now.AddDate(1, 0, 0)); cs == nil || Until(cs.Duration, cs.ExpiresAt, now) != "" {
		t.Errorf("status without expiry: %+v", cs)
	}
	for _, c := range []struct {
		dur     string
		expires time.Time
		want    string
	}{
		{"today", ExpiresAt("today", now, time.Time{}), "до конца дня"},
		{"this_week", ExpiresAt("this_week", now, time.Time{}), "до конца недели"},
		{DateAndTime, time.Date(2026, 11, 5, 9, 30, 0, 0, msk), "до завтра, 09:30"},
		{DateAndTime, time.Date(2026, 11, 20, 18, 0, 0, 0, msk), "до 20 ноября, 18:00"},
		{DateAndTime, time.Date(2027, 1, 2, 10, 0, 0, 0, msk), "до 2 января 2027, 10:00"},
	} {
		if got := Until(c.dur, c.expires, now); got != c.want {
			t.Errorf("Until(%s, %v) = %q, want %q", c.dur, c.expires, got, c.want)
		}
	}
	if got := New("x", string(make([]rune, 150)), "", now, time.Time{}); len([]rune(got.Text)) != 100 {
		t.Errorf("text not cut to 100 runes: %d", len([]rune(got.Text)))
	}
}
