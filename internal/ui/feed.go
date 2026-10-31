package ui

import (
	"fmt"
	"html"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	qt "github.com/mappu/miqt/qt6"

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
}

// Feed — лента канала: QScrollArea с виджетом на пост, новые снизу.
type Feed struct {
	scroll  *qt.QScrollArea
	bar     *qt.QScrollBar
	content *qt.QWidget
	list    *qt.QVBoxLayout
	items   []*postItem
	names   func(userID string) string

	stick          bool // лента у нижнего края: новые посты её прокручивают
	keepFromBottom int  // после подгрузки старых: сохранить расстояние до низа; -1 — не нужно
	programmatic   bool // SetValue из кода, а не прокрутка пользователем
	hasMore        bool
	loadingOlder   bool

	OnNeedOlder func() // докрутили до верха, а на сервере есть ещё
}

func NewFeed(names func(userID string) string) *Feed {
	f := &Feed{scroll: qt.NewQScrollArea2(), names: names}
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

// RefreshNames перерисовывает заголовки, когда догрузились профили.
func (f *Feed) RefreshNames() {
	for i := range f.items {
		f.renderHeader(i)
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

// continues — пост продолжает предыдущий: тот же автор, без долгого перерыва.
func continues(prev, p *model.Post) bool {
	return prev != nil && prev.UserId == p.UserId &&
		p.CreateAt-prev.CreateAt < groupMillis &&
		!prev.IsSystemMessage() && !p.IsSystemMessage()
}

func postBodyHTML(p *model.Post) string {
	var b strings.Builder
	if p.RootId != "" {
		b.WriteString(`<span style="color:gray">↪ ответ в треде</span><br>`)
	}
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

func newTextLabel(format qt.TextFormat) *qt.QLabel {
	l := qt.NewQLabel2()
	l.SetWordWrap(true)
	l.SetTextFormat(format)
	l.SetTextInteractionFlags(qt.TextSelectableByMouse | qt.LinksAccessibleByMouse)
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
