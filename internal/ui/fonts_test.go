package ui

import (
	"io"
	"log/slog"
	"slices"
	"strings"
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

// Масштаб: «Вид → Масштаб» (Ctrl+= и Ctrl++, Ctrl+−, Ctrl+0) — шагами поверх
// выбранных шрифтов, выбранные не меняются; у края шагов — ничего. Шаг
// табуляции в коде ленты — 4 знака моноширинного и растёт с масштабом.
func TestZoom(t *testing.T) {
	onMain(func() {
		defer SetZoom(100)
		defer SetFonts(config.Fonts{})
		SetFonts(config.Fonts{Feed: config.Font{Size: 16}})
		var got []int
		OnZoomChosen = func(p int) { got = append(got, p) }
		defer func() { OnZoomChosen = nil }()
		s := NewShell()
		defer s.w.Close()
		acts := map[string]*qt.QAction{}
		var menu *qt.QMenu
		for _, o := range descendants(s.w.QObject) {
			if !o.Inherits("QMenu") {
				continue
			}
			m := qt.UnsafeNewQMenu(o.UnsafePointer())
			for _, a := range m.Actions() {
				acts[a.Text()] = a
				if a.Text() == "Крупнее" {
					menu = m
				}
			}
		}
		in, out, reset := acts["Крупнее"], acts["Мельче"], acts["Обычный размер"]
		if in == nil || out == nil || reset == nil || menu == nil {
			t.Errorf("zoom actions: %v %v %v", in != nil, out != nil, reset != nil)
			return
		}
		var keys []string
		for _, k := range in.Shortcuts() {
			keys = append(keys, k.ToString())
		}
		if strings.Join(keys, " ") != "Ctrl+= Ctrl++" || out.Shortcut().ToString() != "Ctrl+-" || reset.Shortcut().ToString() != "Ctrl+0" {
			t.Errorf("keys: in %v, out %q, reset %q", keys, out.Shortcut().ToString(), reset.Shortcut().ToString())
		}
		f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
		defer f.dispose()
		mono := qt.NewQFontMetricsF(font(style.fonts.Mono, style.fonts.Mono.Size, qt.QFont__Normal)).HorizontalAdvance(" ")
		if d := f.view.TabStopDistance(); d != 4*mono {
			t.Errorf("tab stop %.1f px, want 4 monospace spaces (%.1f)", d, 4*mono)
		}
		in.Trigger()
		if d := f.view.TabStopDistance(); d <= 4*mono {
			t.Errorf("tab stop %.1f px after zoom in, was %.1f", d, 4*mono)
		}
		if style.zoom != 110 || style.fonts.Feed.Size != 18 || style.fonts.Sidebar.Size != 15 || style.chosen.Feed.Size != 16 || feedFont().PixelSize() != 18 {
			t.Errorf("110%%: zoom %d, feed %d, sidebar %d, chosen feed %d", style.zoom, style.fonts.Feed.Size, style.fonts.Sidebar.Size, style.chosen.Feed.Size)
		}
		if menu.Title() != "Масштаб: 110%" {
			t.Errorf("menu title %q", menu.Title())
		}
		out.Trigger()
		out.Trigger()
		reset.Trigger()
		SetZoom(200)
		in.Trigger() // у края — ничего
		if style.zoom != 200 || style.fonts.Mono.Size != 26 {
			t.Errorf("200%%: zoom %d, mono %d", style.zoom, style.fonts.Mono.Size)
		}
		if want := []int{110, 100, 90, 100}; !slices.Equal(got, want) {
			t.Errorf("chosen %v, want %v", got, want)
		}
		SetZoom(33)
		if style.zoom != 100 {
			t.Errorf("unknown zoom 33 → %d, want 100", style.zoom)
		}
	})
}
