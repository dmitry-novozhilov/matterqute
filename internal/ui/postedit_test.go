package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
)

func TestEditDeletePost(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		c := m.composer
		key := func(c *Composer, k qt.Key) {
			qt.QCoreApplication_SendEvent(c.edit.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(k), qt.NoModifier).QEvent)
		}
		last := func(f *Feed) *model.Post { return f.items[len(f.items)-1].post }

		// Своё сообщение с оформлением.
		c.setBlocks(markdown.ToBlocks("Релиз **завтра** в `10:00`"))
		c.send()
		if !waitFor(t, "own post", func() bool { return last(m.feed).Message == "Релиз **завтра** в `10:00`" }) {
			return
		}
		mine := last(m.feed)

		// ↑ в пустом поле — правка последнего своего, с оформлением; Enter — на сервер.
		key(c, qt.Key_Up)
		if c.Editing() != mine.Id || !c.editBar.IsVisibleTo(c.w) || !strings.Contains(c.editText.Text(), "Релиз завтра в 10:00") {
			t.Errorf("Up: editing %q, bar %v %q", c.Editing(), c.editBar.IsVisibleTo(c.w), c.editText.Text())
			return
		}
		if b := readBlocks(c.edit.Document()); len(b) != 1 || len(b[0].Spans) != 4 || !b[0].Spans[1].Bold || !b[0].Spans[3].Code {
			t.Errorf("message in the editor: %+v", b)
		}
		shotWidget(t, m.w, "edit-post")
		// Курсор в конце, после кода: дописанное — обычным текстом, и после
		// перехода курсором туда же.
		key(c, qt.Key_Home)
		key(c, qt.Key_End)
		c.edit.InsertPlainText(", не сегодня")
		key(c, qt.Key_Return)
		if c.Editing() != "" || !c.empty() {
			t.Errorf("after saving: editing %q, empty %v", c.Editing(), c.empty())
		}
		want := "Релиз **завтра** в `10:00`, не сегодня"
		if !waitFor(t, "edited post", func() bool {
			p := feedPost(m.feed, mine.Id)
			return p != nil && p.Message == want && p.EditAt > 0 && strings.Contains(m.feed.items[m.feed.index(mine.Id)].html, "(изменено)")
		}) {
			return
		}

		// Черновик откладывается на время правки и возвращается по Esc.
		c.setBlocks(markdown.ToBlocks("недописанное *важное*"))
		key(c, qt.Key_Up)
		if c.Editing() != "" {
			t.Error("Up in a non-empty composer started editing")
		}
		m.editPost(m.feed, feedPost(m.feed, mine.Id))
		if got := markdown.FromBlocks(readBlocks(c.edit.Document())); got != want {
			t.Errorf("editor holds %q, want the message", got)
		}
		key(c, qt.Key_Escape)
		if got := markdown.FromBlocks(readBlocks(c.edit.Document())); c.Editing() != "" || got != "недописанное *важное*" {
			t.Errorf("after Esc: editing %q, draft %q", c.Editing(), got)
		}
		c.setBlocks(nil)

		// Стёр весь текст и сохранил — спросить об удалении.
		m.editPost(m.feed, feedPost(m.feed, mine.Id))
		c.setBlocks(nil)
		key(c, qt.Key_Return)
		if d := m.deleteDialog; d == nil || d.closed || !d.w.IsVisible() {
			t.Error("empty edit did not ask to delete")
		} else {
			labelled(d.w.QObject, "Отмена").Click()
		}

		// Удаление с подтверждением: из ленты и с сервера.
		m.confirmDelete(feedPost(m.feed, mine.Id))
		shotWidget(t, m.deleteDialog.w.QWidget, "delete-post")
		labelled(m.deleteDialog.w.QObject, "Удалить").Click()
		waitFor(t, "post deleted", func() bool { return feedPost(m.feed, mine.Id) == nil })

		// В треде: правка и удаление ответа — в поле и ленте треда; у корня в
		// канале счётчик ответов уменьшается.
		m.openThread(root.Id)
		if !waitFor(t, "thread", func() bool { return len(m.panel.feed.items) == 4 }) {
			return
		}
		m.sendReply("мой ответ", nil, "")
		if !waitFor(t, "own reply", func() bool { return last(m.panel.feed).Message == "мой ответ" }) {
			return
		}
		reply := last(m.panel.feed)
		waitFor(t, "4 replies", func() bool { p := feedPost(m.feed, root.Id); return p != nil && p.ReplyCount == 4 })
		pc := m.panel.composer
		key(pc, qt.Key_Up)
		if pc.Editing() != reply.Id || c.Editing() != "" {
			t.Errorf("Up in the thread: thread composer edits %q, channel composer %q", pc.Editing(), c.Editing())
		}
		pc.setBlocks(markdown.ToBlocks("мой ответ, ~~поправленный~~"))
		key(pc, qt.Key_Return)
		waitFor(t, "edited reply", func() bool {
			p := feedPost(m.panel.feed, reply.Id)
			return p != nil && p.Message == "мой ответ, ~~поправленный~~"
		})
		m.confirmDelete(feedPost(m.panel.feed, reply.Id))
		labelled(m.deleteDialog.w.QObject, "Удалить").Click()
		waitFor(t, "reply deleted", func() bool {
			p := feedPost(m.feed, root.Id)
			return feedPost(m.panel.feed, reply.Id) == nil && p != nil && p.ReplyCount == 3
		})
	})
}

func TestEditable(t *testing.T) {
	me := "me"
	hook := &model.Post{UserId: me}
	hook.AddProp(model.PostPropsFromWebhook, "true")
	for _, c := range []struct {
		p    *model.Post
		want bool
	}{
		{&model.Post{UserId: me, Message: "x"}, true},
		{&model.Post{UserId: "bob", Message: "x"}, false},
		{&model.Post{UserId: me, Type: model.PostTypeJoinChannel}, false},
		{hook, false},
	} {
		if got := editable(c.p, me); got != c.want {
			t.Errorf("editable(%+v) = %v, want %v", c.p, got, c.want)
		}
	}
}
