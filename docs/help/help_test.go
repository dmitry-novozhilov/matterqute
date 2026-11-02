package help

import (
	"strings"
	"testing"
)

func TestArticles(t *testing.T) {
	as := Articles()
	if len(as) < 2 || as[0].Name != "links.md" || !strings.HasPrefix(as[0].Title, "Ссылки на сообщения") {
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
