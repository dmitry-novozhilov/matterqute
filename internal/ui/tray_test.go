package ui

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/config"
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

		// Что показать: сообщения мне важнее прочих новых.
		colors := trayColors(config.Tray{}, true)
		for _, c := range []struct {
			counts            Counts
			color, text, what string
		}{
			{Counts{}, colors.Idle, "", ""},
			{Counts{Unread: 5}, colors.Unread, "5", "5 новых сообщений"},
			{Counts{Unread: 1}, colors.Unread, "1", "1 новое сообщение"},
			{Counts{Mentions: 2, Unread: 12}, colors.Mentions, "2", "2 упоминания"},
		} {
			if color, text, what := trayLook(c.counts, colors); color != c.color || text != c.text || what != c.what {
				t.Errorf("trayLook(%+v) = %s %q %q, want %s %q %q", c.counts, color, text, what, c.color, c.text, c.what)
			}
		}
		// По умолчанию — текст панели, «хорошо» и «опасно» темы; заданный цвет
		// важнее, негодный — как не заданный.
		if got := trayColors(config.Tray{}, false); got != (config.Tray{Idle: "#211d18", Unread: "#428731", Mentions: "#c13c36"}) {
			t.Errorf("light panel defaults %+v", got)
		}
		if got := trayColors(config.Tray{Unread: "#00ff00", Mentions: "кумач"}, true); got.Unread != "#00ff00" || got.Mentions != "#ea6b60" {
			t.Errorf("configured colors %+v", got)
		}

		// «#» вырезан насквозь и по пикселям: внутри его квадрата нет
		// полупрозрачных точек, ни в одном размере.
		for _, size := range traySizes {
			img := trayPixmap(size, "#e9e6e1", "").ToImage()
			if img.Width() != size || img.DevicePixelRatio() != 1 {
				t.Errorf("size %d: pixmap %d px, dpr %v", size, img.Width(), img.DevicePixelRatio())
			}
			b := trayLayout(size)
			holes, blurred := 0, 0
			for y := b.y; y < b.y+b.side; y++ {
				for x := b.x; x < b.x+b.side; x++ {
					switch a := img.PixelColor(x, y).Alpha(); {
					case a == 0:
						holes++
					case a != 255:
						blurred++
					}
				}
			}
			if holes == 0 || blurred != 0 {
				t.Errorf("size %d: «#» has %d transparent and %d blurred pixels; want a sharp cut", size, holes, blurred)
			}
			if c := img.PixelColor(0, 0); c.Alpha() != 0 {
				t.Errorf("size %d: corner is not transparent", size)
			}
		}

		// 22 px: «#» крупный — половина значка; облачко во всю ширину.
		b := trayLayout(22)
		if b.side != 11 || b.stroke != 2 {
			t.Errorf("22 px «#»: side %d, stroke %d", b.side, b.stroke)
		}
		// Число вместо «#» — тоже клетками, без полупрозрачных краёв.
		for _, text := range []string{"3", "9+"} {
			img := trayPixmap(22, "#ea6b60", text).ToImage()
			if c := img.PixelColor(1, 11); c.Name() != "#ea6b60" || c.Alpha() != 255 {
				t.Errorf("bubble pixel %s alpha %d, want opaque #ea6b60", c.Name(), c.Alpha())
			}
			holes, blurred := 0, 0
			for y := b.y; y < b.y+b.side; y++ {
				for x := 3; x < 19; x++ {
					switch a := img.PixelColor(x, y).Alpha(); {
					case a == 0:
						holes++
					case a != 255:
						blurred++
					}
				}
			}
			if holes < 10 || blurred != 0 {
				t.Errorf("«%s»: %d transparent and %d blurred pixels; want a sharp cut", text, holes, blurred)
			}
		}

		if dir := os.Getenv("MATTERQUTE_TEST_SHOTS"); dir != "" {
			// Лист в 8× без сглаживания: видно каждый пиксель.
			const zoom = 8
			sheet := qt.NewQPixmap2(zoom*(16+22+32+4*4), zoom*6*36)
			sheet.FillWithFillColor(qt.NewQColor6("#808080"))
			p := qt.NewQPainter2(sheet.QPaintDevice)
			row := 0
			for _, dark := range []bool{true, false} {
				c := trayColors(config.Tray{}, dark)
				bg := map[bool]string{true: "#1c1b1a", false: "#f0efed"}[dark]
				for _, v := range []struct{ color, text string }{{c.Idle, ""}, {c.Unread, "9+"}, {c.Mentions, "7"}} {
					x := 4
					for _, size := range []int{16, 22, 32} {
						p.FillRect5(x*zoom, (row*36+2)*zoom, size*zoom, size*zoom, qt.NewQColor6(bg))
						p.DrawPixmap9(x*zoom, (row*36+2)*zoom, trayPixmap(size, v.color, v.text).Scaled3(size*zoom, size*zoom, qt.IgnoreAspectRatio, qt.FastTransformation))
						x += size + 4
					}
					row++
				}
			}
			p.End()
			sheet.Save(filepath.Join(dir, "tray-zoom.png"))
			for _, dark := range []bool{true, false} {
				c := trayColors(config.Tray{}, dark)
				name := map[bool]string{true: "dark", false: "light"}[dark]
				trayPreview(c, dark, 1).Save(filepath.Join(dir, "tray-"+name+".png"))
				trayPreview(c, dark, 2).Save(filepath.Join(dir, "tray-"+name+"@2x.png"))
				for _, v := range []struct{ color, text string }{{c.Idle, ""}, {c.Unread, "9+"}, {c.Mentions, "7"}} {
					for _, size := range []int{16, 22, 32} {
						trayPixmap(size, v.color, v.text).Save(filepath.Join(dir, fmt.Sprintf("tray-%s-%s-%d.png", name, strings.ReplaceAll(cmp.Or(v.text, "hash"), "+", "plus"), size)))
					}
				}
			}
		}
	})
}

func TestTrayDialog(t *testing.T) {
	onMain(func() {
		var got []config.Tray
		d := showTrayDialog(nil, config.Tray{Mentions: "#ff0000"}, func(c config.Tray) { got = append(got, c) })
		defer d.w.Close()
		if labelled(d.w.QObject, "#ff0000") == nil || labelled(d.w.QObject, trayColors(config.Tray{}, trayPanelDark()).Idle+" · по умолчанию") == nil {
			t.Error("dialog does not show the configured and default colors")
		}
		shotWidget(t, d.w.QWidget, "tray-dialog")
		labelled(d.w.QObject, "Сбросить").Click()
		if len(got) != 1 || got[0] != (config.Tray{}) {
			t.Errorf("reset reported %+v, want empty colors", got)
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
