package ui

import (
	"fmt"
	"html"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/emoji"
	"matterqute/internal/markdown"
)

// Посты одного автора с перерывом меньше этого идут без заголовка.
const groupMillis = 5 * 60 * 1000

// Наибольший размер превью картинки в ленте.
const previewMaxW, previewMaxH = 360, 240

// Отступ документа от краёв ленты.
const feedMargin = 12.0

// Ссылки на действия внутри ленты. Их обрабатывает Feed, а не браузер.
const (
	reactLink     = "mm-react:"      // mm-react:<post>:<emoji> — поставить или снять реакцию
	reactMenuLink = "mm-react-menu:" // mm-react-menu:<post> — меню быстрых реакций
	threadLink    = "mm-thread:"     // mm-thread:<корень>
	openLink      = "mm-open:"       // mm-open:<файл>
	saveLink      = "mm-save:"       // mm-save:<файл>
)

// Плотность картинок-ресурсов: рисуем вдвое крупнее, чтобы на HiDPI было чётко.
const resourceDPR = 2

type postItem struct {
	post  *model.Post
	frame *qt.QTextFrame
	html  string // последняя отрисовка — для тестов и отладки
}

// FeedDeps — что ленте нужно от окна.
type FeedDeps struct {
	Names         func(userID string) string
	Me            *model.User
	Thread        bool    // лента треда: без счётчиков ответов и пометок «в треде»
	Assets        *Assets // nil — без картинок и кастомных эмодзи (тесты прокрутки)
	ChannelExists func(name string) bool
}

// Feed — лента постов одним документом (QTextBrowser): выделение и копирование
// идут через любые сообщения, как в браузере. Каждый пост — свой QTextFrame,
// поэтому новые, изменённые и удалённые посты правятся точечно: позиции
// остальных фреймов документ сдвигает сам. Реакции и превью — картинки-ресурсы
// документа, клики по ним — ссылки mm-*.
type Feed struct {
	d     FeedDeps
	md    *markdown.Renderer
	view  *qt.QTextBrowser
	doc   *qt.QTextDocument
	bar   *qt.QScrollBar
	items []*postItem
	// Картинки, уже добавленные ресурсами документа (сбрасываются с документом).
	resources   map[string][2]int // имя → логический размер
	placeholder bool              // в документе текст-заглушка вместо постов

	stick          bool // лента у нижнего края: новые посты её прокручивают
	keepFromBottom int  // после подгрузки старых: сохранить расстояние до низа; -1 — не нужно
	programmatic   bool // SetValue из кода, а не прокрутка пользователем
	hasMore        bool
	loadingOlder   bool
	refreshPending bool

	OnNeedOlder      func()                            // докрутили до верха, а на сервере есть ещё
	OnOpenThread     func(rootID string)               // открыть тред поста
	OnToggleReaction func(p *model.Post, emoji string) // поставить или снять свою реакцию
	OnLink           func(url string)                  // клик по ссылке в тексте
	OnOpenFile       func(fi *model.FileInfo)
	OnSaveFile       func(fi *model.FileInfo)
}

func NewFeed(d FeedDeps) *Feed {
	f := &Feed{d: d, view: qt.NewQTextBrowser2()}
	f.md = markdown.New(markdown.Options{Me: d.Me.Username, ChannelExists: d.ChannelExists, CustomEmoji: f.customEmoji})
	f.doc = f.view.Document()
	f.view.SetOpenLinks(false)
	f.view.SetFrameShape(qt.QFrame__NoFrame)
	f.view.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	f.view.OnAnchorClicked(func(u *qt.QUrl) { f.onAnchor(u.ToString()) })
	f.view.OnContextMenuEvent(func(_ func(*qt.QContextMenuEvent), e *qt.QContextMenuEvent) { f.contextMenu(e) })
	f.view.OnCreateMimeDataFromSelection(f.copySelection)
	f.view.OnResizeEvent(func(super func(*qt.QResizeEvent), e *qt.QResizeEvent) {
		super(e)
		f.pinToBottom()
	})
	f.bar = f.view.VerticalScrollBar()
	f.bar.OnValueChanged(f.onScroll)
	f.bar.OnRangeChanged(f.onRange)
	f.Reset("")
	return f
}

func (f *Feed) Widget() *qt.QWidget { return f.view.QWidget }

// Reset очищает ленту; msg — текст вместо постов («Загрузка…», ошибка).
func (f *Feed) Reset(msg string) {
	f.clearDoc()
	if msg != "" {
		f.placeholder = true
		c := qt.NewQTextCursor2(f.doc)
		c.InsertHtml(`<p align="center" style="color:gray">` + html.EscapeString(msg) + `</p>`)
	}
	f.stick, f.keepFromBottom, f.hasMore, f.loadingOlder = true, -1, false, false
	f.pinToBottom()
}

func (f *Feed) clearDoc() {
	f.doc.Clear()
	f.doc.SetDocumentMargin(feedMargin)
	f.resources = map[string][2]int{}
	f.items = nil
	f.placeholder = false
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
	fr := f.items[i].frame
	c := qt.NewQTextCursor2(f.doc)
	c.SetPosition(fr.FirstPosition() - 1)
	c.SetPosition2(fr.LastPosition()+1, qt.QTextCursor__KeepAnchor)
	c.RemoveSelectedText()
	f.items = slices.Delete(f.items, i, i+1)
	if i < len(f.items) {
		f.render(i) // мог стать заголовком группы
	}
}

// RefreshNames перерисовывает ленту, когда догрузились профили.
func (f *Feed) RefreshNames() {
	for i := range f.items {
		f.render(i)
	}
}

// ScrollToPost прокручивает к посту (и ставит курсор в его начало).
func (f *Feed) ScrollToPost(id string) {
	i := f.index(id)
	if i < 0 {
		return
	}
	f.stick, f.keepFromBottom = false, -1
	c := qt.NewQTextCursor2(f.doc)
	c.SetPosition(f.items[i].frame.FirstPosition())
	f.view.SetTextCursor(c)
	f.view.EnsureCursorVisible()
}

// refreshSoon перерисует ленту один раз после текущих событий: догрузились
// картинки или эмодзи (их может прийти пачка подряд).
func (f *Feed) refreshSoon() {
	if f.refreshPending || f.d.Assets == nil {
		return
	}
	f.refreshPending = true
	f.d.Assets.Later(func() {
		f.refreshPending = false
		for i := range f.items {
			f.render(i)
		}
	})
}

func (f *Feed) customEmoji(name string) string {
	if f.d.Assets == nil {
		return ""
	}
	p := f.d.Assets.Emoji(name)
	if p == "" {
		f.d.Assets.WantEmoji(name, f.refreshSoon)
	}
	return p
}

func (f *Feed) index(id string) int {
	return slices.IndexFunc(f.items, func(it *postItem) bool { return it.post.Id == id })
}

func (f *Feed) post(id string) *model.Post {
	if i := f.index(id); i >= 0 {
		return f.items[i].post
	}
	return nil
}

// itemAt — пост, в чьём фрейме позиция документа pos.
func (f *Feed) itemAt(pos int) *postItem {
	for _, it := range f.items {
		if it.frame.FirstPosition() <= pos && pos <= it.frame.LastPosition() {
			return it
		}
	}
	return nil
}

func (f *Feed) insert(i int, p *model.Post) {
	if f.placeholder {
		f.clearDoc()
	}
	c := qt.NewQTextCursor2(f.doc)
	switch {
	case i < len(f.items):
		c.SetPosition(f.items[i].frame.FirstPosition() - 1)
	case len(f.items) > 0:
		c.SetPosition(f.items[len(f.items)-1].frame.LastPosition() + 1)
	}
	it := &postItem{post: p, frame: c.InsertFrame(frameFormat(false))}
	f.items = slices.Insert(f.items, i, it)
	f.write(i, c)
	// Между соседними фреймами (и до первого, и после последнего) Qt держит
	// пустой абзац — без этого он давал бы пустую строку между сообщениями.
	f.squash(it.frame.FirstPosition() - 1)
	f.squash(it.frame.LastPosition() + 1)
	if i+1 < len(f.items) {
		f.render(i + 1) // следующий пост мог стать продолжением этого
	}
}

// render перерисовывает пост на месте. Выделение пользователя вне поста
// документ сохраняет сам: его курсор сдвигается вместе с текстом.
func (f *Feed) render(i int) {
	fr := f.items[i].frame
	c := qt.NewQTextCursor2(f.doc)
	c.SetPosition(fr.FirstPosition())
	c.SetPosition2(fr.LastPosition(), qt.QTextCursor__KeepAnchor)
	c.RemoveSelectedText()
	f.write(i, c)
}

// write пишет пост в его фрейм; c стоит в начале пустого фрейма.
func (f *Feed) write(i int, c *qt.QTextCursor) {
	it := f.items[i]
	var prev *model.Post
	if i > 0 {
		prev = f.items[i-1].post
	}
	cont := continues(prev, it.post)
	it.frame.SetFrameFormat(frameFormat(cont))
	it.html = f.postHTML(it.post, cont)
	c.InsertHtml(it.html)
	// После таблицы (блок кода, вложение интеграции) Qt оставляет пустой
	// абзац — он даёт лишнюю строку под сообщением.
	if c.Position() > it.frame.FirstPosition() {
		f.squash(c.Position())
	}
}

// squash ужимает пустой абзац в позиции pos до 1 px; непустой не трогает.
func (f *Feed) squash(pos int) {
	if pos < 0 || pos >= f.doc.CharacterCount() {
		return
	}
	c := qt.NewQTextCursor2(f.doc)
	c.SetPosition(pos)
	if c.Block().Length() != 1 {
		return
	}
	bf := qt.NewQTextBlockFormat()
	bf.SetLineHeight(1, int(qt.QTextBlockFormat__FixedHeight))
	c.SetBlockFormat(bf)
}

func frameFormat(continuation bool) *qt.QTextFrameFormat {
	ff := qt.NewQTextFrameFormat()
	ff.SetBorder(0)
	ff.SetPadding(0)
	ff.SetMargin(0)
	if continuation {
		ff.SetTopMargin(2)
	} else {
		ff.SetTopMargin(10)
	}
	return ff
}

func (f *Feed) postHTML(p *model.Post, continuation bool) string {
	var b strings.Builder
	if !continuation {
		fmt.Fprintf(&b, `<p style="margin:0"><b>%s</b>&nbsp;&nbsp;<span style="color:gray">%s</span></p>`,
			html.EscapeString(f.d.Names(p.UserId)), formatTime(p.CreateAt))
	}
	b.WriteString(f.bodyHTML(p))
	b.WriteString(f.filesHTML(p))
	b.WriteString(f.footerHTML(p))
	return b.String()
}

func (f *Feed) bodyHTML(p *model.Post) string {
	var b strings.Builder
	switch {
	case p.IsSystemMessage():
		msg := p.Message
		if msg == "" {
			msg = p.Type
		}
		b.WriteString(`<p style="margin:0"><i style="color:gray">` + html.EscapeString(msg) + `</i></p>`)
	default:
		b.WriteString(f.md.Render(p.Message))
		b.WriteString(f.md.Attachments(p.Attachments()))
	}
	if p.EditAt > 0 {
		b.WriteString(`<p style="margin:0"><span style="color:gray">(изменено)</span></p>`)
	}
	return b.String()
}

// filesHTML: картинки — превью (место под него резервируется сразу, чтобы
// лента не прыгала, когда оно догрузится), остальное — строкой со ссылками.
func (f *Feed) filesHTML(p *model.Post) string {
	if p.Metadata == nil {
		return ""
	}
	var b strings.Builder
	for _, fi := range p.Metadata.Files {
		title := html.EscapeString(fmt.Sprintf("%s (%s)", fi.Name, formatSize(fi.Size)))
		if isImage(fi) {
			w, h := fitSize(fi.Width, fi.Height, previewMaxW, previewMaxH)
			fmt.Fprintf(&b, `<p style="margin-top:4px;margin-bottom:0"><a href="%s%s"><img src="%s" width="%d" height="%d" title="%s — открыть"></a></p>`,
				openLink, fi.Id, f.previewSrc(fi, w, h), w, h, title)
			continue
		}
		fmt.Fprintf(&b, `<p style="margin-top:4px;margin-bottom:0">📎 %s <span style="color:gray">(%s)</span>&nbsp;&nbsp;`,
			html.EscapeString(fi.Name), formatSize(fi.Size))
		if canOpen(fi) {
			fmt.Fprintf(&b, `<a href="%s%s">Открыть</a>&nbsp;·&nbsp;`, openLink, fi.Id)
		}
		fmt.Fprintf(&b, `<a href="%s%s">Сохранить…</a></p>`, saveLink, fi.Id)
	}
	return b.String()
}

// previewSrc — ресурс с уменьшенным превью или серая заглушка того же
// размера, пока превью грузится.
func (f *Feed) previewSrc(fi *model.FileInfo, w, h int) string {
	name := fmt.Sprintf("mm-img:%s:%dx%d", fi.Id, w, h)
	if _, ok := f.resources[name]; ok {
		return name
	}
	if f.d.Assets != nil {
		if path := f.d.Assets.Preview(fi.Id); path != "" {
			if pm := qt.NewQPixmap4(path); !pm.IsNull() {
				pm = pm.Scaled3(w*resourceDPR, h*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
				pm.SetDevicePixelRatio(resourceDPR)
				f.addResource(name, pm, w, h)
				return name
			}
		} else {
			f.d.Assets.WantPreview(fi.Id, f.refreshSoon)
		}
	}
	ph := fmt.Sprintf("mm-ph:%dx%d", w, h)
	if _, ok := f.resources[ph]; !ok {
		img := qt.NewQImage3(w, h, qt.QImage__Format_ARGB32_Premultiplied)
		img.FillWithColor(qt.NewQColor6("#e6e9ed"))
		f.addResource(ph, qt.QPixmap_FromImage(img), w, h)
	}
	return ph
}

func (f *Feed) addResource(name string, pm *qt.QPixmap, w, h int) {
	f.doc.AddResource(int(qt.QTextDocument__ImageResource), qt.NewQUrl3(name), pm.ToQVariant())
	f.resources[name] = [2]int{w, h}
}

// footerHTML: реакции (картинками-плашками), «+», ответы.
func (f *Feed) footerHTML(p *model.Post) string {
	groups := groupReactions(p)
	showReplies := !f.d.Thread && p.RootId == "" && p.ReplyCount > 0
	inThread := !f.d.Thread && p.RootId != ""
	if len(groups) == 0 && !showReplies && !inThread {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<p style="margin-top:4px;margin-bottom:0">`)
	for _, g := range groups {
		names := make([]string, len(g.users))
		for k, u := range g.users {
			names[k] = f.d.Names(u)
		}
		src, w, h := f.pill(g.name, len(g.users), slices.Contains(g.users, f.d.Me.Id))
		fmt.Fprintf(&b, `<a href="%s%s:%s"><img src="%s" width="%d" height="%d" title="%s"></a>&nbsp;`,
			reactLink, p.Id, url.PathEscape(g.name), src, w, h, html.EscapeString(":"+g.name+": — "+strings.Join(names, ", ")))
	}
	if len(groups) > 0 {
		src, w, h := f.pill("", 0, false)
		fmt.Fprintf(&b, `<a href="%s%s"><img src="%s" width="%d" height="%d" title="Добавить реакцию"></a>&nbsp;&nbsp;`, reactMenuLink, p.Id, src, w, h)
	}
	if showReplies {
		fmt.Fprintf(&b, `<a href="%s%s" style="text-decoration:none">💬 %s</a>`, threadLink, p.Id, pluralReplies(p.ReplyCount))
	}
	if inThread {
		fmt.Fprintf(&b, `<a href="%s%s" style="text-decoration:none;color:gray">↪ ответ в треде</a>`, threadLink, p.RootId)
	}
	b.WriteString("</p>")
	return b.String()
}

// pill — картинка-плашка реакции «эмодзи число»; name="" — плашка «+».
// Рисуем сами: в тексте документа скругления и рамки не сделать.
func (f *Feed) pill(name string, count int, mine bool) (src string, w, h int) {
	label := "+"
	var icon string
	if name != "" {
		if s, ok := emoji.Char(name); ok {
			label = fmt.Sprintf("%s %d", s, count)
		} else if icon = f.customEmoji(name); icon != "" {
			label = fmt.Sprint(count)
		} else {
			label = fmt.Sprintf(":%s: %d", name, count)
		}
	}
	// Имя ресурса — только ASCII: документ сверяет его через QUrl.
	src = fmt.Sprintf("mm-pill:%s:%d:%t:%t", url.PathEscape(name), count, mine, icon != "")
	if size, ok := f.resources[src]; ok {
		return src, size[0], size[1]
	}

	font := f.view.Font()
	fm := qt.NewQFontMetrics(font)
	h = fm.Height() + 6
	iconW := 0
	if icon != "" {
		iconW = h - 8 + 4
	}
	textW := fm.HorizontalAdvance(label)
	w = 8 + iconW + textW + 8

	img := qt.NewQImage3(w*resourceDPR, h*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
	img.Fill2(qt.Transparent)
	img.SetDevicePixelRatio(resourceDPR)
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	border, fill := "#c8c8c8", "#ffffff"
	if mine {
		border, fill = "#6a9fd8", "#dbe8f7"
	}
	p.SetPenWithPen(qt.NewQPen4(qt.NewQBrush3(qt.NewQColor6(border)), 1))
	p.SetBrush(qt.NewQBrush3(qt.NewQColor6(fill)))
	r := float64(h-1) / 2
	p.DrawRoundedRect(qt.NewQRectF4(0.5, 0.5, float64(w-1), float64(h-1)), r, r)
	if icon != "" {
		if pm := qt.NewQPixmap4(icon); !pm.IsNull() {
			p.DrawPixmap11(8, 4, h-8, h-8, pm)
		}
	}
	p.SetPen(qt.NewQColor6("#333333"))
	p.SetFont(font)
	p.DrawText7(8+iconW, 0, textW+1, h, int(qt.AlignVCenter|qt.AlignLeft), label)
	p.End()
	f.addResource(src, qt.QPixmap_FromImage(img), w, h)
	return src, w, h
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

// onAnchor — клик по ссылке в ленте: свои mm-* обрабатываем здесь,
// остальное (http, ~каналы, @упоминания) отдаём окну.
func (f *Feed) onAnchor(link string) {
	switch {
	case strings.HasPrefix(link, reactLink):
		postID, name, _ := strings.Cut(strings.TrimPrefix(link, reactLink), ":")
		name, _ = url.PathUnescape(name)
		if p := f.post(postID); p != nil && name != "" {
			f.toggleReaction(p, name)
		}
	case strings.HasPrefix(link, reactMenuLink):
		if p := f.post(strings.TrimPrefix(link, reactMenuLink)); p != nil {
			menu := f.reactionMenu(p, f.view.QWidget)
			menu.ExecWithPos(qt.QCursor_Pos())
			menu.DeleteLater()
		}
	case strings.HasPrefix(link, threadLink):
		f.openThread(strings.TrimPrefix(link, threadLink))
	case strings.HasPrefix(link, openLink):
		if fi := f.file(strings.TrimPrefix(link, openLink)); fi != nil && f.OnOpenFile != nil {
			f.OnOpenFile(fi)
		}
	case strings.HasPrefix(link, saveLink):
		if fi := f.file(strings.TrimPrefix(link, saveLink)); fi != nil && f.OnSaveFile != nil {
			f.OnSaveFile(fi)
		}
	default:
		if f.OnLink != nil {
			f.OnLink(link)
		}
	}
}

func (f *Feed) file(id string) *model.FileInfo {
	for _, it := range f.items {
		if it.post.Metadata == nil {
			continue
		}
		for _, fi := range it.post.Metadata.Files {
			if fi.Id == id {
				return fi
			}
		}
	}
	return nil
}

// reactionMenu — меню быстрых реакций; parent владеет меню.
func (f *Feed) reactionMenu(p *model.Post, parent *qt.QWidget) *qt.QMenu {
	menu := qt.NewQMenu4("Реакция", parent)
	for _, name := range emoji.Quick {
		a := menu.QWidget.AddActionWithText(emoji.Display(name) + "  :" + name + ":")
		a.OnTriggered(func() { f.toggleReaction(p, name) })
	}
	return menu
}

func (f *Feed) contextMenu(e *qt.QContextMenuEvent) {
	it := f.itemAt(f.view.CursorForPosition(e.Pos()).Position())
	menu := qt.NewQMenu(f.view.QWidget)
	if f.view.TextCursor().HasSelection() {
		cp := menu.QWidget.AddActionWithText("Копировать")
		cp.OnTriggered(f.view.Copy)
	}
	if link := f.view.AnchorAt(e.Pos()); strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		cl := menu.QWidget.AddActionWithText("Копировать ссылку")
		cl.OnTriggered(func() { qt.QGuiApplication_Clipboard().SetText(link) })
	}
	if it != nil {
		p := it.post
		if !p.IsSystemMessage() {
			if !f.d.Thread {
				reply := menu.QWidget.AddActionWithText("Ответить в треде")
				reply.OnTriggered(func() { f.openThread(threadRoot(p)) })
			}
			menu.AddMenu(f.reactionMenu(p, menu.QWidget))
		}
		cp := menu.QWidget.AddActionWithText("Копировать текст сообщения")
		cp.OnTriggered(func() { qt.QGuiApplication_Clipboard().SetText(p.Message) })
	}
	all := menu.QWidget.AddActionWithText("Выделить всё")
	all.OnTriggered(f.view.SelectAll)
	menu.ExecWithPos(e.GlobalPos())
	menu.DeleteLater()
}

// copySelection — в буфер только текст: картинки (реакции, превью) в
// выделении дали бы символы-заглушки U+FFFC.
func (f *Feed) copySelection(super func() *qt.QMimeData) *qt.QMimeData {
	md := super()
	text := md.Text()
	md.Delete()
	text = strings.NewReplacer("\uFFFC", "", "\u2028", "\n", "\u2029", "\n", "\u00a0", " ").Replace(text)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	out := qt.NewQMimeData()
	out.SetText(strings.TrimSpace(strings.Join(lines, "\n")))
	return out
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

// newTextLabel — QLabel с переносом и выделением (заголовки, подписи).
func newTextLabel(format qt.TextFormat) *qt.QLabel {
	l := qt.NewQLabel2()
	l.SetWordWrap(true)
	l.SetTextFormat(format)
	l.SetTextInteractionFlags(qt.TextSelectableByMouse | qt.LinksAccessibleByMouse)
	return l
}

// pinToBottom прижимает короткую ленту к низу, как в чатах: верхнее поле
// документа растягивается на свободное место.
func (f *Feed) pinToBottom() {
	root := f.doc.RootFrame()
	ff := root.FrameFormat()
	cur := ff.TopMargin()
	content := f.doc.Size().Height() - cur
	want := math.Max(feedMargin, float64(f.view.Viewport().Height())-content)
	if math.Abs(want-cur) < 1 {
		return
	}
	ff.SetTopMargin(want)
	root.SetFrameFormat(ff)
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
	f.pinToBottom()
	f.programmatic = true
	defer func() { f.programmatic = false }()
	switch {
	case f.keepFromBottom >= 0:
		f.bar.SetValue(max - f.keepFromBottom)
	case f.stick:
		f.bar.SetValue(max)
	}
}
