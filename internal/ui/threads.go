package ui

import (
	"context"
	"fmt"
	"html"
	"slices"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// threadsID — псевдо-канал «Треды» в сайдбаре (только при CRT).
const threadsID = "@threads"

// Роль данных элемента списка тредов: id корня.
const threadIDRole = int(qt.UserRole) + 2

// ThreadPanel — правая панель с открытым тредом.
type ThreadPanel struct {
	w           *qt.QWidget
	head        *header
	feed        *Feed
	composer    *Composer
	unsubscribe func()
}

func NewThreadPanel(deps FeedDeps, onClose func()) *ThreadPanel {
	p := &ThreadPanel{w: qt.NewQWidget2()}
	p.head = newHeader("threadHeader", true, nil)
	p.head.close.OnClicked(onClose)
	p.feed = NewFeed(deps)
	p.composer = NewComposer()
	p.composer.SetPlaceholder("Ответить в треде")

	lay := qt.NewQVBoxLayout(p.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.SetSpacing(0)
	lay.AddWidget(p.head.f.QWidget)
	lay.AddWidget2(p.feed.Widget(), 1)
	lay.AddWidget(p.composer.Widget())
	p.w.SetVisible(false)
	p.unsubscribe = OnThemeChanged(p.restyle)
	p.restyle()
	return p
}

func (p *ThreadPanel) restyle() {
	p.head.close.SetIcon(icon("x", Pal().Tx2, 18))
}

func (p *ThreadPanel) dispose() {
	p.unsubscribe()
	p.feed.dispose()
	p.composer.dispose()
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
	m.panel.head.set(nil, m.threadTitleHTML(t), "")
	m.panel.feed.Loading()
	m.panel.feed.OnRetry = func() { m.openThread(rootID) }
	m.panel.w.SetVisible(true)
	m.panel.composer.SetEnabled(false)
	m.panel.composer.SwitchTo(rootID)
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
				m.panel.feed.Error(err.Error())
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
				m.panel.head.set(nil, m.threadTitleHTML(t), "")
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

// threadTitleHTML — «Тред · # Town Square» в шапке панели.
func (m *MainWindow) threadTitleHTML(t *threadView) string {
	s := "Тред"
	if ch := m.st.Channel(t.channelID); ch != nil {
		s += fmt.Sprintf(` <span style="font-size:13px;font-weight:400;color:%s">· %s</span>`, Pal().Tx2, html.EscapeString(channelPrefix(ch)+m.st.Title(ch)))
	}
	return s
}

func (m *MainWindow) closeThread() {
	m.thread = nil
	m.panel.w.SetVisible(false)
}

func (m *MainWindow) sendReply(text string, fileIDs []string) {
	t := m.thread
	if t == nil || t.channelID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		p, _, err := m.s.Client.CreatePost(ctx, &model.Post{ChannelId: t.channelID, RootId: t.rootID, Message: text, FileIds: fileIDs})
		m.ui(func() {
			if err != nil {
				m.fail("send reply", fmt.Errorf("reply to thread %s: %w", t.rootID, err))
				m.panel.composer.Restore()
				return
			}
			m.panel.composer.Sent()
			m.applyPosted(store.NewPost{Post: p})
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
			if !remove {
				m.useEmoji(name)
			}
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
	m.refreshHeader()
	m.refreshThreads()
	if m.OnChannelOpened != nil {
		m.OnChannelOpened(threadsID)
	}
}

// setupThreadsList — список тредов (QListWidget#threads): строки рисует
// делегат, фон выбора и разделители — QSS.
func (m *MainWindow) setupThreadsList() {
	l := m.threadsList
	setName(l.QObject, "threads")
	l.SetFrameShape(qt.QFrame__NoFrame)
	l.SetUniformItemSizes(true)
	l.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	d := qt.NewQStyledItemDelegate2(l.QObject)
	d.OnPaint(func(super func(*qt.QPainter, *qt.QStyleOptionViewItem, *qt.QModelIndex), p *qt.QPainter, opt *qt.QStyleOptionViewItem, idx *qt.QModelIndex) {
		super(p, opt, idx)
		if it := l.ItemFromIndex(idx); it != nil {
			m.paintThreadRow(p, opt, it.Data(threadIDRole).ToString())
		}
	})
	d.OnSizeHint(func(super func(*qt.QStyleOptionViewItem, *qt.QModelIndex) *qt.QSize, opt *qt.QStyleOptionViewItem, idx *qt.QModelIndex) *qt.QSize {
		return qt.NewQSize2(opt.Rect().Width(), 80)
	})
	l.SetItemDelegate(d.QAbstractItemDelegate)
	l.OnItemClicked(func(it *qt.QListWidgetItem) {
		if id := it.Data(threadIDRole).ToString(); id != "" {
			m.openThread(id)
		}
	})
}

// refreshThreads перерисовывает пункт «Треды» в сайдбаре и, если открыт, список.
func (m *MainWindow) refreshThreads() {
	m.refreshItem(threadsID)
	if m.current != threadsID {
		return
	}
	selected := ""
	if it := m.threadsList.CurrentItem(); it != nil {
		selected = it.Data(threadIDRole).ToString()
	}
	m.threadsList.Clear()
	threads := m.st.Threads()
	if len(threads) == 0 {
		it := qt.NewQListWidgetItem()
		it.SetFlags(qt.NoItemFlags)
		m.threadsList.AddItemWithItem(it)
		return
	}
	for _, t := range threads {
		it := qt.NewQListWidgetItem()
		it.SetData(threadIDRole, qt.NewQVariant11(t.PostId))
		m.threadsList.AddItemWithItem(it)
		if t.PostId == selected {
			m.threadsList.SetCurrentItem(it)
		}
	}
}

func (m *MainWindow) paintThreadRow(p *qt.QPainter, opt *qt.QStyleOptionViewItem, id string) {
	pal := Pal()
	r := opt.Rect()
	x, y, w := r.X()+16, r.Y(), r.Width()-32
	text := func(s string, x, y, width, h int, f *qt.QFont, color string, align qt.AlignmentFlag) int {
		fm := qt.NewQFontMetrics(f)
		s = fm.ElidedText(s, qt.ElideRight, width)
		p.SetFont(f)
		p.SetPen(qt.NewQColor6(color))
		p.DrawText7(x, y, width, h, int(align|qt.AlignVCenter), s)
		return fm.HorizontalAdvance(s)
	}
	t := m.st.Thread(id)
	if t == nil {
		text("Тредов пока нет", r.X(), y, r.Width(), r.Height(), pixelFont(14, qt.QFont__Normal), pal.Tx2, qt.AlignHCenter)
		return
	}
	unread := t.UnreadReplies > 0 || t.UnreadMentions > 0
	small := pixelFont(12, qt.QFont__Normal)
	mono := pixelFont(12, qt.QFont__Normal)
	mono.SetFamilies([]string{monoFamily()})

	head, msg := "", ""
	if root := t.Post; root != nil {
		if ch := m.st.Channel(root.ChannelId); ch != nil {
			head = channelPrefix(ch) + m.st.Title(ch) + " · "
		}
		if in, ok := store.PostIntegration(root); ok && in.Name != "" {
			head += in.Name
		} else {
			head += m.st.Username(root.UserId)
		}
		msg = firstLine(root.Message, 200)
	}
	timeW := text(time.UnixMilli(t.LastReplyAt).Format("15:04"), x, y+10, w, 16, mono, pal.Tx2, qt.AlignRight)
	text(head, x, y+10, w-timeW-8, 16, small, pal.Tx2, qt.AlignLeft)
	weight := qt.QFont__Normal
	if unread {
		weight = qt.QFont__DemiBold
	}
	text(msg, x, y+28, w, 22, pixelFont(14, weight), pal.Tx, qt.AlignLeft)

	meta, metaColor := pluralReplies(t.ReplyCount)+" · последний "+time.UnixMilli(t.LastReplyAt).Format("15:04"), pal.Tx2
	mx := x
	if unread {
		metaColor = pal.Acc
		p.Save()
		p.SetRenderHint(qt.QPainter__Antialiasing)
		p.SetPenWithStyle(qt.NoPen)
		p.SetBrush(qt.NewQBrush3(qt.NewQColor6(pal.Acc)))
		p.DrawEllipse2(mx, y+55, 6, 6)
		p.Restore()
		mx += 12
	}
	right := x + w
	if t.UnreadMentions > 0 {
		badge := fmt.Sprintf("@ %d", t.UnreadMentions)
		bf := pixelFont(11, qt.QFont__DemiBold)
		bw := qt.NewQFontMetrics(bf).HorizontalAdvance(badge) + 14
		p.Save()
		p.SetRenderHint(qt.QPainter__Antialiasing)
		p.SetPenWithStyle(qt.NoPen)
		p.SetBrush(qt.NewQBrush3(qt.NewQColor6(pal.Acc)))
		p.DrawRoundedRect(qt.NewQRectF4(float64(right-bw), float64(y+49), float64(bw), 18), 9, 9)
		p.SetFont(bf)
		p.SetPen(qt.NewQColor6(pal.OnAcc))
		p.DrawText7(right-bw, y+49, bw, 18, int(qt.AlignCenter), badge)
		p.Restore()
		right -= bw + 8
	}
	text(meta, mx, y+50, right-mx, 16, small, metaColor, qt.AlignLeft)
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
