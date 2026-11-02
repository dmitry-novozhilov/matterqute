// Package sched — отложенные сообщения, как в Telegram: что и когда
// отправить, варианты времени («завтра в 9:00»), подписи, хранение на диске
// для сервера, который сам отложенные не умеет (без лицензии Mattermost) —
// тогда их отправляет приложение, пока оно запущено.
package sched

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Post — отложенное сообщение.
type Post struct {
	ID        string   `json:"id"`
	ChannelID string   `json:"channel_id"`
	RootID    string   `json:"root_id,omitempty"` // ответ в треде
	Message   string   `json:"message"`           // markdown Mattermost
	FileIDs   []string `json:"file_ids,omitempty"`
	At        int64    `json:"at"`              // когда отправить, мс
	Error     string   `json:"error,omitempty"` // почему не ушло; пусто — ждёт
	OnServer  bool     `json:"-"`               // хранит и отправит сервер
}

// Time — когда отправить.
func (p *Post) Time() time.Time { return time.UnixMilli(p.At) }

// Late — сколько опоздания терпимо при запуске приложения: опоздавшее меньше
// уходит сразу, больше — ждёт решения («Отправить сейчас»), чтобы
// устаревшее не ушло неожиданно.
const Late = 10 * time.Minute

// Due — пора отправлять: время пришло, опоздание терпимо, ошибки не было.
func (p *Post) Due(now time.Time) bool {
	at := p.Time()
	return p.Error == "" && !at.After(now) && now.Sub(at) <= Late
}

// Missed — время прошло давно, а сообщение не ушло (приложение было закрыто).
func (p *Post) Missed(now time.Time) bool {
	return p.Error == "" && now.Sub(p.Time()) > Late
}

// Sort — по времени отправки.
func Sort(ps []*Post) {
	slices.SortStableFunc(ps, func(a, b *Post) int { return cmp.Compare(a.At, b.At) })
}

// Preset — вариант времени в окне «Отправить позже».
type Preset struct {
	Title string
	At    time.Time
}

// Presets — как в Telegram и веб-клиенте: через час, сегодня вечером (если
// ещё не вечер), завтра утром, в понедельник утром.
func Presets(now time.Time) []Preset {
	day := func(add, hour int) time.Time {
		y, m, d := now.Date()
		return time.Date(y, m, d+add, hour, 0, 0, 0, now.Location())
	}
	hour := now.Add(time.Hour).Truncate(time.Minute)
	out := []Preset{{"Через час, в " + strings.TrimPrefix(hour.Format("15:04"), "0"), hour}}
	if evening := day(0, 18); evening.Sub(now) > time.Hour {
		out = append(out, Preset{"Сегодня в 18:00", evening})
	}
	out = append(out, Preset{"Завтра в 9:00", day(1, 9)})
	toMonday := (8 - int(now.Weekday())) % 7
	if toMonday == 0 {
		toMonday = 7
	}
	if toMonday > 1 { // завтра понедельник — уже есть «завтра в 9:00»
		out = append(out, Preset{"В понедельник в 9:00", day(toMonday, 9)})
	}
	return out
}

var (
	weekdays = []string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}
	months   = []string{"янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}
)

// Label — «сегодня в 18:00», «завтра в 9:00», «пн, 9 ноя в 9:00», в другом
// году — с годом.
func Label(at, now time.Time) string {
	at = at.In(now.Location())
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	clock := strings.TrimPrefix(at.Format("15:04"), "0") // «9:00», не «09:00»
	switch days := int(at.Sub(today).Hours() / 24); {
	case at.Before(today):
		return fmt.Sprintf("%d %s %d в %s", at.Day(), months[at.Month()-1], at.Year(), clock)
	case days == 0:
		return "сегодня в " + clock
	case days == 1:
		return "завтра в " + clock
	case at.Year() != now.Year():
		return fmt.Sprintf("%d %s %d в %s", at.Day(), months[at.Month()-1], at.Year(), clock)
	}
	return fmt.Sprintf("%s, %d %s в %s", weekdays[at.Weekday()], at.Day(), months[at.Month()-1], clock)
}

// Load — отложенные из файла; нет файла — пусто.
func Load(path string) ([]*Post, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load scheduled posts %s: %w", path, err)
	}
	var ps []*Post
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("load scheduled posts %s: %w", path, err)
	}
	Sort(ps)
	return ps, nil
}

// Save — отложенные в файл (0600: там текст сообщений); пусто — файла нет.
func Save(path string, ps []*Post) error {
	if len(ps) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("save scheduled posts %s: %w", path, err)
		}
		return nil
	}
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return fmt.Errorf("save scheduled posts %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("save scheduled posts %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("save scheduled posts %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("save scheduled posts %s: %w", path, err)
	}
	return nil
}
