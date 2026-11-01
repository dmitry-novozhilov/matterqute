package theme

import (
	"strings"
	"testing"
)

func TestPalettes(t *testing.T) {
	if Light.Acc != "#007255" || Dark.Acc != "#5ec4a1" || !Dark.Dark || Light.Dark {
		t.Errorf("accents: light %s dark %s", Light.Acc, Dark.Acc)
	}
	if len(Light.Avatars) != 5 || len(Dark.Avatars) != 5 {
		t.Errorf("avatar palettes: %d light, %d dark", len(Light.Avatars), len(Dark.Avatars))
	}
	if !strings.Contains(Light.QSS(), "#f6f4f1") || !strings.Contains(Dark.QSS(), "#1b1916") {
		t.Error("qss does not match its theme")
	}
	bg1, _ := Light.Avatar("carol", false)
	bg2, _ := Light.Avatar("carol", false)
	bot, _ := Light.Avatar("carol", true)
	if bg1 != bg2 || bot != Light.Avatars[4][0] {
		t.Errorf("avatar colors: %s %s bot %s", bg1, bg2, bot)
	}
	for _, name := range []string{"alice", "bob", "carol", "dave", "eve", "grafana"} {
		if bg, _ := Light.Avatar(name, false); bg == Light.Avatars[4][0] {
			t.Errorf("person %s got the bot color", name)
		}
	}
}

func TestIconsAndFonts(t *testing.T) {
	svg, err := Icon("hash", "#123456")
	if err != nil || !strings.Contains(string(svg), `stroke="#123456"`) || strings.Contains(string(svg), "currentColor") {
		t.Errorf("Icon(hash): %v\n%s", err, svg)
	}
	if _, err := Icon("../tokens", "#000"); err == nil {
		t.Error("Icon accepted a path outside icons/")
	}
	names := map[string]bool{}
	for _, f := range Fonts() {
		names[f.Name] = len(f.Data) > 1000
	}
	for _, want := range []string{"IBMPlexSans-Regular.ttf", "IBMPlexSans-SemiBold.ttf", "PTMono-Regular.ttf", "NotoColorEmoji.ttf"} {
		if !names[want] {
			t.Errorf("font %s missing", want)
		}
	}
}
