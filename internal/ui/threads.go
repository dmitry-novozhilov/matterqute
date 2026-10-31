package ui

import (
	"context"
	"fmt"
	"html"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/store"
)

// threadsID — псевдо-канал «Треды» в сайдбаре (только при CRT).
const threadsID = "@threads"

// Роль данных элемента списка тредов: id корня.
const threadIDRole = int(qt.UserRole) + 2

// ThreadPanel — правая панель с открытым тредом.
type ThreadPanel struct {
	w        *qt.QWidget
	title    *qt.QLabel
	feed     *Feed
	composer *Composer
}

func NewThreadPanel(names func(string) string, me string, onClose func()) *ThreadPanel {
	p := &ThreadPanel{w: qt.NewQWidget2()}
	p.title = newTextLabel(qt.RichText)
	p.title.SetMargin(10)
	closeBtn := qt.NewQPushButton3("✕")
	closeBtn.SetFlat(true)
	closeBtn.SetToolTip("Закрыть тред")
	closeBtn.OnClicked(onClose)
	head := qt.NewQHBoxLayout2()
	head.AddWidget2(p.title.QWidget, 1)
	head.AddWidget(closeBtn.QWidget)

	p.feed = NewFeed(names, me, true)
	p.composer = NewComposer()
	p.composer.edit.SetPlaceholderText("Ответить в тред… Enter — отправить")

	lay := qt.NewQVBoxLayout(p.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.SetSpacing(0)
	lay.AddLayout(head.QLayout)
	lay.AddWidget2(p.feed.Widget(), 1)
	lay.AddWidget(p.composer.Widget())
	p.w.SetMinimumWidth(320)
	p.w.SetVisible(false)
	return p
}

// threadView — открытый в панели тред.
type threadView struct {
	rootID    string
	channelID string // известен после загрузки
	tl        *store.Timeline
}

// openThread показывает тред в правой панели и грузит его целиком.
func (m *MainWindow) openThread(rootID string) {
	t := &threadView{rootID: rootID, tl: &store.Timeline{}}
	m.thread = t
	m.panel.title.SetText("<b>Тред</b>")
	m.panel.feed.Reset("Загрузка…")
	m.panel.w.SetVisible(true)
	m.panel.composer.SetEnabled(false)
	crt := m.st.CRT()
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		list, _, err := m.s.Client.GetPostThread(ctx, rootID, "", crt)
		m.ui(func() {
			if m.thread != t {
				return
			}
			if err != nil {
				err = fmt.Errorf("load thread %s: %w", rootID, err)
				m.fail("load thread", err)
				m.panel.feed.Reset(err.Error())
				return
			}
			posts := store.PostsOf(list)
			slices.SortFunc(posts, func(a, b *model.Post) int { return int(a.CreateAt - b.CreateAt) })
			m.withUsers(postAuthors(posts), func() {
				if m.thread != t {
					return
				}
				t.tl.Prepend(posts, false)
				t.tl.Loaded = true
				if root := t.tl.Get(rootID); root != nil {
					t.channelID = root.ChannelId
				}
				m.panel.title.SetText(m.threadTitleHTML(t))
				m.panel.feed.SetPosts(t.tl.Posts, false)
				m.panel.composer.SetEnabled(t.channelID != "")
				m.panel.composer.Focus()
				if m.st.CRT() && t.channelID != "" {
					m.markThreadRead(rootID, t.channelID)
				}
			})
		})
	}()
}

func (m *MainWindow) threadTitleHTML(t *threadView) string {
	s := "<b>Тред</b>"
	if ch := m.st.Channel(t.channelID); ch != nil {
		s += ` <span style="color:gray">` + html.EscapeString(channelPrefix(ch)+m.st.Title(ch)) + "</span>"
	}
	return s
}

func (m *MainWindow) closeThread() {
	m.thread = nil
	m.panel.w.SetVisible(false)
}

func (m *MainWindow) sendReply(text string) {
	t := m.thread
	if t == nil || t.channelID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		p, _, err := m.s.Client.CreatePost(ctx, &model.Post{ChannelId: t.channelID, RootId: t.rootID, Message: text})
		m.ui(func() {
			if err != nil {
				m.fail("send reply", fmt.Errorf("reply to thread %s: %w", t.rootID, err))
				if m.thread == t {
					m.panel.composer.Restore(text)
				}
				return
			}
			m.applyPosted(p, false)
		})
	}()
}

// applyToThread кладёт пост в открытый тред, если он оттуда.
func (m *MainWindow) applyToThread(p *model.Post) {
	t := m.thread
	if t == nil || threadRoot(p) != t.rootID {
		return
	}
	// Пока тред грузится, пост ляжет в tl и сольётся с ответом сервера.
	t.tl.Upsert(p)
	if !t.tl.Loaded {
		return
	}
	m.withUsers([]string{p.UserId}, func() {
		if m.thread == t {
			m.panel.feed.Upsert(p)
		}
	})
	if m.st.CRT() && p.UserId != m.s.Me.Id && m.w.IsActiveWindow() {
		m.markThreadRead(t.rootID, p.ChannelId)
	}
}

// markThreadRead отмечает тред прочитанным локально и на сервере (CRT).
func (m *MainWindow) markThreadRead(rootID, channelID string) {
	if m.st.ApplyThreadRead(rootID, 0, 0) {
		m.refreshThreads()
	}
	team := m.st.TeamFor(channelID)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, _, err := m.s.Client.UpdateThreadReadForUser(ctx, m.s.Me.Id, team, rootID, model.GetMillis())
		if err != nil {
			m.log.Warn("mark thread read", "thread", rootID, "team", team, "err", err)
		}
	}()
}

// Реакции

func (m *MainWindow) toggleReaction(p *model.Post, name string) {
	r := &model.Reaction{UserId: m.s.Me.Id, PostId: p.Id, EmojiName: name, ChannelId: p.ChannelId}
	remove := HasReaction(p, m.s.Me.Id, name)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var err error
		if remove {
			_, err = m.s.Client.DeleteReaction(ctx, r)
		} else {
			_, _, err = m.s.Client.SaveReaction(ctx, r)
		}
		m.ui(func() {
			if err != nil {
				m.fail("reaction", fmt.Errorf("reaction :%s: on post %s: %w", name, p.Id, err))
				return
			}
			// То же придёт по WebSocket; повтор ничего не изменит.
			m.applyReaction(p.ChannelId, r, !remove)
		})
	}()
}

func (m *MainWindow) applyReaction(channelID string, r *model.Reaction, added bool) {
	if p, _ := m.st.ApplyReaction(channelID, r, added); p != nil && channelID == m.current && m.feed.Has(p.Id) {
		m.withUsers([]string{r.UserId}, func() { m.feed.Upsert(p) })
	}
	if t := m.thread; t != nil {
		if p := t.tl.Get(r.PostId); p != nil {
			store.WithReaction(p, r, added)
			if t.tl.Loaded {
				m.withUsers([]string{r.UserId}, func() {
					if m.thread == t {
						m.panel.feed.Upsert(p)
					}
				})
			}
		}
	}
}

// Список тредов (CRT)

func (m *MainWindow) showThreads() {
	m.current = threadsID
	m.center.SetCurrentWidget(m.threadsPage)
	m.refreshThreads()
	if m.OnChannelOpened != nil {
		m.OnChannelOpened(threadsID)
	}
}

// refreshThreads перерисовывает пункт «Треды» в сайдбаре и, если открыт, список.
func (m *MainWindow) refreshThreads() {
	m.refreshItem(threadsID)
	if m.current != threadsID {
		return
	}
	m.threadsList.Clear()
	threads := m.st.Threads()
	if len(threads) == 0 {
		m.threadsList.AddItem("Вы не подписаны ни на один тред.")
		return
	}
	for _, t := range threads {
		it := qt.NewQListWidgetItem2(m.threadItemText(t))
		it.SetData(threadIDRole, qt.NewQVariant11(t.PostId))
		if t.UnreadReplies > 0 || t.UnreadMentions > 0 {
			font := it.Font()
			font.SetBold(true)
			it.SetFont(font)
		}
		m.threadsList.AddItemWithItem(it)
	}
}

func (m *MainWindow) threadItemText(t *model.ThreadResponse) string {
	var head, snippet string
	if p := t.Post; p != nil {
		if ch := m.st.Channel(p.ChannelId); ch != nil {
			head = channelPrefix(ch) + m.st.Title(ch) + " · "
		}
		head += m.st.Username(p.UserId)
		snippet = firstLine(p.Message, 140)
	}
	tail := pluralReplies(t.ReplyCount) + " · последний " + formatTime(t.LastReplyAt)
	if t.UnreadMentions > 0 {
		tail += fmt.Sprintf("  (упоминаний: %d)", t.UnreadMentions)
	} else if t.UnreadReplies > 0 {
		tail += fmt.Sprintf("  (новых: %d)", t.UnreadReplies)
	}
	return head + "\n" + snippet + "\n" + tail
}

func firstLine(s string, n int) string {
	s, _, cut := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	} else if cut {
		return s + " …"
	}
	return s
}

// threadPostAuthors — авторы корней тредов, чтобы показать их имена.
func threadPostAuthors(ts []*model.ThreadResponse) []string {
	var ids []string
	for _, t := range ts {
		if t.Post != nil {
			ids = append(ids, t.Post.UserId)
		}
	}
	return ids
}
