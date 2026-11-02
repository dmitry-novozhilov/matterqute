package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"math"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/emoji"
	"matterqute/internal/markdown"
	"matterqute/internal/store"
)

// Посты одного автора с перерывом меньше этого идут без заголовка.
const groupMillis = 5 * 60 * 1000

// Раскладка ленты (feed-html.md): колонка аватара 58 = 16 поля + 32 аватар + 10
// зазор, справа поле 16. Картинки не выше 400 и не шире текстовой колонки.
const (
	feedAvatarCol = 58
	feedPadRight  = 16
	previewMaxH   = 400
)

// Ссылки на действия внутри ленты. Их обрабатывает Feed, а не браузер.
const (
	reactLink     = "mm-react:"      // mm-react:<post>:<emoji> — поставить или снять реакцию
	reactMenuLink = "mm-react-menu:" // mm-react-menu:<post> — меню быстрых реакций
	threadLink    = "mm-thread:"     // mm-thread:<корень>
	openLink      = "mm-open:"       // mm-open:<файл>
	saveLink      = "mm-save:"       // mm-save:<файл>
	imageLink     = "mm-img:"        // mm-img:<файл> — картинка целиком
	retryLink     = "mm-retry:"      // повторить загрузку ленты
	hookLink      = "mm-hook:"       // mm-hook:<post> — карточка интеграции, написавшей пост
	videoLink     = "mm-video:"      // mm-video:<файл> — играть ролик в ленте (повторно — пауза)
	spinnerSrc    = "spinner:24"     // ресурс с текущим кадром крутилки
)

type postItem struct {
	post  *model.Post
	frame *qt.QTextFrame
	html  string // последняя отрисовка — для тестов и отладки
}

// FeedDeps — что ленте нужно от окна.
type FeedDeps struct {
	Names         func(userID string) string
	Users         func(userID string) *model.User // nil — профиль ещё не загружен
	Me            *model.User
	Thread        bool    // лента треда: без счётчиков ответов и пометок «в треде»
	Assets        *Assets // nil — без картинок и кастомных эмодзи (тесты прокрутки)
	ChannelExists func(name string) bool
	LinkPreviews  func() bool // показывать предпросмотр ссылок; nil — да
}

// pill — плашка реакции: обычная и под курсором. render рисует её заново —
// для анимированного эмодзи на каждом кадре.
type pill struct {
	normal, hover *qt.QPixmap
	emoji         string
	render        func() (normal, hover *qt.QPixmap)
}

// Feed — лента постов одним документом (QTextBrowser): выделение и копирование
// идут через любые сообщения, как в браузере. Каждый пост — свой QTextFrame,
// поэтому новые, изменённые и удалённые посты правятся точечно: позиции
// остальных фреймов документ сдвигает сам. Реакции, аватары и превью —
// картинки-ресурсы документа, клики по ним — ссылки mm-*.
type Feed struct {
	d     FeedDeps
	md    *markdown.Renderer
	view  *qt.QTextBrowser
	doc   *qt.QTextDocument
	bar   *qt.QScrollBar
	items []*postItem
	// Картинки, уже добавленные ресурсами документа (сбрасываются с документом).
	resources   map[string][2]int // имя → логический размер
	pills       map[string]pill   // имя ресурса реакции → обычная и при наведении
	hovered     string            // ресурс реакции под курсором
	movies      map[string]*qt.QMovie
	placeholder bool                    // в документе заглушка вместо постов
	history     *qt.QTextFrame          // строка «Загружаем историю…» над постами
	newSince    int64                   // посты новее — «Новые сообщения»
	newMarker   string                  // id поста, над которым отметка
	lastWidth   int                     // ширина, под которую вписаны картинки
	refit       *qt.QTimer              // отложенная перерисовка картинок после ресайза
	spin        *spinner                // крутилка заглушки «Загружаем сообщения…»
	videos      map[string]*inlineVideo // ролики, которые играют в ленте, по id файла
	animSubs    map[string]func()       // подписки на кадры анимированных эмодзи: отписка по имени
	placeTimer  *qt.QTimer              // поставить ролики на места после правок документа
	unsubscribe func()

	stick          bool // лента у нижнего края: новые посты её прокручивают
	keepFromBottom int  // после подгрузки старых: сохранить расстояние до низа; -1 — не нужно
	programmatic   bool // SetValue из кода, а не прокрутка пользователем
	editing        bool // идёт правка документа (edit): прокрутку от неё не принимаем за пользователя
	hasMore        bool
	loadingOlder   bool
	refreshPending bool

	OnNeedOlder      func()                            // докрутили до верха, а на сервере есть ещё
	OnOpenThread     func(rootID string)               // открыть тред поста
	OnToggleReaction func(p *model.Post, emoji string) // поставить или снять свою реакцию
	OnPickReaction   func(p *model.Post)               // окно выбора эмодзи для реакции
	OnPermalink      func(channelID, postID string)    // «Перейти к сообщению» в предпросмотре ссылки
	OnRemovePreview  func(p *model.Post)               // автор убирает предпросмотр ссылки
	OnLink           func(url string)                  // клик по ссылке в тексте
	OnIntegration    func(p *model.Post)               // клик по имени или аватару интеграции
	// OnFullscreenVideo — «Во весь экран» у ролика в ленте: окно с той же
	// позиции; back — вернуть позицию и «играл ли», когда окно закроют.
	OnFullscreenVideo func(fi *model.FileInfo, p *model.Post, path string, pos int64, playing bool, back func(pos int64, playing bool))
	OnOpenFile        func(fi *model.FileInfo)
	OnSaveFile        func(fi *model.FileInfo)
	OnRetry           func() // «Повторить» в заглушке ошибки
}

func NewFeed(d FeedDeps) *Feed {
	if d.Users == nil {
		d.Users = func(string) *model.User { return nil }
	}
	f := &Feed{d: d, view: qt.NewQTextBrowser2(), movies: map[string]*qt.QMovie{}, videos: map[string]*inlineVideo{},
		animSubs: map[string]func(){}}
	setName(f.view.QObject, "feed")
	f.doc = f.view.Document()
	f.doc.SetDefaultFont(feedFont())
	f.view.SetOpenLinks(false)
	f.view.SetFrameShape(qt.QFrame__NoFrame)
	f.view.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	f.view.OnAnchorClicked(func(u *qt.QUrl) { f.onAnchor(u.ToString()) })
	f.view.OnHighlighted(func(u *qt.QUrl) { f.onHover(u.ToString()) })
	f.view.OnContextMenuEvent(func(_ func(*qt.QContextMenuEvent), e *qt.QContextMenuEvent) { f.contextMenu(e) })
	f.view.OnCreateMimeDataFromSelection(f.copySelection)
	f.view.OnResizeEvent(func(super func(*qt.QResizeEvent), e *qt.QResizeEvent) {
		super(e)
		f.onResize()
	})
	// Таймер — ребёнок ленты: удалится вместе с ней и не сработает после.
	f.refit = qt.NewQTimer2(f.view.QObject)
	f.refit.SetSingleShot(true)
	f.refit.SetInterval(50)
	f.refit.OnTimeout(f.fitImages)
	f.placeTimer = qt.NewQTimer2(f.view.QObject)
	f.placeTimer.SetSingleShot(true)
	f.placeTimer.OnTimeout(f.placeVideos)
	f.doc.OnContentsChanged(func() {
		if len(f.videos) > 0 {
			f.placeTimer.Start(0)
		}
	})
	f.spin = newSpinner(f.view.QObject, func(pm *qt.QPixmap) {
		f.doc.AddResource(int(qt.QTextDocument__ImageResource), qt.NewQUrl3(spinnerSrc), pm.ToQVariant())
		f.view.Viewport().Update()
	})
	f.bar = f.view.VerticalScrollBar()
	f.bar.OnValueChanged(f.onScroll)
	f.bar.OnValueChanged(func(int) { f.placeVideos() })
	f.bar.OnRangeChanged(f.onRange)
	f.restyleRenderer()
	f.unsubscribe = OnThemeChanged(f.restyle)
	f.Reset("")
	return f
}

func (f *Feed) Widget() *qt.QWidget { return f.view.QWidget }

func (f *Feed) dispose() {
	f.unsubscribe()
	f.stopMovies()
	f.stopEmojiAnimations()
}

func (f *Feed) restyleRenderer() {
	f.md = markdown.New(markdown.Options{Me: f.d.Me.Username, ChannelExists: f.d.ChannelExists, CustomEmoji: f.customEmoji, Palette: Pal()})
}

// restyle — смена темы: цвета в HTML и картинках рисует код, их надо пересоздать.
func (f *Feed) restyle() {
	f.restyleRenderer()
	f.doc.SetDefaultFont(feedFont())
	f.resources = map[string][2]int{}
	f.pills = map[string]pill{}
	if f.spin.timer.IsActive() {
		f.spin.start(Pal().Tx3, 24)
	}
	for _, v := range f.videos {
		v.restyle()
	}
	f.renderAll()
}

// Reset очищает ленту; msg — текст вместо постов.
func (f *Feed) Reset(msg string) {
	f.clearDoc()
	if msg != "" {
		f.Placeholder("", msg, "")
	}
	f.stick, f.keepFromBottom, f.hasMore, f.loadingOlder = true, -1, false, false
	f.pinToBottom()
}

// Loading — заглушка «Загружаем сообщения…».
func (f *Feed) Loading() {
	f.Placeholder("loader-circle", "", "Загружаем сообщения…")
}

// Placeholder — заглушка по центру вместо постов: иконка, заголовок, текст.
func (f *Feed) Placeholder(iconName, title, text string) {
	f.placeholderHTML(iconName, title, text, "")
}

// Error — загрузка не удалась: причина и «Повторить».
func (f *Feed) Error(text string) {
	f.placeholderHTML("triangle-alert", "Не удалось загрузить сообщения", text,
		fmt.Sprintf(`<p align="center" style="margin-top:8px"><a href="%s" style="color:%s;font-weight:600;text-decoration:none">Повторить</a></p>`, retryLink, Pal().Acc))
}

func (f *Feed) placeholderHTML(iconName, title, text, extra string) {
	p := Pal()
	f.clearDoc()
	f.placeholder = true
	var b strings.Builder
	if iconName != "" {
		src := spinnerSrc
		if iconName == "loader-circle" {
			f.spin.start(p.Tx3, 24) // кадр ложится в ресурс spinnerSrc
		} else {
			src = f.iconSrc(iconName, p.Tx3, 24)
		}
		fmt.Fprintf(&b, `<p align="center" style="margin-bottom:8px"><img src="%s" width="24" height="24"></p>`, src)
	}
	if title != "" {
		fmt.Fprintf(&b, `<p align="center" style="margin:0;font-weight:600;color:%s">%s</p>`, p.Tx, html.EscapeString(title))
	}
	if text != "" {
		fmt.Fprintf(&b, `<p align="center" style="margin:0;font-size:13px;color:%s">%s</p>`, p.Tx2, html.EscapeString(text))
	}
	b.WriteString(extra)
	qt.NewQTextCursor2(f.doc).InsertHtml(scaleFonts(b.String()))
	f.pinToBottom()
}

// clearDoc сначала забывает фреймы, потом чистит документ: Clear их удаляет и
// по ходу синхронно шлёт resize и прокрутку (пропала полоса прокрутки), а
// обработчики ходят по f.items и f.history.
func (f *Feed) clearDoc() {
	f.stopMovies()
	f.stopVideos()
	f.stopEmojiAnimations()
	f.spin.stop()
	f.items = nil
	f.history = nil
	f.placeholder = false
	f.resources = map[string][2]int{}
	f.pills = map[string]pill{}
	f.hovered = ""
	f.doc.Clear()
	f.doc.SetDocumentMargin(0)
}

func (f *Feed) stopMovies() {
	for id, m := range f.movies {
		m.Stop()
		m.DeleteLater()
		delete(f.movies, id)
	}
}

// SetNewSince — посты новее ts (не свои) отмечаются «Новые сообщения»;
// звать до SetPosts. 0 — без отметки.
func (f *Feed) SetNewSince(ts int64) { f.newSince = ts }

func (f *Feed) SetPosts(posts []*model.Post, hasMore bool) {
	f.Reset("")
	f.hasMore = hasMore
	f.newMarker = ""
	if f.newSince > 0 {
		for _, p := range posts {
			if p.CreateAt > f.newSince && p.UserId != f.d.Me.Id && !p.IsSystemMessage() {
				f.newMarker = p.Id
				break
			}
		}
	}
	for _, p := range posts {
		f.insert(len(f.items), p)
	}
}

// PrependOlder добавляет подгруженные старые посты, не сдвигая видимое.
func (f *Feed) PrependOlder(posts []*model.Post, hasMore bool) {
	// Расстояние до низа — до того, как убрать строку «Загружаем историю…»:
	// она над видимыми постами, а полосу прокрутки Qt пересчитает позже.
	if len(posts) > 0 {
		f.keepFromBottom = f.bar.Maximum() - f.bar.Value()
	}
	f.hideHistoryLoading()
	f.loadingOlder = false
	f.hasMore = hasMore
	if len(posts) == 0 {
		return
	}
	for i, p := range posts {
		f.insert(i, p)
	}
}

// OlderFailed — подгрузка старых не удалась; следующая прокрутка вверх повторит.
func (f *Feed) OlderFailed() {
	f.hideHistoryLoading()
	f.loadingOlder = false
}

func (f *Feed) showHistoryLoading() {
	if f.history != nil || len(f.items) == 0 {
		return
	}
	c := qt.NewQTextCursor2(f.doc)
	c.SetPosition(f.items[0].frame.FirstPosition() - 1)
	f.history = c.InsertFrame(frameFormat())
	c.InsertHtml(scaleFonts(fmt.Sprintf(`<p align="center" style="margin-top:8px;margin-bottom:8px;font-size:13px;color:%s">Загружаем историю…</p>`, Pal().Tx2)))
	f.squash(f.history.FirstPosition() - 1)
	f.squash(f.history.LastPosition() + 1)
}

func (f *Feed) hideHistoryLoading() {
	fr := f.history
	if fr == nil {
		return
	}
	f.history = nil // до удаления фрейма — см. clearDoc
	c := qt.NewQTextCursor2(f.doc)
	c.SetPosition(fr.FirstPosition() - 1)
	c.SetPosition2(fr.LastPosition()+1, qt.QTextCursor__KeepAnchor)
	c.RemoveSelectedText()
}

// Has — показан ли пост.
func (f *Feed) Has(id string) bool { return f.index(id) >= 0 }

// Upsert вставляет новый пост по времени или перерисовывает изменённый.
func (f *Feed) Upsert(p *model.Post) {
	f.edit(func() {
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
	})
}

func (f *Feed) Remove(id string) {
	f.edit(func() { f.remove(id) })
}

func (f *Feed) remove(id string) {
	i := f.index(id)
	if i < 0 {
		return
	}
	fr := f.items[i].frame
	f.items = slices.Delete(f.items, i, i+1) // до удаления фрейма — см. clearDoc
	c := qt.NewQTextCursor2(f.doc)
	c.SetPosition(fr.FirstPosition() - 1)
	c.SetPosition2(fr.LastPosition()+1, qt.QTextCursor__KeepAnchor)
	c.RemoveSelectedText()
	if i < len(f.items) {
		f.render(i) // мог стать заголовком группы
	}
	f.renderThreadRoot()
}

// RefreshNames перерисовывает ленту, когда догрузились профили.
func (f *Feed) RefreshNames() { f.renderAll() }

// renderAll перерисовывает все посты: догрузились картинки или имена, сменилась тема.
func (f *Feed) renderAll() {
	f.edit(func() {
		for i := range f.items {
			f.render(i)
		}
	})
}

// edit — правка документа без прыжков прокрутки. Пока пост перерисовывается,
// документ на миг короче, полоса прокрутки обрезает значение до максимума, и
// onScroll принял бы это за прокрутку пользователя к низу — лента прилипала
// к концу. Поэтому на время правки прокрутка «наша», а после неё лента у низа
// остаётся у низа, иначе пост у верхнего края остаётся на своём месте.
func (f *Feed) edit(do func()) {
	if f.editing {
		do()
		return
	}
	f.editing = true
	stick := f.stick
	anchor, top := f.topPost()
	do()
	f.editing = false
	f.programmatic = true
	f.doc.Size() // раскладка до конца: дальше нужны настоящие позиции
	switch {
	case stick:
		f.bar.SetValue(f.bar.Maximum())
	case anchor != "":
		if i := f.index(anchor); i >= 0 {
			f.bar.SetValue(f.bar.Value() + f.postTop(i) - top)
		}
	}
	f.programmatic = false
	f.stick = stick
}

// topPost — пост у верхнего края окна и где его верх (px от края окна).
func (f *Feed) topPost() (string, int) {
	for y := 0; y < 24; y += 4 {
		if it := f.postAtY(y); it != nil {
			return it.post.Id, f.postTop(f.index(it.post.Id))
		}
	}
	return "", 0
}

// postTop — верх фрейма i-го поста в координатах viewport. Из раскладки
// документа, а не из cursorRect у QTextEdit: тот после прокрутки из кода
// бывает сдвинут на старое смещение — и восстановление места в edit() сдвигало
// ленту на эту разницу.
func (f *Feed) postTop(i int) int {
	f.doc.Size() // раскладка ленивая: без этого место фрейма бывает старым
	return int(f.doc.DocumentLayout().FrameBoundingRect(f.items[i].frame).Top()) - f.bar.Value()
}

// cursorRect — место позиции курсора в viewport там, где Qt её рисует и ищет
// под мышью. QTextEdit::cursorRect после прокрутки из кода бывает сдвинут на
// старое смещение (у всех позиций поровну), поэтому верх блока сверяем с
// раскладкой документа.
func (f *Feed) cursorRect(c *qt.QTextCursor) *qt.QRect {
	r := f.view.CursorRect(c)
	start := qt.NewQTextCursor2(f.doc)
	start.SetPosition(c.Block().Position())
	f.doc.Size()
	top := int(f.doc.DocumentLayout().BlockBoundingRect(c.Block()).Top()) - f.bar.Value()
	r.Translate(0, top-f.view.CursorRect(start).Top())
	return r
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
		f.renderAll()
	})
}

// customEmoji — картинка кастомного эмодзи в тексте: ресурс документа
// «emoji:имя» 20 px; у анимированного кадры подменяются в нём же.
func (f *Feed) customEmoji(name string) string {
	path := f.customEmojiPath(name)
	if path == "" {
		return ""
	}
	src := "emoji:" + name
	if _, ok := f.resources[src]; !ok {
		f.addResource(src, f.emojiPixmap(name, path, 20), 20, 20)
		f.animateEmoji(name)
	}
	return src
}

// customEmojiPath — файл кастомного эмодзи; нет — догрузить и перерисовать ленту.
func (f *Feed) customEmojiPath(name string) string {
	if f.d.Assets == nil {
		return ""
	}
	p := f.d.Assets.Emoji(name)
	if p == "" {
		f.d.Assets.WantEmoji(name, f.refreshSoon)
	}
	return p
}

// emojiPixmap — кастомное эмодзи size px: текущий кадр анимированного или картинка с диска.
func (f *Feed) emojiPixmap(name, path string, size int) *qt.QPixmap {
	pm := f.d.Assets.EmojiFrame(name)
	if pm == nil {
		pm = qt.NewQPixmap4(path)
	}
	pm = pm.Scaled3(size*resourceDPR, size*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
	pm.SetDevicePixelRatio(resourceDPR)
	return pm
}

// animateEmoji подписывает ленту на кадры анимированного эмодзи (для
// обычного — ничего): плашки реакций и эмодзи в тексте перерисовываются в
// тех же ресурсах документа.
func (f *Feed) animateEmoji(name string) {
	if _, ok := f.animSubs[name]; ok || f.d.Assets == nil {
		return
	}
	f.animSubs[name] = f.d.Assets.OnEmojiFrame(name, func() { f.emojiFrame(name) })
}

func (f *Feed) emojiFrame(name string) {
	changed := false
	if src := "emoji:" + name; f.resources[src] != [2]int{} {
		f.doc.AddResource(int(qt.QTextDocument__ImageResource), qt.NewQUrl3(src), f.emojiPixmap(name, f.d.Assets.Emoji(name), 20).ToQVariant())
		changed = true
	}
	for src, pl := range f.pills {
		if pl.emoji != name || pl.render == nil {
			continue
		}
		pl.normal, pl.hover = pl.render()
		f.pills[src] = pl
		cur := pl.normal
		if f.hovered == src {
			cur = pl.hover
		}
		f.doc.AddResource(int(qt.QTextDocument__ImageResource), qt.NewQUrl3(src), cur.ToQVariant())
		changed = true
	}
	if changed {
		f.view.Viewport().Update()
	}
}

// stopEmojiAnimations — лента очищается или закрывается: анимации ей больше не нужны.
func (f *Feed) stopEmojiAnimations() {
	for name, cancel := range f.animSubs {
		cancel()
		delete(f.animSubs, name)
	}
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

// postAtY — пост, в чей фрейм (вместе с полями) попадает строка y viewport.
// CursorForPosition для этого не годится: в верхнем поле поста он отдаёт
// ближайшую позицию — конец предыдущего.
func (f *Feed) postAtY(y int) *postItem {
	f.doc.Size() // раскладка ленивая: без этого место фрейма бывает старым
	lay := f.doc.DocumentLayout()
	dy := float64(y + f.bar.Value())
	i := sort.Search(len(f.items), func(i int) bool {
		r := lay.FrameBoundingRect(f.items[i].frame)
		return r.Y()+r.Height() > dy
	})
	if i < len(f.items) && lay.FrameBoundingRect(f.items[i].frame).Y() <= dy {
		return f.items[i]
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
	it := &postItem{post: p, frame: c.InsertFrame(frameFormat())}
	f.items = slices.Insert(f.items, i, it)
	f.write(i, c)
	// Между соседними фреймами (и до первого, и после последнего) Qt держит
	// пустой абзац — без этого он давал бы пустую строку между сообщениями.
	f.squash(it.frame.FirstPosition() - 1)
	f.squash(it.frame.LastPosition() + 1)
	if i+1 < len(f.items) {
		f.render(i + 1) // следующий пост мог стать продолжением этого
	}
	if i > 0 {
		f.renderThreadRoot()
	}
}

// renderThreadRoot — в ленте треда под корнем разделитель «N ответов»: число меняется.
func (f *Feed) renderThreadRoot() {
	if f.d.Thread && len(f.items) > 0 {
		f.render(0)
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
	it.html = f.postHTML(i)
	c.InsertHtml(scaleFonts(it.html))
	f.applyMarkers(it.frame)
	// После таблицы Qt оставляет пустой абзац — он даёт лишнюю строку под сообщением.
	if c.Position() > it.frame.FirstPosition() {
		f.squash(c.Position())
	}
}

// applyMarkers — чек-листы: «☑ »/«☐ » в начале пункта списка становятся
// маркерами Qt (флажок вместо точки).
func (f *Feed) applyMarkers(fr *qt.QTextFrame) {
	for b := f.doc.FindBlock(fr.FirstPosition()); b.IsValid() && b.Position() <= fr.LastPosition(); b = b.Next() {
		text := b.Text()
		marker := qt.QTextBlockFormat__NoMarker
		switch {
		case strings.HasPrefix(text, "☑ "):
			marker = qt.QTextBlockFormat__Checked
		case strings.HasPrefix(text, "☐ "):
			marker = qt.QTextBlockFormat__Unchecked
		default:
			continue
		}
		c := qt.NewQTextCursor2(f.doc)
		c.SetPosition(b.Position())
		c.SetPosition2(b.Position()+2, qt.QTextCursor__KeepAnchor)
		c.RemoveSelectedText()
		bf := qt.NewQTextBlockFormat()
		bf.SetMarker(marker)
		c.MergeBlockFormat(bf)
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

func frameFormat() *qt.QTextFrameFormat {
	ff := qt.NewQTextFrameFormat()
	ff.SetBorder(0)
	ff.SetPadding(0)
	ff.SetMargin(0)
	return ff
}

// startsGroup — пост начинает группу (со строкой автора): другой автор, перерыв,
// новый день, системный пост, отметка «Новые сообщения», первый ответ в треде.
func (f *Feed) startsGroup(i int) bool {
	p := f.items[i].post
	if i == 0 || p.Id == f.newMarker || (f.d.Thread && i == 1) {
		return true
	}
	return !continues(f.items[i-1].post, p)
}

func (f *Feed) postHTML(i int) string {
	pal := Pal()
	p := f.items[i].post
	var b strings.Builder
	if (i == 0 && !f.d.Thread) || (i > 0 && !sameDay(f.items[i-1].post.CreateAt, p.CreateAt)) {
		b.WriteString(f.separatorHTML(dayLabel(p.CreateAt, time.Now()), pal.Tx2, pal.Line, false))
	}
	if p.Id == f.newMarker {
		b.WriteString(f.separatorHTML("Новые сообщения", pal.Dang, pal.Dang, true))
	}

	group := f.startsGroup(i)
	bg := ""
	if f.mentionsMe(p) {
		bg = ` bgcolor="` + pal.MenRow + `"`
	}
	top := 6
	if !group {
		top = 0
	}
	avatar := ""
	if group && !p.IsSystemMessage() {
		avatar = fmt.Sprintf(`<a href="%s"><img src="%s" width="32" height="32"></a>`, f.authorLink(p), f.authorAvatar(p))
	}
	fmt.Fprintf(&b, `<table width="100%%" cellspacing="0" cellpadding="0"%s><tr>`+
		`<td width="%d" valign="top" style="padding-top:%dpx;padding-left:16px;padding-bottom:4px">%s</td>`+
		`<td valign="top" style="padding-top:%dpx;padding-right:%dpx;padding-bottom:4px">`,
		bg, feedAvatarCol, top, avatar, top, feedPadRight)
	if group && !p.IsSystemMessage() {
		b.WriteString(f.authorHTML(p))
	}
	b.WriteString(f.replyHTML(p))
	b.WriteString(f.bodyHTML(p))
	b.WriteString(f.filesHTML(p))
	b.WriteString(f.embedsHTML(p))
	b.WriteString(f.reactionsHTML(p))
	b.WriteString(f.threadHTML(p))
	b.WriteString("</td></tr></table>")
	if f.d.Thread && i == 0 && len(f.items) > 1 {
		b.WriteString(f.separatorHTML(pluralReplies(int64(len(f.items)-1)), pal.Tx2, pal.Line, false))
	}
	return b.String()
}

// separatorHTML — служебная строка с линией: разделитель дней, «N ответов»
// (подпись слева) или «Новые сообщения» (подпись справа). Qt не понимает
// nowrap и сжимает ячейку подписи до буквы — ширину считаем сами; линия —
// ячейка с фоном и шрифтом в 1 px (height у ячейки Qt не слушает).
func (f *Feed) separatorHTML(label, textColor, lineColor string, labelRight bool) string {
	// Ширина — метриками шрифта ленты: размер подписи пересчитает scaleFonts.
	labelW := qt.NewQFontMetrics(font(style.fonts.Feed, feedPx(12), qt.QFont__DemiBold)).HorizontalAdvance(label) + 22
	line := fmt.Sprintf(`<td valign="middle" style="padding-top:12px;padding-bottom:4px"><table width="100%%" cellspacing="0" cellpadding="0"><tr><td bgcolor="%s" style="font-size:1px">&nbsp;</td></tr></table></td>`, lineColor)
	text := fmt.Sprintf(`<td width="%d" valign="middle" style="padding-top:12px;padding-bottom:4px;padding-left:10px;padding-right:10px;font-size:12px;font-weight:600;color:%s">%s</td>`, labelW, textColor, html.EscapeString(label))
	if labelRight {
		return `<table width="100%" cellspacing="0" cellpadding="0" style="margin-left:16px;margin-right:6px"><tr>` + line + text + `</tr></table>`
	}
	return `<table width="100%" cellspacing="0" cellpadding="0" style="margin-left:6px;margin-right:16px"><tr>` + text + line + `</tr></table>`
}

func (f *Feed) isBot(p *model.Post) bool {
	if u := f.d.Users(p.UserId); u != nil && u.IsBot {
		return true
	}
	return p.GetProp("from_bot") == "true" || p.GetProp("from_webhook") == "true"
}

// authorName — имя для показа: у интеграций своё (override_username), а
// user_id у их постов — создатель вебхука.
func (f *Feed) authorName(p *model.Post) string {
	if in, ok := store.PostIntegration(p); ok && in.Name != "" {
		return in.Name
	}
	return f.d.Names(p.UserId)
}

// authorLink — клик по имени или аватару: карточка автора, а у поста
// интеграции — карточка интеграции, не её создателя.
func (f *Feed) authorLink(p *model.Post) string {
	if _, ok := store.PostIntegration(p); ok {
		return hookLink + p.Id
	}
	return markdown.UserLinkScheme + url.PathEscape(strings.ToLower(f.d.Names(p.UserId)))
}

func (f *Feed) authorAvatar(p *model.Post) string {
	in, ok := store.PostIntegration(p)
	if !ok {
		return f.avatarSrc(f.d.Users(p.UserId), p.UserId, f.authorName(p), f.isBot(p))
	}
	name := f.authorName(p)
	// Имя и адрес иконки — чужой текст: в имя ресурса только хэш (ASCII, QUrl его не тронет).
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%t\x00%s", p.UserId, name, in.CreatorIcon, in.IconURL))
	src := "avatar-hook:" + hex.EncodeToString(sum[:12])
	if _, ok := f.resources[src]; ok {
		return src
	}
	reload := func() {
		f.addResource(src, integrationAvatar(f.d.Assets, in, f.d.Users(p.UserId), name, 32, nil), 32, 32)
		f.view.Viewport().Update()
	}
	f.addResource(src, integrationAvatar(f.d.Assets, in, f.d.Users(p.UserId), name, 32, reload), 32, 32)
	return src
}

func (f *Feed) authorHTML(p *model.Post) string {
	pal := Pal()
	bot := ""
	if f.isBot(p) {
		bot = fmt.Sprintf(`&nbsp;<span style="font-size:11px;font-weight:600;background-color:%s;color:%s">&nbsp;БОТ&nbsp;</span>`, pal.Surf2, pal.Tx2)
	}
	return fmt.Sprintf(`<p style="margin:0"><a href="%s" style="color:%s;font-weight:600;text-decoration:none">%s</a>%s&nbsp;&nbsp;<span style="font-family:'PT Mono';font-size:12px;color:%s" title="%s">%s</span></p>`,
		f.authorLink(p), pal.Tx, html.EscapeString(f.authorName(p)), bot+f.statusHTML(p),
		pal.Tx2, formatTime(p.CreateAt), time.UnixMilli(p.CreateAt).Format("15:04"))
}

// replyHTML — без свёрнутых тредов над ответом в ленте канала: «↳ Ответ в треде».
func (f *Feed) replyHTML(p *model.Post) string {
	if f.d.Thread || p.RootId == "" {
		return ""
	}
	pal := Pal()
	about := ""
	if root := f.post(p.RootId); root != nil {
		about = fmt.Sprintf(` <b>%s:</b> %s`, html.EscapeString(f.authorName(root)), html.EscapeString(firstLine(root.Message, 60)))
	}
	return fmt.Sprintf(`<p style="margin:0"><a href="%s%s" style="font-size:12px;color:%s;text-decoration:none">↳ Ответ в треде%s</a></p>`,
		threadLink, p.RootId, pal.Tx2, about)
}

func (f *Feed) bodyHTML(p *model.Post) string {
	pal := Pal()
	if p.IsSystemMessage() {
		msg := p.Message
		if msg == "" {
			msg = p.Type
		}
		return fmt.Sprintf(`<p style="margin:0"><span style="font-size:13px;font-style:italic;color:%s">%s</span></p>`, pal.Tx2, html.EscapeString(msg))
	}
	var body string
	if jumbo, ok := jumboEmoji(p.Message); ok {
		body = `<p style="margin:0;font-size:32px">` + html.EscapeString(jumbo) + `</p>`
	} else {
		body = f.md.Render(p.Message)
	}
	if p.EditAt > 0 {
		edited := fmt.Sprintf(`<span style="font-size:12px;color:%s"> (изменено)</span>`, pal.Tx2)
		if strings.HasSuffix(body, "</p>") {
			body = strings.TrimSuffix(body, "</p>") + edited + "</p>"
		} else {
			body += `<p style="margin:0">` + edited + `</p>`
		}
	}
	return body + f.md.Attachments(p.Attachments())
}

// jumboEmoji — сообщение из одних эмодзи (до восьми) показываем крупно.
func jumboEmoji(msg string) (string, bool) {
	fields := strings.Fields(msg)
	if len(fields) == 0 || len(fields) > 8 {
		return "", false
	}
	var out []string
	for _, f := range fields {
		if strings.HasPrefix(f, ":") && strings.HasSuffix(f, ":") && len(f) > 2 {
			s, ok := emoji.Char(f[1 : len(f)-1])
			if !ok {
				return "", false
			}
			out = append(out, s)
			continue
		}
		for _, r := range f {
			if !(unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r) || r == 0x200d || r == 0xfe0f || (r >= 0x1f3fb && r <= 0x1f3ff) || (r >= 0x1f1e6 && r <= 0x1f1ff)) {
				return "", false
			}
		}
		out = append(out, f)
	}
	return strings.Join(out, " "), true
}

// mentionsMe — пост упоминает меня или всех (@channel, @all, @here).
func (f *Feed) mentionsMe(p *model.Post) bool {
	if p.UserId == f.d.Me.Id || p.IsSystemMessage() {
		return false
	}
	msg := strings.ToLower(p.Message)
	for _, name := range []string{strings.ToLower(f.d.Me.Username), "channel", "all", "here"} {
		token := "@" + name
		for from := 0; ; {
			i := strings.Index(msg[from:], token)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(token)
			if (start == 0 || !isNameByte(msg[start-1])) && (end == len(msg) || !isNameByte(msg[end])) {
				return true
			}
			from = end
		}
	}
	return false
}

func isNameByte(b byte) bool {
	return b == '_' || b == '-' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z'
}

// filesHTML: картинки — превью по месту (размер известен заранее, поэтому
// лента не прыгает, когда превью догрузится), остальное — блок файла.
func (f *Feed) filesHTML(p *model.Post) string {
	if p.Metadata == nil || len(p.Metadata.Files) == 0 {
		return ""
	}
	pal := Pal()
	var b strings.Builder
	for _, fi := range p.Metadata.Files {
		if isVideo(fi) {
			b.WriteString(f.videoHTML(fi))
			continue
		}
		if isImage(fi) {
			w, h := f.imageSize(fi)
			fmt.Fprintf(&b, `<p style="margin-top:6px;margin-bottom:0"><a href="%s%s"><img src="%s" width="%d" height="%d" title="%s"></a></p>`,
				imageLink, fi.Id, f.previewSrc(fi, w, h), w, h, html.EscapeString(fi.Name+" — открыть"))
			continue
		}
		actions := fmt.Sprintf(`<a href="%s%s" style="color:%s">Сохранить…</a>`, saveLink, fi.Id, pal.Acc)
		if canOpen(fi) {
			actions = fmt.Sprintf(`<a href="%s%s" style="color:%s">Открыть</a> · `, openLink, fi.Id, pal.Acc) + actions
		} else {
			actions += fmt.Sprintf(` <span style="font-size:12px;color:%s">· тип небезопасен для открытия</span>`, pal.Tx2)
		}
		fmt.Fprintf(&b, `<table cellspacing="0" cellpadding="0" style="margin-top:6px"><tr><td bgcolor="%s" style="padding-top:8px;padding-bottom:8px;padding-left:10px;padding-right:14px">`+
			`<img src="%s" width="20" height="20">&nbsp;<b>%s</b><span style="font-size:12px;color:%s">&nbsp;&nbsp;%s</span><br>%s</td></tr></table>`,
			pal.Surf2, f.iconSrc("file-text", pal.Tx2, 20), html.EscapeString(fi.Name), pal.Tx2, html.EscapeString(fileKind(fi)), actions)
	}
	return b.String()
}

// fileKind — «PDF · 1,2 МБ».
func fileKind(fi *model.FileInfo) string {
	ext := strings.ToUpper(fi.Extension)
	if ext == "" {
		return formatSize(fi.Size)
	}
	return ext + " · " + formatSize(fi.Size)
}

// imageSize — картинка не увеличивается, не шире текстовой колонки и не выше 400.
func (f *Feed) imageSize(fi *model.FileInfo) (int, int) {
	maxW := f.view.Viewport().Width() - feedAvatarCol - feedPadRight
	if maxW < 100 {
		maxW = 600
	}
	if fi.Width <= 0 || fi.Height <= 0 {
		return min(240, maxW), 160
	}
	w, h := fitSize(fi.Width, fi.Height, maxW, previewMaxH)
	return max(w, 48), max(h, 48)
}

// previewSrc — ресурс с превью или заглушка того же размера, пока превью грузится.
// GIF анимируется: кадры подменяет QMovie в том же ресурсе.
func (f *Feed) previewSrc(fi *model.FileInfo, w, h int) string {
	name := fmt.Sprintf("preview:%s:%dx%d", fi.Id, w, h)
	if _, ok := f.resources[name]; ok {
		return name
	}
	if f.d.Assets != nil {
		if path := f.d.Assets.Preview(fi.Id); path != "" {
			if pm := qt.NewQPixmap4(path); !pm.IsNull() {
				pm = pm.Scaled3(w*resourceDPR, h*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
				pm.SetDevicePixelRatio(resourceDPR)
				f.addResource(name, pm, w, h)
				if fi.MimeType == "image/gif" {
					f.animate(fi, name, w, h)
				}
				return name
			}
		} else {
			f.d.Assets.WantPreview(fi.Id, f.refreshSoon)
		}
	}
	ph := fmt.Sprintf("placeholder:%dx%d", w, h)
	if _, ok := f.resources[ph]; !ok {
		img := qt.NewQImage3(w, h, qt.QImage__Format_ARGB32_Premultiplied)
		img.FillWithColor(qt.NewQColor6(Pal().Surf2))
		f.addResource(ph, qt.QPixmap_FromImage(img), w, h)
	}
	return ph
}

// animate крутит GIF: качает файл целиком, кадры QMovie кладёт в ресурс name.
func (f *Feed) animate(fi *model.FileInfo, name string, w, h int) {
	if f.movies[fi.Id] != nil || f.d.Assets == nil {
		return
	}
	f.movies[fi.Id] = qt.NewQMovie() // занято: второй вызов не начнёт загрузку снова
	f.d.Assets.Download(fi, func(path string, err error) {
		if err != nil {
			return
		}
		old := f.movies[fi.Id]
		if old == nil {
			return // ленту успели очистить
		}
		old.DeleteLater()
		mv := qt.NewQMovie3(path)
		if !mv.IsValid() {
			mv.DeleteLater()
			return
		}
		f.movies[fi.Id] = mv
		mv.OnFrameChanged(func(int) {
			pm := mv.CurrentPixmap().Scaled3(w*resourceDPR, h*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
			pm.SetDevicePixelRatio(resourceDPR)
			gifBadge(pm)
			f.doc.AddResource(int(qt.QTextDocument__ImageResource), qt.NewQUrl3(name), pm.ToQVariant())
			f.view.Viewport().Update()
		})
		mv.Start()
	})
}

// gifBadge — метка «GIF» в углу кадра.
func gifBadge(pm *qt.QPixmap) {
	p := qt.NewQPainter2(pm.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	p.SetPenWithStyle(qt.NoPen)
	p.SetBrush(qt.NewQBrush3(qt.NewQColor11(0, 0, 0, 150)))
	p.DrawRoundedRect(qt.NewQRectF4(6, 6, 30, 16), 3, 3)
	p.SetPen(qt.NewQColor6("#ffffff"))
	p.SetFont(pixelFont(10, qt.QFont__DemiBold))
	p.DrawText7(6, 6, 30, 16, int(qt.AlignCenter), "GIF")
	p.End()
}

func (f *Feed) addResource(name string, pm *qt.QPixmap, w, h int) {
	f.doc.AddResource(int(qt.QTextDocument__ImageResource), qt.NewQUrl3(name), pm.ToQVariant())
	f.resources[name] = [2]int{w, h}
}

// iconSrc — иконка из дизайн-пакета ресурсом документа.
func (f *Feed) iconSrc(name, color string, size int) string {
	src := fmt.Sprintf("icon:%s:%s:%d", name, strings.TrimPrefix(color, "#"), size)
	if _, ok := f.resources[src]; !ok {
		f.addResource(src, iconPixmap(name, color, size), size, size)
	}
	return src
}

// avatarSrc — аватар 32 px ресурсом документа. Фото догружается фоном и
// подменяет инициалы в том же ресурсе.
func (f *Feed) avatarSrc(u *model.User, userID, name string, bot bool) string {
	src := fmt.Sprintf("avatar:%s:%s", userID, url.PathEscape(name))
	if _, ok := f.resources[src]; ok {
		return src
	}
	reload := func() {
		f.addResource(src, avatarPixmap(f.d.Assets, f.d.Users(userID), name, bot, 32, nil), 32, 32)
		f.view.Viewport().Update()
	}
	f.addResource(src, avatarPixmap(f.d.Assets, u, name, bot, 32, reload), 32, 32)
	return src
}

// reactionsHTML — реакции картинками-плашками и «+».
func (f *Feed) reactionsHTML(p *model.Post) string {
	groups := groupReactions(p)
	if len(groups) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<p style="margin-top:6px;margin-bottom:0">`)
	for _, g := range groups {
		names := make([]string, len(g.users))
		for k, u := range g.users {
			names[k] = f.d.Names(u)
		}
		src := fmt.Sprintf("reaction:%s:%s", p.Id, url.PathEscape(g.name))
		w, h := f.reactionPill(src, g.name, len(g.users), slices.Contains(g.users, f.d.Me.Id))
		fmt.Fprintf(&b, `<a href="%s%s:%s"><img src="%s" width="%d" height="%d" title="%s"></a>&nbsp;`,
			reactLink, p.Id, url.PathEscape(g.name), src, w, h, html.EscapeString(":"+g.name+": — "+strings.Join(names, ", ")))
	}
	src := "reaction-plus:" + p.Id
	w, h := f.reactionPill(src, "", 0, false)
	fmt.Fprintf(&b, `<a href="%s%s"><img src="%s" width="%d" height="%d" title="Добавить реакцию"></a></p>`, reactMenuLink, p.Id, src, w, h)
	return b.String()
}

// threadHTML — под корнем треда в ленте канала: «3 ответа последний 08:35»;
// у сообщения, из которого тред ещё не начат, — «Ответить».
func (f *Feed) threadHTML(p *model.Post) string {
	if f.d.Thread || p.RootId != "" || p.IsSystemMessage() {
		return ""
	}
	pal := Pal()
	if p.ReplyCount == 0 {
		return fmt.Sprintf(`<p style="margin-top:4px;margin-bottom:0"><a href="%s%s" style="text-decoration:none"><img src="%s" width="16" height="16">&nbsp;<span style="font-size:13px;color:%s">Ответить</span></a></p>`,
			threadLink, p.Id, f.iconSrc("reply", pal.Tx2, 16), pal.Tx2)
	}
	last := ""
	if p.LastReplyAt > 0 {
		last = fmt.Sprintf(`&nbsp;&nbsp;<span style="font-size:13px;color:%s">последний %s</span>`, pal.Tx2, time.UnixMilli(p.LastReplyAt).Format("15:04"))
	}
	return fmt.Sprintf(`<p style="margin-top:6px;margin-bottom:0"><a href="%s%s" style="text-decoration:none"><img src="%s" width="16" height="16">&nbsp;<span style="color:%s;font-weight:600">%s</span></a>%s</p>`,
		threadLink, p.Id, f.iconSrc("messages-square", pal.Acc, 16), pal.Acc, pluralReplies(p.ReplyCount), last)
}

// reactionPill рисует плашку реакции (обычную и при наведении) в ресурс src;
// name="" — плашка «+». Скругления и рамки в тексте документа не сделать —
// поэтому картинкой.
func (f *Feed) reactionPill(src, name string, count int, mine bool) (w, h int) {
	pal := Pal()
	label, emojiText, custom := "", "", ""
	if name != "" {
		label = fmt.Sprint(count)
		if s, ok := emoji.Char(name); ok {
			emojiText = s
		} else if path := f.customEmojiPath(name); path != "" {
			custom = path
		} else {
			emojiText = ":" + name + ":"
		}
	}
	h = 24
	countFont := pixelFont(12, qt.QFont__DemiBold)
	emojiFont := pixelFont(16, qt.QFont__Normal)
	emojiW := 16
	if custom == "" && emojiText != "" {
		emojiW = qt.NewQFontMetrics(emojiFont).HorizontalAdvance(emojiText)
	}
	if name == "" {
		w = 32
	} else {
		w = 6 + emojiW + 4 + qt.NewQFontMetrics(countFont).HorizontalAdvance(label) + 8
	}
	draw := func(bg, border, fg string) *qt.QPixmap {
		img := qt.NewQImage3(w*resourceDPR, h*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
		img.Fill2(qt.Transparent)
		img.SetDevicePixelRatio(resourceDPR)
		p := qt.NewQPainter2(img.QPaintDevice)
		p.SetRenderHint(qt.QPainter__Antialiasing)
		p.SetPenWithPen(qt.NewQPen4(qt.NewQBrush3(qt.NewQColor6(border)), 1))
		p.SetBrush(qt.NewQBrush3(qt.NewQColor6(bg)))
		p.DrawRoundedRect(qt.NewQRectF4(0.5, 0.5, float64(w-1), float64(h-1)), 11.5, 11.5)
		if name == "" {
			p.DrawPixmap9((w-16)/2, (h-16)/2, iconPixmap("smile-plus", pal.Tx2, 16))
		} else {
			if custom != "" {
				p.DrawPixmap9(6, (h-16)/2, f.emojiPixmap(name, custom, 16))
			} else {
				p.SetFont(emojiFont)
				p.SetPen(qt.NewQColor6(fg))
				p.DrawText7(6, 0, emojiW+1, h, int(qt.AlignVCenter|qt.AlignLeft), emojiText)
			}
			p.SetFont(countFont)
			p.SetPen(qt.NewQColor6(fg))
			p.DrawText7(6+emojiW+4, 0, w-emojiW-10, h, int(qt.AlignVCenter|qt.AlignLeft), label)
		}
		p.End()
		return qt.QPixmap_FromImage(img)
	}
	bg, border, fg := pal.Surf, pal.Line, pal.Tx
	if mine {
		bg, border, fg = pal.AccW, pal.Acc, pal.SelTx
	}
	pl := pill{emoji: name, render: func() (*qt.QPixmap, *qt.QPixmap) { return draw(bg, border, fg), draw(pal.Hover, border, fg) }}
	pl.normal, pl.hover = pl.render()
	f.pills[src] = pl
	if custom != "" {
		f.animateEmoji(name)
	}
	cur := pl.normal
	if f.hovered == src {
		cur = pl.hover
	}
	f.addResource(src, cur, w, h)
	return w, h
}

// onHover — курсор над ссылкой: плашке реакции подменяем картинку на «наведённую».
func (f *Feed) onHover(link string) {
	src := ""
	switch {
	case strings.HasPrefix(link, reactLink):
		src = "reaction:" + strings.TrimPrefix(link, reactLink)
	case strings.HasPrefix(link, reactMenuLink):
		src = "reaction-plus:" + strings.TrimPrefix(link, reactMenuLink)
	}
	if src == f.hovered {
		return
	}
	set := func(name string, hover bool) {
		pl, ok := f.pills[name]
		if !ok {
			return
		}
		pm := pl.normal
		if hover {
			pm = pl.hover
		}
		f.doc.AddResource(int(qt.QTextDocument__ImageResource), qt.NewQUrl3(name), pm.ToQVariant())
	}
	if f.hovered != "" {
		set(f.hovered, false)
	}
	f.hovered = src
	if src != "" {
		set(src, true)
	}
	f.view.Viewport().Update()
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
		if p := f.post(strings.TrimPrefix(link, reactMenuLink)); p != nil && f.OnPickReaction != nil {
			f.OnPickReaction(p)
		}
	case strings.HasPrefix(link, threadLink):
		f.openThread(strings.TrimPrefix(link, threadLink))
	case strings.HasPrefix(link, openLink), strings.HasPrefix(link, imageLink):
		id := strings.TrimPrefix(strings.TrimPrefix(link, openLink), imageLink)
		if fi := f.file(id); fi != nil && f.OnOpenFile != nil {
			f.OnOpenFile(fi)
		}
	case strings.HasPrefix(link, saveLink):
		if fi := f.file(strings.TrimPrefix(link, saveLink)); fi != nil && f.OnSaveFile != nil {
			f.OnSaveFile(fi)
		}
	case link == retryLink:
		if f.OnRetry != nil {
			f.OnRetry()
		}
	case strings.HasPrefix(link, permalinkLink):
		ch, post, _ := strings.Cut(strings.TrimPrefix(link, permalinkLink), ":")
		if f.OnPermalink != nil && ch != "" && post != "" {
			f.OnPermalink(ch, post)
		}
	case strings.HasPrefix(link, videoLink):
		if fi := f.file(strings.TrimPrefix(link, videoLink)); fi != nil {
			f.playInline(fi)
		}
	case strings.HasPrefix(link, hookLink):
		if p := f.post(strings.TrimPrefix(link, hookLink)); p != nil && f.OnIntegration != nil {
			f.OnIntegration(p)
		}
	default:
		if f.OnLink != nil {
			f.OnLink(link)
		}
	}
}

func (f *Feed) file(id string) *model.FileInfo {
	fi, _ := f.fileAndPost(id)
	return fi
}

// fileOwner — пост, к которому приложен файл.
func (f *Feed) fileOwner(id string) *model.Post {
	_, p := f.fileAndPost(id)
	return p
}

func (f *Feed) fileAndPost(id string) (*model.FileInfo, *model.Post) {
	for _, it := range f.items {
		if it.post.Metadata == nil {
			continue
		}
		for _, fi := range it.post.Metadata.Files {
			if fi.Id == id {
				return fi, it.post
			}
		}
	}
	return nil, nil
}

// contextMenu — все действия с сообщением: кнопок при наведении в ленте нет.
func (f *Feed) contextMenu(e *qt.QContextMenuEvent) {
	it := f.postAtY(e.Pos().Y())
	menu := qt.NewQMenu(f.view.QWidget)
	cp := menu.QWidget.AddActionWithText("Копировать выделенное")
	cp.SetShortcut(qt.NewQKeySequence2("Ctrl+C"))
	cp.SetEnabled(f.view.TextCursor().HasSelection())
	cp.OnTriggered(f.view.Copy)
	link := f.view.AnchorAt(e.Pos())
	cl := menu.QWidget.AddActionWithText("Копировать ссылку")
	cl.SetEnabled(strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://"))
	cl.OnTriggered(func() { qt.QGuiApplication_Clipboard().SetText(link) })
	if it != nil {
		p := it.post
		if !p.IsSystemMessage() {
			menu.AddSeparator()
			if !f.d.Thread {
				reply := menu.QWidget.AddActionWithText("Ответить в треде")
				reply.OnTriggered(func() { f.openThread(threadRoot(p)) })
			}
			// Эмодзи в QMenu не кладём: шрифт меню выбирает система, и они
			// бывают чёрно-белыми. Выбор — в своём окне, эмодзи там картинками.
			react := menu.QWidget.AddActionWithText("Реакция…")
			react.OnTriggered(func() {
				if f.OnPickReaction != nil {
					f.OnPickReaction(p)
				}
			})
			if p.UserId == f.d.Me.Id && store.HasLinkPreview(p) && f.OnRemovePreview != nil {
				rm := menu.QWidget.AddActionWithText("Убрать предпросмотр ссылки")
				rm.OnTriggered(func() { f.OnRemovePreview(p) })
			}
		}
		menu.AddSeparator()
		ct := menu.QWidget.AddActionWithText("Копировать текст сообщения")
		ct.OnTriggered(func() { qt.QGuiApplication_Clipboard().SetText(p.Message) })
	} else {
		menu.AddSeparator()
	}
	all := menu.QWidget.AddActionWithText("Выделить всё")
	all.SetShortcut(qt.NewQKeySequence2("Ctrl+A"))
	all.OnTriggered(f.view.SelectAll)
	menu.ExecWithPos(e.GlobalPos())
	menu.DeleteLater()
}

// copySelection — в буфер только текст: картинки (аватары, реакции, превью) в
// выделении дали бы символы-заглушки U+FFFC.
func (f *Feed) copySelection(super func() *qt.QMimeData) *qt.QMimeData {
	md := super()
	text := md.Text()
	md.Delete()
	text = strings.NewReplacer("￼", "", " ", "\n", " ", "\n", " ", " ").Replace(text)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
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

func pluralReplies(n int64) string { return plural(n, "ответ", "ответа", "ответов") }

// continues — пост продолжает предыдущий: тот же автор в тот же день, без
// долгого перерыва, оба не системные.
func continues(prev, p *model.Post) bool {
	return prev != nil && prev.UserId == p.UserId && sameAuthor(prev, p) &&
		p.CreateAt-prev.CreateAt < groupMillis && sameDay(prev.CreateAt, p.CreateAt) &&
		!prev.IsSystemMessage() && !p.IsSystemMessage()
}

// sameAuthor — у постов одного user_id разные авторы, если один из них от
// интеграции: вебхук пишет от имени своего создателя.
func sameAuthor(a, b *model.Post) bool {
	ia, oka := store.PostIntegration(a)
	ib, okb := store.PostIntegration(b)
	return oka == okb && ia == ib
}

func sameDay(a, b int64) bool {
	ta, tb := time.UnixMilli(a), time.UnixMilli(b)
	return ta.Year() == tb.Year() && ta.YearDay() == tb.YearDay()
}

var (
	monthsGen = []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}
	weekdays  = []string{"Воскресенье", "Понедельник", "Вторник", "Среда", "Четверг", "Пятница", "Суббота"}
)

// dayLabel — «Сегодня, 1 ноября», «Вчера, 31 октября», «Суббота, 25 октября»
// (на этой неделе), «12 марта», «12 марта 2025» (другой год).
func dayLabel(millis int64, now time.Time) string {
	t := time.UnixMilli(millis).In(now.Location())
	date := fmt.Sprintf("%d %s", t.Day(), monthsGen[t.Month()-1])
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	switch days := int(math.Round(today.Sub(day).Hours() / 24)); {
	case days == 0:
		return "Сегодня, " + date
	case days == 1:
		return "Вчера, " + date
	case days > 1 && days < 7:
		return weekdays[t.Weekday()] + ", " + date
	case t.Year() != now.Year():
		return fmt.Sprintf("%s %d", date, t.Year())
	}
	return date
}

func formatTime(millis int64) string {
	return time.UnixMilli(millis).Format("2006-01-02 15:04:05")
}

// formatSize — «1,2 МБ», «12 КБ», «23 Б».
func formatSize(n int64) string {
	switch {
	case n >= 1<<20:
		return strings.Replace(fmt.Sprintf("%.1f МБ", float64(n)/(1<<20)), ".", ",", 1)
	case n >= 1<<10:
		return fmt.Sprintf("%d КБ", (n+512)>>10)
	}
	return fmt.Sprintf("%d Б", n)
}

// newTextLabel — QLabel с переносом и выделением (подписи вне ленты).
func newTextLabel(format qt.TextFormat) *qt.QLabel {
	l := qt.NewQLabel2()
	l.SetWordWrap(true)
	l.SetTextFormat(format)
	l.SetTextInteractionFlags(qt.TextSelectableByMouse | qt.LinksAccessibleByMouse)
	return l
}

// onResize — ширина ленты поменялась: картинки впишутся в новую ширину, но
// не сразу. resizeEvent приходит и изнутри правок документа (doc.Clear,
// удаление поста убирают полосу прокрутки), а перерисовка посреди правки
// трогает фреймы, которые та уже удалила. Заодно при перетаскивании
// разделителя перерисовываем один раз, а не на каждый пиксель.
func (f *Feed) onResize() {
	f.pinToBottom()
	f.placeVideos()
	if abs(f.view.Viewport().Width()-f.lastWidth) >= 24 {
		f.refit.Start2()
	}
}

func (f *Feed) fitImages() {
	w := f.view.Viewport().Width()
	if abs(w-f.lastWidth) < 24 {
		return
	}
	f.lastWidth = w
	f.edit(func() {
		for i, it := range f.items {
			if it.post.Metadata != nil && (len(it.post.Metadata.Embeds) > 0 ||
				slices.ContainsFunc(it.post.Metadata.Files, func(fi *model.FileInfo) bool { return isImage(fi) || isVideo(fi) })) {
				f.render(i)
			}
		}
	})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// pinToBottom прижимает короткую ленту к низу, как в чатах (заглушку — к
// середине): верхнее поле документа растягивается на свободное место.
func (f *Feed) pinToBottom() {
	root := f.doc.RootFrame()
	ff := root.FrameFormat()
	cur := ff.TopMargin()
	content := f.doc.Size().Height() - cur
	free := float64(f.view.Viewport().Height()) - content
	if f.placeholder {
		free /= 2
	}
	want := math.Max(0, free)
	if math.Abs(want-cur) < 1 {
		return
	}
	ff.SetTopMargin(want)
	root.SetFrameFormat(ff)
}

func (f *Feed) onScroll(v int) {
	if f.programmatic || f.editing {
		return
	}
	f.keepFromBottom = -1
	f.stick = v >= f.bar.Maximum()-4
	if v <= f.bar.Minimum() && f.hasMore && !f.loadingOlder && len(f.items) > 0 && f.OnNeedOlder != nil {
		f.loadingOlder = true
		f.showHistoryLoading()
		f.OnNeedOlder()
	}
}

// onRange — высота содержимого изменилась (добавились посты, пересчитались
// переносы): держим низ или место, на котором был пользователь.
func (f *Feed) onRange(_, max int) {
	f.pinToBottom()
	if f.editing {
		return // прокрутку выставит edit, когда правка закончится
	}
	f.programmatic = true
	defer func() { f.programmatic = false }()
	switch {
	case f.keepFromBottom >= 0:
		f.bar.SetValue(max - f.keepFromBottom)
	case f.stick:
		f.bar.SetValue(max)
	}
}
