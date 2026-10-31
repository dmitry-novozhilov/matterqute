package ui

import (
	"fmt"
	"html"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/emoji"
	"matterqute/internal/markdown"
)

// Посты одного автора с перерывом меньше этого идут без заголовка.
const groupMillis = 5 * 60 * 1000

type postItem struct {
	post   *model.Post
	w      *qt.QWidget
	lay    *qt.QVBoxLayout
	header *qt.QLabel
	body   *qt.QLabel
	footer *qt.QWidget // реакции и ответы; nil, если показывать нечего
}

// Feed — лента постов: QScrollArea с виджетом на пост, новые снизу.
// Одна и та же лента служит каналу и панели треда (thread=true).
type Feed struct {
	scroll  *qt.QScrollArea
	bar     *qt.QScrollBar
	content *qt.QWidget
	list    *qt.QVBoxLayout
	items   []*postItem
	names   func(userID string) string
	me      string
	thread  bool // лента треда: без счётчиков ответов и пометок «в треде»

	stick          bool // лента у нижнего края: новые посты её прокручивают
	keepFromBottom int  // после подгрузки старых: сохранить расстояние до низа; -1 — не нужно
	programmatic   bool // SetValue из кода, а не прокрутка пользователем
	hasMore        bool
	loadingOlder   bool

	OnNeedOlder      func()                             // докрутили до верха, а на сервере есть ещё
	OnOpenThread     func(rootID string)                // открыть тред поста
	OnToggleReaction func(p *model.Post, emoji string) // поставить или снять свою реакцию
}

func NewFeed(names func(userID string) string, me string, thread bool) *Feed {
	f := &Feed{scroll: qt.NewQScrollArea2(), names: names, me: me, thread: thread}
	f.scroll.SetWidgetResizable(true)
	f.scroll.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	f.scroll.SetFrameShape(qt.QFrame__NoFrame)
	f.bar = f.scroll.VerticalScrollBar()
	f.bar.OnValueChanged(f.onScroll)
	f.bar.OnRangeChanged(f.onRange)
	f.Reset("")
	return f
}

func (f *Feed) Widget() *qt.QWidget { return f.scroll.QWidget }

// Reset очищает ленту; msg — текст вместо постов («Загрузка…», ошибка).
func (f *Feed) Reset(msg string) {
	f.content = qt.NewQWidget2()
	f.list = qt.NewQVBoxLayout(f.content)
	f.list.SetContentsMargins(12, 8, 12, 12)
	f.list.SetSpacing(0)
	f.list.AddStretch() // прижимает короткую ленту к низу
	if msg != "" {
		l := newTextLabel(qt.PlainText)
		l.SetText(msg)
		l.SetAlignment(qt.AlignCenter)
		f.list.AddWidget(l.QWidget)
	}
	f.items = nil
	f.stick, f.keepFromBottom, f.hasMore, f.loadingOlder = true, -1, false, false
	f.scroll.SetWidget(f.content) // прежний content Qt удалит сам
}

func (f *Feed) SetPosts(posts []*model.Post, hasMore bool) {
	f.Reset("")
	f.hasMore = hasMore
	for _, p := range posts {
		f.insert(len(f.items), p)
	}
}

// PrependOlder добавляет подгруженные старые посты, не сдвигая видимое.
func (f *Feed) PrependOlder(posts []*model.Post, hasMore bool) {
	f.loadingOlder = false
	f.hasMore = hasMore
	if len(posts) == 0 {
		return
	}
	f.keepFromBottom = f.bar.Maximum() - f.bar.Value()
	for i, p := range posts {
		f.insert(i, p)
	}
}

// OlderFailed — подгрузка старых не удалась; следующая прокрутка вверх повторит.
func (f *Feed) OlderFailed() { f.loadingOlder = false }

// Has — показан ли пост.
func (f *Feed) Has(id string) bool { return f.index(id) >= 0 }

// Upsert вставляет новый пост по времени или перерисовывает изменённый.
func (f *Feed) Upsert(p *model.Post) {
	if i := f.index(p.Id); i >= 0 {
		f.items[i].post = p
		f.render(i)
		return
	}
	i := len(f.items)
	for i > 0 && f.items[i-1].post.CreateAt > p.CreateAt {
		i--
	}
	f.keepFromBottom = -1
	f.insert(i, p)
}

func (f *Feed) Remove(id string) {
	i := f.index(id)
	if i < 0 {
		return
	}
	it := f.items[i]
	f.list.RemoveWidget(it.w)
	it.w.DeleteLater()
	f.items = slices.Delete(f.items, i, i+1)
	if i < len(f.items) {
		f.renderHeader(i)
	}
}

// RefreshNames перерисовывает заголовки и подсказки реакций, когда догрузились профили.
func (f *Feed) RefreshNames() {
	for i := range f.items {
		f.renderHeader(i)
		f.renderFooter(i)
	}
}

func (f *Feed) index(id string) int {
	return slices.IndexFunc(f.items, func(it *postItem) bool { return it.post.Id == id })
}

func (f *Feed) insert(i int, p *model.Post) {
	it := &postItem{post: p, w: qt.NewQWidget2()}
	it.lay = qt.NewQVBoxLayout(it.w)
	it.lay.SetSpacing(1)
	it.header = newTextLabel(qt.RichText)
	it.body = newTextLabel(qt.RichText)
	it.body.SetOpenExternalLinks(true)
	it.lay.AddWidget(it.header.QWidget)
	it.lay.AddWidget(it.body.QWidget)
	it.w.SetContextMenuPolicy(qt.CustomContextMenu)
	it.w.OnCustomContextMenuRequested(func(pos *qt.QPoint) { f.contextMenu(it, pos) })

	f.items = slices.Insert(f.items, i, it)
	f.list.InsertWidget(i+1, it.w) // +1: растяжка сверху
	f.render(i)
	if i+1 < len(f.items) {
		f.renderHeader(i + 1) // следующий пост мог стать продолжением этого
	}
}

func (f *Feed) render(i int) {
	f.renderHeader(i)
	p := f.items[i].post
	f.items[i].body.SetText(postBodyHTML(p))
	f.items[i].body.SetToolTip(formatTime(p.CreateAt))
	f.renderFooter(i)
}

func (f *Feed) renderHeader(i int) {
	it := f.items[i]
	var prev *model.Post
	if i > 0 {
		prev = f.items[i-1].post
	}
	if continues(prev, it.post) {
		it.header.SetVisible(false)
		it.lay.SetContentsMargins(0, 2, 0, 0)
		return
	}
	it.header.SetText(fmt.Sprintf(`<b>%s</b>&nbsp;&nbsp;<span style="color:gray">%s</span>`,
		html.EscapeString(f.names(it.post.UserId)), formatTime(it.post.CreateAt)))
	it.header.SetVisible(true)
	it.lay.SetContentsMargins(0, 10, 0, 0)
}

// renderFooter пересобирает строку под постом: реакции, «+», ответы.
func (f *Feed) renderFooter(i int) {
	it := f.items[i]
	p := it.post
	if it.footer != nil {
		it.lay.RemoveWidget(it.footer)
		it.footer.DeleteLater()
		it.footer = nil
	}
	groups := groupReactions(p)
	showReplies := !f.thread && p.RootId == "" && p.ReplyCount > 0
	inThread := !f.thread && p.RootId != ""
	if len(groups) == 0 && !showReplies && !inThread {
		return
	}

	it.footer = qt.NewQWidget2()
	row := qt.NewQHBoxLayout(it.footer)
	row.SetContentsMargins(0, 2, 0, 0)
	row.SetSpacing(4)
	for _, g := range groups {
		b := qt.NewQPushButton3(fmt.Sprintf("%s %d", emoji.Display(g.name), len(g.users)))
		b.SetStyleSheet(reactionStyle(slices.Contains(g.users, f.me)))
		names := make([]string, len(g.users))
		for k, u := range g.users {
			names[k] = f.names(u)
		}
		b.SetToolTip(":" + g.name + ": — " + strings.Join(names, ", "))
		name := g.name
		b.OnClicked(func() { f.toggleReaction(p, name) })
		row.AddWidget(b.QWidget)
	}
	if len(groups) > 0 {
		add := qt.NewQPushButton3("+")
		add.SetStyleSheet(reactionStyle(false))
		add.SetToolTip("Добавить реакцию")
		add.SetMenu(f.reactionMenu(p, add.QWidget))
		row.AddWidget(add.QWidget)
	}
	if showReplies {
		b := qt.NewQPushButton3("💬 " + pluralReplies(p.ReplyCount))
		b.SetFlat(true)
		b.OnClicked(func() { f.openThread(p.Id) })
		row.AddWidget(b.QWidget)
	}
	if inThread {
		b := qt.NewQPushButton3("↪ ответ в треде")
		b.SetFlat(true)
		b.OnClicked(func() { f.openThread(p.RootId) })
		row.AddWidget(b.QWidget)
	}
	row.AddStretch()
	it.lay.AddWidget(it.footer)
}

func reactionStyle(mine bool) string {
	if mine {
		return "QPushButton{padding:1px 8px;border:1px solid #6a9fd8;border-radius:10px;background:#dbe8f7}"
	}
	return "QPushButton{padding:1px 8px;border:1px solid #c8c8c8;border-radius:10px;background:transparent}"
}

type reactionGroup struct {
	name  string
	users []string
}

// groupReactions — реакции поста по эмодзи в порядке появления.
func groupReactions(p *model.Post) []reactionGroup {
	if p.Metadata == nil {
		return nil
	}
	var out []reactionGroup
	for _, r := range p.Metadata.Reactions {
		i := slices.IndexFunc(out, func(g reactionGroup) bool { return g.name == r.EmojiName })
		if i < 0 {
			out = append(out, reactionGroup{name: r.EmojiName})
			i = len(out) - 1
		}
		out[i].users = append(out[i].users, r.UserId)
	}
	return out
}

// reactionMenu — меню быстрых реакций; parent владеет меню (SetMenu и
// AddMenu владение не передают).
func (f *Feed) reactionMenu(p *model.Post, parent *qt.QWidget) *qt.QMenu {
	menu := qt.NewQMenu4("Реакция", parent)
	for _, name := range emoji.Quick {
		a := menu.QWidget.AddActionWithText(emoji.Display(name) + "  :" + name + ":")
		a.OnTriggered(func() { f.toggleReaction(p, name) })
	}
	return menu
}

func (f *Feed) contextMenu(it *postItem, pos *qt.QPoint) {
	p := it.post
	menu := qt.NewQMenu2()
	if !p.IsSystemMessage() {
		if !f.thread {
			reply := menu.QWidget.AddActionWithText("Ответить в треде")
			reply.OnTriggered(func() { f.openThread(threadRoot(p)) })
		}
		menu.AddMenu(f.reactionMenu(p, menu.QWidget))
	}
	cp := menu.QWidget.AddActionWithText("Копировать текст")
	cp.OnTriggered(func() { qt.QGuiApplication_Clipboard().SetText(p.Message) })
	menu.ExecWithPos(it.w.MapToGlobalWithQPoint(pos))
	menu.DeleteLater()
}

func (f *Feed) openThread(rootID string) {
	if f.OnOpenThread != nil {
		f.OnOpenThread(rootID)
	}
}

func (f *Feed) toggleReaction(p *model.Post, name string) {
	if f.OnToggleReaction != nil {
		f.OnToggleReaction(p, name)
	}
}

// threadRoot — id корня треда, в котором пост (или он сам — корень).
func threadRoot(p *model.Post) string {
	if p.RootId != "" {
		return p.RootId
	}
	return p.Id
}

// HasReaction — ставил ли user эту реакцию на пост.
func HasReaction(p *model.Post, user, name string) bool {
	return p.Metadata != nil && slices.ContainsFunc(p.Metadata.Reactions, func(r *model.Reaction) bool {
		return r.UserId == user && r.EmojiName == name
	})
}

func pluralReplies(n int64) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return fmt.Sprintf("%d ответ", n)
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return fmt.Sprintf("%d ответа", n)
	}
	return fmt.Sprintf("%d ответов", n)
}

// continues — пост продолжает предыдущий: тот же автор, без долгого перерыва.
func continues(prev, p *model.Post) bool {
	return prev != nil && prev.UserId == p.UserId &&
		p.CreateAt-prev.CreateAt < groupMillis &&
		!prev.IsSystemMessage() && !p.IsSystemMessage()
}

func postBodyHTML(p *model.Post) string {
	var b strings.Builder
	switch {
	case p.IsSystemMessage():
		msg := p.Message
		if msg == "" {
			msg = p.Type
		}
		b.WriteString(`<i style="color:gray">` + markdown.Render(msg) + `</i>`)
	default:
		b.WriteString(markdown.Render(p.Message))
	}
	if p.Metadata != nil && len(p.Metadata.Files) > 0 {
		for _, fi := range p.Metadata.Files {
			fmt.Fprintf(&b, `<br>📎 %s <span style="color:gray">(%s)</span>`, html.EscapeString(fi.Name), formatSize(fi.Size))
		}
	} else if len(p.FileIds) > 0 {
		fmt.Fprintf(&b, `<br>📎 файлов: %d`, len(p.FileIds))
	}
	if p.EditAt > 0 {
		b.WriteString(` <span style="color:gray">(изменено)</span>`)
	}
	return b.String()
}

func formatTime(millis int64) string {
	return time.UnixMilli(millis).Format("2006-01-02 15:04:05")
}

func formatSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f КБ", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d Б", n)
}

// newTextLabel — QLabel с переносом и выделением. Своё контекстное меню
// выключено, чтобы правый клик доходил до меню поста.
func newTextLabel(format qt.TextFormat) *qt.QLabel {
	l := qt.NewQLabel2()
	l.SetWordWrap(true)
	l.SetTextFormat(format)
	l.SetTextInteractionFlags(qt.TextSelectableByMouse | qt.LinksAccessibleByMouse)
	l.SetContextMenuPolicy(qt.NoContextMenu)
	return l
}

func (f *Feed) onScroll(v int) {
	if f.programmatic {
		return
	}
	f.keepFromBottom = -1
	f.stick = v >= f.bar.Maximum()-4
	if v <= f.bar.Minimum() && f.hasMore && !f.loadingOlder && len(f.items) > 0 && f.OnNeedOlder != nil {
		f.loadingOlder = true
		f.OnNeedOlder()
	}
}

// onRange — высота содержимого изменилась (добавились посты, пересчитались
// переносы): держим низ или место, на котором был пользователь.
func (f *Feed) onRange(_, max int) {
	f.programmatic = true
	defer func() { f.programmatic = false }()
	switch {
	case f.keepFromBottom >= 0:
		f.bar.SetValue(max - f.keepFromBottom)
	case f.stick:
		f.bar.SetValue(max)
	}
}
