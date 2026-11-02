package ui

import (
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/docs/help"
)

// Меню «Справка»: статьи открываются в окне приложения, ссылки между ними —
// там же; «О программе» — версия сборки.
func TestHelpMenu(t *testing.T) {
	onMain(func() {
		s := NewShell()
		defer s.w.Close()
		var menu []string
		for _, o := range descendants(s.w.QObject) {
			if o.Inherits("QMenu") {
				if m := qt.UnsafeNewQMenu(o.UnsafePointer()); m.Title() == "Сп&равка" {
					for _, a := range m.Actions() {
						if !a.IsSeparator() {
							menu = append(menu, a.Text())
						}
					}
				}
			}
		}
		// Сочетания клавиш, статьи, «О программе».
		if want := help.Articles()[0].Title + "…"; len(menu) != len(help.Articles())+2 || menu[0] != "Сочетания клавиш" || menu[1] != want || menu[len(menu)-1] != "О программе…" {
			t.Errorf("help menu %q", menu)
		}

		s.showHelp("links.md")
		h := s.help
		if h == nil || !h.w.IsVisible() || !strings.HasPrefix(h.w.WindowTitle(), "Ссылки на сообщения") {
			t.Error("help window not shown")
			return
		}
		text := h.view.ToPlainText()
		for _, want := range []string{"Как настроить открытие из браузера", "xdg-open 'mattermost://", "__landingPageSeen__"} {
			if !strings.Contains(text, want) {
				t.Errorf("article lacks %q", want)
			}
		}
		if strings.Contains(text, "```") || strings.Contains(text, "## ") {
			t.Error("article shows raw markdown")
		}
		shotWidget(t, h.w, "help")
		h.follow("README.md")
		if h.name != "README.md" || !strings.Contains(h.view.ToPlainText(), "Справка Matterqute") {
			t.Errorf("link to README: showing %s", h.name)
		}
		s.showHelp("links.md")
		if s.help != h {
			t.Error("second showHelp opened another window")
		}

		s.showAbout()
		if s.about == nil || !strings.Contains(aboutHTML(), "Сборка: ") || !strings.Contains(aboutHTML(), "Qt 6.") {
			t.Errorf("about: %q", aboutHTML())
		}
		shotWidget(t, s.about.w.QWidget, "about")
		s.about.w.Close()
	})
}
