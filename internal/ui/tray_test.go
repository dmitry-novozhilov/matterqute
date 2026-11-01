package ui

import (
	"os"
	"path/filepath"
	"testing"

	qt "github.com/mappu/miqt/qt6"
)

func TestTrayPixmap(t *testing.T) {
	onMain(func() {
		for _, c := range []struct {
			n    int64
			want string
		}{{0, ""}, {1, "1"}, {9, "9"}, {10, "9+"}, {120, "9+"}} {
			if got := badgeText(c.n); got != c.want {
				t.Errorf("badgeText(%d) = %q, want %q", c.n, got, c.want)
			}
		}
		if got := pluralMentions(1) + ", " + pluralMentions(3) + ", " + pluralMentions(11) + ", " + pluralMentions(22); got != "1 упоминание, 3 упоминания, 11 упоминаний, 22 упоминания" {
			t.Errorf("plural: %s", got)
		}

		// 22 px на тёмной панели со счётчиком: справа сверху — красный счётчик,
		// облачко светлое, «#» вырезана насквозь, углы прозрачные.
		img := trayPixmap(22, true, "3").ToImage()
		px := func(x, y float64) *qt.QColor { return img.PixelColor(int(x*resourceDPR), int(y*resourceDPR)) }
		if c := px(11.5, 6); c.Name() != "#ea6b60" {
			t.Errorf("badge pixel %s, want #ea6b60", c.Name())
		}
		if c := px(0.5, 0.5); c.Alpha() != 0 {
			t.Errorf("corner is not transparent: %s alpha %d", c.Name(), c.Alpha())
		}
		if c := px(4, 11); c.Name() != "#e9e6e1" || c.Alpha() != 255 {
			t.Errorf("bubble pixel %s alpha %d, want opaque #e9e6e1", c.Name(), c.Alpha())
		}
		holes := 0
		for x := 7.0; x <= 14; x += 0.5 {
			if px(x, 10).Alpha() == 0 {
				holes++
			}
		}
		if holes == 0 {
			t.Error("no transparent «#» cut through the bubble")
		}
		if dir := os.Getenv("MATTERQUTE_TEST_SHOTS"); dir != "" {
			for _, v := range []struct {
				name  string
				dark  bool
				badge string
			}{{"tray-dark-2", true, "2"}, {"tray-light-2", false, "2"}, {"tray-dark", true, ""}, {"tray-dark-9plus", true, "9+"}} {
				trayPixmap(22, v.dark, v.badge).Save(filepath.Join(dir, v.name+".png"))
			}
		}
	})
}

func TestShellHideOnClose(t *testing.T) {
	onMain(func() {
		s := NewShell()
		defer s.w.Close()
		quits, saves := 0, 0
		s.quitApp = func() { quits++ }
		s.OnClose = func() { saves++ }
		s.HideOnClose = true
		s.Show()
		settle()
		s.w.Close()
		if s.w.IsVisible() || saves != 1 || quits != 0 {
			t.Errorf("close with tray: visible=%v saves=%d quits=%d; want hidden, saved, running", s.w.IsVisible(), saves, quits)
		}
		s.Raise()
		settle()
		if !s.w.IsVisible() {
			t.Error("Raise did not show the window hidden in the tray")
		}
		s.Quit()
		if quits != 1 || saves != 2 || s.w.IsVisible() {
			t.Errorf("Quit: quits=%d saves=%d visible=%v; want quit once, saved, closed", quits, saves, s.w.IsVisible())
		}
	})
}
