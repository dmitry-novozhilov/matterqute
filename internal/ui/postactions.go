package ui

import (
	"fmt"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// postActions — кнопка «Ответить в треде» у сообщения под курсором: в ленте
// канала или лички, у сообщения, из которого тред ещё не начат (у остальных
// есть ссылка «N ответов» или «Ответ в треде»). Виджеты в документ не
// встраиваются, поэтому это ребёнок viewport ленты у правого края первой
// строки поста — как ролики (placeVideos).
type postActions struct {
	f     *Feed
	w     *qt.QFrame
	reply *qt.QToolButton
	post  string // id поста, у которого показана; пусто — спрятана
}

func newPostActions(f *Feed) *postActions {
	a := &postActions{f: f, w: qt.NewQFrame(f.view.Viewport())}
	setName(a.w.QObject, "postActions")
	lay := qt.NewQHBoxLayout(a.w.QWidget)
	lay.SetContentsMargins(2, 2, 2, 2)
	lay.SetSpacing(0)
	a.reply = qt.NewQToolButton2()
	setName(a.reply.QObject, "replyInThread")
	a.reply.SetFixedSize2(28, 24)
	a.reply.SetIconSize(qt.NewQSize2(16, 16))
	a.reply.SetFocusPolicy(qt.NoFocus)
	a.reply.SetToolTip("Ответить в треде")
	a.reply.OnClicked(func() {
		if id := a.post; id != "" {
			a.hide()
			f.openThread(id)
		}
	})
	lay.AddWidget(a.reply.QWidget)
	a.restyle()
	a.w.Hide()
	return a
}

func (a *postActions) restyle() {
	pal := Pal()
	a.w.SetStyleSheet(fmt.Sprintf(`QFrame#postActions { background: %s; border: 1px solid %s; border-radius: 6px; }
QToolButton { border: none; border-radius: 4px; background: transparent; min-width: 0; min-height: 0; }
QToolButton:hover { background: %s; }`, pal.Surf, pal.Line2, pal.Hover))
	a.reply.SetIcon(icon("reply", pal.Tx2, 16))
	a.w.AdjustSize()
}

// wants — показывать ли кнопку у поста: лента канала, пост — не ответ, ответов
// у него нет и он не системный.
func (a *postActions) wants(p *model.Post) bool {
	return !a.f.d.Thread && p.Id != "" && p.RootId == "" && p.ReplyCount == 0 && !p.IsSystemMessage()
}

// hover — курсор в точке pos viewport: кнопка у поста под ним или прочь.
func (a *postActions) hover(pos *qt.QPoint) {
	it := a.f.postAtY(pos.Y())
	if it == nil || !a.wants(it.post) {
		a.hide()
		return
	}
	a.post = it.post.Id
	a.place()
}

// follow — лента прокрутилась или изменилась: кнопка — у поста, который
// теперь под курсором; курсор ушёл из ленты — прочь.
func (a *postActions) follow() {
	if a.post == "" {
		return
	}
	vp := a.f.view.Viewport()
	pos := vp.MapFromGlobalWithQPoint(qt.QCursor_Pos())
	if !vp.Rect().ContainsWithQPoint(pos) {
		a.hide()
		return
	}
	a.hover(pos)
}

// place — справа, по середине первой строки поста.
func (a *postActions) place() {
	i := a.f.index(a.post)
	if i < 0 || !a.wants(a.f.items[i].post) {
		a.hide()
		return
	}
	c := qt.NewQTextCursor2(a.f.doc)
	c.SetPosition(a.f.items[i].frame.FirstPosition())
	line := a.f.cursorRect(c)
	size := a.w.SizeHint()
	a.w.Move(a.f.view.Viewport().Width()-size.Width()-feedPadRight, max(0, line.Top()+(line.Height()-size.Height())/2))
	a.w.Raise()
	a.w.Show()
}

func (a *postActions) hide() {
	a.post = ""
	a.w.Hide()
}
