package ui

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"slices"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/complete"
)

// peopleLimit — сколько подходящих людей показывать и просить у сервера.
const peopleLimit = 50

// peoplePicker — выбор людей в окне d: строка поиска, выбранные — строкой
// над ней (✕ — убрать, Backspace в пустой строке — последнего), ниже — кто
// подходит: что знает клиент (local) и, через 150 мс после паузы в наборе,
// ответ сервера (remote). Enter или щелчок — выбрать; Enter в пустой строке
// без выделенного — OnSubmit.
type peoplePicker struct {
	m      *MainWindow
	d      *dialog
	field  *qt.QLineEdit
	picked *qt.QLabel
	list   *qt.QListWidget
	hint   *qt.QLabel
	limit  int // сколько можно выбрать; 0 — сколько угодно

	local  func(q string) []*model.User
	remote func(ctx context.Context, q string) ([]*model.User, error)

	chosen  []*model.User
	shown   []*model.User
	found   []*model.User // ответ сервера на запрос foundQ
	foundQ  string
	gen     int
	loading bool

	OnChange func() // выбранные изменились
	OnSubmit func()
}

func (m *MainWindow) newPeoplePicker(d *dialog, limit int, local func(string) []*model.User,
	remote func(context.Context, string) ([]*model.User, error)) *peoplePicker {
	pp := &peoplePicker{m: m, d: d, limit: limit, local: local, remote: remote,
		field: qt.NewQLineEdit2(), picked: qt.NewQLabel2(), list: qt.NewQListWidget2(), hint: qt.NewQLabel2()}
	pp.field.SetPlaceholderText("Имя, фамилия или @имя")
	pp.field.SetClearButtonEnabled(true)
	pp.picked.SetTextFormat(qt.RichText)
	pp.picked.SetWordWrap(true)
	pp.picked.SetVisible(false)
	pp.picked.OnLinkActivated(func(link string) {
		id := strings.TrimPrefix(link, "rm:")
		pp.chosen = slices.DeleteFunc(pp.chosen, func(u *model.User) bool { return u.Id == id })
		pp.changed()
		pp.field.SetFocus()
	})
	pp.list.SetIconSize(qt.NewQSize2(24, 24))
	pp.list.SetMinimumHeight(280)
	pp.list.SetFocusPolicy(qt.NoFocus) // стрелки и Enter — из строки поиска
	pp.hint.SetProperty("role", qt.NewQVariant11("hint"))
	pp.hint.SetWordWrap(true)
	d.lay.AddWidget(pp.picked.QWidget)
	d.lay.AddWidget(pp.field.QWidget)
	d.lay.AddWidget2(pp.list.QWidget, 1)
	d.lay.AddWidget(pp.hint.QWidget)

	timer := qt.NewQTimer2(d.w.QObject)
	timer.SetSingleShot(true)
	timer.SetInterval(150)
	timer.OnTimeout(pp.ask)
	pp.field.OnTextChanged(func(string) {
		pp.fill()
		timer.Start2()
	})
	pp.field.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		switch qt.Key(e.Key()) {
		case qt.Key_Down:
			pp.list.SetCurrentRow(min(pp.list.CurrentRow()+1, pp.list.Count()-1))
		case qt.Key_Up:
			pp.list.SetCurrentRow(max(pp.list.CurrentRow()-1, 0))
		case qt.Key_Return, qt.Key_Enter:
			if pp.list.CurrentRow() >= 0 {
				pp.pick()
			} else if pp.field.Text() == "" && len(pp.chosen) > 0 && pp.OnSubmit != nil {
				pp.OnSubmit()
			}
		case qt.Key_Backspace:
			if pp.field.Text() == "" && len(pp.chosen) > 0 {
				pp.chosen = pp.chosen[:len(pp.chosen)-1]
				pp.changed()
				return
			}
			super(e)
		default:
			super(e)
		}
	})
	// Список меняется при выборе — после сигнала щелчка, а не внутри него.
	pp.list.OnItemClicked(func(it *qt.QListWidgetItem) {
		row := pp.list.Row(it)
		mainthread.Start(func() {
			if !pp.d.closed {
				pp.list.SetCurrentRow(row)
				pp.pick()
			}
		})
	})
	pp.fill()
	pp.ask()
	return pp
}

func (pp *peoplePicker) query() string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(pp.field.Text()), "@"))
}

func userFields(u *model.User) []string {
	return []string{u.Username, u.FirstName, u.LastName, u.Nickname, displayName(u)}
}

// fill — список подходящих под запрос: свои, потом с сервера; ответ сервера
// на прежний запрос фильтруется по новому, пока не придёт свежий.
func (pp *peoplePicker) fill() {
	q := pp.query()
	skip := map[string]bool{pp.m.s.Me.Id: true}
	for _, u := range pp.chosen {
		skip[u.Id] = true
	}
	type scored struct {
		u     *model.User
		score int
	}
	var out []scored
	add := func(us []*model.User, trusted bool) {
		for _, u := range us {
			if u == nil || u.DeleteAt > 0 || skip[u.Id] {
				continue
			}
			s := 1
			if q != "" {
				s = complete.Score(q, userFields(u)...)
				if s == 0 && !trusted {
					continue
				}
			}
			skip[u.Id] = true
			out = append(out, scored{u, max(s, 1)})
		}
	}
	if pp.local != nil {
		add(pp.local(q), false)
	}
	add(pp.found, pp.foundQ == q) // сервер ищет и по тому, чего мы не знаем (почта и т. п.)
	slices.SortStableFunc(out, func(a, b scored) int { return cmp.Compare(b.score, a.score) })
	pp.shown = pp.shown[:0]
	pp.list.Clear()
	for _, s := range out[:min(len(out), peopleLimit)] {
		u := s.u
		pp.shown = append(pp.shown, u)
		text := u.Username
		if dn := displayName(u); dn != u.Username {
			text += "   " + dn
		}
		row := qt.NewQListWidgetItem7(text, pp.list)
		row.SetIcon(qt.NewQIcon2(avatarPixmap(pp.m.assets, u, u.Username, u.IsBot, 24, nil)))
	}
	// В пустой строке ничего не выделено: Enter — готово, ↓ — к списку.
	if q != "" && len(pp.shown) > 0 {
		pp.list.SetCurrentRow(0)
	} else {
		pp.list.SetCurrentRow(-1)
	}
	switch {
	case pp.limit > 0 && len(pp.chosen) >= pp.limit:
		pp.hint.SetText(fmt.Sprintf("Выбрано %d — больше нельзя", len(pp.chosen)))
	case len(pp.shown) == 0 && pp.loading:
		pp.hint.SetText("Ищем…")
	case len(pp.shown) == 0:
		pp.hint.SetText("Никого не нашлось")
	default:
		pp.hint.SetText("Enter или щелчок — выбрать")
	}
}

// ask — спросить сервер о запросе в строке.
func (pp *peoplePicker) ask() {
	if pp.remote == nil {
		return
	}
	q := pp.query()
	pp.gen++
	mine := pp.gen
	pp.loading = true
	m := pp.m
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		users, err := pp.remote(ctx, q)
		m.ui(func() {
			if pp.d.closed || mine != pp.gen {
				return
			}
			pp.loading = false
			if err != nil {
				m.log.Warn("search people", "query", q, "err", err)
				pp.fill()
				pp.hint.SetText("Сервер не ответил: " + commandError(err))
				return
			}
			m.st.AddUsers(users)
			pp.found, pp.foundQ = users, q
			pp.fill()
		})
	}()
}

// pick — выбрать выделенного в списке.
func (pp *peoplePicker) pick() {
	i := pp.list.CurrentRow()
	if i < 0 || i >= len(pp.shown) || pp.limit > 0 && len(pp.chosen) >= pp.limit {
		return
	}
	pp.chosen = append(pp.chosen, pp.shown[i])
	pp.field.Clear()
	pp.changed()
	pp.field.SetFocus()
}

// changed — выбранные изменились: плашки над строкой, список, OnChange.
func (pp *peoplePicker) changed() {
	pal := Pal()
	var chips []string
	for _, u := range pp.chosen {
		chips = append(chips, fmt.Sprintf(`<a href="rm:%s" style="color:%s;text-decoration:none"><span style="background-color:%s">&nbsp;@%s&nbsp;✕&nbsp;</span></a>`,
			u.Id, pal.Tx, pal.AccW, html.EscapeString(u.Username)))
	}
	pp.picked.SetText(strings.Join(chips, " "))
	pp.picked.SetVisible(len(chips) > 0)
	pp.fill()
	if pp.OnChange != nil {
		pp.OnChange()
	}
}

// ids — id выбранных.
func (pp *peoplePicker) ids() []string {
	out := make([]string, len(pp.chosen))
	for i, u := range pp.chosen {
		out[i] = u.Id
	}
	return out
}
