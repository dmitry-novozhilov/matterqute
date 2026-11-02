package sched

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPresets(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	titles := func(now time.Time) []string {
		var out []string
		for _, p := range Presets(now) {
			out = append(out, p.Title+" "+p.At.Format("02.01 15:04"))
		}
		return out
	}
	// Среда, 14:20: через час, вечер, завтра, понедельник.
	got := titles(time.Date(2026, 11, 4, 14, 20, 30, 0, loc))
	want := []string{"Через час, в 15:20 04.11 15:20", "Сегодня в 18:00 04.11 18:00", "Завтра в 9:00 05.11 09:00", "В понедельник в 9:00 09.11 09:00"}
	if len(got) != len(want) {
		t.Fatalf("Wednesday: %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Wednesday %d: %q, want %q", i, got[i], want[i])
		}
	}
	// Воскресенье, 17:30: вечер уже скоро — без него; завтра и есть понедельник.
	got = titles(time.Date(2026, 11, 8, 17, 30, 0, 0, loc))
	if len(got) != 2 || got[1] != "Завтра в 9:00 09.11 09:00" {
		t.Errorf("Sunday evening: %q", got)
	}
}

func TestLabel(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 11, 4, 14, 20, 0, 0, loc)
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 4, 18, 0, 0, 0, loc), "сегодня в 18:00"},
		{time.Date(2026, 11, 5, 9, 0, 0, 0, loc), "завтра в 9:00"},
		{time.Date(2026, 11, 9, 9, 0, 0, 0, loc), "пн, 9 ноя в 9:00"},
		{time.Date(2027, 1, 2, 9, 0, 0, 0, loc), "2 янв 2027 в 9:00"},
		{time.Date(2026, 11, 3, 9, 0, 0, 0, loc), "3 ноя 2026 в 9:00"},
	} {
		if got := Label(c.at, now); got != c.want {
			t.Errorf("Label(%s) = %q, want %q", c.at, got, c.want)
		}
	}
}

func TestDueMissed(t *testing.T) {
	now := time.Date(2026, 11, 4, 14, 20, 0, 0, time.UTC)
	at := func(d time.Duration) *Post { return &Post{At: now.Add(d).UnixMilli()} }
	if p := at(time.Minute); p.Due(now) || p.Missed(now) {
		t.Error("future post is due or missed")
	}
	if p := at(-5 * time.Minute); !p.Due(now) || p.Missed(now) {
		t.Error("5 minutes late: should be sent")
	}
	if p := at(-time.Hour); p.Due(now) || !p.Missed(now) {
		t.Error("an hour late: should wait for the user")
	}
	if p := (&Post{At: now.Add(-time.Minute).UnixMilli(), Error: "x"}); p.Due(now) {
		t.Error("failed post is due")
	}
}

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s", "scheduled.json")
	if ps, err := Load(path); err != nil || ps != nil {
		t.Fatalf("missing file: %v %v", ps, err)
	}
	in := []*Post{{ID: "b", At: 2, Message: "второе"}, {ID: "a", At: 1, Message: "**первое**", FileIDs: []string{"f"}}}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil || len(out) != 2 || out[0].ID != "a" || out[0].Message != "**первое**" || out[0].FileIDs[0] != "f" {
		t.Fatalf("loaded %+v %v", out, err)
	}
	if err := Save(path, nil); err != nil {
		t.Fatal(err)
	}
	if ps, _ := Load(path); ps != nil {
		t.Error("empty save left the file")
	}
}
