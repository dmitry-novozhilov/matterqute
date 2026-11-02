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
		// По умолчанию — текст панели, зелёный значка приложения и тёмный
		// красный; заданный цвет важнее, негодный — как не заданный.
		if got := trayColors(config.Tray{}, false); got != (config.Tray{Idle: "#211d18", Unread: appGreen, Mentions: "#a7332f"}) {
			t.Errorf("light panel defaults %+v", got)
		}
		if got := trayColors(config.Tray{Unread: "#00ff00", Mentions: "кумач"}, true); got.Unread != "#00ff00" || got.Mentions != "#e54d43" {
			t.Errorf("configured colors %+v", got)
		}

		// «#» вырезан насквозь и по пикселям: внутри его квадрата нет
		// полупрозрачных точек, ни в одном размере.
		for _, size := range traySizes {
			img := trayPixmap(size, "#e9e6e1", "", 0).ToImage()
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
		// Число вместо «#» вырезано шрифтом и помещается в облачко.
		for _, text := range []string{"1", "3", "9+"} {
			img := trayPixmap(22, "#ea6b60", text, 0).ToImage()
			if c := img.PixelColor(1, 11); c.Name() != "#ea6b60" || c.Alpha() != 255 {
				t.Errorf("bubble pixel %s alpha %d, want opaque #ea6b60", c.Name(), c.Alpha())
			}
			holes := 0
			for y := b.y; y < b.y+b.side; y++ {
				for x := 3; x < 19; x++ {
					if img.PixelColor(x, y).Alpha() < 64 {
						holes++
					}
				}
			}
			if holes < 8 {
				t.Errorf("«%s» is not cut through the bubble: %d transparent pixels", text, holes)
			}
		}
		// Размер цифр из настройки: крупнее — больше вырезано; «9+» всё равно
		// помещается в облачко.
		cut := func(text string, digits int) (holes int, fits bool) {
			img := trayPixmap(22, "#ea6b60", text, digits).ToImage()
			fits = true
			for y := 0; y < 22; y++ {
				for x := 0; x < 22; x++ {
					a := img.PixelColor(x, y).Alpha()
					if y >= b.y && y < b.y+b.side && x >= 3 && x < 19 && a < 64 {
						holes++
					}
					mid := b.top + b.h/2
					if (x == 2 || x == 19) && y >= mid-3 && y <= mid+3 && a < 200 {
						fits = false // дырка у края облачка — число вылезло за поля
					}
				}
			}
			return holes, fits
		}
		small, _ := cut("5", 40)
		big, _ := cut("5", 90)
		if big <= small {
			t.Errorf("digits 90%% cut %d px, 40%% — %d: size setting has no effect", big, small)
		}
		if _, fits := cut("9+", 100); !fits {
			t.Error("«9+» at 100% does not fit the bubble")
		}
		if got := trayColors(config.Tray{Digits: 70}, true).Digits; got != 70 {
			t.Errorf("trayColors dropped digits: %d", got)
		}

		// Иконка приложения — то же облачко, зелёное с белой «#», без плитки.
		app := appIconPixmap(32).ToImage()
		if c := app.PixelColor(2, 16); c.Name() != appGreen || c.Alpha() != 255 {
			t.Errorf("app icon bubble %s alpha %d, want %s", c.Name(), c.Alpha(), appGreen)
		}
		ab := trayLayout(32)
		if c := app.PixelColor(ab.x+ab.overhang, ab.y); c.Name() != "#fcfbf9" {
			t.Errorf("app icon «#» pixel %s, want white", c.Name())
		}
		if c := app.PixelColor(0, 0); c.Alpha() != 0 {
			t.Error("app icon has a tile: corner is not transparent")
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
				for _, v := range []struct{ color, text string }{{c.Idle, ""}, {c.Unread, "1"}, {c.Mentions, "9+"}} {
					x := 4
					for _, size := range []int{16, 22, 32} {
						p.FillRect5(x*zoom, (row*36+2)*zoom, size*zoom, size*zoom, qt.NewQColor6(bg))
						p.DrawPixmap9(x*zoom, (row*36+2)*zoom, trayPixmap(size, v.color, v.text, 0).Scaled3(size*zoom, size*zoom, qt.IgnoreAspectRatio, qt.FastTransformation))
						x += size + 4
					}
					row++
				}
			}
			p.End()
			sheet.Save(filepath.Join(dir, "tray-zoom.png"))
			appIconPixmap(64).Save(filepath.Join(dir, "app-icon-64.png"))
			appIconPixmap(22).Scaled3(22*zoom, 22*zoom, qt.IgnoreAspectRatio, qt.FastTransformation).Save(filepath.Join(dir, "app-icon-22-zoom.png"))
			for _, dark := range []bool{true, false} {
				c := trayColors(config.Tray{}, dark)
				name := map[bool]string{true: "dark", false: "light"}[dark]
				trayPreview(c, dark, 1).Save(filepath.Join(dir, "tray-"+name+".png"))
				trayPreview(c, dark, 2).Save(filepath.Join(dir, "tray-"+name+"@2x.png"))
				for _, v := range []struct{ color, text string }{{c.Idle, ""}, {c.Unread, "9+"}, {c.Mentions, "7"}} {
					for _, size := range []int{16, 22, 32} {
						trayPixmap(size, v.color, v.text, 0).Save(filepath.Join(dir, fmt.Sprintf("tray-%s-%s-%d.png", name, strings.ReplaceAll(cmp.Or(v.text, "hash"), "+", "plus"), size)))
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
		var spin *qt.QSpinBox
		for _, o := range descendants(d.w.QObject) {
			if o.Inherits("QSpinBox") {
				spin = qt.UnsafeNewQSpinBox(o.UnsafePointer())
			}
		}
		if spin == nil || spin.Text() != "авто" {
			t.Error("no digit size spin box showing «авто»")
			return
		}
		spin.SetValue(80)
		if len(got) != 1 || got[0].Digits != 80 || got[0].Mentions != "#ff0000" {
			t.Errorf("digit size reported %+v", got)
		}
		labelled(d.w.QObject, "Сбросить").Click()
		if n := len(got); n < 2 || got[n-1] != (config.Tray{}) || spin.Text() != "авто" {
			t.Errorf("reset reported %+v, spin %q; want empty settings and «авто»", got, spin.Text())
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
