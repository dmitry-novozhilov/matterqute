package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"math"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/emoji"
	"matterqute/internal/markdown"
	"matterqute/internal/sched"
	"matterqute/internal/store"
)

// Посты одного автора с перерывом меньше этого идут без заголовка.
const groupMillis = 5 * 60 * 1000

// Ленту канала рисуем по частям: HTML поста Qt разбирает долго (длинный пост
// с разметкой — десятки миллисекунд), а 60 таких подряд вешали окно на
// секунды. Сразу — то, что видно, остальное — порциями не дольше drainSlice
// над ним; между порциями окно отвечает.
const drainSlice = 25 * time.Millisecond

// Длинное сообщение (и длинный текст вложения) — свёрнуто, как в веб-клиенте
// («Show more»): видно начало и «Показать полностью». Свёрнутое и рисуется в
// разы быстрее — длинный пост стоил десятки миллисекунд разбора и раскладки.
const (
	longLines, longChars = 20, 2000 // длиннее — сворачиваем
	clipLines, clipChars = 12, 1200 // столько видно свёрнутым
)

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
	linkImageLink = "mm-linkimg:"    // mm-linkimg:<пост>:<N> — N-я картинка по ссылке в посте
	retryLink     = "mm-retry:"      // повторить загрузку ленты
	hookLink      = "mm-hook:"       // mm-hook:<post> — карточка интеграции, написавшей пост
	videoLink     = "mm-video:"      // mm-video:<файл> — играть ролик в ленте (повторно — пауза)
	moreLink      = "mm-more:"       // mm-more:<пост> — развернуть длинное сообщение
	lessLink      = "mm-less:"       // mm-less:<пост> — свернуть его обратно
	historyLink   = "mm-history:"    // mm-history:<пост> — история правок (своего поста)
	actionLink    = "mm-action:"     // mm-action:<пост>:<id действия> — кнопка или список интеграции
	spinnerSrc    = "spinner:24"     // ресурс с текущим кадром крутилки
)

// Пост в документе — фрейм (frame) с полями под колонку аватара и справа.
// С разделителем над текстом (день, «Новые сообщения», где в поиске) или
// «N ответов» под ним (корень треда) он на всю ширину, а текст — во
// вложенном фрейме (content); у обычного content == frame. Аватар и подсветку строки
// рисует лента поверх (paintUnder): таблица вокруг поста вдвое удорожала
// раскладку, а вложенные в неё таблицы (вложения, цитаты, код) — ещё больше.
type postItem struct {
	post    *model.Post
	frame   *qt.QTextFrame
	content *qt.QTextFrame // текст поста
	html    string         // последняя отрисовка — для тестов и отладки
	prev    string         // id поста перед ним, с которым он отрисован (заголовок, разделитель дня)
	avatar  string         // аватар у строки автора: ключ в Feed.avatars; "" — без аватара
	link    string         // клик по аватару — как по имени автора
	bg      string         // подсветка строки (переход по ссылке, упоминание меня); "" — нет
	images  []string       // картинки по требованию (lazyimages.go), на которые ссылается его HTML
}

// postParts — пост для вставки в документ: над текстом, текст, под ним.
type postParts struct {
	head, body, tail string
	top              int // поле над текстом: у первого в группе 6, у продолжения 0
	avatar, link, bg string
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
	// Search — лента результатов поиска: у каждого сообщения — где оно и
	// «Перейти», без ссылок на треды и разделителей дней.
	Search bool
	// Where — «# backend», «# backend · в треде» для результатов поиска.
	Where func(p *model.Post) string
	// QuickReactions — эмодзи панели реакций у сообщения под курсором; nil — только «все реакции».
	QuickReactions func() []string
	// Outbox — сообщение id в очереди отправки (нет связи) и отказ сервера
	// ("" — ждёт связи); ok=false — не в очереди.
	Outbox func(id string) (failed string, ok bool)
	// EmojiImage — эмодзи картинкой (кастомное догружается, потом done).
	EmojiImage func(name string, size int, done func()) *qt.QPixmap
	// Log — посты, которые рисуются долго (debug); nil — не писать.
	Log *slog.Logger
	// Saved — пост сохранён (метка «Сохранено» и пункт меню); nil — нет.
	Saved func(postID string) bool
	// ThreadFollow — подписаны ли на тред с корнем rootID; ok=false — свёрнутых
	// тредов нет (пункта «Подписаться на тред» тоже). nil — как ok=false.
	ThreadFollow func(rootID string) (following, ok bool)
}

// slowPost — дольше рисуется пост: в debug-лог, с размерами, без текста.
const slowPost = 50 * time.Millisecond

// savedScroll — где была лента, когда её убрали с экрана.
type savedScroll struct {
	stick  bool
	anchor string // пост у верхнего края
	top    int    // и где его верх
}

// pill — плашка реакции: обычная и под курсором. render рисует её заново —
// для анимированного эмодзи на каждом кадре.
type pill struct {
	normal, hover *qt.QPixmap
	emoji         string
	render        func() (normal, hover *qt.QPixmap)
}

// free — картинки плашки (у выключенной кнопки обе — одна): в ресурсе
// документа своя копия.
func (pl pill) free() {
	free(pl.normal)
	if pl.hover != pl.normal {
		free(pl.hover)
	}
}

// setPill — плашка src: прежние её картинки больше не нужны.
func (f *Feed) setPill(src string, pl pill) {
	if old, ok := f.pills[src]; ok {
		old.free()
	}
	f.pills[src] = pl
}

// dropPills — плашки всех постов прочь (лента очищается или перерисовывается вся).
func (f *Feed) dropPills() {
	for _, pl := range f.pills {
		pl.free()
	}
	f.pills = map[string]pill{}
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
	resources   map[string][2]int      // имя → логический размер
	sweepWanted bool                   // ширина сменилась: картинки прежнего размера убрать (sweepResources)
	pills       map[string]pill        // имя ресурса реакции или кнопки → обычная и при наведении
	avatars     map[string]*qt.QPixmap // аватары у строк автора (рисует paintUnder)
	onAvatar    bool                   // курсор над аватаром (рука)
	expanded    map[string]bool        // длинные посты, развёрнутые пользователем
	chosen      map[string]string      // «пост:действие» → выбранное в списке интеграции (подпись)
	hovered     string                 // ресурс реакции под курсором
	movies      map[string]*qt.QMovie
	movieAt     map[string]movieTarget  // куда класть кадры GIF: ресурс того размера, в котором пост нарисован сейчас
	placeholder bool                    // в документе заглушка вместо постов
	history     *qt.QTextFrame          // строка «Загружаем историю…» над постами
	newSince    int64                   // посты новее — «Новые сообщения»
	newMarker   string                  // id поста, над которым отметка
	lastWidth   int                     // ширина, под которую вписаны картинки
	built       time.Time               // документ собран заново (SetPosts, Rebuild)
	refit       *qt.QTimer              // отложенная перерисовка картинок после ресайза
	spin        *spinner                // крутилка заглушки «Загружаем сообщения…»
	videos      map[string]*inlineVideo // ролики, которые играют в ленте, по id файла
	animSubs    map[string]func()       // подписки на кадры анимированных эмодзи: отписка по имени
	placeTimer  *qt.QTimer              // поставить ролики и панель реакций на места после правок документа
	highlight   string                  // id поста, к которому перешли по ссылке: подсвечен
	unhighlight *qt.QTimer
	quick       *quickBar // реакции у поста под курсором
	marks       []string  // подсвечиваемые слова (результаты поиска)
	unsubscribe func()

	stick          bool // лента у нижнего края: новые посты её прокручивают
	keepFromBottom int  // после подгрузки старых: сохранить расстояние до низа; -1 — не нужно
	programmatic   bool // SetValue из кода, а не прокрутка пользователем
	editing        bool // идёт правка документа (edit): прокрутку от неё не принимаем за пользователя
	hasMore        bool
	loadingOlder   bool
	refreshPending bool
	dirty          map[string]bool // посты, у которых догрузились картинки или эмодзи
	dirtyAll       bool            // перерисовать всё (догрузилось то, что не у поста)
	rendering      string          // id поста, который сейчас рисуется

	// Ещё не отрисованные старые посты над отрисованными, по времени (лента
	// канала рисуется по частям); drainTimer дорисовывает их порциями.
	pending    []*model.Post
	drainTimer *qt.QTimer
	stale      map[string]bool    // посты перерисовать порциями (тема, ширина, профиль)
	relayout   map[*postItem]bool // посты с устаревшей раскладкой Qt (checkLayout)
	disposed   bool

	// Ленты каналов готовы заранее, на экране — одна (MainWindow.showFeed).
	// Скрытая не прокручивается и не крутит анимации; её порции рисует окно
	// (OnWork), место прокрутки — запомнено при уходе.
	shown       bool
	ready       bool // в ленте посты канала (или «пока нет сообщений»), а не загрузка
	needFit     bool // скрытой — размер ленты на экране (MainWindow.buildStep)
	saved       savedScroll
	pausedAnims []string
	OnWork      func() // у скрытой ленты есть порции для отрисовки

	OnNeedOlder      func()                               // докрутили до верха, а на сервере есть ещё
	OnOpenThread     func(rootID string)                  // открыть тред поста
	OnThreadWindow   func(rootID string)                  // «Открыть тред в отдельном окне»
	OnEditHistory    func(p *model.Post)                  // щелчок по «(изменено)» у своего поста
	OnToggleReaction func(p *model.Post, emoji string)    // поставить или снять свою реакцию
	OnPickReaction   func(p *model.Post)                  // окно выбора эмодзи для реакции
	OnPermalink      func(channelID, postID string)       // «Перейти к сообщению» в предпросмотре ссылки
	OnRemovePreview  func(p *model.Post)                  // автор убирает предпросмотр ссылки
	OnEditPost       func(p *model.Post)                  // «Изменить» своё сообщение
	OnDeletePost     func(p *model.Post)                  // «Удалить…» своё сообщение
	OnCopyLink       func(p *model.Post)                  // «Копировать ссылку на сообщение»
	OnRemind         func(p *model.Post, at time.Time)    // «Напомнить»; at нулевое — спросить время
	OnForward        func(p *model.Post)                  // «Переслать…»
	OnQuote          func(p *model.Post, selected string) // «Цитировать»: выделенное в нём или ("") всё сообщение
	OnOutbox         func(id string, retry bool)          // у сообщения из очереди отправки: «Повторить» или «Удалить»

	// ReadOnly — канал в архиве: без панели реакций, реакций, цитаты,
	// правки, удаления и закрепления в меню.
	ReadOnly       bool
	OnPin          func(p *model.Post, pin bool)               // «Закрепить в канале» / «Открепить»
	OnSave         func(p *model.Post, save bool)              // «Сохранить» / «Убрать из сохранённых»
	OnMarkUnread   func(p *model.Post)                         // «Пометить непрочитанным»
	OnFollowThread func(rootID, channelID string, follow bool) // «Подписаться на тред» / «Отписаться от треда»
	OnLink         func(url string)                            // клик по ссылке в тексте
	OnIntegration  func(p *model.Post)                         // клик по имени или аватару интеграции
	OnAction       func(p *model.Post, a *model.PostAction)    // кнопка или список во вложении интеграции
	// OnFullscreenVideo — «Во весь экран» у ролика в ленте: окно с той же
	// позиции; back — вернуть позицию и «играл ли», когда окно закроют.
	OnFullscreenVideo func(fi *model.FileInfo, p *model.Post, path string, pos int64, playing bool, back func(pos int64, playing bool))
	OnOpenFile        func(fi *model.FileInfo)
	OnOpenImage       func(p *model.Post, key string) // клик по картинке: key — id файла или «link:N»
	OnCopyImage       func(p *model.Post, key string) // «Копировать картинку» у картинки под курсором; key — как у OnOpenImage
	OnSaveFile        func(fi *model.FileInfo)
	OnRetry           func() // «Повторить» в заглушке ошибки

	// Картинки по требованию (lazyimages.go): как получить, какие раскодированы,
	// какие посты были видны при последней отрисовке.
	lazy               map[string]lazyImage
	loaded             map[string]bool
	loadedLo, loadedHi int
	releaseTimer       *qt.QTimer // отпустить далёкие от видимого места (releaseFar)
}

func NewFeed(d FeedDeps) *Feed {
	if d.Users == nil {
		d.Users = func(string) *model.User { return nil }
	}
	f := &Feed{d: d, view: qt.NewQTextBrowser2(), movies: map[string]*qt.QMovie{}, movieAt: map[string]movieTarget{}, videos: map[string]*inlineVideo{},
		animSubs: map[string]func(){}, dirty: map[string]bool{}, stale: map[string]bool{}, relayout: map[*postItem]bool{}, shown: true, expanded: map[string]bool{}, chosen: map[string]string{},
		lazy: map[string]lazyImage{}, loaded: map[string]bool{}, loadedLo: -1, loadedHi: -1}
	setName(f.view.QObject, "feed")
	f.doc = f.view.Document()
	f.doc.SetDefaultFont(feedFont())
	f.setTabs()
	f.view.SetOpenLinks(false)
	f.view.SetFrameShape(qt.QFrame__NoFrame)
	f.view.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	f.view.OnAnchorClicked(func(u *qt.QUrl) { f.onAnchor(u.ToString()) })
	f.view.OnHighlighted(func(u *qt.QUrl) { f.onHover(u.ToString()) })
	f.view.OnContextMenuEvent(func(_ func(*qt.QContextMenuEvent), e *qt.QContextMenuEvent) { f.contextMenu(e) })
	f.quick = newQuickBar(f)
	f.view.OnMouseMoveEvent(func(super func(*qt.QMouseEvent), e *qt.QMouseEvent) {
		super(e)
		if e.Buttons() == qt.NoButton {
			f.quick.hover(e.Pos())
			f.hoverAvatar(e.Pos())
		} else {
			f.quick.hide() // выделяют текст
		}
	})
	f.view.OnMousePressEvent(func(super func(*qt.QMouseEvent), e *qt.QMouseEvent) {
		if it := f.avatarAt(e.Pos()); it != nil && e.Button() == qt.LeftButton {
			f.onAnchor(it.link) // как клик по имени автора
			return
		}
		super(e)
	})
	f.view.OnPaintEvent(func(super func(*qt.QPaintEvent), e *qt.QPaintEvent) {
		f.paintUnder()
		super(e)
	})
	f.view.OnLeaveEvent(func(super func(*qt.QEvent), e *qt.QEvent) {
		super(e)
		f.quick.hide()
	})
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
	f.releaseTimer = qt.NewQTimer2(f.view.QObject)
	f.releaseTimer.SetSingleShot(true)
	f.releaseTimer.OnTimeout(f.releaseFar)
	f.unhighlight = qt.NewQTimer2(f.view.QObject)
	f.unhighlight.SetSingleShot(true)
	f.unhighlight.OnTimeout(func() {
		if i := f.index(f.highlight); i >= 0 {
			f.highlight = ""
			f.edit(func() { f.render(i) })
		}
		f.highlight = ""
	})
	f.drainTimer = qt.NewQTimer2(f.view.QObject)
	f.drainTimer.SetSingleShot(true)
	f.drainTimer.OnTimeout(f.drain)
	f.placeTimer = qt.NewQTimer2(f.view.QObject)
	f.placeTimer.SetSingleShot(true)
	f.placeTimer.OnTimeout(func() {
		f.placeVideos()
		f.quick.follow()
	})
	f.doc.OnContentsChanged(func() {
		if len(f.videos) > 0 || f.quick.post != "" {
			f.placeTimer.Start(0)
		}
	})
	f.spin = newSpinner(f.view.QObject, func(pm *qt.QPixmap) {
		f.setResource(spinnerSrc, pm)
		f.view.Viewport().Update()
	})
	f.bar = f.view.VerticalScrollBar()
	f.bar.OnValueChanged(f.onScroll)
	f.bar.OnValueChanged(func(int) {
		f.placeVideos()
		f.quick.follow()
	})
	f.bar.OnRangeChanged(f.onRange)
	f.restyleRenderer()
	f.unsubscribe = OnThemeChanged(f.restyle)
	f.Reset("")
	return f
}

func (f *Feed) Widget() *qt.QWidget { return f.view.QWidget }

func (f *Feed) dispose() {
	f.disposed = true
	f.unsubscribe()
	f.stopMovies()
	f.stopEmojiAnimations()
	f.quick.hide() // и её подписки на кадры — до stopAnimations окна
	f.dropPills()
}

func (f *Feed) restyleRenderer() {
	f.md = markdown.New(markdown.Options{Me: f.d.Me.Username, ChannelExists: f.d.ChannelExists, CustomEmoji: f.customEmoji, Palette: Pal(),
		Highlight: f.marks})
}

// SetMarks — что подсвечивать в тексте (слова запроса поиска).
func (f *Feed) SetMarks(terms []string) {
	f.marks = terms
	f.restyleRenderer()
}

// restyle — смена темы: цвета в HTML и картинках рисует код, их надо пересоздать.
func (f *Feed) restyle() {
	f.restyleRenderer()
	f.doc.SetDefaultFont(feedFont())
	f.setTabs()
	f.resources = map[string][2]int{}
	f.dropPills()
	f.avatars = map[string]*qt.QPixmap{}
	if f.spin.timer.IsActive() {
		f.spin.start(Pal().Tx3, 24)
	}
	for _, v := range f.videos {
		v.restyle()
	}
	f.quick.restyle()
	f.invalidateAll()
}

// setTabs — табуляция в коде — 4 знака моноширинного (по умолчанию у Qt —
// 80 px: код с табами в узкой колонке треда переносился).
func (f *Feed) setTabs() {
	f.view.SetTabStopDistance(4 * tmp(qt.NewQFontMetricsF(font(style.fonts.Mono, style.fonts.Mono.Size, qt.QFont__Normal))).HorizontalAdvance(" "))
}

// Reset очищает ленту; msg — текст вместо постов.
func (f *Feed) Reset(msg string) {
	f.clearDoc()
	if msg != "" {
		f.Placeholder("", msg, "")
	}
	f.stick, f.keepFromBottom, f.hasMore, f.loadingOlder = true, -1, false, false
	f.saved = savedScroll{stick: true} // скрытая покажется с конца
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
	tmp(qt.NewQTextCursor2(f.doc)).InsertHtml(scaleFonts(b.String()))
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
	f.pending = nil
	f.stale = map[string]bool{}
	f.ready = false
	f.drainTimer.Stop()
	f.history = nil
	f.placeholder = false
	f.resources = map[string][2]int{}
	f.lazy, f.loaded = map[string]lazyImage{}, map[string]bool{}
	f.loadedLo, f.loadedHi = -1, -1
	f.dropPills()
	f.avatars = map[string]*qt.QPixmap{}
	f.hovered = ""
	f.quick.hide()
	f.doc.Clear()
	f.doc.SetDocumentMargin(0)
}

func (f *Feed) stopMovies() {
	for id, m := range f.movies {
		m.Stop()
		m.DeleteLater()
		delete(f.movies, id)
	}
	clear(f.movieAt)
}

// SetNewSince — посты новее ts (не свои) отмечаются «Новые сообщения»
// (0 — без отметки). Готовая лента переносит отметку сразу.
func (f *Feed) SetNewSince(ts int64) {
	f.newSince = ts
	old := f.newMarker
	f.newMarker = f.markerFor(append(slices.Clone(f.pending), f.posts()...))
	if old != f.newMarker {
		f.edit(func() {
			for _, id := range []string{old, f.newMarker} {
				if i := f.index(id); i >= 0 && id != "" {
					f.render(i)
				}
			}
		})
	}
}

// markerFor — над каким постом из posts (от старых к новым) «Новые сообщения».
func (f *Feed) markerFor(posts []*model.Post) string {
	if f.newSince > 0 {
		for _, p := range posts {
			if p.CreateAt > f.newSince && p.UserId != f.d.Me.Id && !p.IsSystemMessage() {
				return p.Id
			}
		}
	}
	return ""
}

// isRoot — p — корень в ленте треда: самый первый пост (отрисованный или в очереди).
func (f *Feed) isRoot(p *model.Post) bool {
	if !f.d.Thread || p == nil {
		return false
	}
	switch {
	case len(f.pending) > 0:
		return f.pending[0].Id == p.Id
	case len(f.items) > 0:
		return f.items[0].post.Id == p.Id
	}
	return false
}

// posts — отрисованные посты по порядку.
func (f *Feed) posts() []*model.Post {
	out := make([]*model.Post, len(f.items))
	for i, it := range f.items {
		out[i] = it.post
	}
	return out
}

func (f *Feed) SetPosts(posts []*model.Post, hasMore bool) {
	f.Reset("")
	f.built = time.Now()
	f.hasMore = hasMore
	f.newMarker = f.markerFor(posts)
	if f.d.Search {
		// Поиск — новые сверху: рисуем целиком.
		for _, p := range posts {
			f.insert(len(f.items), p)
		}
		return
	}
	// Снизу вверх, пока посты не займут окно с запасом; остальное — drain.
	// Скрытая лента — вся порциями.
	f.pending = slices.Clone(posts)
	want := float64(f.view.Viewport().Height()) * 1.5
	for f.shown && len(f.pending) > 0 && (len(f.items) == 0 || f.contentHeight() < want) {
		f.insertPending()
	}
	f.drainSoon()
}

// Rebuild — документ скрытой ленты заново из тех же постов (отрисованных и
// ждущих очереди). Перерисовка поста стирает его текст, а фреймы и таблицы
// из него (фрейм текста у поста с разделителем, блоки кода, вложения) Qt
// держит в документе до его очистки — около 2 КБ на перерисовку. Посты
// встают в очередь и рисуются фоном, порциями, как при первой сборке; место,
// где лента была, и развёрнутые посты остаются. false — сейчас нельзя: на
// экране (был бы виден пустой документ), заглушка, грузится история.
func (f *Feed) Rebuild() bool {
	if f.shown || !f.ready || f.placeholder || f.history != nil || f.loadingOlder || f.d.Search || f.d.Thread || len(f.items) == 0 {
		return false
	}
	posts := append(slices.Clone(f.pending), f.posts()...)
	saved, hasMore := f.saved, f.hasMore
	f.SetPosts(posts, hasMore)
	f.saved, f.ready = saved, true
	return true
}

// orphans — фреймы и таблицы, которые документ держит после удаления их
// текста: его дети, которых нет в дереве фреймов.
func (f *Feed) orphans() int {
	live := 0
	var walk func(fr *qt.QTextFrame)
	walk = func(fr *qt.QTextFrame) {
		live++
		for _, c := range fr.ChildFrames() {
			walk(c)
		}
	}
	walk(f.doc.RootFrame())
	all := 0
	for _, o := range f.doc.Children() {
		if o.Inherits("QTextFrame") { // и QTextTable
			all++
		}
	}
	return all - live
}

// contentHeight — высота постов в документе (без поля, которым короткая
// лента прижата к низу).
func (f *Feed) contentHeight() float64 {
	return f.doc.Size().Height() - f.doc.RootFrame().FrameFormat().TopMargin()
}

// insertPending — самый новый из неотрисованных — над лентой. Его сосед снизу
// уже отрисован с ним как предыдущим (prevPost), перерисовка не нужна.
func (f *Feed) insertPending() {
	n := len(f.pending)
	p := f.pending[n-1]
	f.pending = f.pending[:n-1]
	f.insert(0, p)
}

// hasWork — есть порции: перерисовать устаревшие или дорисовать старые.
func (f *Feed) hasWork() bool { return len(f.pending) > 0 || len(f.stale) > 0 }

func (f *Feed) drainSoon() {
	switch {
	case !f.hasWork():
		if f.sweepWanted {
			f.sweepResources()
		}
	case f.shown:
		f.drainTimer.Start(0)
	case f.OnWork != nil:
		f.OnWork()
	}
}

// drain — порция: сначала устаревшие посты (снизу вверх), потом
// неотрисованные над лентой; не дольше drainSlice, но хотя бы один пост.
// Видимое не сдвигается (edit), лента у низа остаётся у низа.
func (f *Feed) drain() {
	if wait := waitInput(lastKey, idleDrain); f.shown && wait > 0 {
		f.drainTimer.Start(int(wait.Milliseconds()) + 1) // печатают: потом
		return
	}
	start := time.Now()
	f.edit(func() {
		// На экране — снизу вверх (там обычно смотрят); скрытую — сверху:
		// перерисовка внизу заставила бы Qt разложить весь документ выше сразу,
		// а не лениво.
		for k := range f.items {
			if len(f.stale) == 0 {
				break
			}
			i := k
			if f.shown {
				i = len(f.items) - 1 - k
			}
			if id := f.items[i].post.Id; f.stale[id] {
				delete(f.stale, id)
				f.render(i)
				if time.Since(start) >= drainSlice {
					return
				}
			}
		}
		clear(f.stale) // остались посты, которых в ленте уже нет
		for len(f.pending) > 0 {
			f.insertPending()
			if time.Since(start) >= drainSlice {
				return
			}
		}
	})
	if !f.shown {
		// Полоса прокрутки появилась — viewport уже: раскладка — сейчас, пока
		// документ мал, а не при показе.
		f.fitTo(f.view.Size())
	}
	f.drainSoon()
}

// invalidate — перерисовать посты, где match: видимые на экране — сразу,
// остальные — порциями (drain). Длинная лента так не вешает окно.
func (f *Feed) invalidate(match func(p *model.Post) bool) {
	for _, it := range f.items {
		if match(it.post) {
			f.stale[it.post.Id] = true
		}
	}
	if len(f.stale) == 0 {
		return
	}
	if lo, hi, ok := f.visibleRange(); ok {
		// Видимые — сразу, снизу вверх, но не дольше нескольких порций:
		// длинные посты, которых на экране много, — тоже порциями.
		start := time.Now()
		f.edit(func() {
			for i := hi; i >= lo; i-- {
				if id := f.items[i].post.Id; f.stale[id] {
					delete(f.stale, id)
					f.render(i)
					if time.Since(start) >= 4*drainSlice {
						return
					}
				}
			}
		})
	}
	f.drainSoon()
}

// paintUnder — под текстом ленты: подсветка строк и аватары у строк автора
// (их нет в документе — см. postItem).
func (f *Feed) paintUnder() {
	lo, hi, ok := f.visibleRange()
	if !ok {
		return
	}
	f.loadImages(lo, hi) // до отрисовки документа: на экран — уже картинки
	p := qt.NewQPainter2(f.view.Viewport().QPaintDevice)
	defer free(p) // деструктор и заканчивает рисование
	w := f.view.Viewport().Width()
	for _, it := range f.items[lo : hi+1] {
		if it.content == nil {
			continue
		}
		f.checkLayout(it)
		r := f.contentRect(it)
		if it.bg != "" {
			p.FillRect5(0, r.Y(), w, r.Height(), qcolor(it.bg))
		}
		if pm := f.avatars[it.avatar]; pm != nil {
			p.DrawPixmap9(16, r.Y()+int(tmp(it.content.FrameFormat()).TopMargin()), pm)
		}
	}
}

// checkLayout — раскладка Qt поста не устарела? Бывало (окно из снимка:
// лента строилась до показа окна, потом смена ширины и раскладка заново
// порциями посреди дорисовки и сверки): текст поста на месте, а фрейм
// разложен почти пустым — 50 px вместо 300, на экране аватар без текста.
// Признаки: абзац с текстом без строк или ниже низа своего фрейма. Тогда —
// после отрисовки (документ в paint не трогаем) — переразложить пост.
func (f *Feed) checkLayout(it *postItem) {
	if f.relayout[it] {
		return
	}
	// На каждой отрисовке, по каждому абзацу видимых постов: блоки и
	// прямоугольники, которые Qt отдаёт по значению, — сразу прочь (tmp).
	lay := f.doc.DocumentLayout()
	bottom := tmp(lay.FrameBoundingRect(it.content)).Bottom()
	for b := tmp(f.doc.FindBlock(it.content.FirstPosition())); b.IsValid() && b.Position() <= it.content.LastPosition(); b = tmp(b.Next()) {
		if b.Length() <= 1 {
			continue // пустые абзацы бывают ужаты до 0–1 px — их не меряем
		}
		if b.Layout().LineCount() == 0 || tmp(lay.BlockBoundingRect(b)).Bottom() > bottom+1 {
			if len(f.relayout) == 0 {
				mainthread.Start(f.fixLayout)
			}
			f.relayout[it] = true
			return
		}
	}
}

// fixLayout — переразложить посты, найденные checkLayout.
func (f *Feed) fixLayout() {
	if f.disposed {
		return
	}
	n := 0
	for it := range f.relayout {
		if f.index(it.post.Id) >= 0 { // ещё в ленте
			fr := it.frame
			f.doc.MarkContentsDirty(fr.FirstPosition(), fr.LastPosition()-fr.FirstPosition()+1)
			n++
		}
	}
	clear(f.relayout)
	if n == 0 {
		return
	}
	if f.d.Log != nil {
		f.d.Log.Debug("feed relayout", "posts", n)
	}
	f.edit(func() {}) // раскладка до конца, место прокрутки — как было
	f.view.Viewport().Update()
}

// contentRect — фрейм текста поста в координатах viewport (с полями).
func (f *Feed) contentRect(it *postItem) *qt.QRect {
	r := f.doc.DocumentLayout().FrameBoundingRect(it.content)
	defer free(r)
	return tmp(qt.NewQRect4(int(r.Left()), int(r.Top())-f.bar.Value(), int(r.Width()), int(r.Height())))
}

// avatarAt — пост, на чей аватар показывает pos (viewport); nil — не на аватаре.
func (f *Feed) avatarAt(pos *qt.QPoint) *postItem {
	if pos.X() < 16 || pos.X() >= 48 {
		return nil
	}
	lo, hi, ok := f.visibleRange()
	if !ok {
		return nil
	}
	for _, it := range f.items[lo : hi+1] {
		if it.avatar == "" || it.content == nil {
			continue
		}
		top := f.contentRect(it).Y() + int(tmp(it.content.FrameFormat()).TopMargin())
		if pos.Y() >= top && pos.Y() < top+32 {
			return it
		}
	}
	return nil
}

// hoverAvatar — над аватаром курсор-рука, как над ссылкой; ушли — снова
// текстовый (QTextBrowser сам его не вернёт: над пустым полем ссылки не было).
func (f *Feed) hoverAvatar(pos *qt.QPoint) {
	on := f.avatarAt(pos) != nil
	if on == f.onAvatar {
		return
	}
	f.onAvatar = on
	if on {
		f.view.Viewport().SetCursor(tmp(qt.NewQCursor2(qt.PointingHandCursor)))
	} else {
		f.view.Viewport().SetCursor(tmp(qt.NewQCursor2(qt.IBeamCursor)))
	}
}

// visibleRange — индексы первого и последнего поста, видимых в окне (по
// раскладке документа). ok=false — лента не на экране или видимых нет.
func (f *Feed) visibleRange() (lo, hi int, ok bool) {
	if !f.shown || len(f.items) == 0 {
		return 0, 0, false
	}
	h := float64(f.view.Viewport().Height())
	tmp(f.doc.Size())
	lay, y := f.doc.DocumentLayout(), float64(f.bar.Value())
	lo, hi = -1, -1
	for i, it := range f.items {
		r := tmp(lay.FrameBoundingRect(it.frame)) // на каждой отрисовке, по каждому посту
		if r.Bottom() <= y {
			continue
		}
		if r.Top() >= y+h {
			break
		}
		if lo < 0 {
			lo = i
		}
		hi = i
	}
	return lo, hi, lo >= 0
}

// SetShown — лента на экране или убрана (на экране другой канал). Скрытая
// запоминает место, не крутит GIF и эмодзи и не держит ролики.
func (f *Feed) SetShown(on bool) {
	if f.shown == on {
		return
	}
	f.shown = on
	if !on {
		id, top := f.topPost()
		f.saved = savedScroll{stick: f.stick, anchor: id, top: top}
		f.drainTimer.Stop()
		f.stopVideos()
		f.quick.hide()
		// Картинки — снова заглушки (и GIF стоят): раскодирует отрисовка, когда
		// лента вернётся на экран.
		f.releaseTimer.Stop()
		f.releaseImages(nil)
		f.loadedLo, f.loadedHi = -1, -1
		for _, mv := range f.movies {
			if mv.State() == qt.QMovie__Running {
				mv.SetPaused(true)
			}
		}
		for name, cancel := range f.animSubs {
			cancel()
			delete(f.animSubs, name)
			f.pausedAnims = append(f.pausedAnims, name)
		}
		// Наверх: смена ширины, пока лента скрыта, раскладывает документ
		// лениво (Qt — от видимого места), а не весь сразу.
		f.programmatic = true
		f.bar.SetValue(0)
		f.programmatic = false
		f.drainSoon()
		return
	}
	for _, mv := range f.movies {
		if mv.State() == qt.QMovie__Paused {
			mv.SetPaused(false)
		}
	}
	for _, name := range f.pausedAnims {
		f.animateEmoji(name)
	}
	f.pausedAnims = nil
	tmp(f.doc.Size()) // раскладка до конца, если не успела
	f.pinToBottom()
	f.programmatic = true
	switch sv := f.saved; {
	case sv.stick:
		f.bar.SetValue(f.bar.Maximum())
	case sv.anchor != "":
		if f.pendingIndex(sv.anchor) >= 0 {
			f.flushPending() // пересобранная фоном (Rebuild) не дорисовалась до него
			tmp(f.doc.Size())
		}
		if i := f.index(sv.anchor); i >= 0 {
			f.bar.SetValue(f.bar.Value() + f.postTop(i) - sv.top)
		}
	}
	f.programmatic = false
	f.stick, f.keepFromBottom = f.saved.stick, -1
	f.fitImages() // ширина могла смениться, пока лента была скрыта: видимое — сразу
	f.drainSoon()
}

// fitTo — размер скрытой ленты. Qt отдаёт resize скрытому виджету (и его
// viewport, по которому QTextEdit раскладывает документ) только при показе —
// и тогда на показе весь документ раскладывается заново. Отдаём сразу.
func (f *Feed) fitTo(size *qt.QSize) {
	w := f.view.QWidget
	if w.Width() != size.Width() || w.Height() != size.Height() {
		w.ResizeWithQSize(size)
	}
	for _, x := range []*qt.QWidget{w, f.view.Viewport()} {
		if x.TestAttribute(qt.WA_PendingResizeEvent) {
			qt.QCoreApplication_SendEvent(x.QObject, tmp(qt.NewQResizeEvent(x.Size(), tmp(qt.NewQSize()))).QEvent)
			x.SetAttribute2(qt.WA_PendingResizeEvent, false)
		}
	}
}

// flushPending дорисовывает всё сразу: нужен пост, который ещё не отрисован.
func (f *Feed) flushPending() {
	if len(f.pending) == 0 {
		return
	}
	f.drainTimer.Stop()
	f.edit(func() {
		for len(f.pending) > 0 {
			f.insertPending()
		}
	})
}

func (f *Feed) pendingIndex(id string) int {
	return slices.IndexFunc(f.pending, func(p *model.Post) bool { return p.Id == id })
}

// prevPost — пост перед i-м: в ленте или самый новый из неотрисованных над ней.
func (f *Feed) prevPost(i int) *model.Post {
	if i > 0 {
		return f.items[i-1].post
	}
	if n := len(f.pending); n > 0 {
		return f.pending[n-1]
	}
	return nil
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
	// Лента могла пережить обрыв связи с историей старше той, что знает store.
	posts = slices.DeleteFunc(slices.Clone(posts), func(p *model.Post) bool { return f.Has(p.Id) })
	if len(posts) == 0 {
		return
	}
	f.pending = append(posts, f.pending...)
	// Верхний пост был первым — теперь над ним есть предыдущий.
	if len(f.items) > 0 && f.items[0].prev != f.pending[len(f.pending)-1].Id {
		f.edit(func() { f.render(0) })
	}
	f.drainSoon()
}

// OlderFailed — подгрузка старых не удалась; следующая прокрутка вверх повторит.
func (f *Feed) OlderFailed() {
	f.hideHistoryLoading()
	f.loadingOlder = false
}

// LoadingOlder — подгрузка старого запущена не прокруткой (поиск сообщения
// по ссылке): строка «Загружаем историю…» сверху.
func (f *Feed) LoadingOlder() {
	f.loadingOlder = true
	f.showHistoryLoading()
}

func (f *Feed) showHistoryLoading() {
	if f.history != nil || len(f.items) == 0 {
		return
	}
	c := tmp(qt.NewQTextCursor2(f.doc))
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
	c := tmp(qt.NewQTextCursor2(f.doc))
	c.SetPosition(fr.FirstPosition() - 1)
	c.SetPosition2(fr.LastPosition()+1, qt.QTextCursor__KeepAnchor)
	c.RemoveSelectedText()
}

// Has — показан ли пост.
func (f *Feed) Has(id string) bool { return f.index(id) >= 0 || f.pendingIndex(id) >= 0 }

// Upsert вставляет новый пост по времени или перерисовывает изменённый.
func (f *Feed) Upsert(p *model.Post) {
	f.edit(func() {
		if i := f.index(p.Id); i >= 0 {
			f.items[i].post = p
			f.render(i)
			return
		}
		if j := f.pendingIndex(p.Id); j >= 0 {
			f.pending[j] = p // отрисуется, когда до него дойдёт очередь
			return
		}
		f.insertByTime(p)
	})
}

// insertByTime — нового поста в ленте нет: на его место по времени (старше
// отрисованных — в очередь неотрисованных).
func (f *Feed) insertByTime(p *model.Post) {
	i := len(f.items)
	for i > 0 && f.items[i-1].post.CreateAt > p.CreateAt {
		i--
	}
	if n := len(f.pending); i == 0 && n > 0 && f.pending[n-1].CreateAt > p.CreateAt {
		j := n
		for j > 0 && f.pending[j-1].CreateAt > p.CreateAt {
			j--
		}
		f.pending = slices.Insert(f.pending, j, p) // среди неотрисованных
		return
	}
	f.keepFromBottom = -1
	f.insert(i, p)
}

// Merge — лента сверена с сервером после обрыва связи (Timeline.Sync): новые
// посты — на места, пропавшие — прочь, изменённые — перерисовать (видимые
// сразу, остальные порциями).
func (f *Feed) Merge(changed, removed []*model.Post) {
	redraw := map[string]bool{}
	f.edit(func() {
		for _, p := range removed {
			f.remove(p.Id)
		}
		for _, p := range changed {
			switch i, j := f.index(p.Id), f.pendingIndex(p.Id); {
			case i >= 0:
				f.items[i].post = p
				redraw[p.Id] = true
			case j >= 0:
				f.pending[j] = p
			default:
				f.insertByTime(p)
			}
		}
	})
	f.invalidate(func(p *model.Post) bool { return redraw[p.Id] })
}

func (f *Feed) Remove(id string) {
	f.edit(func() { f.remove(id) })
}

func (f *Feed) remove(id string) {
	if j := f.pendingIndex(id); j >= 0 {
		f.pending = slices.Delete(f.pending, j, j+1)
		if j == len(f.pending) && len(f.items) > 0 {
			f.render(0) // у верхнего поста сменился предыдущий
		}
		return
	}
	i := f.index(id)
	if i < 0 {
		return
	}
	fr := f.items[i].frame
	f.items = slices.Delete(f.items, i, i+1) // до удаления фрейма — см. clearDoc
	c := tmp(qt.NewQTextCursor2(f.doc))
	c.SetPosition(fr.FirstPosition() - 1)
	c.SetPosition2(fr.LastPosition()+1, qt.QTextCursor__KeepAnchor)
	c.RemoveSelectedText()
	if i < len(f.items) {
		f.render(i) // мог стать заголовком группы
	}
	f.renderThreadRoot()
}

// invalidateAll — перерисовать всё (видимое сразу, остальное порциями).
func (f *Feed) invalidateAll() { f.invalidate(func(*model.Post) bool { return true }) }

// RefreshUser перерисовывает посты пользователя: изменился профиль (имя, свой статус у имени).
func (f *Feed) RefreshUser(id string) {
	f.invalidate(func(p *model.Post) bool { return p.UserId == id })
}

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
	if f.editing || !f.shown {
		do() // скрытую не прокручиваем: место вернёт SetShown
		return
	}
	f.editing = true
	stick := f.stick
	anchor, top := f.topPost()
	do()
	f.editing = false
	f.programmatic = true
	tmp(f.doc.Size()) // раскладка до конца: дальше нужны настоящие позиции
	switch {
	case stick:
		f.bar.SetValue(f.bar.Maximum())
	case f.keepFromBottom >= 0:
		// Подгружены старые (PrependOlder): как onRange, держим расстояние до
		// низа — верхний пост уже сдвинут, убрали «Загружаем историю…».
		f.bar.SetValue(f.bar.Maximum() - f.keepFromBottom)
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
	tmp(f.doc.Size()) // раскладка ленивая: без этого место фрейма бывает старым
	return int(f.doc.DocumentLayout().FrameBoundingRect(f.items[i].frame).Top()) - f.bar.Value()
}

// cursorRect — место позиции курсора в viewport там, где Qt её рисует и ищет
// под мышью. QTextEdit::cursorRect после прокрутки из кода бывает сдвинут на
// старое смещение (у всех позиций поровну), поэтому верх блока сверяем с
// раскладкой документа.
func (f *Feed) cursorRect(c *qt.QTextCursor) *qt.QRect {
	r := f.view.CursorRect(c)
	start := tmp(qt.NewQTextCursor2(f.doc))
	start.SetPosition(c.Block().Position())
	tmp(f.doc.Size())
	top := int(f.doc.DocumentLayout().BlockBoundingRect(c.Block()).Top()) - f.bar.Value()
	r.Translate(0, top-f.view.CursorRect(start).Top())
	return r
}

// ScrollToPost прокручивает к посту — его верх на трети высоты ленты — и
// ставит курсор в его начало.
func (f *Feed) ScrollToPost(id string) {
	if f.pendingIndex(id) >= 0 {
		f.flushPending()
	}
	i := f.index(id)
	if i < 0 {
		return
	}
	c := tmp(qt.NewQTextCursor2(f.doc))
	c.SetPosition(f.items[i].frame.FirstPosition())
	f.view.SetTextCursor(c)
	f.programmatic = true
	f.bar.SetValue(f.bar.Value() + f.postTop(i) - f.view.Viewport().Height()/3)
	f.programmatic = false
	f.stick, f.keepFromBottom = f.bar.Value() >= f.bar.Maximum()-4, -1
}

// ScrollToTop — к началу ленты (результаты поиска: новые сверху).
func (f *Feed) ScrollToTop() {
	f.stick, f.keepFromBottom = false, -1
	f.programmatic = true
	f.bar.SetValue(0)
	f.programmatic = false
}

// Highlight подсвечивает пост на несколько секунд: к нему перешли по ссылке.
func (f *Feed) Highlight(id string) {
	if f.pendingIndex(id) >= 0 {
		f.flushPending()
	}
	prev := f.highlight
	f.highlight = id
	f.edit(func() {
		for _, x := range []string{prev, id} {
			if i := f.index(x); i >= 0 && x != "" {
				f.render(i)
			}
		}
	})
	f.unhighlight.Start(highlightFor)
}

// refreshLater — что звать, когда догрузится картинка или эмодзи: перерисовать
// пост, который сейчас рисуется (вне отрисовки поста — всю ленту).
func (f *Feed) refreshLater() func() {
	id := f.rendering
	return func() { f.refreshSoon(id) }
}

// refreshSoon перерисует пост id ("" — всю ленту) один раз после текущих
// событий: картинок и эмодзи может прийти пачка подряд.
func (f *Feed) refreshSoon(id string) {
	if id == "" {
		f.dirtyAll = true
	} else {
		f.dirty[id] = true
	}
	if f.refreshPending || f.d.Assets == nil {
		return
	}
	f.refreshPending = true
	f.d.Assets.Later(func() {
		f.refreshPending = false
		all, dirty := f.dirtyAll, f.dirty
		f.dirtyAll, f.dirty = false, map[string]bool{}
		f.invalidate(func(p *model.Post) bool { return all || dirty[p.Id] })
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
		pm := f.emojiPixmap(name, path, 20)
		f.addResource(src, pm, 20, 20)
		free(pm)
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
		f.d.Assets.WantEmoji(name, f.refreshLater())
	}
	return p
}

// emojiPixmap — кастомное эмодзи size px: текущий кадр анимированного или
// картинка с диска. Картинка новая: free, когда станет не нужна.
func (f *Feed) emojiPixmap(name, path string, size int) *qt.QPixmap {
	src := f.d.Assets.EmojiFrame(name)
	if src == nil {
		src = qt.NewQPixmap4(path)
	}
	defer free(src)
	pm := src.Scaled3(size*resourceDPR, size*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
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
	if !f.shown {
		if !slices.Contains(f.pausedAnims, name) {
			f.pausedAnims = append(f.pausedAnims, name) // запустим при показе
		}
		return
	}
	f.animSubs[name] = f.d.Assets.OnEmojiFrame(name, func() { f.emojiFrame(name) })
}

func (f *Feed) emojiFrame(name string) {
	changed := false
	if src := "emoji:" + name; f.resources[src] != [2]int{} {
		pm := f.emojiPixmap(name, f.d.Assets.Emoji(name), 20)
		f.setResource(src, pm)
		free(pm)
		changed = true
	}
	for src, pl := range f.pills {
		if pl.emoji != name || pl.render == nil {
			continue
		}
		pl.free() // прежний кадр: в ресурсе его заменит новый
		pl.normal, pl.hover = pl.render()
		f.pills[src] = pl
		cur := pl.normal
		if f.hovered == src {
			cur = pl.hover
		}
		f.setResource(src, cur)
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

// attachmentMax — ширина вложения интеграции (Grafana и т. п.) не больше этой:
// по колонке ленты, а на широком окне строки не растягиваются бесконечно.
const attachmentMax = 800

// highlightFor — сколько держится подсветка поста, к которому перешли, мс.
const highlightFor = 4000

// postAtY — пост, в чей фрейм (вместе с полями) попадает строка y viewport.
// CursorForPosition для этого не годится: в верхнем поле поста он отдаёт
// ближайшую позицию — конец предыдущего.
func (f *Feed) postAtY(y int) *postItem {
	tmp(f.doc.Size()) // раскладка ленивая: без этого место фрейма бывает старым
	lay := f.doc.DocumentLayout()
	dy := float64(y + f.bar.Value())
	i := sort.Search(len(f.items), func(i int) bool {
		r := tmp(lay.FrameBoundingRect(f.items[i].frame))
		return r.Y()+r.Height() > dy
	})
	if i < len(f.items) && tmp(lay.FrameBoundingRect(f.items[i].frame)).Y() <= dy {
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
		ready := f.ready // «пока нет сообщений» — и вот первое
		f.clearDoc()
		f.ready = ready
	}
	c := tmp(qt.NewQTextCursor2(f.doc))
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
	if i+1 < len(f.items) && f.items[i+1].prev != p.Id {
		f.render(i + 1) // следующий пост мог стать продолжением этого
	}
	if i > 0 {
		f.renderThreadRoot()
	}
}

// renderThreadRoot — в ленте треда под корнем разделитель «N ответов»: число меняется.
func (f *Feed) renderThreadRoot() {
	if f.d.Thread && len(f.items) > 0 && len(f.pending) == 0 {
		f.render(0) // корень отрисован (а не ждёт в очереди)
	}
}

// render перерисовывает пост на месте. Выделение пользователя вне поста
// документ сохраняет сам: его курсор сдвигается вместе с текстом.
func (f *Feed) render(i int) {
	fr := f.items[i].frame
	c := tmp(qt.NewQTextCursor2(f.doc))
	c.SetPosition(fr.FirstPosition())
	c.SetPosition2(fr.LastPosition(), qt.QTextCursor__KeepAnchor)
	c.RemoveSelectedText()
	f.write(i, c)
}

// write пишет пост в его фрейм; c стоит в начале пустого фрейма.
func (f *Feed) write(i int, c *qt.QTextCursor) {
	it := f.items[i]
	start := time.Now()
	defer func() {
		if d := time.Since(start); d > slowPost && f.d.Log != nil {
			f.logSlow(it, d)
		}
	}()
	it.prev = ""
	if prev := f.prevPost(i); prev != nil {
		it.prev = prev.Id
	}
	f.rendering = it.post.Id
	pp := f.parts(i)
	f.rendering = ""
	it.html = pp.head + pp.body + pp.tail
	it.images = f.lazyNames(it.html)
	it.avatar, it.link, it.bg = pp.avatar, pp.link, pp.bg
	// Пустой абзац, в который пишем, мог остаться от прежней отрисовки поста
	// (render стирает текст фрейма, а последний абзац остаётся со своим
	// форматом) — ужатый до 0–1 px пустой абзац вокруг фрейма текста. Первый
	// абзац HTML встаёт в него и берёт его высоту строки: пост «пропадал»,
	// когда с него уходила черта «Новые сообщения». Пишем с чистого формата.
	c.SetBlockFormat(tmp(qt.NewQTextBlockFormat()))
	// Обычный пост — один фрейм с полями; с разделителем над текстом или
	// «N ответов» под ним — фрейм на всю ширину и в нём фрейм текста.
	nested := pp.head != "" || pp.tail != ""
	if nested {
		it.frame.SetFrameFormat(frameFormat())
		if pp.head != "" {
			c.InsertHtml(scaleFonts(pp.head))
		}
		it.content = c.InsertFrame(contentFormat(pp.top))
		c.SetBlockFormat(tmp(qt.NewQTextBlockFormat()))
	} else {
		it.frame.SetFrameFormat(contentFormat(pp.top))
		it.content = it.frame
	}
	c.InsertHtml(scaleFonts(pp.body))
	// После таблицы Qt оставляет пустой абзац — он даёт лишнюю строку под сообщением.
	if c.Position() > it.content.FirstPosition() {
		f.squash(c.Position())
	}
	f.applyMarkers(it.content)
	if !nested {
		return
	}
	if pp.tail != "" {
		c.SetPosition(it.content.LastPosition() + 1)
		c.InsertHtml(scaleFonts(pp.tail))
		f.squash(c.Position())
	}
	// Вокруг фрейма текста Qt держит пустые абзацы: до него — под таблицей
	// разделителя (как под любой таблицей), после — 0 px, чтобы пост с
	// разделителем был той же высоты, что в ячейке таблицы.
	f.squash(it.content.FirstPosition() - 1)
	if pp.tail == "" {
		f.squashTo(it.content.LastPosition()+1, 0)
	}
}

// logSlow — что в посте, который рисовался d: размеры и состав, не текст
// (чтобы понять, что дорого, по логу с настоящего сервера).
func (f *Feed) logSlow(it *postItem, d time.Duration) {
	p := it.post
	fields, attText := 0, 0
	for _, a := range p.Attachments() {
		fields += len(a.Fields)
		attText += utf8.RuneCountInString(a.Text) + utf8.RuneCountInString(a.Pretext)
	}
	files, embeds := 0, 0
	if p.Metadata != nil {
		files, embeds = len(p.Metadata.Files), len(p.Metadata.Embeds)
	}
	f.d.Log.Debug("slow post", "took", d.Round(time.Millisecond), "post", p.Id, "channel", p.ChannelId,
		"html", len(it.html), "message", utf8.RuneCountInString(p.Message), "lines", strings.Count(p.Message, "\n")+1,
		"tables", strings.Count(it.html, "<table"), "attachments", len(p.Attachments()), "attachment_text", attText,
		"fields", fields, "files", files, "embeds", embeds, "collapsed", isLong(p) && !f.expanded[p.Id], "width", f.view.Viewport().Width())
}

// contentFormat — фрейм текста поста: слева колонка аватара, справа поле;
// сверху top (у продолжения группы 0), снизу 4.
func contentFormat(top int) *qt.QTextFrameFormat {
	ff := frameFormat()
	ff.SetLeftMargin(feedAvatarCol)
	ff.SetRightMargin(feedPadRight)
	ff.SetTopMargin(float64(top))
	ff.SetBottomMargin(4)
	return ff
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
		c := tmp(qt.NewQTextCursor2(f.doc))
		c.SetPosition(b.Position())
		c.SetPosition2(b.Position()+2, qt.QTextCursor__KeepAnchor)
		c.RemoveSelectedText()
		bf := tmp(qt.NewQTextBlockFormat())
		bf.SetMarker(marker)
		c.MergeBlockFormat(bf)
	}
}

// squash ужимает пустой абзац в позиции pos до 1 px; непустой не трогает.
func (f *Feed) squash(pos int) { f.squashTo(pos, 1) }

// squashTo — пустой абзац в pos высотой px.
func (f *Feed) squashTo(pos, px int) {
	if pos < 0 || pos >= f.doc.CharacterCount() {
		return
	}
	c := tmp(qt.NewQTextCursor2(f.doc))
	c.SetPosition(pos)
	if c.Block().Length() != 1 {
		return
	}
	bf := tmp(qt.NewQTextBlockFormat())
	bf.SetLineHeight(float64(px), int(qt.QTextBlockFormat__FixedHeight))
	c.SetBlockFormat(bf)
}

// frameFormat — формат фрейма без рамок и полей (временный, см. tmp).
func frameFormat() *qt.QTextFrameFormat {
	ff := tmp(qt.NewQTextFrameFormat())
	ff.SetBorder(0)
	ff.SetPadding(0)
	ff.SetMargin(0)
	return ff
}

// startsGroup — пост начинает группу (со строкой автора): другой автор, перерыв,
// новый день, системный пост, отметка «Новые сообщения», первый ответ в треде.
func (f *Feed) startsGroup(i int) bool {
	p, prev := f.items[i].post, f.prevPost(i)
	if prev == nil || f.d.Search || p.Id == f.newMarker || f.isRoot(prev) {
		return true
	}
	return !continues(prev, p)
}

// postHTML — пост целиком одной строкой (как он вставлен): для тестов и сравнения.
func (f *Feed) postHTML(i int) string {
	pp := f.parts(i)
	return pp.head + pp.body + pp.tail
}

func (f *Feed) parts(i int) postParts {
	pal := Pal()
	p, prev := f.items[i].post, f.prevPost(i)
	var pp postParts
	var b strings.Builder
	switch {
	case f.d.Search:
		b.WriteString(f.whereHTML(p))
	case (prev == nil && !f.d.Thread) || (prev != nil && !sameDay(prev.CreateAt, p.CreateAt)):
		b.WriteString(f.separatorHTML(dayLabel(p.CreateAt, time.Now()), pal.Tx2, pal.Line, false))
	}
	if p.Id == f.newMarker {
		b.WriteString(f.separatorHTML("Новые сообщения", pal.Dang, pal.Dang, true))
	}

	pp.head = b.String()
	b.Reset()

	group := f.startsGroup(i)
	switch {
	case p.Id == f.highlight:
		pp.bg = pal.AccW
	case f.mentionsMe(p):
		pp.bg = pal.MenRow
	}
	if group {
		pp.top = 6
	}
	if group && !p.IsSystemMessage() {
		pp.avatar, pp.link = f.authorAvatar(p), f.authorLink(p)
		b.WriteString(f.authorHTML(p))
	} else if marks := f.marksHTML(p); marks != "" {
		b.WriteString(`<p style="margin:0">` + marks + `</p>`)
	}
	b.WriteString(f.replyHTML(p))
	b.WriteString(f.bodyHTML(p))
	b.WriteString(f.filesHTML(p))
	b.WriteString(f.embedsHTML(p))
	b.WriteString(f.reactionsHTML(p))
	b.WriteString(f.threadHTML(p))
	b.WriteString(f.outboxHTML(p))
	// Картинки в строке (эмодзи, плашки реакций, иконки) — по верху строки,
	// как было в ячейке таблицы с valign="top" (его наследовало всё в ней):
	// по базовой линии строка выше на свисание букв.
	pp.body = `<div style="vertical-align:top">` + b.String() + `</div>`
	if n := len(f.items) + len(f.pending); f.isRoot(p) && n > 1 {
		pp.tail = f.separatorHTML(pluralReplies(int64(n-1)), pal.Tx2, pal.Line, false)
	}
	return pp
}

// whereHTML — над результатом поиска: где и когда, «Перейти» к нему.
func (f *Feed) whereHTML(p *model.Post) string {
	pal := Pal()
	where := ""
	if f.d.Where != nil {
		where = f.d.Where(p)
	}
	return fmt.Sprintf(`<table width="100%%" cellspacing="0" cellpadding="0" style="margin-top:10px"><tr>`+
		`<td style="padding-left:16px;font-size:12px;color:%s">%s · %s</td>`+
		`<td align="right" style="padding-right:%dpx;font-size:12px"><a href="%s%s:%s" style="color:%s;text-decoration:none">Перейти</a></td></tr></table>`,
		pal.Tx2, html.EscapeString(where), html.EscapeString(dayLabel(p.CreateAt, time.Now())), feedPadRight, permalinkLink, p.ChannelId, p.Id, pal.Acc)
}

// separatorHTML — служебная строка с линией: разделитель дней, «N ответов»
// (подпись слева) или «Новые сообщения» (подпись справа). Qt не понимает
// nowrap и сжимает ячейку подписи до буквы — ширину считаем сами; линия —
// ячейка с фоном и шрифтом в 1 px (height у ячейки Qt не слушает).
func (f *Feed) separatorHTML(label, textColor, lineColor string, labelRight bool) string {
	// Ширина — метриками шрифта ленты: размер подписи пересчитает scaleFonts.
	labelW := tmp(qt.NewQFontMetrics(font(style.fonts.Feed, feedPx(12), qt.QFont__DemiBold))).HorizontalAdvance(label) + 22
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
	if _, ok := f.avatars[src]; ok {
		return src
	}
	reload := func() {
		if f.disposed {
			return
		}
		f.avatars[src] = integrationAvatar(f.d.Assets, in, f.d.Users(p.UserId), name, 32, nil)
		f.view.Viewport().Update()
	}
	f.avatars[src] = integrationAvatar(f.d.Assets, in, f.d.Users(p.UserId), name, 32, reload)
	return src
}

func (f *Feed) authorHTML(p *model.Post) string {
	pal := Pal()
	bot := ""
	if f.isBot(p) {
		bot = fmt.Sprintf(`&nbsp;<span style="font-size:11px;font-weight:600;background-color:%s;color:%s">&nbsp;БОТ&nbsp;</span>`, pal.Surf2, pal.Tx2)
	}
	marks := f.marksHTML(p)
	if marks != "" {
		marks = "&nbsp;&nbsp;" + marks
	}
	return fmt.Sprintf(`<p style="margin:0"><a href="%s" style="color:%s;font-weight:600;text-decoration:none">%s</a>%s&nbsp;&nbsp;<span style="font-family:'PT Mono';font-size:12px;color:%s" title="%s">%s</span>%s</p>`,
		f.authorLink(p), pal.Tx, html.EscapeString(f.authorName(p)), bot+f.statusHTML(p)+priorityHTML(postPriority(p)),
		pal.Tx2, formatTime(p.CreateAt), time.UnixMilli(p.CreateAt).Format("15:04"), marks)
}

// marksHTML — «Закреплено», «Сохранено» у поста (иконка и подпись), как в
// веб-клиенте; "" — ни того ни другого.
func (f *Feed) marksHTML(p *model.Post) string {
	pal := Pal()
	mark := func(iconName, label string) string {
		return fmt.Sprintf(`<img src="%s" width="12" height="12">&nbsp;<span style="font-size:12px;color:%s">%s</span>`,
			f.iconSrc(iconName, pal.Tx2, 12), pal.Tx2, label)
	}
	var out []string
	if p.IsPinned {
		out = append(out, mark("pin", "Закреплено"))
	}
	if f.d.Saved != nil && f.d.Saved(p.Id) {
		out = append(out, mark("bookmark", "Сохранено"))
	}
	return strings.Join(out, "&nbsp;&nbsp;")
}

// replyHTML — без свёрнутых тредов над ответом в ленте канала: «↳ Ответ в треде».
func (f *Feed) replyHTML(p *model.Post) string {
	if f.d.Thread || f.d.Search || p.RootId == "" {
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
	if p.Type == model.PostTypeEphemeral {
		// Ответ команды: с разметкой (бывают списки, ссылки) и пометкой.
		return f.md.Render(p.Message) + fmt.Sprintf(`<p style="margin:0"><span style="font-size:12px;color:%s">Видно только вам</span></p>`, pal.Tx2)
	}
	if p.IsSystemMessage() {
		msg := p.Message
		if msg == "" {
			msg = p.Type
		}
		return fmt.Sprintf(`<p style="margin:0"><span style="font-size:13px;font-style:italic;color:%s">%s</span></p>`, pal.Tx2, html.EscapeString(msg))
	}
	msg, atts := p.Message, p.Attachments()
	if r, ok := reminderMarkdown(p); ok {
		msg = r // текст сервера — на языке его настроек, а ссылка в нём на сайт
	}
	long := isLong(p)
	collapsed := long && !f.expanded[p.Id]
	if collapsed {
		msg, atts = clipText(msg), clipAttachments(atts)
	}
	var body string
	if jumbo, ok := jumboEmoji(msg); ok {
		body = `<p style="margin:0;font-size:32px">` + html.EscapeString(jumbo) + `</p>`
	} else {
		body = f.md.Render(msg)
	}
	if p.EditAt > 0 {
		edited := fmt.Sprintf(`<span style="font-size:12px;color:%s"> (изменено)</span>`, pal.Tx2)
		if p.UserId == f.d.Me.Id && f.OnEditHistory != nil { // историю правок сервер показывает автору
			edited = fmt.Sprintf(` <a href="%s%s" style="font-size:12px;color:%s;text-decoration:none">(изменено)</a>`, historyLink, p.Id, pal.Tx2)
		}
		if strings.HasSuffix(body, "</p>") {
			body = strings.TrimSuffix(body, "</p>") + edited + "</p>"
		} else {
			body += `<p style="margin:0">` + edited + `</p>`
		}
	}
	body += f.md.Attachments(atts, min(f.columnWidth(), attachmentMax), f.actionsHTML(p))
	if long {
		body += f.moreHTML(p.Id, collapsed)
	}
	return body
}

// isLong — в посте длинный текст: сообщение или текст вложения интеграции.
func isLong(p *model.Post) bool {
	if markdown.Long(p.Message, longLines, longChars) {
		return true
	}
	for _, a := range p.Attachments() {
		if markdown.Long(a.Text, longLines, longChars) || markdown.Long(a.Pretext, longLines, longChars) {
			return true
		}
	}
	return false
}

// clipText — длинный текст свёрнутого поста — его начало, короткий — как есть.
func clipText(s string) string {
	if !markdown.Long(s, longLines, longChars) {
		return s
	}
	return markdown.Clip(s, clipLines, clipChars)
}

// clipAttachments — вложения свёрнутого поста: длинный текст — началом.
func clipAttachments(atts []*model.MessageAttachment) []*model.MessageAttachment {
	out := make([]*model.MessageAttachment, len(atts))
	for i, a := range atts {
		c := *a
		c.Text, c.Pretext = clipText(c.Text), clipText(c.Pretext)
		out[i] = &c
	}
	return out
}

// moreHTML — «Показать полностью» под свёрнутым, «Свернуть» под развёрнутым.
func (f *Feed) moreHTML(id string, collapsed bool) string {
	link, label := moreLink+id, "Показать полностью"
	if !collapsed {
		link, label = lessLink+id, "Свернуть"
	}
	return fmt.Sprintf(`<p style="margin-top:4px;margin-bottom:0"><a href="%s" style="font-size:13px;color:%s;font-weight:600;text-decoration:none">%s</a></p>`,
		link, Pal().Acc, label)
}

// setExpanded — длинный пост развернуть или свернуть. Он растёт или
// сжимается вниз: пост у верха окна остаётся на месте, даже если лента была
// у низа.
func (f *Feed) setExpanded(id string, on bool) {
	i := f.index(id)
	if i < 0 {
		return
	}
	if on {
		f.expanded[id] = true
	} else {
		delete(f.expanded, id)
	}
	f.stick = false
	f.edit(func() { f.render(i) })
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
		if fi.MimeType == "image/gif" && f.loaded[name] {
			f.animate(fi, name, w, h) // кадры — в ресурс этого размера
		}
		return name
	}
	if f.d.Assets != nil {
		if path := f.d.Assets.Preview(fi.Id); path != "" {
			lz := lazyImage{load: func() *qt.QPixmap { return scaledFile(path, w, h) }}
			if fi.MimeType == "image/gif" {
				lz.onLoad = func() { f.animate(fi, name, w, h) }
				lz.onRelease = func() { f.stopMovie(fi.Id) }
			}
			f.addLazy(name, w, h, lz)
			return name
		}
		f.d.Assets.WantPreview(fi.Id, f.refreshLater())
	}
	return f.placeholderSrc(w, h)
}

// movieTarget — ресурс документа (и его размер), в который идут кадры GIF.
type movieTarget struct {
	name string
	w, h int
}

// animate крутит GIF: качает файл целиком, кадры QMovie кладёт в ресурс name.
// Пост перерисовали в другом размере (смена ширины) — ресурс уже другой:
// кадры теперь в него, а QMovie тот же.
func (f *Feed) animate(fi *model.FileInfo, name string, w, h int) {
	if f.d.Assets == nil {
		return
	}
	f.movieAt[fi.Id] = movieTarget{name, w, h}
	if mv := f.movies[fi.Id]; mv != nil {
		if mv.IsValid() {
			f.putFrame(fi.Id, mv) // текущий кадр сразу: до следующего ресурс показывал бы первый
		}
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
		mv.OnFrameChanged(func(int) { f.putFrame(fi.Id, mv) })
		mv.Start()
		if !f.shown {
			mv.SetPaused(true) // пойдёт при показе
		}
	})
}

// putFrame — текущий кадр GIF id в ресурс того размера, в котором пост нарисован сейчас.
func (f *Feed) putFrame(id string, mv *qt.QMovie) {
	t, ok := f.movieAt[id]
	if !ok {
		return
	}
	// Кадр — на каждом шаге GIF: и он, и уменьшенный удаляются сразу, иначе
	// до сборки мусора копятся мегабайты (см. tmp).
	cur := mv.CurrentPixmap()
	pm := cur.Scaled3(t.w*resourceDPR, t.h*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
	free(cur)
	pm.SetDevicePixelRatio(resourceDPR)
	gifBadge(pm)
	f.setResource(t.name, pm)
	free(pm)
	f.view.Viewport().Update()
}

// gifBadge — метка «GIF» в углу кадра.
func gifBadge(pm *qt.QPixmap) {
	p := qt.NewQPainter2(pm.QPaintDevice)
	defer free(p)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	p.SetPenWithStyle(qt.NoPen)
	p.SetBrush(tmp(qt.NewQBrush3(tmp(qt.NewQColor11(0, 0, 0, 150)))))
	p.DrawRoundedRect(tmp(qt.NewQRectF4(6, 6, 30, 16)), 3, 3)
	p.SetPen(qcolor("#ffffff"))
	p.SetFont(pixelFont(10, qt.QFont__DemiBold))
	p.DrawText7(6, 6, 30, 16, int(qt.AlignCenter), "GIF")
}

// resourceSrcRe — картинки в HTML поста.
var resourceSrcRe = regexp.MustCompile(`<img src="([^"]+)"`)

// sweepResources — картинки прежних размеров прочь из документа. Имя ресурса
// картинки, ролика, предпросмотра и заглушки — с размером: после смены ширины
// посты нарисованы с ресурсами нового размера, а документ держал бы и старые
// (мегабайты на картинку) до своей очистки. Убираем те, на которые не
// ссылается ни один пост; неотрисованные создадут свои сами.
func (f *Feed) sweepResources() {
	f.sweepWanted = false
	used := map[string]bool{}
	for _, it := range f.items {
		for _, m := range resourceSrcRe.FindAllStringSubmatch(it.html, -1) {
			used[m[1]] = true
		}
	}
	for name := range f.resources {
		if isSizedResource(name) && !used[name] {
			// Пустое значение вместо картинки: удалить ресурс QTextDocument не умеет.
			f.doc.AddResource(int(qt.QTextDocument__ImageResource), tmp(qt.NewQUrl3(name)), tmp(qt.NewQVariant()))
			delete(f.resources, name)
			delete(f.lazy, name)
			delete(f.loaded, name)
		}
	}
}

// setResource — картинка pm ресурсом документа name (документ держит свою
// копию: pm можно удалять).
func (f *Feed) setResource(name string, pm *qt.QPixmap) {
	f.doc.AddResource(int(qt.QTextDocument__ImageResource), tmp(qt.NewQUrl3(name)), tmp(pm.ToQVariant()))
}

func (f *Feed) addResource(name string, pm *qt.QPixmap, w, h int) {
	f.setResource(name, pm)
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

// avatarSrc — аватар 32 px (рисует paintUnder): ключ в f.avatars. Фото
// догружается фоном и подменяет инициалы под тем же ключом.
func (f *Feed) avatarSrc(u *model.User, userID, name string, bot bool) string {
	src := fmt.Sprintf("avatar:%s:%s", userID, url.PathEscape(name))
	if _, ok := f.avatars[src]; ok {
		return src
	}
	reload := func() {
		if f.disposed { // лента ушла (dropFeed), пока фото качалось
			return
		}
		f.avatars[src] = avatarPixmap(f.d.Assets, f.d.Users(userID), name, bot, 32, nil)
		f.view.Viewport().Update()
	}
	f.avatars[src] = avatarPixmap(f.d.Assets, u, name, bot, 32, reload)
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
// у сообщения, из которого тред ещё не начат, — «Ответить» (не в архиве).
func (f *Feed) threadHTML(p *model.Post) string {
	if f.d.Thread || f.d.Search || p.RootId != "" || p.IsSystemMessage() || isQueued(p) {
		return ""
	}
	pal := Pal()
	if p.ReplyCount == 0 {
		if f.ReadOnly {
			return ""
		}
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
		emojiW = tmp(qt.NewQFontMetrics(emojiFont)).HorizontalAdvance(emojiText)
	}
	if name == "" {
		w = 32
	} else {
		w = 6 + emojiW + 4 + tmp(qt.NewQFontMetrics(countFont)).HorizontalAdvance(label) + 8
	}
	draw := func(bg, border, fg string) *qt.QPixmap {
		// Плашка с анимированным эмодзи рисуется на каждом его кадре.
		img := qt.NewQImage3(w*resourceDPR, h*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
		defer free(img)
		img.Fill2(qt.Transparent)
		img.SetDevicePixelRatio(resourceDPR)
		p := qt.NewQPainter2(img.QPaintDevice)
		p.SetRenderHint(qt.QPainter__Antialiasing)
		p.SetPenWithPen(qpen(border, 1))
		p.SetBrush(qbrush(bg))
		p.DrawRoundedRect(tmp(qt.NewQRectF4(0.5, 0.5, float64(w-1), float64(h-1))), 11.5, 11.5)
		if name == "" {
			p.DrawPixmap9((w-16)/2, (h-16)/2, iconPixmap("smile-plus", pal.Tx2, 16))
		} else {
			if custom != "" {
				em := f.emojiPixmap(name, custom, 16)
				p.DrawPixmap9(6, (h-16)/2, em)
				free(em)
			} else {
				p.SetFont(emojiFont)
				p.SetPen(qcolor(fg))
				p.DrawText7(6, 0, emojiW+1, h, int(qt.AlignVCenter|qt.AlignLeft), emojiText)
			}
			p.SetFont(countFont)
			p.SetPen(qcolor(fg))
			p.DrawText7(6+emojiW+4, 0, w-emojiW-10, h, int(qt.AlignVCenter|qt.AlignLeft), label)
		}
		free(p) // до картинки: рисование кончается в деструкторе
		return qt.QPixmap_FromImage(img)
	}
	bg, border, fg := pal.Surf, pal.Line, pal.Tx
	if mine {
		bg, border, fg = pal.AccW, pal.Acc, pal.SelTx
	}
	pl := pill{emoji: name, render: func() (*qt.QPixmap, *qt.QPixmap) { return draw(bg, border, fg), draw(pal.Hover, border, fg) }}
	pl.normal, pl.hover = pl.render()
	f.setPill(src, pl)
	cur := pl.normal
	if f.hovered == src {
		cur = pl.hover
	}
	f.addResource(src, cur, w, h)
	if custom != "" {
		// После ресурса: QMovie отдаёт первый кадр прямо из start, и
		// emojiFrame перерисовывает плашку — картинки pl уже удалены.
		f.animateEmoji(name)
	}
	return w, h
}

// onHover — курсор над ссылкой: плашке реакции и кнопке интеграции подменяем
// картинку на «наведённую».
func (f *Feed) onHover(link string) {
	src := ""
	switch {
	case strings.HasPrefix(link, reactLink):
		src = "reaction:" + strings.TrimPrefix(link, reactLink)
	case strings.HasPrefix(link, reactMenuLink):
		src = "reaction-plus:" + strings.TrimPrefix(link, reactMenuLink)
	case strings.HasPrefix(link, actionLink):
		src = "action:" + strings.TrimPrefix(link, actionLink)
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
		f.setResource(name, pm)
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
	case strings.HasPrefix(link, historyLink):
		if p := f.post(strings.TrimPrefix(link, historyLink)); p != nil && f.OnEditHistory != nil {
			f.OnEditHistory(p)
		}
	case strings.HasPrefix(link, actionLink):
		postID, id, _ := strings.Cut(strings.TrimPrefix(link, actionLink), ":")
		id, _ = url.PathUnescape(id)
		if p := f.post(postID); p != nil && f.OnAction != nil {
			if a := p.GetAction(id); a != nil && !a.Disabled {
				f.OnAction(p, a)
			}
		}
	case strings.HasPrefix(link, moreLink):
		f.setExpanded(strings.TrimPrefix(link, moreLink), true)
	case strings.HasPrefix(link, lessLink):
		f.setExpanded(strings.TrimPrefix(link, lessLink), false)
	case strings.HasPrefix(link, openLink):
		if fi := f.file(strings.TrimPrefix(link, openLink)); fi != nil && f.OnOpenFile != nil {
			f.OnOpenFile(fi)
		}
	case strings.HasPrefix(link, imageLink):
		if fi, p := f.fileAndPost(strings.TrimPrefix(link, imageLink)); fi != nil && f.OnOpenImage != nil {
			f.OnOpenImage(p, fi.Id)
		}
	case strings.HasPrefix(link, linkImageLink):
		id, n, _ := strings.Cut(strings.TrimPrefix(link, linkImageLink), ":")
		if p := f.post(id); p != nil && f.OnOpenImage != nil {
			f.OnOpenImage(p, "link:"+n)
		}
	case strings.HasPrefix(link, saveLink):
		if fi := f.file(strings.TrimPrefix(link, saveLink)); fi != nil && f.OnSaveFile != nil {
			f.OnSaveFile(fi)
		}
	case link == retryLink:
		if f.OnRetry != nil {
			f.OnRetry()
		}
	case strings.HasPrefix(link, outboxRetryLink), strings.HasPrefix(link, outboxDeleteLink):
		retry := strings.HasPrefix(link, outboxRetryLink)
		id, _ := url.PathUnescape(strings.TrimPrefix(strings.TrimPrefix(link, outboxRetryLink), outboxDeleteLink))
		if f.OnOutbox != nil && id != "" {
			f.OnOutbox(id, retry)
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

// linkAddress — что копировать по «Копировать адрес ссылки»: адрес веб-ссылки
// или почты; внутренние ссылки (@имя, ~канал, кнопки ленты) — нечего.
func linkAddress(anchor string) (string, bool) {
	switch {
	case strings.HasPrefix(anchor, "http://"), strings.HasPrefix(anchor, "https://"):
		return anchor, true
	case strings.HasPrefix(anchor, "mailto:"):
		return strings.TrimPrefix(anchor, "mailto:"), true
	}
	return "", false
}

// imageAt — картинка под курсором по её ссылке (картинки в ленте — ссылки
// mm-img: и mm-linkimg:): пост и ключ, как у OnOpenImage.
func (f *Feed) imageAt(anchor string) (*model.Post, string, bool) {
	switch {
	case strings.HasPrefix(anchor, imageLink):
		if fi, p := f.fileAndPost(strings.TrimPrefix(anchor, imageLink)); fi != nil {
			return p, fi.Id, true
		}
	case strings.HasPrefix(anchor, linkImageLink):
		id, n, _ := strings.Cut(strings.TrimPrefix(anchor, linkImageLink), ":")
		if p := f.post(id); p != nil {
			return p, "link:" + n, true
		}
	}
	return nil, "", false
}

// contextMenu — все действия с сообщением; при наведении — панель реакций (quickBar).
func (f *Feed) contextMenu(e *qt.QContextMenuEvent) {
	it := f.postAtY(e.Pos().Y())
	menu := qt.NewQMenu(f.view.QWidget)
	cp := menu.QWidget.AddActionWithText("Копировать выделенное")
	cp.SetShortcut(tmp(qt.NewQKeySequence2("Ctrl+C")))
	cp.SetEnabled(f.view.TextCursor().HasSelection())
	cp.OnTriggered(f.view.Copy)
	// Адрес ссылки — только если щёлкнули по ссылке; ссылка на само
	// сообщение — «Копировать ссылку на сообщение» ниже.
	if addr, ok := linkAddress(f.view.AnchorAt(e.Pos())); ok {
		cl := menu.QWidget.AddActionWithText("Копировать адрес ссылки")
		cl.OnTriggered(func() { qt.QGuiApplication_Clipboard().SetText(addr) })
	}
	if p, key, ok := f.imageAt(f.view.AnchorAt(e.Pos())); ok && f.OnCopyImage != nil {
		ci := menu.QWidget.AddActionWithText("Копировать картинку")
		ci.OnTriggered(func() { f.OnCopyImage(p, key) })
	}
	if it != nil {
		p := it.post
		if !p.IsSystemMessage() && !isQueued(p) {
			menu.AddSeparator()
			if !f.d.Thread {
				reply := menu.QWidget.AddActionWithText("Ответить в треде")
				reply.OnTriggered(func() { f.openThread(threadRoot(p)) })
				if f.OnThreadWindow != nil {
					win := menu.QWidget.AddActionWithText("Открыть тред в отдельном окне")
					win.OnTriggered(func() { f.OnThreadWindow(threadRoot(p)) })
				}
			}
			if sel := f.selectionIn(it); f.OnQuote != nil && !f.ReadOnly && (p.Message != "" || sel != "") {
				q := menu.QWidget.AddActionWithText("Цитировать")
				q.OnTriggered(func() { f.OnQuote(p, sel) })
			}
			// Эмодзи в QMenu не кладём: шрифт меню выбирает система, и они
			// бывают чёрно-белыми. Выбор — в своём окне, эмодзи там картинками.
			if !f.ReadOnly {
				react := menu.QWidget.AddActionWithText("Реакция…")
				react.OnTriggered(func() {
					if f.OnPickReaction != nil {
						f.OnPickReaction(p)
					}
				})
			}
			if f.OnForward != nil {
				fw := menu.QWidget.AddActionWithText("Переслать…")
				fw.OnTriggered(func() { f.OnForward(p) })
			}
			if f.OnMarkUnread != nil {
				un := menu.QWidget.AddActionWithText("Пометить непрочитанным")
				un.OnTriggered(func() { f.OnMarkUnread(p) })
			}
			if f.OnRemind != nil {
				items := []menuItem{}
				for _, pr := range sched.RemindPresets(time.Now()) {
					at := pr.At
					items = append(items, menuItem{title: pr.Title, do: func() { f.OnRemind(p, at) }})
				}
				items = append(items, menuItem{}, menuItem{title: "Своё время…", do: func() { f.OnRemind(p, time.Time{}) }})
				fillMenu(menu.AddMenuWithTitle("Напомнить"), items)
			}
			if f.OnSave != nil {
				saved := f.d.Saved != nil && f.d.Saved(p.Id)
				label := "Сохранить"
				if saved {
					label = "Убрать из сохранённых"
				}
				sv := menu.QWidget.AddActionWithText(label)
				sv.OnTriggered(func() { f.OnSave(p, !saved) })
			}
			if f.OnFollowThread != nil && f.d.ThreadFollow != nil {
				root := threadRoot(p)
				if following, ok := f.d.ThreadFollow(root); ok {
					label := "Подписаться на тред"
					if following {
						label = "Отписаться от треда"
					}
					fl := menu.QWidget.AddActionWithText(label)
					channel := p.ChannelId
					fl.OnTriggered(func() { f.OnFollowThread(root, channel, !following) })
				}
			}
			if f.OnPin != nil && !f.ReadOnly {
				label := "Закрепить в канале"
				if p.IsPinned {
					label = "Открепить"
				}
				pin := menu.QWidget.AddActionWithText(label)
				pinned := p.IsPinned
				pin.OnTriggered(func() { f.OnPin(p, !pinned) })
			}
			if p.UserId == f.d.Me.Id && store.HasLinkPreview(p) && f.OnRemovePreview != nil {
				rm := menu.QWidget.AddActionWithText("Убрать предпросмотр ссылки")
				rm.OnTriggered(func() { f.OnRemovePreview(p) })
			}
			if editable(p, f.d.Me.Id) && f.OnEditPost != nil && !f.ReadOnly {
				edit := menu.QWidget.AddActionWithText("Изменить")
				edit.OnTriggered(func() { f.OnEditPost(p) })
			}
			if p.UserId == f.d.Me.Id && f.OnDeletePost != nil && !f.ReadOnly {
				del := menu.QWidget.AddActionWithText("Удалить…")
				del.OnTriggered(func() { f.OnDeletePost(p) })
			}
		}
		menu.AddSeparator()
		ct := menu.QWidget.AddActionWithText("Копировать текст сообщения")
		ct.OnTriggered(func() { qt.QGuiApplication_Clipboard().SetText(p.Message) })
		if !p.IsSystemMessage() && !isQueued(p) && f.OnCopyLink != nil {
			pl := menu.QWidget.AddActionWithText("Копировать ссылку на сообщение")
			pl.OnTriggered(func() { f.OnCopyLink(p) })
		}
	} else {
		menu.AddSeparator()
	}
	all := menu.QWidget.AddActionWithText("Выделить всё")
	all.SetShortcut(tmp(qt.NewQKeySequence2("Ctrl+A")))
	all.OnTriggered(f.view.SelectAll)
	if dbg := f.debugItems(); len(dbg) > 0 {
		menu.AddSeparator()
		fillMenu(menu, dbg)
	}
	menu.ExecWithPos(e.GlobalPos())
	menu.DeleteLater()
}

// copySelection — в буфер только текст: картинки (аватары, реакции, превью) в
// выделении дали бы символы-заглушки U+FFFC.
func (f *Feed) copySelection(super func() *qt.QMimeData) *qt.QMimeData {
	md := super()
	text := md.Text()
	md.Delete()
	out := qt.NewQMimeData()
	out.SetText(copiedText(text))
	return out
}

// outboxHTML — под сообщением из очереди отправки: ждёт связи или отказ
// сервера, «Повторить», «Удалить».
func (f *Feed) outboxHTML(p *model.Post) string {
	if !isQueued(p) || f.d.Outbox == nil {
		return ""
	}
	failed, ok := f.d.Outbox(p.Id)
	if !ok {
		return ""
	}
	pal := Pal()
	id := url.PathEscape(p.Id)
	link := func(href, text string) string {
		return fmt.Sprintf(`<a href="%s%s" style="color:%s">%s</a>`, href, id, pal.Acc, text)
	}
	if failed == "" {
		return fmt.Sprintf(`<p style="margin-top:2px;margin-bottom:0;font-size:12px;color:%s"><img src="%s" width="14" height="14">&nbsp;Не отправлено — уйдёт, когда появится связь · %s</p>`,
			pal.Tx2, f.iconSrc("clock", pal.Tx2, 14), link(outboxDeleteLink, "Удалить"))
	}
	return fmt.Sprintf(`<p style="margin-top:2px;margin-bottom:0;font-size:12px;color:%s">Не отправлено: %s · %s · %s</p>`,
		pal.Dang, html.EscapeString(failed), link(outboxRetryLink, "Повторить"), link(outboxDeleteLink, "Удалить"))
}

// setReadOnly — канал в архиве (или вернули): посты заново — без «Ответить».
func (f *Feed) setReadOnly(on bool) {
	if f.ReadOnly != on {
		f.ReadOnly = on
		f.invalidateAll()
	}
}

// copiedText — выделенный текст ленты без заглушек картинок и хвостовых пробелов.
func copiedText(text string) string {
	text = strings.NewReplacer("￼", "", " ", "\n", " ", "\n", " ", " ").Replace(text)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// selectionIn — выделенный текст, если выделение целиком внутри поста it; иначе "".
func (f *Feed) selectionIn(it *postItem) string {
	cur := tmp(f.view.TextCursor())
	if it == nil || !cur.HasSelection() || cur.SelectionStart() < it.frame.FirstPosition() || cur.SelectionEnd() > it.frame.LastPosition() {
		return ""
	}
	return copiedText(cur.Selection().ToPlainText())
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
// editable — своё сообщение, которое можно править: не системное и не от
// вебхука (у того текст пишет интеграция).
func editable(p *model.Post, me string) bool {
	return p.UserId == me && !p.IsSystemMessage() && p.GetProp(model.PostPropsFromWebhook) != "true"
}

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
		!prev.IsSystemMessage() && !p.IsSystemMessage() && postPriority(p) == ""
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
	if f.shown {
		f.pinToBottom()
		f.placeVideos()
		f.quick.follow()
	}
	if abs(f.view.Viewport().Width()-f.lastWidth) >= 24 {
		f.refit.Start2()
	}
}

func (f *Feed) fitImages() {
	if !f.shown {
		return // скрытой — при показе (SetShown): незачем перерисовывать фоном
	}
	w := f.view.Viewport().Width()
	if abs(w-f.lastWidth) < 24 {
		return
	}
	f.lastWidth = w
	f.sweepWanted = true
	// Картинки, видео и вложения интеграций — по ширине колонки.
	f.invalidate(func(p *model.Post) bool {
		return len(p.Attachments()) > 0 || p.Metadata != nil && (len(p.Metadata.Embeds) > 0 ||
			slices.ContainsFunc(p.Metadata.Files, func(fi *model.FileInfo) bool { return isImage(fi) || isVideo(fi) }))
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
	if !f.shown {
		return // доделает SetShown: раскладка всего документа скрытой ленте ни к чему
	}
	root := f.doc.RootFrame()
	ff := root.FrameFormat()
	cur := ff.TopMargin()
	content := f.doc.Size().Height() - cur
	free := float64(f.view.Viewport().Height()) - content
	switch {
	case f.placeholder:
		free /= 2
	case f.d.Search:
		free = 0 // результаты и списки — сверху, а не прижаты к низу, как чат
	}
	want := math.Max(0, free)
	if math.Abs(want-cur) < 1 {
		return
	}
	ff.SetTopMargin(want)
	root.SetFrameFormat(ff)
}

func (f *Feed) onScroll(v int) {
	if f.programmatic || f.editing || !f.shown {
		return
	}
	f.keepFromBottom = -1
	f.stick = v >= f.bar.Maximum()-4
	if v <= f.bar.Minimum() && f.hasMore && !f.loadingOlder && len(f.items) > 0 && len(f.pending) == 0 && f.OnNeedOlder != nil {
		f.loadingOlder = true
		f.showHistoryLoading()
		f.OnNeedOlder()
	}
}

// onRange — высота содержимого изменилась (добавились посты, пересчитались
// переносы): держим низ или место, на котором был пользователь.
func (f *Feed) onRange(_, max int) {
	f.pinToBottom()
	if f.editing || !f.shown {
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
