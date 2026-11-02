package help

import (
	"slices"
	"strings"
	"testing"
)

func TestArticles(t *testing.T) {
	as := Articles()
	i := slices.IndexFunc(as, func(a Article) bool { return a.Name == "links.md" })
	if len(as) < 2 || i < 0 || !strings.HasPrefix(as[i].Title, "Ссылки на сообщения") ||
		!slices.IsSortedFunc(as, func(a, b Article) int { return strings.Compare(a.Name, b.Name) }) {
		t.Fatalf("articles %+v", as)
	}
	for _, a := range as {
		if a.Title == "" || Text(a.Name) == "" {
			t.Errorf("article %s has no title or text", a.Name)
		}
	}
	if Text("README.md") == "" || Text("nope.md") != "" {
		t.Error("Text: README missing or unknown article found")
	}
}
