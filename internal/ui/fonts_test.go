package ui

import (
	"io"
	"log/slog"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/config"
)

func TestScaleFonts(t *testing.T) {
	onMain(func() {
		defer SetFonts(config.Fonts{})
		in := `<span style="font-family:'PT Mono';font-size:12px">10:30</span><p style="font-size:13px">x</p>`
		if got := scaleFonts(in); got != in {
			t.Errorf("default fonts changed the HTML: %s", got)
		}
		SetFonts(config.Fonts{Feed: config.Font{Size: 21}, Mono: config.Font{Family: "IBM Plex Sans", Size: 16}})
		want := `<span style="font-family:'IBM Plex Sans';font-size:15px">10:30</span><p style="font-size:20px">x</p>`
		if got := scaleFonts(in); got != want {
			t.Errorf("scaleFonts:\n got %s\nwant %s", got, want)
		}
		SetFonts(config.Fonts{Feed: config.Font{Size: 100}})
		if style.fonts.Feed.Size != maxFontSize || style.fonts.Sidebar != defaultFonts.Sidebar {
			t.Errorf("fonts not clamped or defaults lost: %+v", style.fonts)
		}
	})
}

// Шрифты меняются сразу: лента, поле ввода, строки сайдбара; окно настроек
// сохраняет выбор и сбрасывает его.
func TestFonts(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		defer SetFonts(config.Fonts{})
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		rowHeight := func() int { return m.sidebar.VisualItemRect(m.items[m.current]).Height() }
		before := rowHeight()
		var saved []config.Fonts
		OnFontsChosen = func(f config.Fonts) { saved = append(saved, f) }
		defer func() { OnFontsChosen = nil }()

		showFontsDialog(m.w)
		dlg := qt.QApplication_ActiveModalWidget()
		if dlg == nil {
			t.Fatal("no fonts dialog")
		}
		var spins []*qt.QSpinBox
		for _, o := range descendants(dlg.QObject) {
			if o.Inherits("QSpinBox") {
				spins = append(spins, qt.UnsafeNewQSpinBox(o.UnsafePointer()))
			}
		}
		if len(spins) != 3 || spins[0].Value() != 14 || spins[1].Value() != 14 || spins[2].Value() != 13 {
			t.Fatalf("size boxes %d: want sidebar 14, feed 14, mono 13", len(spins))
		}
		spins[0].SetValue(22)
		spins[1].SetValue(18)
		settle()
		shotWidget(t, dlg, "fonts-dialog")
		shot(t, m, "fonts-big")
		if got := m.feed.doc.DefaultFont().PixelSize(); got != 18 {
			t.Errorf("feed font %d px, want 18", got)
		}
		if got, ph := m.composer.edit.Font().PixelSize(), m.composer.edit.Viewport().Font().PixelSize(); got != 18 || ph != 18 {
			t.Errorf("composer font %d px, placeholder %d px, want 18", got, ph)
		}
		if h := rowHeight(); h <= before {
			t.Errorf("sidebar row %d px with a 22 px font, was %d", h, before)
		}
		if len(saved) == 0 || saved[len(saved)-1].Sidebar.Size != 22 || saved[len(saved)-1].Feed.Size != 18 {
			t.Errorf("saved fonts %+v", saved)
		}
		labelled(dlg.QObject, "Сбросить").Click()
		if last := saved[len(saved)-1]; last != (config.Fonts{}) || m.feed.doc.DefaultFont().PixelSize() != 14 {
			t.Errorf("reset: saved %+v, feed %d px", last, m.feed.doc.DefaultFont().PixelSize())
		}
		dlg.Close()
	})
}
