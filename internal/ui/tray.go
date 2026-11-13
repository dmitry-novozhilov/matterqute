package ui

import (
	"fmt"
	"math"
	"os"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/config"
	"matterqute/internal/qtx"
	"matterqute/internal/theme"
)

// Tray — значок в системном трее: облачко с «#» в цвет панели; когда есть
// новое, вместо «#» — число: облачко зелёное, если новые сообщения только в
// каналах, и красное, если есть сообщения мне (упоминания, лички). Цвета —
// «Вид → Значок в трее…». Меню: «Открыть», «Выход».
type Tray struct {
	icon   *qt.QSystemTrayIcon
	count  *qt.QAction // «2 упоминания», «5 новых сообщений» — только когда есть
	counts Counts
	colors config.Tray

	OnOpen func()
	OnQuit func()
}

// Counts — что ждёт на сервере: сообщения мне (упоминания и непрочитанные
// лички) и вообще новые сообщения в незаглушённых каналах.
type Counts struct{ Mentions, Unread int64 }

func (c Counts) add(o Counts) Counts {
	return Counts{Mentions: c.Mentions + o.Mentions, Unread: c.Unread + o.Unread}
}

// NewTray — значок в трее; nil, если трея нет (GNOME без расширения
// AppIndicator, offscreen).
func NewTray(colors config.Tray) *Tray {
	if !qt.QSystemTrayIcon_IsSystemTrayAvailable() {
		return nil
	}
	t := &Tray{icon: qt.NewQSystemTrayIcon(), colors: colors}
	menu := qt.NewQMenu2()
	open := menu.QWidget.AddActionWithText("Открыть " + AppTitle)
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
	t.refresh()
	qtx.WatchSystemScheme(func(bool) { t.refresh() })
	t.icon.Show()
	return t
}

func (t *Tray) open() {
	if t.OnOpen != nil {
		t.OnOpen()
	}
}

// SetCounts — сколько ждёт сообщений мне и новых вообще (сумма по серверам).
func (t *Tray) SetCounts(c Counts) {
	t.counts = c
	t.refresh()
}

// Configure — цвета и размер цифр из «Вид → Значок в трее…»; пустое — само.
func (t *Tray) Configure(c config.Tray) {
	t.colors = c
	t.refresh()
}

func (t *Tray) refresh() {
	color, text, what := trayLook(t.counts, trayColors(t.colors, trayPanelDark()))
	t.icon.SetIcon(trayIcon(color, text, t.colors.Digits))
	t.count.SetVisible(what != "")
	tip := AppTitle
	if what != "" {
		t.count.SetText(what)
		tip += " — " + what
	}
	t.icon.SetToolTip(tip)
}

// trayLook — цвет облачка, число в нём (пусто — «#») и подпись для меню и
// подсказки: сообщения мне важнее прочих новых.
func trayLook(c Counts, colors config.Tray) (color, text, what string) {
	switch {
	case c.Mentions > 0:
		return colors.Mentions, badgeText(c.Mentions), pluralMentions(c.Mentions)
	case c.Unread > 0:
		return colors.Unread, badgeText(c.Unread), plural(c.Unread, "новое сообщение", "новых сообщения", "новых сообщений")
	}
	return colors.Idle, "", ""
}

// trayColors — цвета значка: заданные (если это цвет) или по умолчанию: нового
// нет — цвет текста панели, новые — зелёный значка приложения, сообщения мне —
// «опасно» темы, но чуть темнее (у тёмной панели — #ea6b60 → #e54d43).
func trayColors(c config.Tray, dark bool) config.Tray {
	pal, red := theme.Light, "#a7332f"
	if dark {
		pal, red = theme.Dark, "#e54d43"
	}
	pick := func(set, def string) string {
		if set != "" && qcolor(set).IsValid() {
			return set
		}
		return def
	}
	return config.Tray{
		Idle:     pick(c.Idle, pal.Tx),
		Unread:   pick(c.Unread, appGreen),
		Mentions: pick(c.Mentions, red),
		Digits:   c.Digits,
	}
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

// trayDigitsMin — меньший размер цифр в настройке, % высоты значка.
const trayDigitsMin = 30

// traySizes — размеры значка: трей берёт ближайший и масштабирует, поэтому
// каждый нарисован отдельно и по пикселям, без DPR (SNI шлёт их как есть).
var traySizes = []int{16, 20, 22, 24, 32, 40, 44, 48, 64}

// trayIcon — значок трея всех размеров (временный, см. tmp: для SetIcon).
// Рисуется заново при каждой смене счётчика.
func trayIcon(color, text string, digits int) *qt.QIcon {
	ic := tmp(qt.NewQIcon())
	for _, size := range traySizes {
		ic.AddPixmap(trayPixmap(size, color, text, digits))
	}
	return ic
}

// trayBox — где в значке size×size облачко и знак внутри него; всё в целых
// пикселях, чтобы прямые края «#» не размывались.
type trayBox struct {
	top, h           int // облачко: во всю ширину, по высоте — top…top+h
	x, y, side       int // квадрат «#» (и высота числа)
	stroke, overhang int
}

func trayLayout(size int) trayBox {
	b := trayBox{h: int(math.Round(float64(size) * 0.82)), side: int(math.Round(float64(size) * 0.5))}
	b.top = (size - b.h) / 2
	b.x = (size - b.side) / 2
	b.y = b.top + (b.h-b.side)/2
	b.stroke = max(2, int(math.Round(float64(size)*0.085)))
	b.overhang = int(math.Round(float64(b.side) * 0.18))
	return b
}

// trayPixmap — знак «облачко с #» без плитки (AppIcon, режим mono) размером
// size физических пикселей: облачко цвета color, «#» (или text — число
// шрифтом digits % высоты значка, 0 — сам) вырезан насквозь, сквозь него
// видно панель.
func trayPixmap(size int, color, text string, digits int) *qt.QPixmap {
	return bubblePixmap(size, color, "", text, digits)
}

// bubblePixmap — облачко цвета bubble во всю ширину и в нём «#» (или text —
// число) цвета glyph; glyph пустой — знак вырезан насквозь. size — в
// физических пикселях: «#» — прямоугольниками по сетке, без сглаживания и
// масштабирования. Картинка временная (tmp).
func bubblePixmap(size int, bubble, glyph, text string, digits int) *qt.QPixmap {
	img := qt.NewQImage3(size, size, qt.QImage__Format_ARGB32_Premultiplied)
	defer free(img)
	img.Fill2(qt.Transparent)
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	b := trayLayout(size)
	h := float64(b.h)
	drawBubble(p, 0, float64(b.top), float64(size), h, h/2, max(1, math.Round(h*0.11)), bubble)
	ink := qcolor(glyph)
	if glyph == "" {
		p.SetCompositionMode(qt.QPainter__CompositionMode_Clear)
		ink = tmp(qt.NewQColor2(qt.Black)) // цвет не важен: режим Clear
	}
	if text == "" {
		// «#» прямоугольниками по сетке: две стойки и две перекладины.
		gap := b.side - 2*b.overhang - 2*b.stroke
		for _, off := range []int{b.overhang, b.overhang + b.stroke + gap} {
			p.FillRect5(b.x+off, b.y, b.stroke, b.side, ink)
			p.FillRect5(b.x, b.y+off, b.side, b.stroke, ink)
		}
	} else {
		cutText(p, size, b, text, ink, digits)
	}
	free(p)
	return tmp(qt.QPixmap_FromImage(img))
}

// cutText — число по центру облачка: шрифт Plex жирный размером digits %
// высоты значка, а без настройки — высотой со знак «#»; в любом случае не
// шире облачка без скруглённых краёв и не выше облачка. Цвета ink в текущем
// режиме (Clear — вырезает). Хинтинг — по умолчанию: полный ломал форму цифр
// (у «7» вылезала засечка), а клетками «1» походила на «i».
func cutText(p *qt.QPainter, size int, b trayBox, text string, ink *qt.QColor, digits int) {
	f := tmp(qt.NewQFont())
	f.SetFamilies([]string{theme.SansFamily})
	f.SetWeight(qt.QFont__Bold)
	maxW := size - 2*max(2, size/7)
	start, maxH := b.side*3/2+1, b.side
	if digits > 0 {
		start, maxH = max(6, int(math.Round(float64(size*digits)/100))), b.h
	}
	var r *qt.QRect
	for px := start; px >= 6; px-- {
		f.SetPixelSize(px)
		r = tmp(qt.NewQFontMetrics(f)).TightBoundingRect(text)
		if r.Width() <= maxW && r.Height() <= maxH {
			break
		}
	}
	p.SetRenderHint(qt.QPainter__TextAntialiasing)
	p.SetFont(f)
	p.SetPen(ink)
	p.DrawText3((size-r.Width())/2-r.X(), b.top+(b.h-r.Height()+1)/2-r.Y(), text)
}

// drawBubble — облачко: прямоугольник со скруглением r, у левого нижнего угла
// маленькое скругление tail — это хвостик.
func drawBubble(p *qt.QPainter, x, y, w, h, r, tail float64, color string) {
	path := tmp(qt.NewQPainterPath())
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
	p.SetBrush(qbrush(color))
	p.DrawPath(path)
}

// showTrayDialog — «Вид → Значок в трее…»: цвет облачка для каждого
// состояния и размер цифр; меняется сразу (onChange), пустое — по умолчанию.
func showTrayDialog(parent *qt.QWidget, colors config.Tray, onChange func(config.Tray)) *dialog {
	d := newDialog(parent, "Значок в трее")
	preview := qt.NewQLabel2()
	preview.SetAlignment(qt.AlignCenter)
	d.lay.AddWidget(preview.QWidget)
	rows := []struct {
		title string
		color *string
	}{
		{"Нового нет", &colors.Idle},
		{"Новые сообщения в каналах", &colors.Unread},
		{"Сообщения мне: упоминания и лички", &colors.Mentions},
	}
	buttons := make([]*qt.QPushButton, len(rows))
	refresh := func() {
		dark := trayPanelDark()
		eff := trayColors(colors, dark)
		for i, c := range []string{eff.Idle, eff.Unread, eff.Mentions} {
			text := c
			if *rows[i].color == "" {
				text += " · по умолчанию"
			}
			buttons[i].SetText(text)
			buttons[i].SetIcon(tmp(qt.NewQIcon2(swatch(c, 14))))
		}
		preview.SetPixmap(trayPreview(eff, dark, max(1, int(math.Round(d.w.DevicePixelRatioF())))))
	}
	grid := qt.NewQGridLayout2()
	grid.SetHorizontalSpacing(12)
	grid.SetVerticalSpacing(8)
	for i, r := range rows {
		b := qt.NewQPushButton2()
		b.SetStyleSheet("text-align:left; padding-left:8px")
		b.OnClicked(func() {
			cur := trayColors(colors, trayPanelDark())
			start := []string{cur.Idle, cur.Unread, cur.Mentions}[i]
			c := qt.QColorDialog_GetColor3(qcolor(start), d.w.QWidget, r.title)
			if d.closed || !c.IsValid() {
				return
			}
			*r.color = c.Name()
			refresh()
			onChange(colors)
		})
		buttons[i] = b
		grid.AddWidget2(fieldLabel(r.title).QWidget, i, 0)
		grid.AddWidget2(b.QWidget, i, 1)
	}
	// Размер цифр — доля высоты значка: трей берёт значок 16…64 px, одно
	// число пикселей подошло бы не всем. Ниже 30 % — «авто».
	const auto = trayDigitsMin - 1
	digits := qt.NewQSpinBox2()
	digits.SetRange(auto, 100)
	digits.SetSpecialValueText("авто")
	digits.SetSuffix(" %")
	digits.SetToolTip("Размер шрифта цифр — доля высоты значка; «авто» — по облачку")
	digits.SetValue(max(auto, colors.Digits))
	digits.OnValueChanged(func(v int) {
		if v == auto {
			v = 0
		}
		if v == colors.Digits {
			return
		}
		colors.Digits = v
		refresh()
		onChange(colors)
	})
	grid.AddWidget2(fieldLabel("Размер цифр").QWidget, len(rows), 0)
	grid.AddWidget4(digits.QWidget, len(rows), 1, qt.AlignLeft)
	grid.SetColumnStretch(1, 1)
	d.lay.AddLayout(grid.QLayout)
	refresh()

	reset := qt.NewQPushButton3("Сбросить")
	reset.OnClicked(func() {
		colors = config.Tray{}
		digits.SetValue(auto)
		refresh()
		onChange(colors)
	})
	d.bottom.AddWidget(reset.QWidget)
	d.bottom.AddStretch()
	done := qt.NewQPushButton3("Готово")
	done.SetProperty("primary", tmp(qt.NewQVariant11("true")))
	done.SetDefault(true)
	done.OnClicked(func() { d.w.Close() })
	d.bottom.AddWidget(done.QWidget)
	d.w.SetMinimumWidth(480)
	d.w.Show()
	return d
}

// swatch — квадратик цвета для кнопки (временный, см. tmp).
func swatch(color string, size int) *qt.QPixmap {
	pm := tmp(qt.NewQPixmap2(size*resourceDPR, size*resourceDPR))
	pm.SetDevicePixelRatio(resourceDPR)
	pm.FillWithFillColor(tmp(qt.NewQColor2(qt.Transparent)))
	p := qt.NewQPainter2(pm.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	p.SetPen(qcolor(Pal().Line2))
	p.SetBrush(qbrush(color))
	p.DrawRoundedRect(tmp(qt.NewQRectF4(0.5, 0.5, float64(size)-1, float64(size)-1)), 3, 3)
	free(p)
	return pm
}

// trayPreview — три состояния значка на полоске цвета панели, как в трее;
// значки — тех же пикселей, что уйдут в трей при масштабе dpr. Картинка
// временная (tmp).
func trayPreview(c config.Tray, dark bool, dpr int) *qt.QPixmap {
	const size, gap = 22, 16
	bg := "#f0efed"
	if dark {
		bg = "#1c1b1a"
	}
	pm := tmp(qt.NewQPixmap2((3*size+4*gap)*dpr, (size+12)*dpr))
	pm.SetDevicePixelRatio(float64(dpr))
	pm.FillWithFillColor(qcolor(bg))
	p := qt.NewQPainter2(pm.QPaintDevice)
	for i, v := range []struct{ color, text string }{{c.Idle, ""}, {c.Unread, "5"}, {c.Mentions, "9+"}} {
		icon := trayPixmap(size*dpr, v.color, v.text, c.Digits)
		icon.SetDevicePixelRatio(float64(dpr))
		p.DrawPixmap9(gap+i*(size+gap), 6, icon)
	}
	free(p)
	return pm
}
