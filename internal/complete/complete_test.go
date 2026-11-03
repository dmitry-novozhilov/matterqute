package complete

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestAt(t *testing.T) {
	for _, tc := range []struct {
		before string
		want   Token
		ok     bool
	}{
		{"@", Token{User, "", 1}, true},
		{"привет @bo", Token{User, "bo", 3}, true},
		{"(@alice.k", Token{User, "alice.k", 8}, true},
		{"@Борис", Token{User, "Борис", 6}, true},
		{"mail@example", Token{}, false}, // не с начала слова
		{"@bob ", Token{}, false},        // слово закончилось
		{"глянь ~town", Token{Channel, "town", 5}, true},
		{"#релиз", Token{Channel, "релиз", 6}, true},
		{"#", Token{Channel, "", 1}, true},
		{"тег #a#b", Token{}, false},
		{":ta", Token{Emoji, "ta", 3}, true},
		{"ок :+", Token{}, false}, // меньше двух символов после двоеточия
		{"ок :+1", Token{Emoji, "+1", 3}, true},
		{"ок :+1_", Token{Emoji, "+1_", 4}, true},
		{"в 10:30", Token{}, false}, // не с начала слова
		{":)", Token{}, false},
		{":ти", Token{}, false}, // эмодзи — латиница
		{"", Token{}, false},
		{"просто текст", Token{}, false},
		{"😀 @al", Token{User, "al", 3}, true},
		{"a\u00a0@bo", Token{User, "bo", 3}, true}, // неразрывный пробел — тоже разделитель
		{"a\u3000~tow", Token{Channel, "tow", 4}, true},
	} {
		got, ok := At(tc.before)
		if ok != tc.ok || got != tc.want {
			t.Errorf("At(%q) = %+v, %v; want %+v, %v", tc.before, got, ok, tc.want, tc.ok)
		}
	}
}

func TestUsers(t *testing.T) {
	users := []*model.User{
		{Id: "1", Username: "carol", FirstName: "Карина", LastName: "Орлова"},
		{Id: "2", Username: "bob", FirstName: "Борис", LastName: "Иванов"},
		{Id: "3", Username: "alice", FirstName: "Алиса", LastName: "Ковалёва"},
		{Id: "4", Username: "boris.old", DeleteAt: 1},
		{Id: "5", Username: "dave", Nickname: "bobcat"},
	}
	names := func(us []*model.User) string {
		var out []string
		for _, u := range us {
			out = append(out, u.Username)
		}
		return strings.Join(out, ",")
	}
	for q, want := range map[string]string{
		"bo":  "bob,dave", // имя пользователя с запроса — раньше ника
		"бор": "bob",
		"ОРЛ": "carol",
		"":    "alice,bob,carol,dave",
		"zzz": "",
	} {
		if got := names(Users(users, q, 10)); got != want {
			t.Errorf("Users(%q) = %s, want %s", q, got, want)
		}
	}
	if got := Users(users, "", 2); len(got) != 2 {
		t.Errorf("limit: %d users", len(got))
	}
	if got := Specials("ch"); len(got) != 1 || got[0].Name != "channel" {
		t.Errorf("Specials(ch) = %v", got)
	}
}

func TestChannels(t *testing.T) {
	chs := []*model.Channel{
		{Id: "1", Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen},
		{Id: "2", Name: "releases", DisplayName: "Релизы", Type: model.ChannelTypePrivate},
		{Id: "3", Name: "a__b", DisplayName: "bob", Type: model.ChannelTypeDirect},
		{Id: "4", Name: "backend", DisplayName: "Backend", Type: model.ChannelTypeOpen},
		{Id: "5", Name: "squad", DisplayName: "Отряд", Type: model.ChannelTypeOpen},
	}
	ids := func(cs []*model.Channel) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return strings.Join(out, ",")
	}
	for q, want := range map[string]string{
		"sq":  "squad,town-square", // начало имени канала — раньше слова в середине
		"рел": "releases",
		"b":   "backend",
		"":    "backend,town-square,squad,releases",
	} {
		if got := ids(Channels(chs, q, 10)); got != want {
			t.Errorf("Channels(%q) = %s, want %s", q, got, want)
		}
	}
}

func TestEmoji(t *testing.T) {
	names := []string{"tada", "taco", "potato", "t-rex", "smile", "tanabata_tree"}
	if got := strings.Join(EmojiNames(names, "ta", 10), ","); got != "taco,tada,tanabata_tree,potato" {
		t.Errorf("EmojiNames(ta) = %s", got)
	}
}

func TestCommandAt(t *testing.T) {
	for in, want := range map[string]Token{
		"/":          {Kind: Command, Query: "", Len: 1},
		"/aw":        {Kind: Command, Query: "aw", Len: 3},
		"/join town": {Kind: Command, Query: "join town", Len: 10},
		"/эхо":       {Kind: Command, Query: "эхо", Len: 4},
	} {
		if got, ok := CommandAt(in); !ok || got != want {
			t.Errorf("CommandAt(%q) = %+v, %v; want %+v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "a/b", "//comment", " /away"} {
		if _, ok := CommandAt(in); ok {
			t.Errorf("CommandAt(%q) is a command", in)
		}
	}
}

func TestScore(t *testing.T) {
	for _, c := range []struct {
		q      string
		fields []string
		want   int
	}{
		{"back", []string{"Backend", "backend"}, 4},
		{"api", []string{"Сервисы и API", "services"}, 3},
		{"serv", []string{"Сервисы и API", "services-api"}, 2},
		{"kend", []string{"Backend", "backend"}, 1},
		{"zzz", []string{"Backend", "backend"}, 0},
		{"", []string{"Backend"}, 0},
		{"Боб", []string{"bob", "Боб Иванов"}, 2},
	} {
		if got := Score(c.q, c.fields...); got != c.want {
			t.Errorf("Score(%q, %q) = %d, want %d", c.q, c.fields, got, c.want)
		}
	}
}
