package ui

import (
	"context"
	"fmt"
	"html"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// showUserCard — карточка пользователя у курсора (клик по @имени или автору):
// аватар, имя, статус, должность и «Написать». Закрывается кликом мимо или Esc.
func (m *MainWindow) showUserCard(username string) {
	pos := qt.QCursor_Pos()
	if u := m.st.UserByName(username); u != nil {
		m.openUserCard(u, pos)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		u, _, err := m.s.Client.GetUserByUsername(ctx, username, "")
		m.ui(func() {
			if err != nil {
				m.fail("user card", fmt.Errorf("load user @%s: %w", username, err))
				return
			}
			m.st.AddUsers([]*model.User{u})
			m.openUserCard(u, pos)
		})
	}()
}

func (m *MainWindow) openUserCard(u *model.User, pos *qt.QPoint) {
	pal := Pal()
	me := u.Id == m.s.Me.Id
	// Окно прозрачное ради скруглённых углов, фон и рамку рисует QFrame#userCard
	// внутри: окну верхнего уровня с WA_TranslucentBackground Qt QSS-фон не рисует.
	popup := qt.NewQWidget3(m.w, qt.Popup|qt.FramelessWindowHint|qt.NoDropShadowWindowHint)
	popup.SetAttribute(qt.WA_DeleteOnClose)
	popup.SetAttribute(qt.WA_TranslucentBackground)
	closed := false // после закрытия окно удалено, трогать его нельзя
	popup.OnDestroyed(func() { closed = true })
	popup.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		if qt.Key(e.Key()) == qt.Key_Escape {
			popup.Close()
			return
		}
		super(e)
	})
	card := qt.NewQFrame(popup)
	setName(card.QObject, "userCard")
	card.SetFixedWidth(280)
	outer := qt.NewQVBoxLayout(popup)
	outer.SetContentsMargins(0, 0, 0, 0)
	outer.AddWidget(card.QWidget)

	status := m.st.Status(u.Id)
	avatar := qt.NewQLabel2()
	drawAvatar := func() {
		pm := avatarPixmap(m.assets, u, u.Username, u.IsBot, 56, nil)
		withDot := qt.NewQPixmap5(pm)
		p := qt.NewQPainter2(withDot.QPaintDevice)
		drawStatusDot(p, 56-14, 56-14, 12, status, pal.Surf)
		p.End()
		avatar.SetPixmap(withDot)
	}
	drawAvatar()

	name := qt.NewQLabel3(displayName(u))
	setName(name.QObject, "userName")
	name.SetWordWrap(true)
	handle := qt.NewQLabel3("@" + u.Username)
	setName(handle.QObject, "userHandle")
	names := qt.NewQVBoxLayout2()
	names.SetSpacing(0)
	names.AddStretch()
	names.AddWidget(name.QWidget)
	names.AddWidget(handle.QWidget)
	names.AddStretch()
	top := qt.NewQHBoxLayout2()
	top.SetSpacing(12)
	top.AddWidget(avatar.QWidget)
	top.AddLayout2(names.QLayout, 1)

	dot := qt.NewQLabel2()
	dot.SetPixmap(dotPixmap(statusColor(status), 8))
	statusLabel := qt.NewQLabel3(statusText(status))
	statusRow := qt.NewQHBoxLayout2()
	statusRow.SetSpacing(6)
	statusRow.AddWidget(dot.QWidget)
	statusRow.AddWidget2(statusLabel.QWidget, 1)

	lay := qt.NewQVBoxLayout(card.QWidget)
	lay.SetContentsMargins(16, 16, 16, 16)
	lay.SetSpacing(6)
	lay.AddLayout(top.QLayout)
	lay.AddSpacing(8)
	lay.AddLayout(statusRow.QLayout)
	if u.Position != "" {
		role := qt.NewQLabel3(u.Position)
		setName(role.QObject, "userRole")
		role.SetWordWrap(true)
		lay.AddWidget(role.QWidget)
	}
	write := qt.NewQPushButton3("Написать")
	if me {
		write.SetText("Написать себе")
	} else {
		write.SetProperty("primary", qt.NewQVariant11("true"))
	}
	write.SetIcon(icon("message-circle", map[bool]string{true: pal.Tx, false: pal.OnAcc}[me], 16))
	write.OnClicked(func() {
		popup.Close()
		m.openDM(u.Id)
	})
	lay.AddSpacing(10)
	lay.AddWidget(write.QWidget)

	popup.AdjustSize()
	popup.Move(pos.X()+8, pos.Y()+8)
	popup.Show()

	// Статус и «с какого времени» — свежие, с сервера.
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		st, _, err := m.s.Client.GetUserStatus(ctx, u.Id, "")
		m.ui(func() {
			if err != nil || closed {
				return
			}
			status = st.Status
			m.st.SetStatus(u.Id, st.Status)
			statusLabel.SetText(html.EscapeString(statusLine(st, me)))
			dot.SetPixmap(dotPixmap(statusColor(st.Status), 8))
			drawAvatar()
		})
	}()
}

// statusLine — «В сети», «Не беспокоить до 18:00», «Не в сети с 23:10», «Это вы».
func statusLine(st *model.Status, me bool) string {
	text := statusText(st.Status)
	switch {
	case st.Status == model.StatusDnd && st.DNDEndTime > 0:
		// DNDEndTime сервер хранит в секундах, в отличие от прочих меток.
		text += " до " + time.Unix(st.DNDEndTime, 0).Format("15:04")
	case st.Status == model.StatusOffline && st.LastActivityAt > 0:
		text += " с " + shortWhen(time.UnixMilli(st.LastActivityAt), time.Now())
	}
	if me {
		text += " · это вы"
	}
	return text
}

// shortWhen — «23:10» сегодня, иначе «31 октября 23:10».
func shortWhen(t, now time.Time) string {
	if sameDay(t.UnixMilli(), now.UnixMilli()) {
		return t.Format("15:04")
	}
	return fmt.Sprintf("%d %s %s", t.Day(), monthsGen[t.Month()-1], t.Format("15:04"))
}

// openDM открывает личку с пользователем, при необходимости создавая её.
func (m *MainWindow) openDM(userID string) {
	for _, ch := range m.st.DirectChannels() {
		if ch.Type == model.ChannelTypeDirect && m.st.DMPeer(ch) == userID {
			m.selectChannel(ch.Id)
			return
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		ch, _, err := m.s.Client.CreateDirectChannel(ctx, m.s.Me.Id, userID)
		m.ui(func() {
			if err != nil {
				m.fail("open direct channel", fmt.Errorf("create direct channel with %s: %w", userID, err))
				return
			}
			m.selectAfterLoad = ch.Id
			m.reloadSidebar()
		})
	}()
}
