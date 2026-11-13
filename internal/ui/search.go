package ui

import (
	"context"
	"fmt"
	"html"
	"slices"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// Поиск по сообщениям (Ctrl+F, лупа в шапке канала): запрос с синтаксисом
// Mattermost (from:, in:, on:, before:, after:, "фраза", -исключить, слово*),
// результаты — в правой колонке вместо треда, лентой с подсветкой слов;
// «Перейти» — к сообщению. Ищет сервер по всем командам, до searchLimit
// последних совпадений.

const searchLimit = 100

type searchPanel struct {
	w     *qt.QWidget
	head  *header
	field *qt.QLineEdit
	info  *qt.QLabel
	feed  *Feed
	query string // показаны результаты этого запроса; пусто — результатов нет
	gen   int    // номер поиска: ответ на прежний запрос отбрасываем
	// Вместо поиска — список (postactions.go): listPinned (закреплённые в
	// listChannel) или listSaved; "" — поиск.
	list, listChannel string
	// Что ищем: сообщения (feed) или файлы (files) — вкладки над результатами.
	filesMode          bool
	modes              *qt.QWidget
	postsTab, filesTab *qt.QPushButton
	stack              *qt.QStackedWidget
	files              *fileList

	unsubscribe func()
}

func newSearchPanel(deps FeedDeps, onClose func(), onSearch func(q string)) *searchPanel {
	sp := &searchPanel{w: qt.NewQWidget2(), field: qt.NewQLineEdit2(), info: qt.NewQLabel2()}
	sp.head = newHeader("threadHeader", true, nil)
	sp.head.set(nil, "Поиск", "")
	sp.head.close.OnClicked(onClose)
	setName(sp.field.QObject, "searchField")
	sp.field.SetPlaceholderText(`слова, "фраза", from:имя, in:канал, after:2026-11-01`)
	sp.field.SetToolTip("Enter — искать. from:имя — автор, in:канал — где, on:/before:/after:ГГГГ-ММ-ДД — когда,\n" +
		"\"фраза\" — точно, -слово — без него, слово* — начало слова")
	sp.field.SetClearButtonEnabled(true)
	sp.field.OnReturnPressed(func() { onSearch(strings.TrimSpace(sp.field.Text())) })
	sp.field.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		if qt.Key(e.Key()) == qt.Key_Escape {
			onClose()
			return
		}
		super(e)
	})
	sp.info.SetProperty("role", tmp(qt.NewQVariant11("hint")))
	sp.info.SetWordWrap(true)
	deps.Search, deps.Thread = true, false
	sp.feed = NewFeed(deps)
	sp.feed.Reset("Введите запрос и нажмите Enter")
	sp.stack = qt.NewQStackedWidget2()
	sp.stack.AddWidget(sp.feed.Widget())

	// Вкладки «Сообщения» и «Файлы»: смена — тот же запрос заново.
	sp.modes = qt.NewQWidget2()
	ml := qt.NewQHBoxLayout(sp.modes)
	ml.SetContentsMargins(0, 0, 0, 0)
	ml.SetSpacing(4)
	tab := func(title string, files bool) *qt.QPushButton {
		b := qt.NewQPushButton3(title)
		b.SetCheckable(true)
		b.SetProperty("role", tmp(qt.NewQVariant11("tab")))
		b.SetFocusPolicy(qt.NoFocus)
		b.OnClicked(func() {
			sp.setFilesMode(files)
			if q := strings.TrimSpace(sp.field.Text()); q != "" {
				onSearch(q)
			}
		})
		ml.AddWidget(b.QWidget)
		return b
	}
	sp.postsTab = tab("Сообщения", false)
	sp.filesTab = tab("Файлы", true)
	ml.AddStretch()
	sp.setFilesMode(false)

	top := qt.NewQVBoxLayout2()
	top.SetContentsMargins(16, 8, 16, 6)
	top.SetSpacing(4)
	top.AddWidget(sp.field.QWidget)
	top.AddWidget(sp.modes)
	top.AddWidget(sp.info.QWidget)
	lay := qt.NewQVBoxLayout(sp.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.SetSpacing(0)
	lay.AddWidget(sp.head.f.QWidget)
	lay.AddLayout(top.QLayout)
	lay.AddWidget2(sp.stack.QWidget, 1)
	sp.w.SetVisible(false)
	sp.unsubscribe = OnThemeChanged(sp.restyle)
	sp.restyle()
	return sp
}

func (sp *searchPanel) restyle() {
	sp.head.close.SetIcon(icon("x", Pal().Tx2, 18))
	sp.head.icon.SetPixmap(iconPixmap(sp.iconName(), Pal().Tx2, 16))
	sp.head.icon.SetVisible(true)
}

// setList — панель показывает список kind ("" — снова поиск: строка запроса).
// setFilesMode — ищем файлы или сообщения: вкладка и что под ней.
func (sp *searchPanel) setFilesMode(on bool) {
	sp.filesMode = on
	sp.postsTab.SetChecked(!on)
	sp.filesTab.SetChecked(on)
	if on && sp.files != nil {
		sp.stack.SetCurrentWidget(sp.files.list.QWidget)
	} else {
		sp.stack.SetCurrentWidget(sp.feed.Widget())
	}
}

// attachFiles — список для вкладки «Файлы» (его делает окно: щелчки по файлам — его).
func (sp *searchPanel) attachFiles(fl *fileList) {
	sp.files = fl
	sp.stack.AddWidget(fl.list.QWidget)
	sp.setFilesMode(sp.filesMode)
}

func (sp *searchPanel) setList(kind string) {
	sp.list = kind
	sp.query = ""
	sp.field.SetVisible(kind == "")
	sp.modes.SetVisible(kind == "")
	if kind != "" {
		sp.stack.SetCurrentWidget(sp.feed.Widget()) // списки — сообщения
	} else {
		sp.setFilesMode(sp.filesMode)
	}
	if kind == "" {
		sp.head.set(nil, sp.title(), "")
		sp.restyle()
	}
}

func (sp *searchPanel) iconName() string {
	switch sp.list {
	case listPinned:
		return "pin"
	case listSaved:
		return "bookmark"
	case listMentions:
		return "at-sign"
	}
	return "search"
}

func (sp *searchPanel) title() string {
	switch sp.list {
	case listPinned:
		return "Закреплённые"
	case listSaved:
		return "Сохранённые"
	case listMentions:
		return "Упоминания"
	}
	return "Поиск"
}

func (sp *searchPanel) dispose() {
	sp.unsubscribe()
	sp.feed.dispose()
}

// searchTerms — слова запроса для подсветки: без операторов (from:, in:,
// даты, -исключённые), без кавычек и «*».
func searchTerms(q string) []string {
	var out []string
	inQuote := false
	for _, tok := range strings.FieldsFunc(q, func(r rune) bool { return r == ' ' || r == '\t' }) {
		quoted := inQuote || strings.HasPrefix(tok, `"`)
		if strings.Count(tok, `"`)%2 == 1 {
			inQuote = !inQuote
		}
		if !quoted && (strings.HasPrefix(tok, "-") || strings.Contains(tok, ":")) {
			continue
		}
		tok = strings.Trim(tok, `"*`)
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// openSearch — Ctrl+F: панель поиска справа (тред закрывается), курсор в строке.
func (m *MainWindow) openSearch() {
	if m.thread != nil && !m.threadDetached() {
		m.closeThread()
	}
	if m.search.list != "" {
		m.search.gen++
		m.search.setList("")
		m.search.feed.Reset("Введите запрос и нажмите Enter")
		m.search.info.SetText("")
	}
	m.showRight(m.search.w)
	m.search.field.SetFocus()
	m.search.field.SelectAll()
}

func (m *MainWindow) closeSearch() {
	m.search.setList("")
	m.search.gen++
	m.search.feed.Reset("Введите запрос и нажмите Enter")
	m.search.files.fill(nil)
	m.search.info.SetText("")
	m.showRight(nil)
}

// rightPanels — что бывает в правой колонке, в порядке в разделителе после
// сайдбара и центра.
func (m *MainWindow) rightPanels() []*qt.QWidget {
	if m.threadDetached() { // панель треда — в своём окне, не в разделителе
		return []*qt.QWidget{m.search.w, m.members.w, m.files.w}
	}
	return []*qt.QWidget{m.panel.w, m.search.w, m.members.w, m.files.w}
}

// showRight — правая колонка: тред, поиск, участники или ничего; ширина — общая.
func (m *MainWindow) showRight(w *qt.QWidget) {
	rights := m.rightPanels()
	for _, x := range rights {
		x.SetVisible(w != nil && x.UnsafePointer() == w.UnsafePointer())
	}
	if w == nil {
		return
	}
	sizes := m.split.Sizes()
	if len(sizes) == 2+len(rights) {
		total := 0
		for _, s := range sizes {
			total += s
		}
		right := m.columns.thread
		out := []int{sizes[0], max(total-sizes[0]-right, 1)}
		for _, x := range rights {
			if x.UnsafePointer() == w.UnsafePointer() {
				out = append(out, right)
			} else {
				out = append(out, 0)
			}
		}
		m.split.SetSizes(out)
	}
}

// runSearch — запрос q на сервер; результаты — новые сверху.
func (m *MainWindow) runSearch(q string) {
	sp := m.search
	if q == "" {
		return
	}
	if sp.filesMode {
		m.runFileSearch(q)
		return
	}
	sp.gen++
	gen := sp.gen
	sp.info.SetText("Ищем…")
	_, offset := time.Now().Zone()
	params := &model.SearchParameter{Terms: &q, IsOrSearch: new(false), TimeZoneOffset: &offset, Page: new(0), PerPage: new(searchLimit)}
	teams, fallback := m.searchTeams(q), m.teamOf(m.current)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var posts []*model.Post
		seen := map[string]bool{}
		err := searchEach(teams, fallback, func(team string) error {
			list, _, err := m.s.Client.SearchPostsWithParams(ctx, team, params)
			if err == nil {
				for _, p := range store.PostsOf(list) {
					if !seen[p.Id] {
						seen[p.Id] = true
						posts = append(posts, p)
					}
				}
			}
			return err
		})
		m.ui(func() {
			if sp.gen != gen {
				return
			}
			if err != nil {
				m.log.Warn("search", "query", truncate(q, 200), "err", err)
				sp.info.SetText("Поиск не удался: " + commandError(err))
				return
			}
			slices.SortFunc(posts, func(a, b *model.Post) int { return int(b.CreateAt - a.CreateAt) })
			if len(posts) > searchLimit {
				posts = posts[:searchLimit]
			}
			m.withUsers(postAuthors(posts), func() {
				if sp.gen != gen {
					return
				}
				sp.query = q
				sp.feed.SetMarks(searchTerms(q))
				if len(posts) == 0 {
					sp.feed.Reset("Ничего не нашлось")
					sp.info.SetText(html.EscapeString("По запросу «" + truncate(q, 80) + "» ничего нет"))
					return
				}
				sp.feed.SetPosts(posts, false)
				sp.feed.ScrollToTop()
				info := "Найдено: " + fmt.Sprint(len(posts))
				if len(posts) >= searchLimit {
					info = fmt.Sprintf("Показаны последние %d — уточните запрос, чтобы увидеть остальное", len(posts))
				}
				sp.info.SetText(info)
			})
		})
	}()
}

// runFileSearch — вкладка «Файлы»: файлы по запросу во всех каналах,
// новые сверху (имя, ext:, from:, in:, даты).
func (m *MainWindow) runFileSearch(q string) {
	sp := m.search
	sp.gen++
	gen := sp.gen
	sp.info.SetText("Ищем…")
	_, offset := time.Now().Zone()
	params := &model.SearchParameter{Terms: &q, IsOrSearch: new(false), TimeZoneOffset: &offset, Page: new(0), PerPage: new(searchLimit)}
	teams, fallback := m.searchTeams(q), m.teamOf(m.current)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var files []*model.FileInfo
		var authors []string
		seen := map[string]bool{}
		err := searchEach(teams, fallback, func(team string) error {
			list, _, err := m.s.Client.SearchFilesWithParams(ctx, team, params)
			if err == nil {
				for _, id := range list.Order {
					if fi := list.FileInfos[id]; fi != nil && fi.DeleteAt == 0 && !seen[id] {
						seen[id] = true
						files = append(files, fi)
						authors = append(authors, fi.CreatorId)
					}
				}
			}
			return err
		})
		m.ui(func() {
			if sp.gen != gen {
				return
			}
			if err != nil {
				m.log.Warn("search files", "query", truncate(q, 200), "err", err)
				sp.info.SetText("Поиск не удался: " + commandError(err))
				return
			}
			m.withUsers(authors, func() {
				if sp.gen != gen {
					return
				}
				slices.SortStableFunc(files, func(a, b *model.FileInfo) int { return int(b.CreateAt - a.CreateAt) })
				if len(files) > searchLimit {
					files = files[:searchLimit]
				}
				sp.query = q
				sp.files.fill(files)
				switch {
				case len(files) == 0:
					sp.info.SetText(html.EscapeString("По запросу «" + truncate(q, 80) + "» файлов нет"))
				case len(files) >= searchLimit:
					sp.info.SetText(fmt.Sprintf("Показаны последние %d — уточните запрос, чтобы увидеть остальное", len(files)))
				default:
					sp.info.SetText("Найдено: " + plural(int64(len(files)), "файл", "файла", "файлов"))
				}
			})
		})
	}()
}

// searchTeams — где искать запрос q: по всем командам сразу (""), а с in:
// — по каждой команде отдельно: имя канала сервер ищет в команде поиска, и
// в поиске по всем командам его не найти (convertChannelNamesToChannelIds).
func (m *MainWindow) searchTeams(q string) []string {
	for _, tok := range strings.Fields(strings.ToLower(q)) {
		if t := strings.TrimPrefix(tok, "-"); strings.HasPrefix(t, "in:") || strings.HasPrefix(t, "channel:") {
			var out []string
			for _, team := range m.st.Teams() {
				out = append(out, team.Id)
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return []string{""}
}

// searchEach — поиск в каждой из teams; по всем командам ("") не вышло —
// по команде fallback (сервер постарше). Ошибка — только если не вышло нигде.
func searchEach(teams []string, fallback string, search func(team string) error) error {
	var errs []error
	for _, team := range teams {
		err := search(team)
		if err != nil && team == "" && fallback != "" {
			err = search(fallback)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == len(teams) && len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// postWhere — где сообщение: «# backend», «# backend · в треде», «@bob».
func (m *MainWindow) postWhere(p *model.Post) string {
	where := "?"
	if ch := m.st.Channel(p.ChannelId); ch != nil {
		where = channelPrefix(ch) + m.st.Title(ch)
	}
	if p.RootId != "" {
		where += " · в треде"
	}
	return where
}
