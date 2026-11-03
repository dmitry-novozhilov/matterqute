package spell

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestWords(t *testing.T) {
	text := "Привет, @alice! Глянь ~town-square и :smile: тут https://example.com/a — кто-нибудь знает API v2? 👍 don't 12:30 file.go e-mail"
	var got []string
	for _, w := range Words(text) {
		got = append(got, w.Text)
		// Границы в UTF-16 — как у Qt: проверяем по самому тексту.
		u := utf16.Encode([]rune(text))
		if s := string(utf16.Decode(u[w.Start16:w.End16])); s != w.Text || text[w.Start:w.End] != w.Text {
			t.Errorf("%q: utf16 %q, bytes %q", w.Text, s, text[w.Start:w.End])
		}
	}
	want := []string{"Привет", "Глянь", "и", "тут", "кто-нибудь", "знает", "don't", "e-mail"}
	want = slices.DeleteFunc(want, func(s string) bool { return len([]rune(s)) < 2 })
	if !slices.Equal(got, want) {
		t.Errorf("words %q\nwant  %q", got, want)
	}
}

// С настоящими словарями (DICPATH или системные); нет — пропуск.
func TestChecker(t *testing.T) {
	personal := filepath.Join(t.TempDir(), "words.txt")
	c, err := Open(Langs, personal)
	if errors.Is(err, ErrNoLibrary) || err != nil && strings.Contains(err.Error(), "no dictionaries") {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, w := range []string{"привет", "сообщения", "кто-нибудь", "hello", "deploy", "Москва"} {
		if !c.Correct(w) {
			t.Errorf("%q marked wrong (dicts %v)", w, c.Loaded)
		}
	}
	for _, w := range []string{"превет", "сообшения", "helo"} {
		if c.Correct(w) {
			t.Errorf("%q marked correct", w)
		}
	}
	if s := c.Suggest("превет", 5); !slices.Contains(s, "привет") {
		t.Errorf("suggestions for превет: %v", s)
	}
	if c.Correct("матеркьют") {
		t.Fatal("made-up word is correct before adding")
	}
	if err := c.Add("матеркьют"); err != nil || !c.Correct("матеркьют") {
		t.Errorf("add: %v", err)
	}
	if data, _ := os.ReadFile(personal); string(data) != "матеркьют\n" {
		t.Errorf("personal file %q", data)
	}
	c2, err := Open(Langs, personal)
	if err != nil || !c2.Correct("матеркьют") {
		t.Errorf("personal word not loaded: %v", err)
	}
	if c2 != nil {
		c2.Close()
	}
}

// Двоеточие и слеш перед пробелом кончают слово, а внутри адреса или пути — нет.
func TestWordsBeforeColon(t *testing.T) {
	var got []string
	for _, w := range Words("слово: дальше http://x.y/z путь/к тест/ конец") {
		got = append(got, w.Text)
	}
	if want := []string{"слово", "дальше", "тест", "конец"}; !slices.Equal(got, want) {
		t.Errorf("words %q\nwant  %q", got, want)
	}
}
