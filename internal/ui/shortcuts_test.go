package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Окно «Сочетания клавиш» знает все сочетания, заведённые в окне сервера,
// в поле ввода и в меню, — список не отстаёт от приложения.
func TestShortcutsList(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		sh := NewShell()
		defer sh.w.Close()
		listed := map[string]bool{}
		for _, g := range shortcutGroups {
			for _, r := range g.rows {
				for _, k := range strings.Split(r.keys, " / ") {
					listed[k] = true
				}
			}
		}
		var acts []*qt.QAction
		acts = append(acts, m.w.Actions()...)
		a := m.composer.acts
		acts = append(acts, a.bold, a.italic, a.strike, a.code, a.link, a.bullet, a.ordered, a.quote, a.codeBlock)
		for _, o := range descendants(sh.w.QObject) { // все меню окна, с подменю
			if o.Inherits("QMenu") {
				acts = append(acts, qt.UnsafeNewQMenu(o.UnsafePointer()).Actions()...)
			}
		}
		for _, x := range acts {
			k := x.Shortcut().ToString()
			if k == "" || strings.HasPrefix(k, "Ctrl+") && len(k) == 6 && k[5] >= '1' && k[5] <= '9' {
				continue // Ctrl+1…9 — одной строкой
			}
			if !listed[k] {
				t.Errorf("shortcut %q (%s) is not in the list", k, x.Text())
			}
		}
		sh.showShortcuts()
		if sh.shortcuts == nil || sh.shortcuts.WindowTitle() != "Сочетания клавиш" {
			t.Error("no shortcuts window")
			return
		}
		shotWidget(t, sh.shortcuts, "shortcuts")
		sh.shortcuts.Close()
	})
}
