package ui

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"slices"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
	"matterqute/internal/store"
)

// «Черновики» — псевдо-канал в сайдбаре, пока есть хоть один черновик (как
// «Drafts» веб-клиента): все недописанные сообщения по каналам и тредам —
// где, когда правили, начало текста; «Открыть» — в поле ввода своего канала
// или треда, «Отправить» (только текст: файлы черновика — из «Открыть»),
// «Удалить». Черновики лежат в полях ввода (канала и треда), отсюда и список.

// draftsID — псевдо-канал «Черновики» в сайдбаре.
const draftsID = "@drafts"

// draftItem — черновик для списка.
type draftItem struct {
	k     draftKey
	text  string
	files bool
	at    time.Time
}

// draftList — все черновики, свежие сверху. Открытый сейчас канал (и тред)
// тоже: веб-клиент показывает и их.
func (m *MainWindow) draftList() []draftItem {
	var out []draftItem
	seen := map[draftKey]bool{}
	for _, c := range []*Composer{m.composer, m.panel.composer} {
		for _, key := range c.DraftKeys() {
			k, ok := m.drafts.keyOf(c, key)
			if !ok || seen[k] {
				continue
			}
			text, files := c.DraftText(key), c.DraftFiles(key)
			if text == "" && !files {
				continue
			}
			seen[k] = true
			out = append(out, draftItem{k: k, text: text, files: files, at: m.drafts.edited[k]})
		}
	}
	slices.SortStableFunc(out, func(a, b draftItem) int { return cmp.Compare(b.at.UnixMilli(), a.at.UnixMilli()) })
	return out
}

// draftCount — сколько черновиков; дёшево (без разбора текста): зовут на
// каждую правку в поле.
func (m *MainWindow) draftCount() int {
	n := 0
	seen := map[draftKey]bool{}
	for _, c := range []*Composer{m.composer, m.panel.composer} {
		for _, key := range c.DraftKeys() {
			if k, ok := m.drafts.keyOf(c, key); ok && !seen[k] && c.DraftNonEmpty(key) {
				seen[k] = true
				n++
			}
		}
	}
	return n
}

// draftsChanged — черновики поменялись: строка в сайдбаре (появилась,
// пропала, число) и открытая страница списка.
func (m *MainWindow) draftsChanged() {
	has := m.draftCount() > 0
	switch {
	case has != (m.items[draftsID] != nil):
		if !has && m.current == draftsID {
			m.current = ""
		}
		m.rebuildSidebarSoon()
	default:
		m.sidebar.Viewport().Update()
	}
	if m.current == draftsID {
		m.renderDrafts()
	}
}

// showDrafts — страница «Черновики» (та же, что у отложенных).
func (m *MainWindow) showDrafts() {
	m.current = draftsID
	m.center.SetCurrentWidget(m.schedPage)
	m.refreshHeader()
	m.renderDrafts()
}

// renderDrafts — список черновиков: где, когда, текст, действия.
func (m *MainWindow) renderDrafts() {
	pal := Pal()
	note := "Недописанные сообщения. Хранит сервер — они общие с веб-клиентом."
	if !m.drafts.server {
		note = "Недописанные сообщения. Сервер их не хранит — они только в этом приложении."
	}
	m.schedHead.set(iconPixmap("pencil", pal.Tx2, 16), "Черновики", html.EscapeString(note))
	lay := m.schedRows
	for lay.Count() > 0 {
		if it := lay.TakeAt(0); it != nil && it.Widget() != nil {
			it.Widget().DeleteLater()
		}
	}
	items := m.draftList()
	now := time.Now()
	for _, d := range items {
		lay.AddWidget(m.draftRow(d, now))
	}
	if len(items) == 0 {
		empty := qt.NewQLabel3("Черновиков нет")
		empty.SetAlignment(qt.AlignCenter)
		lay.AddWidget(empty.QWidget)
	}
	lay.AddStretch()
}

func (m *MainWindow) draftRow(d draftItem, now time.Time) *qt.QWidget {
	pal := Pal()
	f := qt.NewQFrame2()
	setName(f.QObject, "schedRow")
	f.SetStyleSheet(fmt.Sprintf(`QFrame#schedRow { border-bottom: 1px solid %s; }
QPushButton { border: none; background: transparent; color: %s; padding: 2px 4px; min-height: 0; }
QPushButton:hover { color: %s; text-decoration: underline; }`, pal.Line, pal.Acc, pal.AccH))
	col := qt.NewQVBoxLayout(f.QWidget)
	col.SetContentsMargins(16, 10, 16, 8)
	col.SetSpacing(4)

	where := "?"
	if ch := m.st.Channel(d.k.channel); ch != nil {
		where = channelPrefix(ch) + m.st.Title(ch)
	}
	if d.k.root != "" {
		where += " · в треде"
	}
	head := fmt.Sprintf(`<span style="color:%s">%s</span>`, pal.Tx2, html.EscapeString(where))
	if !d.at.IsZero() {
		head += fmt.Sprintf(` · <span style="color:%s">%s</span>`, pal.Tx2, html.EscapeString(shortWhen(d.at, now)))
	}
	if d.files {
		head += fmt.Sprintf(` · <span style="color:%s">с файлами</span>`, pal.Tx2)
	}
	top := qt.NewQLabel3(head)
	top.SetTextFormat(qt.RichText)
	top.SetFont(pixelFont(12, qt.QFont__Normal))
	col.AddWidget(top.QWidget)
	body := qt.NewQLabel3(markdown.PreviewLine(d.text, markdown.Options{Me: m.s.Me.Username, Palette: pal}))
	body.SetTextFormat(qt.RichText)
	body.SetFont(feedFont())
	col.AddWidget(body.QWidget)

	acts := qt.NewQHBoxLayout2()
	acts.SetSpacing(4)
	act := func(title string, do func()) {
		b := qt.NewQPushButton3(title)
		b.SetCursor(tmp(qt.NewQCursor2(qt.PointingHandCursor)))
		b.SetFocusPolicy(qt.NoFocus)
		b.OnClicked(do)
		acts.AddWidget(b.QWidget)
	}
	k := d.k
	act("Открыть", func() { m.openDraft(k) })
	if !d.files && d.text != "" {
		act("Отправить", func() { m.sendDraft(k) })
	}
	act("Удалить", func() { m.deleteDraft(k) })
	acts.AddStretch()
	col.AddLayout(acts.QLayout)
	return f.QWidget
}

// openDraft — черновик в его поле ввода: канал (и тред), курсор в поле.
func (m *MainWindow) openDraft(k draftKey) {
	m.selectChannel(k.channel)
	if k.root != "" {
		m.openThread(k.root)
		m.panel.composer.Focus()
		return
	}
	m.composer.Focus()
}

// sendDraft — отправить черновик как есть (только текст).
func (m *MainWindow) sendDraft(k draftKey) {
	c, key := m.drafts.composer(k)
	text := c.DraftText(key)
	if text == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		p, _, err := m.s.Client.CreatePost(ctx, &model.Post{ChannelId: k.channel, RootId: k.root, Message: text})
		m.ui(func() {
			if err != nil {
				m.fail("send draft", fmt.Errorf("send draft to channel %s: %w", k.channel, err))
				return
			}
			m.deleteDraft(k)
			m.message("Отправлено")
			m.applyPosted(store.NewPost{Post: p})
		})
	}()
}

// deleteDraft — черновик прочь (и на сервере: сохранение пустого удаляет).
func (m *MainWindow) deleteDraft(k draftKey) {
	c, key := m.drafts.composer(k)
	c.ClearDraft(key)
	m.drafts.changed(c, key)
}
