package ui

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/theme"
)

// Кнопки и списки в сообщениях интеграций (props.attachments[].actions, как
// «Interactive messages» веб-клиента): лента рисует их картинками-плашками,
// щелчок — POST /posts/{пост}/actions/{действие} с выбранным значением и
// cookie действия. Дальше решает интеграция: правит пост (post_edited),
// отвечает «только вам» (ephemeral_message), открывает окно с полями
// (open_dialog, interactivedialog.go); переход — goto_location в ответе.

const actionLabelMax = 240 // подпись плашки длиннее — с «…»

var actionHexRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// actionsHTML — кнопки и списки вложения a поста p плашками в строку.
func (f *Feed) actionsHTML(p *model.Post) func(*model.MessageAttachment) string {
	return func(a *model.MessageAttachment) string {
		var b strings.Builder
		for _, ac := range a.Actions {
			if ac == nil || ac.Id == "" {
				continue // без id сервер действие не выполнит
			}
			id := url.PathEscape(ac.Id)
			src := "action:" + p.Id + ":" + id
			label := ac.Name
			menu := ac.Type == model.PostActionTypeSelect
			if menu {
				if t := f.chosenText(p.Id, ac); t != "" {
					label = t
				}
			}
			w, h := f.actionPill(src, label, ac.Style, menu, ac.Disabled)
			img := fmt.Sprintf(`<img src="%s" width="%d" height="%d" title="%s">`, src, w, h, html.EscapeString(ac.Tooltip))
			if ac.Disabled {
				b.WriteString(img)
			} else {
				fmt.Fprintf(&b, `<a href="%s%s:%s">%s</a>`, actionLink, p.Id, id, img)
			}
			b.WriteString("&nbsp; ")
		}
		if b.Len() == 0 {
			return ""
		}
		return `<p style="margin-top:8px;margin-bottom:0">` + b.String() + "</p>"
	}
}

// chosenText — подпись списка: что выбрали здесь, иначе вариант по умолчанию.
func (f *Feed) chosenText(postID string, a *model.PostAction) string {
	if t := f.chosen[postID+":"+a.Id]; t != "" {
		return t
	}
	for _, o := range a.Options {
		if o != nil && a.DefaultOption != "" && o.Value == a.DefaultOption {
			return o.Text
		}
	}
	return ""
}

// setChosen — в списке выбрали text: подпись плашки — он.
func (f *Feed) setChosen(postID, actionID, text string) {
	f.chosen[postID+":"+actionID] = text
	f.invalidate(func(p *model.Post) bool { return p.Id == postID })
}

// actionColor — цвет кнопки по style: именованные — цвета темы, HEX — как
// есть; "" — обычная.
func actionColor(pal theme.Palette, style string) string {
	switch style {
	case "good", "success":
		return pal.Good
	case "warning":
		return pal.Warn
	case "danger":
		return pal.Dang
	}
	if actionHexRe.MatchString(style) {
		return style
	}
	return ""
}

// actionPill рисует кнопку (menu — список, со стрелкой) в ресурс src, как
// реакции: скруглений и рамок у текста документа нет. primary — залитая
// акцентом, цветная — рамка и текст цветом, обычная — как поля ввода.
func (f *Feed) actionPill(src, label, style string, menu, disabled bool) (w, h int) {
	pal := Pal()
	font := pixelFont(13, qt.QFont__DemiBold)
	fm := qt.NewQFontMetrics(font)
	label = fm.ElidedText(label, qt.ElideRight, actionLabelMax)
	h = 28
	w = 12 + fm.HorizontalAdvance(label) + 12
	if menu {
		w += 16
	}
	type colors struct{ bg, border, fg *qt.QColor }
	solid := func(c string) *qt.QColor { return qt.NewQColor6(c) }
	faint := func(c string, a float32) *qt.QColor {
		q := qt.NewQColor6(c)
		q.SetAlphaF(a)
		return q
	}
	normal := colors{solid(pal.Surf), solid(pal.Line), solid(pal.Tx)}
	hover := colors{solid(pal.Hover), solid(pal.Line), solid(pal.Tx)}
	if c := actionColor(pal, style); style == "primary" {
		normal = colors{solid(pal.Acc), solid(pal.Acc), solid(pal.OnAcc)}
		hover = colors{solid(pal.AccH), solid(pal.AccH), solid(pal.OnAcc)}
	} else if c != "" {
		normal = colors{faint(c, 0.08), faint(c, 0.6), solid(c)}
		hover = colors{faint(c, 0.16), solid(c), solid(c)}
	}
	draw := func(c colors) *qt.QPixmap {
		img := qt.NewQImage3(w*resourceDPR, h*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
		img.Fill2(qt.Transparent)
		img.SetDevicePixelRatio(resourceDPR)
		p := qt.NewQPainter2(img.QPaintDevice)
		p.SetRenderHint(qt.QPainter__Antialiasing)
		if disabled {
			p.SetOpacity(0.45)
		}
		p.SetPenWithPen(qt.NewQPen4(qt.NewQBrush3(c.border), 1))
		p.SetBrush(qt.NewQBrush3(c.bg))
		p.DrawRoundedRect(qt.NewQRectF4(0.5, 0.5, float64(w-1), float64(h-1)), 4, 4)
		p.SetFont(font)
		p.SetPen(c.fg)
		p.DrawText7(12, 0, w-24, h, int(qt.AlignVCenter|qt.AlignLeft), label)
		if menu {
			p.DrawPixmap9(w-12-14, (h-14)/2, iconPixmap("chevron-down", c.fg.Name(), 14))
		}
		p.End()
		return qt.QPixmap_FromImage(img)
	}
	pl := pill{render: func() (*qt.QPixmap, *qt.QPixmap) { return draw(normal), draw(hover) }}
	pl.normal, pl.hover = pl.render()
	if disabled {
		pl.hover = pl.normal
	}
	f.pills[src] = pl
	cur := pl.normal
	if f.hovered == src {
		cur = pl.hover
	}
	f.addResource(src, cur, w, h)
	return w, h
}

// postAction — щелчок по кнопке или списку интеграции в ленте f: кнопка —
// сразу на сервер, список — сначала выбрать.
func (m *MainWindow) postAction(f *Feed, p *model.Post, a *model.PostAction) {
	if a.Type != model.PostActionTypeSelect {
		m.runAction(p, a, "")
		return
	}
	choose := func(value, text string) {
		f.setChosen(p.Id, a.Id, text)
		m.runAction(p, a, value)
	}
	switch a.DataSource {
	case model.PostActionDataSourceUsers:
		m.chooseUser(a.Name, func(u *model.User) { choose(u.Id, "@"+u.Username) })
	case model.PostActionDataSourceChannels:
		m.chooseChannel(a.Name, m.teamOf(p.ChannelId), func(it switchItem) { choose(it.id, it.title) })
	default:
		m.actionMenu(a, f.chosenText(p.Id, a), choose).Popup(qt.QCursor_Pos())
	}
}

// actionMenu — варианты списка интеграции; выбранный сейчас отмечен.
func (m *MainWindow) actionMenu(a *model.PostAction, current string, choose func(value, text string)) *qt.QMenu {
	menu := qt.NewQMenu(m.w)
	menu.SetAttribute(qt.WA_DeleteOnClose)
	var items []menuItem
	for _, o := range a.Options {
		if o == nil {
			continue
		}
		items = append(items, menuItem{title: o.Text, check: checkIf(o.Text == current), do: func() { choose(o.Value, o.Text) }})
	}
	if len(items) == 0 {
		items = append(items, menuItem{title: "Нет вариантов", do: func() {}})
	}
	fillMenu(menu, items)
	return menu
}

// runAction — действие a поста p на сервере (selected — выбранное в списке).
func (m *MainWindow) runAction(p *model.Post, a *model.PostAction, selected string) {
	req := model.DoPostActionRequest{SelectedOption: selected, Cookie: a.Cookie}
	name := a.Name
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var resp model.PostActionAPIResponse
		r, err := m.s.Client.DoAPIPostJSON(ctx, "/posts/"+url.PathEscape(p.Id)+"/actions/"+url.PathEscape(a.Id), req)
		if err == nil {
			defer r.Body.Close()
			if err = json.NewDecoder(r.Body).Decode(&resp); errors.Is(err, io.EOF) {
				err = nil
			}
		}
		m.ui(func() {
			if err != nil {
				m.log.Warn("post action", "post", p.Id, "action", a.Id, "err", err)
				m.message(fmt.Sprintf("«%s» не выполнено: %s", name, commandError(err)))
				return
			}
			if resp.GotoLocation != "" {
				m.gotoLocation(resp.GotoLocation)
			}
		})
	}()
}

// chooseUser — выбрать одного человека (список интеграции с data_source
// users): известные клиенту и поиск по серверу.
func (m *MainWindow) chooseUser(title string, done func(u *model.User)) *peoplePicker {
	d := newDialog(m.w, title)
	m.channelDialog = d
	d.w.SetMinimumWidth(420)
	local := func(string) []*model.User {
		users := m.st.KnownUsers()
		slices.SortFunc(users, func(a, b *model.User) int {
			return cmp.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username))
		})
		return users
	}
	remote := func(ctx context.Context, q string) ([]*model.User, error) {
		if q == "" {
			return nil, nil
		}
		users, _, err := m.s.Client.SearchUsers(ctx, &model.UserSearch{Term: q, Limit: peopleLimit})
		return users, err
	}
	pp := m.newPeoplePicker(d, 1, local, remote)
	pp.OnChange = func() {
		if len(pp.chosen) == 1 {
			u := pp.chosen[0]
			d.w.Close()
			done(u)
		}
	}
	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { d.w.Close() })
	d.bottom.AddWidget(cancel.QWidget)
	d.w.Show()
	pp.field.SetFocus()
	return pp
}

// chooseChannel — выбрать канал команды team (список интеграции с
// data_source channels): свои открытые и закрытые каналы, по названию.
func (m *MainWindow) chooseChannel(title, team string, done func(it switchItem)) *dialog {
	var all []switchItem
	for _, it := range m.switchSources(nil, nil) {
		if ch := m.st.Channel(it.id); it.kind == switchChannel && ch != nil && !ch.IsGroupOrDirect() && (team == "" || ch.TeamId == team) {
			all = append(all, it)
		}
	}
	return m.chooseItem(title, "Канал", all, done)
}

// chooseItem — окно выбора из items: строка поиска и список (стрелки, Enter,
// щелчок).
func (m *MainWindow) chooseItem(title, placeholder string, all []switchItem, done func(it switchItem)) *dialog {
	d := newDialog(m.w, title)
	m.channelDialog = d
	d.w.SetMinimumWidth(420)
	field := qt.NewQLineEdit2()
	field.SetPlaceholderText(placeholder)
	field.SetClearButtonEnabled(true)
	list := qt.NewQListWidget2()
	list.SetIconSize(qt.NewQSize2(20, 20))
	list.SetMinimumHeight(260)
	list.SetFocusPolicy(qt.NoFocus)
	d.lay.AddWidget(field.QWidget)
	d.lay.AddWidget2(list.QWidget, 1)
	var shown []switchItem
	fill := func() {
		shown = rankSwitch(all, field.Text(), "")
		list.Clear()
		for _, it := range shown {
			label := it.title
			if it.sub != "" {
				label += "   " + it.sub
			}
			row := qt.NewQListWidgetItem7(label, list)
			if it.icon != nil {
				row.SetIcon(qt.NewQIcon2(it.icon))
			}
		}
		if len(shown) > 0 {
			list.SetCurrentRow(0)
		}
	}
	pick := func() {
		if i := list.CurrentRow(); i >= 0 && i < len(shown) {
			it := shown[i]
			d.w.Close()
			done(it)
		}
	}
	field.OnTextChanged(func(string) { fill() })
	field.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		switch qt.Key(e.Key()) {
		case qt.Key_Down:
			list.SetCurrentRow(min(list.CurrentRow()+1, list.Count()-1))
		case qt.Key_Up:
			list.SetCurrentRow(max(list.CurrentRow()-1, 0))
		case qt.Key_Return, qt.Key_Enter:
			pick()
		default:
			super(e)
		}
	})
	list.OnItemClicked(func(it *qt.QListWidgetItem) {
		list.SetCurrentRow(list.Row(it))
		pick()
	})
	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { d.w.Close() })
	d.bottom.AddWidget(cancel.QWidget)
	fill()
	d.w.Show()
	field.SetFocus()
	return d
}
