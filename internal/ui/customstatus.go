package ui

import (
	"context"
	"fmt"
	"html"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/emoji"
	"matterqute/internal/status"
	"matterqute/internal/store"
)

// statusTip — «На встрече · до 15:30».
func statusTip(cs *model.CustomStatus) string {
	tip := cs.Text
	if until := status.Until(cs.Duration, cs.ExpiresAt, time.Now()); until != "" {
		if tip != "" {
			tip += " · "
		}
		tip += until
	}
	return tip
}

// statusHTML — эмодзи своего статуса у имени автора в ленте; подсказка — текст и срок.
func (f *Feed) statusHTML(p *model.Post) string {
	if _, hook := store.PostIntegration(p); hook {
		return ""
	}
	cs := status.Current(f.d.Users(p.UserId), time.Now())
	if cs == nil {
		return ""
	}
	tip := html.EscapeString(statusTip(cs))
	if c, ok := emoji.Char(cs.Emoji); ok {
		return fmt.Sprintf(`&nbsp;<span title="%s">%s</span>`, tip, c)
	}
	if src := f.customEmoji(cs.Emoji); src != "" {
		return fmt.Sprintf(`&nbsp;<img src="%s" width="16" height="16" title="%s">`, src, tip)
	}
	return ""
}

// statusPixmap — эмодзи своего статуса size px (системное или эмодзи сервера).
func (m *MainWindow) statusPixmap(cs *model.CustomStatus, size int, loaded func()) *qt.QPixmap {
	return m.emojiImage(cs.Emoji, size, loaded)
}

// me — свой профиль: в store свежее, чем в сессии (user_updated).
func (m *MainWindow) me() *model.User {
	if u := m.st.User(m.s.Me.Id); u != nil {
		return u
	}
	return m.s.Me
}

// ownStatusChanged — свой статус или присутствие поменялись: строка состояния.
func (m *MainWindow) ownStatusChanged() {
	if m.OnOwnStatus == nil {
		return
	}
	cs := status.Current(m.me(), time.Now())
	if cs == nil {
		m.OnOwnStatus(nil, "")
		return
	}
	m.OnOwnStatus(m.statusPixmap(cs, 16, m.ownStatusChanged), statusTip(cs))
}

// presences — присутствие в панели статуса.
var presences = []struct{ status, title string }{
	{model.StatusOnline, "В сети"},
	{model.StatusAway, "Отошёл"},
	{model.StatusDnd, "Не беспокоить"},
	{model.StatusOffline, "Не в сети"},
}

// showStatusPanel — панель статуса над точкой at (своё имя в строке
// состояния): свой статус (эмодзи, текст, срок) и присутствие — в сети,
// отошёл, не беспокоить со сроком, не в сети.
func (m *MainWindow) showStatusPanel(at *qt.QPoint) {
	pal := Pal()
	me := m.me()
	c := m.newCard()
	m.statusPanel = c
	avatar := qt.NewQLabel2()
	avatar.SetPixmap(avatarPixmap(m.assets, me, me.Username, false, 56, nil))
	c.header(avatar, displayName(me), "@"+me.Username)
	c.w.SetStyleSheet(fmt.Sprintf(`QPushButton[role="row"] { text-align: left; border: none; border-radius: 4px; padding: 0 8px; min-height: 32px; background: transparent; }
QPushButton[role="row"]:hover { background: %s; }
QFrame[role="line"] { background: %s; max-height: 1px; min-height: 1px; border: none; }`, pal.Hover, pal.Line))

	row := func(icon *qt.QPixmap, text string, check bool) *qt.QPushButton {
		b := qt.NewQPushButton2()
		b.SetProperty("role", qt.NewQVariant11("row"))
		b.SetFocusPolicy(qt.NoFocus)
		l := qt.NewQHBoxLayout(b.QWidget)
		l.SetContentsMargins(8, 0, 8, 0)
		l.SetSpacing(10)
		ic := qt.NewQLabel2()
		ic.SetFixedSize2(20, 20)
		ic.SetAlignment(qt.AlignCenter)
		if icon != nil {
			ic.SetPixmap(icon)
		}
		tl := qt.NewQLabel3(text)
		l.AddWidget(ic.QWidget)
		l.AddWidget2(tl.QWidget, 1)
		if check {
			ck := qt.NewQLabel2()
			ck.SetPixmap(iconPixmap("check", pal.Acc, 16))
			l.AddWidget(ck.QWidget)
		}
		for _, w := range []*qt.QWidget{ic.QWidget, tl.QWidget} {
			w.SetAttribute(qt.WA_TransparentForMouseEvents)
		}
		return b
	}
	line := func() {
		f := qt.NewQFrame2()
		f.SetProperty("role", qt.NewQVariant11("line"))
		c.lay.AddWidget(f.QWidget)
	}

	c.lay.AddSpacing(6)
	if cs := status.Current(me, time.Now()); cs != nil {
		text := cs.Text
		if until := status.Until(cs.Duration, cs.ExpiresAt, time.Now()); until != "" {
			text += " · " + until
		}
		edit := row(m.statusPixmap(cs, 20, nil), text, false)
		edit.OnClicked(func() { c.w.Close(); m.editCustomStatus() })
		clear := qt.NewQToolButton2()
		clear.SetIcon(icon("x", pal.Tx2, 16))
		clear.SetToolTip("Сбросить свой статус")
		clear.SetAutoRaise(true)
		clear.OnClicked(func() { c.w.Close(); m.saveCustomStatus(nil) })
		h := qt.NewQHBoxLayout2()
		h.SetSpacing(4)
		h.AddWidget2(edit.QWidget, 1)
		h.AddWidget(clear.QWidget)
		c.lay.AddLayout(h.QLayout)
	} else {
		set := row(iconPixmap("smile-plus", pal.Tx2, 20), "Установить свой статус…", false)
		set.OnClicked(func() { c.w.Close(); m.editCustomStatus() })
		c.lay.AddWidget(set.QWidget)
	}
	line()
	current := m.st.Status(me.Id)
	for _, pr := range presences {
		text := pr.title
		if pr.status == model.StatusDnd && current == model.StatusDnd && m.dndEnd > 0 {
			text += " " + status.Until(status.DateAndTime, time.Unix(m.dndEnd, 0), time.Now())
		}
		b := row(dotPixmap(statusColor(pr.status), 10), text, pr.status == current)
		st := pr.status
		b.OnClicked(func() {
			if st != model.StatusDnd {
				c.w.Close()
				m.setPresence(st, time.Time{})
				return
			}
			// «Не беспокоить» — на сколько: меню под строкой (без эмодзи — только текст).
			menu := qt.NewQMenu(c.w)
			for _, d := range status.DNDDurations {
				a := menu.QWidget.AddActionWithText(d.Title)
				id := d.ID
				a.OnTriggered(func() {
					c.w.Close()
					if id == status.DateAndTime {
						m.pickDNDEnd()
						return
					}
					m.setPresence(model.StatusDnd, status.ExpiresAt(id, time.Now(), time.Time{}))
				})
			}
			menu.ExecWithPos(b.MapToGlobalWithQPoint(qt.NewQPoint2(0, b.Height())))
			menu.DeleteLater()
		})
		c.lay.AddWidget(b.QWidget)
	}
	c.frame.SetFixedWidth(300) // окно по ширине карточки: лишнее место было бы пустым (чёрным) краем
	c.showAbove(at)
}

// pickDNDEnd — «не беспокоить» до выбранного времени.
func (m *MainWindow) pickDNDEnd() {
	dlg := newDialog(m.w, "Не беспокоить до")
	at := newDateTimeEdit(time.Now().Add(time.Hour))
	dlg.lay.AddWidget(at.QWidget)
	dlg.buttons("Включить", func() {
		m.setPresence(model.StatusDnd, time.UnixMilli(at.DateTime().ToMSecsSinceEpoch()))
	})
	dlg.w.Show()
}

// setPresence — в сети, отошёл, не беспокоить (until — когда выключить; ноль
// — не выключать), не в сети.
func (m *MainWindow) setPresence(st string, until time.Time) {
	me := m.s.Me.Id
	s := &model.Status{UserId: me, Status: st}
	if st == model.StatusDnd && !until.IsZero() {
		s.DNDEndTime = until.Unix() // секунды, в отличие от прочих меток Mattermost
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, _, err := m.s.Client.UpdateUserStatus(ctx, me, s)
		m.ui(func() {
			if err != nil {
				m.fail("set status", fmt.Errorf("set status %s: %w", st, err))
				return
			}
			m.dndEnd = s.DNDEndTime
			m.st.SetStatus(me, st) // то же придёт status_change
			m.refreshItem("")
			m.ownStatusChanged()
		})
	}()
}

// saveCustomStatus — свой статус на сервер; nil — сбросить. Профиль обновится
// и по user_updated, но показываем сразу.
func (m *MainWindow) saveCustomStatus(cs *model.CustomStatus) {
	me := m.s.Me.Id
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var err error
		if cs == nil {
			_, err = m.s.Client.RemoveUserCustomStatus(ctx, me)
		} else {
			_, _, err = m.s.Client.UpdateUserCustomStatus(ctx, me, cs)
		}
		m.ui(func() {
			if err != nil {
				m.fail("custom status", fmt.Errorf("save custom status: %w", err))
				return
			}
			u := *m.me()
			u.Props = model.StringMap{}
			for k, v := range m.me().Props {
				u.Props[k] = v
			}
			if cs == nil {
				delete(u.Props, model.UserPropsKeyCustomStatus)
			} else {
				u.SetCustomStatus(cs)
			}
			m.applyUser(&u)
		})
	}()
}

// applyUser — профиль изменился (свой статус, имя): перерисовать, где он виден.
func (m *MainWindow) applyUser(u *model.User) {
	m.st.AddUsers([]*model.User{u})
	m.feed.RefreshNames()
	m.panel.feed.RefreshNames()
	m.refreshItem("")
	if u.Id == m.s.Me.Id {
		m.ownStatusChanged()
	}
}

// editCustomStatus — окно своего статуса: эмодзи, текст, стандартные, срок.
func (m *MainWindow) editCustomStatus() {
	pal := Pal()
	cur := status.Current(m.me(), time.Now())
	dlg := newDialog(m.w, "Свой статус")
	m.statusDialog = dlg
	emojiName := ""
	if cur != nil {
		emojiName = cur.Emoji
	}
	pick := qt.NewQToolButton2()
	pick.SetFixedSize2(36, 36)
	pick.SetIconSize(qt.NewQSize2(22, 22))
	pick.SetToolTip("Выбрать эмодзи")
	setEmoji := func(name string) {
		emojiName = name
		if name == "" {
			pick.SetIcon(icon("smile-plus", pal.Tx2, 22))
			return
		}
		pick.SetIcon(qt.NewQIcon2(m.emojiImage(name, 22, nil)))
	}
	setEmoji(emojiName)
	text := qt.NewQLineEdit2()
	text.SetPlaceholderText("Какой у вас статус?")
	text.SetMaxLength(model.CustomStatusTextMaxRunes)
	text.SetClearButtonEnabled(true)
	if cur != nil {
		text.SetText(cur.Text)
	}
	pick.OnClicked(func() {
		m.pickEmoji(dlg.w.QWidget, pick.MapToGlobalWithQPoint(qt.NewQPoint2(0, pick.Height())), setEmoji)
	})
	top := qt.NewQHBoxLayout2()
	top.SetSpacing(8)
	top.AddWidget(pick.QWidget)
	top.AddWidget2(text.QWidget, 1)
	dlg.lay.AddLayout(top.QLayout)

	durations := qt.NewQComboBox2()
	for _, d := range status.Durations {
		durations.AddItem(d.Title)
	}
	at := newDateTimeEdit(time.Now().Add(time.Hour))
	at.SetVisible(false)
	durations.OnCurrentIndexChanged(func(i int) { at.SetVisible(status.Durations[i].ID == status.DateAndTime) })
	setDuration := func(id string) {
		for i, d := range status.Durations {
			if d.ID == id {
				durations.SetCurrentIndex(i)
			}
		}
	}
	if cur != nil {
		setDuration(cur.Duration)
		if cur.Duration == status.DateAndTime {
			at.SetDateTime(qt.QDateTime_FromMSecsSinceEpoch(cur.ExpiresAt.UnixMilli()))
		}
	} else {
		setDuration("today") // как веб-клиент: свой статус по умолчанию — до конца дня
	}

	presets := fieldLabel("Стандартные")
	dlg.lay.AddSpacing(4)
	dlg.lay.AddWidget(presets.QWidget)
	for _, p := range status.Presets {
		b := qt.NewQPushButton3(p.Text)
		b.SetIcon(qt.NewQIcon2(m.emojiImage(p.Emoji, 20, nil)))
		b.SetIconSize(qt.NewQSize2(20, 20))
		b.SetFlat(true)
		b.SetStyleSheet(fmt.Sprintf("QPushButton { text-align: left; padding: 0 8px; min-height: 30px; border: none; border-radius: 4px; background: transparent; } QPushButton:hover { background: %s; }", pal.Hover))
		b.OnClicked(func() {
			setEmoji(p.Emoji)
			text.SetText(p.Text)
			setDuration(p.Duration)
		})
		dlg.lay.AddWidget(b.QWidget)
	}
	dlg.lay.AddSpacing(4)
	dlg.lay.AddWidget(fieldLabel("Сбросить статус").QWidget)
	when := qt.NewQHBoxLayout2()
	when.SetSpacing(8)
	when.AddWidget2(durations.QWidget, 1)
	when.AddWidget2(at.QWidget, 1)
	dlg.lay.AddLayout(when.QLayout)

	if cur != nil {
		dlg.extra("Очистить статус", func() { m.saveCustomStatus(nil) })
	}
	dlg.buttons("Сохранить", func() {
		if emojiName == "" && text.Text() == "" {
			m.saveCustomStatus(nil)
			return
		}
		at := time.UnixMilli(at.DateTime().ToMSecsSinceEpoch())
		m.saveCustomStatus(status.New(emojiName, text.Text(), status.Durations[durations.CurrentIndex()].ID, time.Now(), at))
	})
	dlg.w.Show()
	text.SetFocus()
}

// dialog — небольшое окно: содержимое сверху, кнопки снизу.
type dialog struct {
	w      *qt.QDialog
	lay    *qt.QVBoxLayout
	bottom *qt.QHBoxLayout
	closed bool
}

func newDialog(parent *qt.QWidget, title string) *dialog {
	d := &dialog{w: qt.NewQDialog(parent)}
	d.w.SetWindowTitle(title)
	d.w.SetAttribute(qt.WA_DeleteOnClose)
	d.w.SetModal(true)
	d.w.SetMinimumWidth(400)
	d.w.OnDestroyed(func() { d.closed = true })
	root := qt.NewQVBoxLayout(d.w.QWidget)
	root.SetContentsMargins(20, 20, 20, 16)
	root.SetSpacing(12)
	d.lay = qt.NewQVBoxLayout2()
	d.lay.SetSpacing(6)
	root.AddLayout(d.lay.QLayout)
	d.bottom = qt.NewQHBoxLayout2()
	d.bottom.SetSpacing(8)
	root.AddLayout(d.bottom.QLayout)
	return d
}

// extra — кнопка слева внизу (например, «Очистить»): делает своё и закрывает окно.
func (d *dialog) extra(title string, do func()) {
	b := qt.NewQPushButton3(title)
	b.OnClicked(func() { do(); d.w.Close() })
	d.bottom.AddWidget(b.QWidget)
}

// buttons — «Отмена» и основная кнопка справа; её и возвращает.
func (d *dialog) buttons(ok string, do func()) *qt.QPushButton {
	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { d.w.Close() })
	okb := qt.NewQPushButton3(ok)
	okb.SetProperty("primary", qt.NewQVariant11("true"))
	okb.SetDefault(true)
	okb.OnClicked(func() { do(); d.w.Close() })
	d.bottom.AddWidget(cancel.QWidget)
	d.bottom.AddWidget(okb.QWidget)
	return okb
}

// danger — основная кнопка необратимого действия («Удалить»): красная.
func danger(b *qt.QPushButton) {
	pal := Pal()
	b.SetStyleSheet(fmt.Sprintf(`QPushButton { background: %[1]s; border-color: %[1]s; color: #fcfcfc; }
QPushButton:hover, QPushButton:pressed { background: %[2]s; border-color: %[2]s; }`, pal.Dang, darker(pal.Dang)))
}

// darker — цвет на 12 % темнее.
func darker(c string) string { return qt.NewQColor6(c).DarkerWithInt(112).Name() }

func newDateTimeEdit(t time.Time) *qt.QDateTimeEdit {
	e := qt.NewQDateTimeEdit2()
	e.SetCalendarPopup(true)
	e.SetDisplayFormat("dd.MM.yyyy HH:mm")
	e.SetMinimumDateTime(qt.QDateTime_FromMSecsSinceEpoch(time.Now().UnixMilli()))
	e.SetDateTime(qt.QDateTime_FromMSecsSinceEpoch(t.UnixMilli()))
	return e
}
