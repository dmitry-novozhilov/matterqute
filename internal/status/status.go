// Package status — свой статус пользователя Mattermost (эмодзи, текст, срок)
// и «не беспокоить» со сроком: стандартные статусы, сроки, когда истекает, как
// подписать. Без Qt и без сети.
package status

import (
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Preset — стандартный статус, как в веб-клиенте (эмодзи и срок — его).
type Preset struct {
	Emoji, Text, Duration string
}

var Presets = []Preset{
	{"calendar", "На встрече", "one_hour"},
	{"hamburger", "Обедаю", "thirty_minutes"},
	{"sneezing_face", "Болею", "today"},
	{"house", "Работаю из дома", "today"},
	{"palm_tree", "В отпуске", "this_week"},
}

// Choice — вариант срока в списке.
type Choice struct{ ID, Title string }

// DateAndTime — срок «до выбранных даты и времени».
const DateAndTime = "date_and_time"

// Durations — когда сбросить свой статус (id — как CustomStatus.Duration).
var Durations = []Choice{
	{"", "Не сбрасывать"},
	{"thirty_minutes", "30 минут"},
	{"one_hour", "1 час"},
	{"four_hours", "4 часа"},
	{"today", "Сегодня"},
	{"this_week", "На этой неделе"},
	{DateAndTime, "Выбрать дату и время"},
}

// DNDDurations — на сколько включить «не беспокоить».
var DNDDurations = []Choice{
	{"", "Не выключать"},
	{"thirty_minutes", "30 минут"},
	{"one_hour", "1 час"},
	{"two_hours", "2 часа"},
	{"tomorrow", "До завтра"},
	{DateAndTime, "Выбрать время…"},
}

// ExpiresAt — когда истекает срок id, считая от now; для DateAndTime — at.
// Нулевое время — без срока. «Сегодня» — до конца дня, «неделя» — до конца
// воскресенья, «до завтра» — завтра в 9:00, как в веб-клиенте.
func ExpiresAt(id string, now, at time.Time) time.Time {
	day := func(t time.Time, add int) time.Time {
		y, m, d := t.Date()
		return time.Date(y, m, d+add, 0, 0, 0, 0, t.Location())
	}
	switch id {
	case "thirty_minutes":
		return now.Add(30 * time.Minute)
	case "one_hour":
		return now.Add(time.Hour)
	case "two_hours":
		return now.Add(2 * time.Hour)
	case "four_hours":
		return now.Add(4 * time.Hour)
	case "today":
		return day(now, 1).Add(-time.Millisecond)
	case "this_week":
		toMonday := (8 - int(now.Weekday())) % 7 // дней до следующего понедельника
		if toMonday == 0 {
			toMonday = 7
		}
		return day(now, toMonday).Add(-time.Millisecond)
	case "tomorrow":
		return day(now, 1).Add(9 * time.Hour)
	case DateAndTime:
		return at
	}
	return time.Time{}
}

// New — свой статус для сохранения: emoji пустой — «💬», как у сервера.
func New(emoji, text, duration string, now, at time.Time) *model.CustomStatus {
	if emoji == "" {
		emoji = model.DefaultCustomStatusEmoji
	}
	if r := []rune(text); len(r) > model.CustomStatusTextMaxRunes {
		text = string(r[:model.CustomStatusTextMaxRunes])
	}
	return &model.CustomStatus{Emoji: emoji, Text: text, Duration: duration, ExpiresAt: ExpiresAt(duration, now, at)}
}

// Current — свой статус пользователя, если он задан и не истёк, иначе nil.
func Current(u *model.User, now time.Time) *model.CustomStatus {
	if u == nil {
		return nil
	}
	cs := u.GetCustomStatus()
	if cs == nil || cs.Emoji == "" && cs.Text == "" {
		return nil
	}
	if !cs.ExpiresAt.IsZero() && cs.ExpiresAt.Before(now) {
		return nil
	}
	if cs.Emoji == "" {
		cs.Emoji = model.DefaultCustomStatusEmoji
	}
	return cs
}

var months = []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}

// Until — «до 15:30», «до завтра, 15:30», «до 3 ноября, 15:30», «до конца
// дня», «до конца недели»; "" — без срока.
func Until(duration string, expires, now time.Time) string {
	switch {
	case expires.IsZero():
		return ""
	case duration == "today":
		return "до конца дня"
	case duration == "this_week":
		return "до конца недели"
	}
	expires = expires.In(now.Location())
	clock := expires.Format("15:04")
	y, m, d := now.Date()
	ey, em, ed := expires.Date()
	switch {
	case y == ey && m == em && d == ed:
		return "до " + clock
	case time.Date(y, m, d+1, 0, 0, 0, 0, now.Location()).Equal(time.Date(ey, em, ed, 0, 0, 0, 0, now.Location())):
		return "до завтра, " + clock
	case y == ey:
		return fmt.Sprintf("до %d %s, %s", ed, months[em-1], clock)
	}
	return fmt.Sprintf("до %d %s %d, %s", ed, months[em-1], ey, clock)
}
