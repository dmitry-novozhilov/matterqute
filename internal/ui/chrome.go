package ui

import (
	"fmt"
	"html"
	"math"
	"time"

	qt "github.com/mappu/miqt/qt6"
)

// header — шапка 52 px над лентой (#channelHeader) или тредом (#threadHeader):
// иконка и заголовок, второй строкой подпись с разметкой.
type header struct {
	f     *qt.QFrame
	icon  *qt.QLabel
	title *qt.QLabel
	chev  *qt.QLabel // стрелка меню после заголовка; видна, если задан onTitle
	sub   *qt.QLabel
	close *qt.QToolButton
	row   *qt.QHBoxLayout

	subHTML string // вторая строка, заданная set
	note    string // «bob печатает…» — на время вместо второй строки

	onTitle func() // щелчок по заголовку (меню канала)
}

func newHeader(objectName string, closable bool, onLink func(string)) *header {
	h := &header{f: qt.NewQFrame2(), icon: qt.NewQLabel2(), title: qt.NewQLabel2(), chev: qt.NewQLabel2(), sub: qt.NewQLabel2()}
	setName(h.f.QObject, objectName)
	h.f.SetFixedHeight(52)
	h.title.SetTextFormat(qt.RichText)
	setName(h.title.QObject, "channelName")
	h.sub.SetTextFormat(qt.RichText)
	setName(h.sub.QObject, "channelPurpose")
	h.sub.SetTextInteractionFlags(qt.TextSelectableByMouse | qt.LinksAccessibleByMouse)
	if onLink != nil {
		h.sub.OnLinkActivated(onLink)
		h.title.OnLinkActivated(onLink)
	}

	h.chev.SetVisible(false)
	for _, l := range []*qt.QLabel{h.title, h.chev} {
		l.OnMousePressEvent(func(super func(*qt.QMouseEvent), e *qt.QMouseEvent) {
			if h.onTitle != nil && h.chev.IsVisible() && e.Button() == qt.LeftButton {
				h.onTitle()
				return
			}
			super(e)
		})
	}

	top := qt.NewQHBoxLayout2()
	top.SetSpacing(6)
	top.AddWidget(h.icon.QWidget)
	top.AddWidget(h.title.QWidget)
	top.AddWidget(h.chev.QWidget)
	top.AddStretch()
	col := qt.NewQVBoxLayout2()
	col.SetSpacing(0)
	col.AddLayout(top.QLayout)
	col.AddWidget(h.sub.QWidget)
	row := qt.NewQHBoxLayout(h.f.QWidget)
	h.row = row
	row.SetContentsMargins(16, 6, 10, 6)
	row.AddLayout2(col.QLayout, 1)
	if closable {
		h.close = qt.NewQToolButton2()
		h.close.SetFixedSize2(28, 28)
		h.close.SetToolTip("Закрыть")
		row.AddWidget3(h.close.QWidget, 0, qt.AlignVCenter)
	}
	return h
}

// addTool — кнопка справа в шапке (перед «закрыть»): значок задаёт вызывающий.
func (h *header) addTool(tip string, do func()) *qt.QToolButton {
	b := qt.NewQToolButton2()
	b.SetFixedSize2(28, 28)
	b.SetIconSize(qt.NewQSize2(18, 18))
	b.SetToolTip(tip)
	b.SetFocusPolicy(qt.NoFocus)
	b.OnClicked(do)
	at := h.row.Count()
	if h.close != nil {
		at--
	}
	h.row.InsertWidget3(at, b.QWidget, 0, qt.AlignVCenter)
	return b
}

// set: icon — иконка 16 px (nil — без неё), sub — HTML второй строки ("" — одна строка).
func (h *header) set(icon *qt.QPixmap, titleHTML, subHTML string) {
	h.icon.SetVisible(icon != nil)
	if icon != nil {
		h.icon.SetPixmap(icon)
	}
	h.title.SetText(titleHTML)
	h.subHTML = subHTML
	h.showSub()
}

// setNote — временная вторая строка («bob печатает…»): высота шапки не
// меняется, поэтому лента и поле ввода не прыгают; "" — снова своя.
func (h *header) setNote(text string) {
	h.note = text
	h.showSub()
}

func (h *header) showSub() {
	text := h.subHTML
	if h.note != "" {
		text = fmt.Sprintf(`<i style="color:%s">%s</i>`, Pal().Tx2, html.EscapeString(h.note))
	}
	h.sub.SetText(text)
	h.sub.SetVisible(text != "")
}

// banner — плашка «Нет связи» под шапкой канала с обратным отсчётом до
// следующей попытки и ссылкой «Повторить сейчас».
type banner struct {
	f        *qt.QFrame
	icon     *qt.QLabel
	text     *qt.QLabel
	link     *qt.QLabel
	timer    *qt.QTimer
	host     string
	deadline time.Time

	OnRetry func()
}

func newBanner(host string) *banner {
	b := &banner{f: qt.NewQFrame2(), icon: qt.NewQLabel2(), text: qt.NewQLabel2(), link: qt.NewQLabel2(), host: host}
	setName(b.f.QObject, "connectionBanner")
	b.link.SetTextFormat(qt.RichText)
	b.link.OnLinkActivated(func(string) {
		if b.OnRetry != nil {
			b.OnRetry()
		}
	})
	row := qt.NewQHBoxLayout(b.f.QWidget)
	row.SetContentsMargins(16, 6, 16, 6)
	row.SetSpacing(8)
	row.AddWidget(b.icon.QWidget)
	row.AddWidget2(b.text.QWidget, 1)
	row.AddWidget(b.link.QWidget)
	b.timer = qt.NewQTimer2(b.f.QObject)
	b.timer.OnTimeout(b.tick)
	b.f.SetVisible(false)
	b.restyle()
	return b
}

func (b *banner) restyle() {
	p := Pal()
	b.icon.SetPixmap(iconPixmap("wifi-off", p.Warn, 16))
	b.text.SetStyleSheet("color:" + p.Tx)
	b.link.SetText(fmt.Sprintf(`<a href="retry" style="color:%s;font-weight:600;text-decoration:none">Повторить сейчас</a>`, p.Acc))
}

// show — обрыв; следующая попытка через retryIn.
func (b *banner) show(retryIn time.Duration) {
	b.deadline = time.Now().Add(retryIn)
	b.f.SetVisible(true)
	b.tick()
	b.timer.Start(1000)
}

func (b *banner) hide() {
	b.timer.Stop()
	b.f.SetVisible(false)
}

func (b *banner) tick() {
	left := int(math.Ceil(time.Until(b.deadline).Seconds()))
	msg := "Нет соединения с " + html.EscapeString(b.host) + ". "
	if left > 0 {
		msg += fmt.Sprintf("Переподключаемся через %d с…", left)
	} else {
		msg += "Переподключаемся…"
	}
	b.text.SetText(msg)
}
