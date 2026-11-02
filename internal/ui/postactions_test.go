package ui

import (
	"io"
	"log/slog"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// hoverPost — курсор над первой строкой i-го поста ленты: событие и место
// курсора (по нему кнопка ищет пост после прокрутки и правок).
func hoverPost(f *Feed, i int) {
	c := qt.NewQTextCursor2(f.doc)
	c.SetPosition(f.items[i].frame.FirstPosition())
	r := f.cursorRect(c)
	at := qt.NewQPoint2(feedAvatarCol+40, r.Top()+r.Height()/2)
	vp := f.view.Viewport()
	qt.QCursor_SetPosWithQPoint(vp.MapToGlobalWithQPoint(at))
	ev := qt.NewQMouseEvent(qt.QEvent__MouseMove, qt.NewQPointF3(float64(at.X()), float64(at.Y())), qt.NoButton, qt.NoButton, qt.NoModifier)
	qt.QCoreApplication_SendEvent(vp.QObject, ev.QEvent)
}

func TestReplyButton(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		shot(t, m, "reply-button-before") // размер окна как на скриншотах
		f := m.feed
		last := len(f.items) - 1
		for last >= 0 && !f.actions.wants(f.items[last].post) {
			last--
		}
		if last < 0 {
			t.Error("no plain post without a thread in town-square")
			return
		}
		p := f.items[last].post
		// Системное сообщение — без кнопки.
		if sys := len(f.items) - 1; sys != last && f.items[sys].post.IsSystemMessage() {
			hoverPost(f, sys)
			if f.actions.w.IsVisible() {
				t.Error("reply button over a system message")
			}
		}

		// Наведение на сообщение без треда — кнопка справа, у его первой строки.
		hoverPost(f, last)
		a := f.actions
		if !a.w.IsVisible() || a.post != p.Id {
			t.Errorf("no reply button over the last plain post (visible %v, post %q)", a.w.IsVisible(), a.post)
			return
		}
		g, vw := a.w.Geometry(), f.view.Viewport().Width()
		if g.X()+g.Width() != vw-feedPadRight || g.Y() < f.postTop(last)-g.Height() || g.Y() > f.postTop(last)+g.Height() {
			t.Errorf("reply button at %d,%d %dx%d; viewport %d wide, post top %d", g.X(), g.Y(), g.Width(), g.Height(), vw, f.postTop(last))
		}
		shotWidget(t, m.w, "reply-button")

		// У корня с ответами есть «N ответов» — кнопки нет.
		f.ScrollToPost(root.Id)
		settle()
		hoverPost(f, f.index(root.Id))
		if a.w.IsVisible() {
			t.Error("reply button over a post that already has a thread")
		}

		// Курсор ушёл из ленты — кнопка прячется.
		f.bar.SetValue(f.bar.Maximum())
		settle()
		hoverPost(f, last)
		qt.QCoreApplication_SendEvent(f.view.QObject, qt.NewQEvent(qt.QEvent__Leave))
		if a.w.IsVisible() {
			t.Error("reply button stays after the cursor left the feed")
		}

		// Клик — тред этого сообщения в правой панели, с полем ввода.
		hoverPost(f, last)
		a.reply.Click()
		if !waitFor(t, "thread of the last post", func() bool {
			return m.thread != nil && m.thread.rootID == p.Id && m.panel.w.IsVisible() && len(m.panel.feed.items) == 1
		}) {
			return
		}
		// В ленте треда кнопки нет.
		hoverPost(m.panel.feed, 0)
		if m.panel.feed.actions.w.IsVisible() {
			t.Error("reply button in the thread panel")
		}
	})
}
