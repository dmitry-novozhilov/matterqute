package ui

import (
	"fmt"
	"slices"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/emoji"
)

const (
	quickReactCount = 10 // сколько недавних реакций в панели у сообщения
	quickButton     = 26 // кнопка панели, px (и в QSS: иначе min-width темы)
)

// quickBar — панель реакций у сообщения под курсором: десять недавних (как
// «Недавние» в окне выбора — настройка recent_emojis, общая с веб-клиентом) и
// кнопка окна выбора из всех. Своя реакция отмечена, клик снимает её.
// Виджеты в документ не встраиваются, поэтому это ребёнок viewport ленты над
// правым верхним углом поста (как ролики, placeVideos); пост под курсором —
// postAtY.
type quickBar struct {
	f     *Feed
	w     *qt.QFrame
	lay   *qt.QHBoxLayout
	btns  []*qt.QToolButton
	names []string
	more  *qt.QToolButton
	post  string            // id поста, у которого показана; пусто — спрятана
	anims map[string]func() // подписки на кадры анимированных эмодзи, пока панель видна
}

func newQuickBar(f *Feed) *quickBar {
	q := &quickBar{f: f, w: qt.NewQFrame(f.view.Viewport()), anims: map[string]func(){}}
	setName(q.w.QObject, "quickBar")
	q.lay = qt.NewQHBoxLayout(q.w.QWidget)
	q.lay.SetContentsMargins(3, 2, 3, 2)
	q.lay.SetSpacing(0)
	q.more = q.button("Все реакции…")
	q.more.SetCheckable(false)
	q.more.OnClicked(func() {
		if p := f.post(q.post); p != nil && f.OnPickReaction != nil {
			f.OnPickReaction(p)
		}
	})
	sep := qt.NewQFrame2()
	setName(sep.QObject, "quickSep")
	sep.SetFixedSize2(1, 18)
	q.lay.AddSpacing(3)
	q.lay.AddWidget(sep.QWidget)
	q.lay.AddSpacing(3)
	q.lay.AddWidget(q.more.QWidget)
	q.restyle()
	q.w.Hide()
	return q
}

func (q *quickBar) button(tip string) *qt.QToolButton {
	b := qt.NewQToolButton2()
	b.SetFixedSize2(quickButton, quickButton)
	b.SetIconSize(qt.NewQSize2(20, 20))
	b.SetFocusPolicy(qt.NoFocus)
	b.SetCheckable(true)
	b.SetToolTip(tip)
	return b
}

func (q *quickBar) restyle() {
	pal := Pal()
	q.w.SetStyleSheet(fmt.Sprintf(`QFrame#quickBar { background: %[1]s; border: 1px solid %[2]s; border-radius: 6px; }
QFrame#quickSep { background: %[2]s; }
QToolButton { border: none; border-radius: 4px; background: transparent; min-width: %[5]dpx; min-height: %[5]dpx; }
QToolButton:hover { background: %[3]s; }
QToolButton:checked { background: %[4]s; }`, pal.Surf, pal.Line2, pal.Hover, pal.SelBg, quickButton))
	q.more.SetIcon(icon("smile-plus", pal.Tx2, 18))
	q.refresh()
}

// refresh — иконки заново (догрузились кастомные эмодзи, сменилась тема) и
// набор — по текущим недавним.
func (q *quickBar) refresh() {
	for i, b := range q.btns {
		q.setIcon(b, q.names[i])
	}
	if q.post != "" {
		q.place()
	}
}

// setNames — кнопки под эмодзи names (если они сменились).
func (q *quickBar) setNames(names []string) {
	if slices.Equal(names, q.names) {
		return
	}
	q.stopAnims()
	q.names = slices.Clone(names)
	for _, b := range q.btns {
		q.lay.RemoveWidget(b.QWidget)
		b.Hide()
		b.DeleteLater()
	}
	q.btns = q.btns[:0]
	for i, name := range names {
		b := q.button(":" + name + ":")
		q.setIcon(b, name)
		b.OnClicked(func() {
			if p := q.f.post(q.post); p != nil && q.f.OnToggleReaction != nil {
				q.f.OnToggleReaction(p, name)
			}
		})
		q.lay.InsertWidget(i, b.QWidget)
		// В видимой панели Qt показал бы кнопку только после событий — и размер
		// панели посчитался бы без неё.
		b.Show()
		q.btns = append(q.btns, b)
	}
	q.w.AdjustSize()
}

func (q *quickBar) setIcon(b *qt.QToolButton, name string) {
	if q.f.d.EmojiImage == nil {
		return
	}
	b.SetIcon(qt.NewQIcon2(q.f.d.EmojiImage(name, 20, func() {
		if slices.Contains(q.names, name) {
			q.refresh() // кастомное догрузилось
		}
	})))
}

// wants — панель у поста: не системного, уже отправленного и не в архиве.
func (q *quickBar) wants(p *model.Post) bool {
	return p.Id != "" && !p.IsSystemMessage() && !q.f.ReadOnly && !isQueued(p)
}

// hover — курсор в точке pos viewport: панель у поста под ним или прочь.
func (q *quickBar) hover(pos *qt.QPoint) {
	it := q.f.postAtY(pos.Y())
	if it == nil || !q.wants(it.post) {
		q.hide()
		return
	}
	q.post = it.post.Id
	q.place()
}

// follow — лента прокрутилась или изменилась: панель — у поста, который
// теперь под курсором; курсор ушёл из ленты — прочь.
func (q *quickBar) follow() {
	if q.post == "" {
		return
	}
	vp := q.f.view.Viewport()
	pos := vp.MapFromGlobalWithQPoint(qt.QCursor_Pos())
	if !vp.Rect().ContainsWithQPoint(pos) {
		q.hide()
		return
	}
	q.hover(pos)
}

// place — над правым верхним углом поста, наполовину на его верхней границе;
// в пределах ленты. Своя реакция — отмечена.
func (q *quickBar) place() {
	i := q.f.index(q.post)
	if i < 0 || !q.wants(q.f.items[i].post) {
		q.hide()
		return
	}
	p := q.f.items[i].post
	var names []string
	if q.f.d.QuickReactions != nil {
		names = q.f.d.QuickReactions()
	}
	q.setNames(names)
	for j, b := range q.btns {
		b.SetChecked(HasReaction(p, q.f.d.Me.Id, q.names[j]))
	}
	size := q.w.SizeHint()
	vp := q.f.view.Viewport()
	y := min(max(0, q.f.postTop(i)-size.Height()/2), vp.Height()-size.Height())
	q.w.Move(max(0, vp.Width()-size.Width()-feedPadRight), y)
	q.w.Raise()
	q.w.Show()
	q.animate()
}

func (q *quickBar) hide() {
	q.post = ""
	q.w.Hide()
	q.stopAnims()
}

// animate — анимированные эмодзи сервера в видимой панели крутятся: кадр —
// новый значок кнопки. Ещё не скачанное подпишется в refresh → place, когда
// догрузится.
func (q *quickBar) animate() {
	a := q.f.d.Assets
	if a == nil || q.f.disposed {
		return
	}
	for i, name := range q.names {
		if _, ok := q.anims[name]; ok || a.EmojiMovie(name) == nil {
			continue
		}
		b := q.btns[i]
		q.anims[name] = a.OnEmojiFrame(name, func() { q.setIcon(b, name) })
	}
}

func (q *quickBar) stopAnims() {
	for name, cancel := range q.anims {
		cancel()
		delete(q.anims, name)
	}
}

// quickReactions — эмодзи для панели у сообщения: недавние (самые частые
// первыми), не хватает — из быстрых по умолчанию.
func quickReactions(recent emoji.Recent) []string {
	names := recent.Names()
	for _, n := range emoji.Quick {
		if len(names) >= quickReactCount {
			break
		}
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return names[:min(quickReactCount, len(names))]
}
