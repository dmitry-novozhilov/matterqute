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
	sp.info.SetProperty("role", qt.NewQVariant11("hint"))
	sp.info.SetWordWrap(true)
	deps.Search, deps.Thread = true, false
	sp.feed = NewFeed(deps)
	sp.feed.Reset("Введите запрос и нажмите Enter")

	top := qt.NewQVBoxLayout2()
	top.SetContentsMargins(16, 8, 16, 6)
	top.SetSpacing(4)
	top.AddWidget(sp.field.QWidget)
	top.AddWidget(sp.info.QWidget)
	lay := qt.NewQVBoxLayout(sp.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.SetSpacing(0)
	lay.AddWidget(sp.head.f.QWidget)
	lay.AddLayout(top.QLayout)
	lay.AddWidget2(sp.feed.Widget(), 1)
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
func (sp *searchPanel) setList(kind string) {
	sp.list = kind
	sp.query = ""
	sp.field.SetVisible(kind == "")
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
	if m.thread != nil {
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
	m.search.info.SetText("")
	m.showRight(nil)
}

// rightPanels — что бывает в правой колонке, в порядке в разделителе после
// сайдбара и центра.
func (m *MainWindow) rightPanels() []*qt.QWidget {
	return []*qt.QWidget{m.panel.w, m.search.w, m.members.w}
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
	sp.gen++
	gen := sp.gen
	sp.info.SetText("Ищем…")
	offset := 0
	if _, off := time.Now().Zone(); off != 0 {
		offset = off
	}
	params := &model.SearchParameter{Terms: &q, IsOrSearch: new(false), TimeZoneOffset: &offset, Page: new(0), PerPage: new(searchLimit)}
	team := m.teamOf(m.current)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		list, _, err := m.s.Client.SearchPostsWithParams(ctx, "", params) // по всем командам
		if err != nil && team != "" {
			list, _, err = m.s.Client.SearchPostsWithParams(ctx, team, params) // сервер постарше — по команде
		}
		m.ui(func() {
			if sp.gen != gen {
				return
			}
			if err != nil {
				m.log.Warn("search", "query", truncate(q, 200), "err", err)
				sp.info.SetText("Поиск не удался: " + commandError(err))
				return
			}
			posts := store.PostsOf(list)
			slices.SortFunc(posts, func(a, b *model.Post) int { return int(b.CreateAt - a.CreateAt) })
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
