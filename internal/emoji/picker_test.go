package emoji

import (
	"slices"
	"strings"
	"testing"
)

func TestCategories(t *testing.T) {
	cats := Categories()
	if len(cats) != 9 || cats[0].Title != "Смайлы и эмоции" || cats[8].Title != "Флаги" {
		t.Fatalf("categories: %d, first %q", len(cats), cats[0].Title)
	}
	if got := strings.Join(cats[0].Names[:3], ","); got != "grinning,smiley,smile" {
		t.Errorf("first smileys in Unicode order: %s", got)
	}
	total := 0
	seen := map[string]bool{}
	for _, c := range cats {
		for _, n := range c.Names {
			if seen[n] {
				t.Errorf("%s twice", n)
			}
			seen[n] = true
			if _, ok := Char(n); !ok {
				t.Errorf("%s is not a system emoji", n)
			}
		}
		total += len(c.Names)
	}
	if total < 1500 {
		t.Errorf("only %d emoji in the picker", total)
	}
	if !seen["+1"] || seen["thumbsup"] {
		t.Error("👍 should be listed once, as +1")
	}
}

func TestSearch(t *testing.T) {
	got := Search("thumbs", nil, 5)
	if len(got) == 0 || got[0] != "+1" {
		t.Errorf("Search(thumbs) = %v, want +1 first (one name per symbol)", got)
	}
	if got := Search(":tad", []string{"tadpole_dance"}, 10); !slices.Contains(got, "tada") || !slices.Contains(got, "tadpole_dance") {
		t.Errorf("Search(:tad) = %v", got)
	}
	if Search("  ", nil, 5) != nil {
		t.Error("empty query matched something")
	}
}

func TestRecent(t *testing.T) {
	var r Recent
	r = r.Add("+1").Add("tada").Add("+1").Add("eyes")
	if got := strings.Join(r.Names(), ","); got != "+1,eyes,tada" {
		t.Errorf("recent order %s, want most used first", got)
	}
	for i := range 40 {
		r = r.Add(string(rune('a'+i%26)) + "x")
	}
	if len(r) != MaxRecent {
		t.Errorf("%d recent, want %d", len(r), MaxRecent)
	}
}

func TestCharZWJ(t *testing.T) {
	// Без fe0f внутри составного эмодзи Qt собирает его в один символ.
	if got, ok := Char("rainbow-flag"); !ok || got != "\U0001F3F3\u200D\U0001F308" {
		t.Errorf("Char(rainbow-flag) = %q, %v", got, ok)
	}
	if got, _ := Char("heart"); got != "\u2764\uFE0F" {
		t.Errorf("Char(heart) = %q: single emoji keeps fe0f", got)
	}
}
