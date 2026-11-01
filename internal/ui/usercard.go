package ui

import (
	"context"
	"fmt"
	"html"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
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
	c := m.newCard()

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
	c.header(avatar, displayName(u), "@"+u.Username)

	dot := qt.NewQLabel2()
	dot.SetPixmap(dotPixmap(statusColor(status), 8))
	statusLabel := qt.NewQLabel3(statusText(status))
	statusRow := qt.NewQHBoxLayout2()
	statusRow.SetSpacing(6)
	statusRow.AddWidget(dot.QWidget)
	statusRow.AddWidget2(statusLabel.QWidget, 1)
	c.lay.AddSpacing(8)
	c.lay.AddLayout(statusRow.QLayout)
	if u.Position != "" {
		role := qt.NewQLabel3(u.Position)
		setName(role.QObject, "userRole")
		role.SetWordWrap(true)
		c.lay.AddWidget(role.QWidget)
	}
	write := qt.NewQPushButton3("Написать")
	if me {
		write.SetText("Написать себе")
	} else {
		write.SetProperty("primary", qt.NewQVariant11("true"))
	}
	write.SetIcon(icon("message-circle", map[bool]string{true: pal.Tx, false: pal.OnAcc}[me], 16))
	write.OnClicked(func() {
		c.w.Close()
		m.openDM(u.Id)
	})
	c.lay.AddSpacing(10)
	c.lay.AddWidget(write.QWidget)
	c.show(pos)

	// Статус и «с какого времени» — свежие, с сервера.
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		st, _, err := m.s.Client.GetUserStatus(ctx, u.Id, "")
		m.ui(func() {
			if err != nil || c.closed {
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

// showIntegrationCard — карточка по клику на пост интеграции (вебхука): её имя
// и иконка и чей это вебхук, как в веб-клиенте. «Написать» нет — писать
// некому; создатель вебхука открывается по ссылке.
func (m *MainWindow) showIntegrationCard(p *model.Post) {
	in, ok := store.PostIntegration(p)
	if !ok {
		return
	}
	pos := qt.QCursor_Pos()
	creator, creatorName := m.st.User(p.UserId), m.st.Username(p.UserId)
	name := in.Name
	if name == "" {
		name = creatorName
	}
	c := m.newCard()
	avatar := qt.NewQLabel2()
	avatar.SetPixmap(integrationAvatar(m.assets, in, creator, name, 56, func() {
		if !c.closed {
			avatar.SetPixmap(integrationAvatar(m.assets, in, creator, name, 56, nil))
		}
	}))
	c.header(avatar, name, "интеграция")

	from := qt.NewQLabel2()
	setName(from.QObject, "userRole")
	from.SetWordWrap(true)
	from.SetTextFormat(qt.RichText)
	from.SetText(fmt.Sprintf(`Сообщение создано интеграцией от <a href="creator" style="color:%s;text-decoration:none">@%s</a>`,
		Pal().Acc, html.EscapeString(creatorName)))
	from.OnLinkActivated(func(string) {
		c.w.Close()
		if creator != nil {
			m.openUserCard(creator, pos)
		} else {
			m.showUserCard(creatorName)
		}
	})
	c.lay.AddSpacing(8)
	c.lay.AddWidget(from.QWidget)
	c.show(pos)
}

// card — всплывающая карточка у курсора, закрывается кликом мимо или Esc.
// Окно прозрачное ради скруглённых углов, фон и рамку рисует QFrame#userCard
// внутри: окну верхнего уровня с WA_TranslucentBackground Qt QSS-фон не рисует.
type card struct {
	w      *qt.QWidget
	lay    *qt.QVBoxLayout // содержимое карточки
	closed bool            // после закрытия окно удалено, трогать его нельзя
}

func (m *MainWindow) newCard() *card {
	c := &card{w: qt.NewQWidget3(m.w, qt.Popup|qt.FramelessWindowHint|qt.NoDropShadowWindowHint)}
	c.w.SetAttribute(qt.WA_DeleteOnClose)
	c.w.SetAttribute(qt.WA_TranslucentBackground)
	c.w.OnDestroyed(func() { c.closed = true })
	c.w.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		if qt.Key(e.Key()) == qt.Key_Escape {
			c.w.Close()
			return
		}
		super(e)
	})
	frame := qt.NewQFrame(c.w)
	setName(frame.QObject, "userCard")
	frame.SetFixedWidth(280)
	outer := qt.NewQVBoxLayout(c.w)
	outer.SetContentsMargins(0, 0, 0, 0)
	outer.AddWidget(frame.QWidget)
	c.lay = qt.NewQVBoxLayout(frame.QWidget)
	c.lay.SetContentsMargins(16, 16, 16, 16)
	c.lay.SetSpacing(6)
	return c
}

// header — аватар 56 и справа имя с подписью под ним.
func (c *card) header(avatar *qt.QLabel, name, sub string) {
	nameLabel := qt.NewQLabel3(name)
	setName(nameLabel.QObject, "userName")
	nameLabel.SetWordWrap(true)
	subLabel := qt.NewQLabel3(sub)
	setName(subLabel.QObject, "userHandle")
	names := qt.NewQVBoxLayout2()
	names.SetSpacing(0)
	names.AddStretch()
	names.AddWidget(nameLabel.QWidget)
	names.AddWidget(subLabel.QWidget)
	names.AddStretch()
	top := qt.NewQHBoxLayout2()
	top.SetSpacing(12)
	top.AddWidget(avatar.QWidget)
	top.AddLayout2(names.QLayout, 1)
	c.lay.AddLayout(top.QLayout)
}

func (c *card) show(pos *qt.QPoint) {
	c.w.AdjustSize()
	c.w.Move(pos.X()+8, pos.Y()+8)
	c.w.Show()
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
