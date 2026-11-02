package ui

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
	"matterqute/internal/sched"
)

// «Напомнить» и «Переслать…» в меню сообщения, как в веб-клиенте.
//
// Напоминание ставит сервер (POST /users/{id}/posts/{id}/reminder, время —
// с точностью до 5 минут), он же в срок присылает личку от системного бота
// с типом reminder; её текст — на языке сервера и со ссылкой на сайт,
// поэтому рисуем свой по props (reminderMarkdown).
//
// Пересылка — новое сообщение в выбранный канал или личку: комментарий и
// ссылка на исходное; сервер добавит к нему предпросмотр (permalink), его
// лента уже рисует. Кто не видит исходный канал, увидит только ссылку.

// remind — напомнить о сообщении в at; нулевое — спросить время.
func (m *MainWindow) remind(p *model.Post, at time.Time) {
	if at.IsZero() {
		m.showScheduleDialog("Напомнить о сообщении", "Напомнить", time.Now().Add(time.Hour), func(at time.Time) { m.remind(p, at) })
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, err := m.s.Client.SetPostReminder(ctx, &model.PostReminder{PostId: p.Id, UserId: m.s.Me.Id, TargetTime: at.Unix()})
		m.ui(func() {
			if err != nil {
				m.fail("set reminder", fmt.Errorf("remind about post %s at %s: %w", p.Id, at.Format("2006-01-02 15:04:05"), err))
				return
			}
			m.message("Напомню " + sched.Label(at, time.Now()))
		})
	}()
}

var (
	teamNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	postIDRe   = regexp.MustCompile(`^[a-z0-9]{26}$`)
)

// reminderMarkdown — текст напоминания сервера по его props: «⏰ Напоминание
// о сообщении от @bob» со ссылкой на сообщение; false — не напоминание или
// props не те (тогда — текст сервера как есть).
func reminderMarkdown(p *model.Post) (string, bool) {
	if p.Type != model.PostTypeReminder {
		return "", false
	}
	team, _ := p.GetProp("team_name").(string)
	post, _ := p.GetProp("post_id").(string)
	user, _ := p.GetProp("username").(string)
	if !teamNameRe.MatchString(team) || !postIDRe.MatchString(post) || !model.IsValidUsername(user) {
		return "", false
	}
	return fmt.Sprintf("⏰ Напоминание о [сообщении](/%s/pl/%s) от @%s", team, post, user), true
}

// showForward — «Переслать…»: куда (свои каналы, лички, люди — как в
// быстром переходе), комментарий; Enter в строке поиска — выбрать,
// «Переслать» — отправить. Возвращает окно (тесты).
func (m *MainWindow) showForward(p *model.Post) *dialog {
	d := newDialog(m.w, "Переслать сообщение")
	m.channelDialog = d
	d.w.SetMinimumWidth(480)
	pal := Pal()
	quote := qt.NewQLabel2()
	quote.SetTextFormat(qt.RichText)
	quote.SetWordWrap(true)
	text := markdown.PlainText(p.Message)
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200]) + "…"
	}
	from := "@" + m.st.Username(p.UserId)
	if ch := m.st.Channel(p.ChannelId); ch != nil {
		from += " в " + channelPrefix(ch) + m.st.Title(ch)
	}
	quote.SetText(fmt.Sprintf(`<span style="color:%s">%s</span><br>%s`, pal.Tx2, html.EscapeString(from), html.EscapeString(text)))
	field := qt.NewQLineEdit2()
	field.SetPlaceholderText("Куда: канал, личка или человек")
	field.SetClearButtonEnabled(true)
	list := qt.NewQListWidget2()
	list.SetIconSize(qt.NewQSize2(20, 20))
	list.SetMinimumHeight(220)
	list.SetFocusPolicy(qt.NoFocus)
	comment := qt.NewQLineEdit2()
	comment.SetPlaceholderText("Комментарий (необязательно)")
	hint := qt.NewQLabel2()
	hint.SetProperty("role", qt.NewQVariant11("hint"))
	hint.SetWordWrap(true)
	if ch := m.st.Channel(p.ChannelId); ch != nil && ch.Type != model.ChannelTypeOpen {
		hint.SetText("Сообщение из закрытого канала или лички: кто его не видит, получит только ссылку.")
	}
	d.lay.AddWidget(quote.QWidget)
	d.lay.AddWidget(fieldLabel("Куда").QWidget)
	d.lay.AddWidget(field.QWidget)
	d.lay.AddWidget2(list.QWidget, 1)
	d.lay.AddWidget(comment.QWidget)
	d.lay.AddWidget(hint.QWidget)

	var shown []switchItem
	fill := func() {
		var items []switchItem
		for _, it := range m.switchSources(nil, nil) {
			if it.kind != switchJoin {
				items = append(items, it)
			}
		}
		shown = rankSwitch(items, field.Text(), "")
		list.Clear()
		for _, it := range shown {
			label := it.title
			if it.sub != "" {
				label += "   " + it.sub
			}
			row := qt.NewQListWidgetItem7(label, list)
			row.SetIcon(qt.NewQIcon2(it.icon))
		}
		if len(shown) > 0 {
			list.SetCurrentRow(0)
		}
	}
	field.OnTextChanged(func(string) { fill() })
	field.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		switch qt.Key(e.Key()) {
		case qt.Key_Down:
			list.SetCurrentRow(min(list.CurrentRow()+1, list.Count()-1))
		case qt.Key_Up:
			list.SetCurrentRow(max(list.CurrentRow()-1, 0))
		case qt.Key_Return, qt.Key_Enter:
			comment.SetFocus()
		default:
			super(e)
		}
	})

	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { d.w.Close() })
	ok := qt.NewQPushButton3("Переслать")
	ok.SetProperty("primary", qt.NewQVariant11("true"))
	send := func() {
		i := list.CurrentRow()
		if i < 0 || i >= len(shown) {
			hint.SetText("Выберите, куда переслать")
			return
		}
		to := shown[i]
		message := m.postLink(p)
		if c := strings.TrimSpace(comment.Text()); c != "" {
			message = c + "\n" + message
		}
		ok.SetEnabled(false)
		hint.SetText("Пересылаем…")
		me := m.s.Me.Id
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			channelID := to.id
			var err error
			if to.kind == switchUser {
				var ch *model.Channel
				if ch, _, err = m.s.Client.CreateDirectChannel(ctx, me, to.id); err == nil {
					channelID = ch.Id
				}
			}
			if err == nil {
				_, _, err = m.s.Client.CreatePost(ctx, &model.Post{ChannelId: channelID, Message: message})
			}
			m.ui(func() {
				if err != nil {
					m.log.Warn("forward post", "post", p.Id, "to", channelID, "err", err)
					if !d.closed {
						ok.SetEnabled(true)
						hint.SetText("Не переслано: " + commandError(err))
					}
					return
				}
				if !d.closed {
					d.w.Close()
				}
				m.message("Переслано: " + to.title)
				if to.kind == switchUser {
					m.reloadSidebar() // новая личка
				}
			})
		}()
	}
	ok.OnClicked(send)
	comment.OnReturnPressed(send)
	d.bottom.AddWidget(cancel.QWidget)
	d.bottom.AddWidget(ok.QWidget)
	fill()
	d.w.Show()
	field.SetFocus()
	return d
}
