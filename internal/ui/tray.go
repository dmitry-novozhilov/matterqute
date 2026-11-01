package ui

import (
	"fmt"
	"os"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/qtx"
	"matterqute/internal/theme"
)

// Tray — значок в системном трее: монохромный знак в цвет панели и счётчик
// упоминаний поверх (внутри картинки, по дизайну), меню «Открыть», «Выход».
type Tray struct {
	icon     *qt.QSystemTrayIcon
	count    *qt.QAction // «2 упоминания», только когда они есть
	mentions int64

	OnOpen func()
	OnQuit func()
}

// NewTray — значок в трее; nil, если трея нет (GNOME без расширения
// AppIndicator, offscreen).
func NewTray() *Tray {
	if !qt.QSystemTrayIcon_IsSystemTrayAvailable() {
		return nil
	}
	t := &Tray{icon: qt.NewQSystemTrayIcon()}
	menu := qt.NewQMenu2()
	open := menu.QWidget.AddActionWithText("Открыть matterqute")
	open.OnTriggered(func() { t.open() })
	t.count = menu.QWidget.AddActionWithText("")
	t.count.SetEnabled(false)
	menu.AddSeparator()
	quit := menu.QWidget.AddActionWithText("Выход")
	quit.OnTriggered(func() {
		if t.OnQuit != nil {
			t.OnQuit()
		}
	})
	t.icon.SetContextMenu(menu)
	t.icon.OnActivated(func(reason qt.QSystemTrayIcon__ActivationReason) {
		if reason == qt.QSystemTrayIcon__Trigger {
			t.open()
		}
	})
	t.SetMentions(0)
	qtx.WatchSystemScheme(func(bool) { t.SetMentions(t.mentions) })
	t.icon.Show()
	return t
}

func (t *Tray) open() {
	if t.OnOpen != nil {
		t.OnOpen()
	}
}

// SetMentions — сколько упоминаний и непрочитанных личных сообщений ждёт.
func (t *Tray) SetMentions(n int64) {
	t.mentions = n
	t.icon.SetIcon(trayIcon(trayPanelDark(), badgeText(n)))
	t.count.SetVisible(n > 0)
	tip := "matterqute"
	if n > 0 {
		t.count.SetText(pluralMentions(n))
		tip += " — " + pluralMentions(n)
	}
	t.icon.SetToolTip(tip)
}

// trayPanelDark — панель тёмная: у GNOME верхняя панель тёмная всегда, у
// остальных — как системная тема.
func trayPanelDark() bool {
	dark, _ := qtx.SystemDark()
	return dark || strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), "GNOME")
}

// badgeText — «3», больше 9 — «9+», без упоминаний — пусто.
func badgeText(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n > 9:
		return "9+"
	}
	return fmt.Sprint(n)
}

func pluralMentions(n int64) string {
	return plural(n, "упоминание", "упоминания", "упоминаний")
}

// plural — «1 ответ», «2 ответа», «5 ответов».
func plural(n int64, one, few, many string) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return fmt.Sprintf("%d %s", n, one)
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return fmt.Sprintf("%d %s", n, few)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func trayIcon(dark bool, badge string) *qt.QIcon {
	ic := qt.NewQIcon()
	for _, size := range []int{16, 22, 24, 32, 48} {
		ic.AddPixmap(trayPixmap(size, dark, badge))
	}
	return ic
}

// trayPixmap — знак «облачко с #» без плитки (AppIcon, режим mono): облачко
// цвета панельного текста, «#» вырезана насквозь; счётчик — в правом верхнем углу.
func trayPixmap(size int, dark bool, badge string) *qt.QPixmap {
	ink, badgeBg, badgeFg := "#211d18", "#c13c36", "#fcfcfc"
	if dark {
		ink, badgeBg, badgeFg = "#e9e6e1", "#ea6b60", "#1b1916"
	}
	px := size * resourceDPR
	img := qt.NewQImage3(px, px, qt.QImage__Format_ARGB32_Premultiplied)
	img.SetDevicePixelRatio(resourceDPR)
	img.Fill2(qt.Transparent)
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	p.SetRenderHint(qt.QPainter__TextAntialiasing)
	// Макет рисует знак на поле 192 и в трее показывает середину 140×140.
	k := float64(size) / 140
	at := func(v float64) float64 { return (v - 26) * k }
	drawBubble(p, at(34), at(38), 124*k, 108*k, 54*k, 12*k, ink)
	p.SetCompositionMode(qt.QPainter__CompositionMode_Clear)
	drawHash(p, at(34), at(38), 124*k, 108*k, 72*k, "#000000") // цвет не важен: режим Clear
	p.SetCompositionMode(qt.QPainter__CompositionMode_SourceOver)

	if badge != "" {
		bs, bf := 12.0, 9
		if size > 24 {
			bs, bf = 18, 11
		}
		f := pixelFont(bf, qt.QFont__DemiBold)
		bw := max(bs, float64(qt.NewQFontMetrics(f).HorizontalAdvance(badge))+6)
		r := qt.NewQRectF4(float64(size)-bw, 0, bw, bs)
		p.SetPenWithStyle(qt.NoPen)
		p.SetBrush(qt.NewQBrush3(qt.NewQColor6(badgeBg)))
		p.DrawRoundedRect(r, bs/2, bs/2)
		p.SetFont(f)
		p.SetPen(qt.NewQColor6(badgeFg))
		p.DrawText5(r, int(qt.AlignCenter), badge)
	}
	p.End()
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	return pm
}

// drawBubble — облачко: прямоугольник со скруглением r, у левого нижнего угла
// маленькое скругление tail — это хвостик.
func drawBubble(p *qt.QPainter, x, y, w, h, r, tail float64, color string) {
	path := qt.NewQPainterPath()
	path.MoveTo2(x+r, y)
	path.LineTo2(x+w-r, y)
	path.ArcTo2(x+w-2*r, y, 2*r, 2*r, 90, -90)
	path.LineTo2(x+w, y+h-r)
	path.ArcTo2(x+w-2*r, y+h-2*r, 2*r, 2*r, 0, -90)
	path.LineTo2(x+tail, y+h)
	path.ArcTo2(x, y+h-2*tail, 2*tail, 2*tail, 270, -90)
	path.LineTo2(x, y+r)
	path.ArcTo2(x, y, 2*r, 2*r, 180, -90)
	path.CloseSubpath()
	p.SetPenWithStyle(qt.NoPen)
	p.SetBrush(qt.NewQBrush3(qt.NewQColor6(color)))
	p.DrawPath(path)
}

// drawHash — «#» шрифтом Plex 600 по центру прямоугольника.
func drawHash(p *qt.QPainter, x, y, w, h, px float64, color string) {
	f := qt.NewQFont()
	f.SetFamilies([]string{theme.SansFamily})
	f.SetPixelSize(max(1, int(px)))
	f.SetWeight(qt.QFont__DemiBold)
	p.SetFont(f)
	p.SetPen(qt.NewQColor6(color))
	p.DrawText5(qt.NewQRectF4(x, y, w, h), int(qt.AlignCenter), "#")
}
