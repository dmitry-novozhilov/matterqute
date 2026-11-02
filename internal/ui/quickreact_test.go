package ui

import (
	"io"
	"log/slog"
	"slices"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/emoji"
)

// hoverPost — курсор над i-м постом ленты (ниже его верхней границы):
// событие и место курсора (по нему панель ищет пост после прокрутки и правок).
func hoverPost(f *Feed, i int) {
	at := qt.NewQPoint2(feedAvatarCol+40, f.postTop(i)+10)
	vp := f.view.Viewport()
	qt.QCursor_SetPosWithQPoint(vp.MapToGlobalWithQPoint(at))
	ev := qt.NewQMouseEvent(qt.QEvent__MouseMove, qt.NewQPointF3(float64(at.X()), float64(at.Y())), qt.NoButton, qt.NoButton, qt.NoModifier)
	qt.QCoreApplication_SendEvent(vp.QObject, ev.QEvent)
}

func TestQuickReactions(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		shot(t, m, "quick-before") // размер окна как на скриншотах
		f := m.feed
		q := f.quick
		last := len(f.items) - 1
		for last >= 0 && f.items[last].post.IsSystemMessage() {
			// Системное сообщение — без панели.
			hoverPost(f, last)
			if q.w.IsVisible() {
				t.Error("quick reactions over a system message")
			}
			last--
		}
		p := f.items[last].post

		// Наведение: 10 недавних (пока своих нет — быстрые по умолчанию) и «все реакции».
		hoverPost(f, last)
		if !q.w.IsVisible() || q.post != p.Id {
			t.Errorf("no quick reactions over the last post (visible %v, post %q)", q.w.IsVisible(), q.post)
			return
		}
		if want := quickReactions(m.recent); len(q.btns) != quickReactCount || !slices.Equal(q.names, want) {
			t.Errorf("quick reactions %v, want %v", q.names, want)
		}
		if g, vw := q.w.Geometry(), f.view.Viewport().Width(); g.X()+g.Width() != vw-feedPadRight || abs(g.Y()+g.Height()/2-f.postTop(last)) > 1 {
			t.Errorf("bar at %d,%d %dx%d; viewport %d wide, post top %d", g.X(), g.Y(), g.Width(), g.Height(), vw, f.postTop(last))
		}
		waitFor(t, "recent emoji loaded", func() bool { return m.emojiLoaded })
		settle()
		// Панель по размеру всех кнопок — и после перерисовки набора в видимой панели.
		if w := q.w.Width(); w < (quickReactCount+1)*quickButton {
			t.Errorf("bar is %d px wide for %d buttons", w, quickReactCount+1)
		}
		q.setNames(nil)
		hoverPost(f, last)
		if w := q.w.Width(); w < (quickReactCount+1)*quickButton {
			t.Errorf("bar rebuilt while visible is %d px wide", w)
		}
		shotWidget(t, m.w, "quick-reactions")

		// Клик — реакция; она отмечена, попала в недавние; второй клик снимает.
		name := q.names[3]
		q.btns[3].Click()
		if !waitFor(t, "reaction "+name, func() bool {
			fp := feedPost(f, p.Id)
			return fp != nil && HasReaction(fp, m.s.Me.Id, name)
		}) {
			return
		}
		hoverPost(f, last)
		if i := slices.Index(q.names, name); i != 0 || !q.btns[i].IsChecked() {
			t.Errorf("used reaction :%s: is at %d in %v, checked %v; want first and checked", name, i, q.names, i >= 0 && q.btns[i].IsChecked())
		}
		if got := m.recent.Names(); len(got) == 0 || got[0] != name {
			t.Errorf("recent emoji %v, want %s first", got, name)
		}
		q.btns[0].Click()
		waitFor(t, "reaction removed", func() bool {
			fp := feedPost(f, p.Id)
			return fp != nil && !HasReaction(fp, m.s.Me.Id, name)
		})

		// «Все реакции» — окно выбора.
		hoverPost(f, last)
		q.more.Click()
		if m.picker == nil || m.picker.closed || !m.picker.w.IsVisible() {
			t.Error("«all reactions» did not open the emoji picker")
		} else {
			m.picker.w.Close()
			settle()
		}

		// Курсор ушёл из ленты — панель прячется.
		hoverPost(f, last)
		qt.QCoreApplication_SendEvent(f.view.QObject, qt.NewQEvent(qt.QEvent__Leave))
		if q.w.IsVisible() {
			t.Error("quick reactions stay after the cursor left the feed")
		}

		// В панели треда — тоже.
		m.openThread(root.Id)
		if !waitFor(t, "thread", func() bool { return len(m.panel.feed.items) == 4 }) {
			return
		}
		hoverPost(m.panel.feed, 1)
		if pq := m.panel.feed.quick; !pq.w.IsVisible() || pq.post != m.panel.feed.items[1].post.Id || len(pq.btns) != quickReactCount {
			t.Errorf("quick reactions in the thread panel: visible %v, buttons %d", pq.w.IsVisible(), len(pq.btns))
		}
		shotWidget(t, m.w, "quick-reactions-thread")
	})
}

func TestQuickReactionsList(t *testing.T) {
	// Недавних нет — быстрые по умолчанию; мало — добавляются быстрые; много — первые 10.
	if got := quickReactions(nil); !slices.Equal(got, emoji.Quick[:quickReactCount]) {
		t.Errorf("no recent: %v", got)
	}
	r := emoji.Recent{}.Add("partyparrot").Add("smile").Add("smile")
	got := quickReactions(r)
	if len(got) != quickReactCount || got[0] != "smile" || got[1] != "partyparrot" || got[2] != emoji.Quick[0] {
		t.Errorf("two recent: %v", got)
	}
	var many emoji.Recent
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		many = many.Add(n)
	}
	if got := quickReactions(many); len(got) != quickReactCount || got[0] != "l" {
		t.Errorf("many recent: %v", got)
	}
}
