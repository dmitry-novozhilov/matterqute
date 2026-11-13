package ui

import (
	"context"
	"fmt"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
	"matterqute/internal/sched"
)

// Правка и удаление своих сообщений. Правка — в поле ввода той же колонки
// (лента канала — поле канала, тред — поле треда), с оформлением: сообщение
// разбирается в абзацы редактора (markdown.ToBlocks).

// composerFor — поле ввода колонки ленты f.
func (m *MainWindow) composerFor(f *Feed) *Composer {
	if m.panel != nil && f == m.panel.feed {
		return m.panel.composer
	}
	return m.composer
}

// editPost — «Изменить» у своего сообщения.
func (m *MainWindow) editPost(f *Feed, p *model.Post) {
	m.composerFor(f).StartEdit(p.Id, editPreview(p), markdown.ToBlocks(p.Message))
}

func editPreview(p *model.Post) string {
	return firstLine(markdown.PlainText(p.Message), 80)
}

// editLast — ↑ в пустом поле: последнее своё сообщение в ленте f.
func (m *MainWindow) editLast(f *Feed) {
	for i := len(f.items) - 1; i >= 0; i-- {
		if p := f.items[i].post; editable(p, m.s.Me.Id) {
			m.editPost(f, p)
			return
		}
	}
}

// saveEdit — правка из поля ленты f уходит на сервер; не ушла — снова в поле.
// Стёртый текст — значит, удалить (с подтверждением).
func (m *MainWindow) saveEdit(f *Feed) func(id, text string, blocks []markdown.Block) {
	return func(id, text string, blocks []markdown.Block) {
		if sp := m.sched.find(id); sp != nil { // текст отложенного
			if text == "" {
				m.sched.remove(id, nil)
			} else {
				m.sched.update(id, func(p *sched.Post) { p.Message = text })
			}
			return
		}
		p := f.post(id)
		switch {
		case text == "":
			if p != nil {
				m.confirmDelete(p)
			}
			return
		case p != nil && p.Message == text:
			return // ничего не поменяли
		}
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			edited, _, err := m.s.Client.PatchPost(ctx, id, &model.PostPatch{Message: &text})
			m.ui(func() {
				if err != nil {
					m.fail("edit post", fmt.Errorf("edit post %s: %w", id, err))
					preview := ""
					if p != nil {
						preview = editPreview(p)
					}
					m.composerFor(f).StartEdit(id, preview, blocks)
					return
				}
				m.applyEdited(edited) // то же придёт по WebSocket; повтор ничего не изменит
			})
		}()
	}
}

// confirmDelete — «Удалить…»: спросить и удалить.
func (m *MainWindow) confirmDelete(p *model.Post) {
	d := newDialog(m.w, "Удалить сообщение")
	m.deleteDialog = d
	text := "Удалить сообщение? Отменить это нельзя."
	if p.RootId == "" && p.ReplyCount > 0 {
		text += fmt.Sprintf(" Ответы в треде (%d) тоже удалятся.", p.ReplyCount)
	}
	label := qt.NewQLabel3(text)
	label.SetWordWrap(true)
	d.lay.AddWidget(label.QWidget)
	if preview := editPreview(p); preview != "" {
		quote := qt.NewQLabel3(preview)
		quote.SetTextFormat(qt.PlainText)
		quote.SetProperty("role", tmp(qt.NewQVariant11("hint")))
		d.lay.AddWidget(quote.QWidget)
	}
	danger(d.buttons("Удалить", func() { m.deletePost(p) }))
	d.w.Show()
}

func (m *MainWindow) deletePost(p *model.Post) {
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		_, err := m.s.Client.DeletePost(ctx, p.Id)
		m.ui(func() {
			if err != nil {
				m.fail("delete post", fmt.Errorf("delete post %s: %w", p.Id, err))
				return
			}
			m.applyDeleted(p) // то же придёт по WebSocket; повтор ничего не изменит
		})
	}()
}

// applyDeleted — пост удалён: прочь из ленты и треда, счётчик ответов корня
// меньше; правка этого поста в поле ввода отменяется.
func (m *MainWindow) applyDeleted(p *model.Post) {
	removed, root, first := m.st.ApplyDeleted(p)
	if !first {
		return
	}
	if f := m.feeds[p.ChannelId]; f != nil {
		if removed {
			f.Remove(p.Id)
		}
		if root != nil && f.Has(root.Id) {
			f.Upsert(root)
		}
	}
	if t := m.thread; t != nil {
		switch {
		case p.Id == t.rootID:
			m.closeThread()
		case t.tl.Remove(p.Id):
			m.panel.feed.Remove(p.Id)
			if r := t.tl.Get(t.rootID); r != nil && r != root && r.ReplyCount > 0 {
				r.ReplyCount--
			}
			if r := t.tl.Get(t.rootID); r != nil {
				m.panel.feed.Upsert(r)
			}
		}
	}
	for _, c := range []*Composer{m.composer, m.panel.composer} {
		if c.Editing() == p.Id {
			c.CancelEdit()
		}
	}
}
