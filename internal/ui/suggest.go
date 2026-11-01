package ui

import (
	"context"
	"fmt"
	"slices"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/complete"
	"matterqute/internal/emoji"
)

const (
	suggestLimit = 20 // строк в списке; видно 8, остальное — прокруткой
	suggestRow   = 32
)

// Suggestion — строка списка автодополнения.
type Suggestion struct {
	Insert string      // что вставить вместо набранного слова: «@bob », «~town-square », «:tada: »
	Title  string      // «@bob», «Town Square», «:tada:»
	Sub    string      // «Борис Иванов», «~town-square»
	Icon   *qt.QPixmap // аватар, иконка канала, эмодзи (20 px)
}

// suggestPopup — список подсказок над полем ввода. Фокус остаётся в поле:
// стрелки, Enter, Tab и Esc поле перехватывает и передаёт сюда.
type suggestPopup struct {
	frame *qt.QFrame
	list  *qt.QListWidget
	items []Suggestion

	onPick func(Suggestion)
}

func newSuggestPopup(parent *qt.QWidget) *suggestPopup {
	p := &suggestPopup{frame: qt.NewQFrame(parent), list: qt.NewQListWidget2()}
	setName(p.frame.QObject, "suggest")
	p.list.SetFocusPolicy(qt.NoFocus)
	p.list.Viewport().SetFocusPolicy(qt.NoFocus)
	p.list.SetFrameShape(qt.QFrame__NoFrame)
	p.list.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	p.list.SetUniformItemSizes(true)
	p.list.SetMouseTracking(true)
	d := qt.NewQStyledItemDelegate2(p.list.QObject)
	d.OnPaint(func(_ func(*qt.QPainter, *qt.QStyleOptionViewItem, *qt.QModelIndex), painter *qt.QPainter, opt *qt.QStyleOptionViewItem, idx *qt.QModelIndex) {
		if row := idx.Row(); row < len(p.items) {
			p.paintRow(painter, opt, p.items[row])
		}
	})
	d.OnSizeHint(func(_ func(*qt.QStyleOptionViewItem, *qt.QModelIndex) *qt.QSize, opt *qt.QStyleOptionViewItem, _ *qt.QModelIndex) *qt.QSize {
		return qt.NewQSize2(opt.Rect().Width(), suggestRow)
	})
	p.list.SetItemDelegate(d.QAbstractItemDelegate)
	p.list.OnItemClicked(func(it *qt.QListWidgetItem) {
		if row := p.list.Row(it); row >= 0 && row < len(p.items) && p.onPick != nil {
			p.onPick(p.items[row])
		}
	})
	lay := qt.NewQVBoxLayout(p.frame.QWidget)
	lay.SetContentsMargins(4, 4, 4, 4)
	lay.AddWidget(p.list.QWidget)
	p.frame.Hide()
	return p
}

func (p *suggestPopup) visible() bool { return p.frame.IsVisible() && len(p.items) > 0 }

// show — список items над виджетом anchor (рамка поля ввода), по его ширине.
func (p *suggestPopup) show(items []Suggestion, anchor *qt.QWidget) {
	keep := ""
	if row := p.list.CurrentRow(); row >= 0 && row < len(p.items) {
		keep = p.items[row].Insert // догрузились результаты сервера — выбор не сбрасываем
	}
	p.items = items
	if len(items) == 0 {
		p.hide()
		return
	}
	p.list.Clear()
	cur := 0
	for i, it := range items {
		p.list.AddItem(it.Title)
		if it.Insert == keep {
			cur = i
		}
	}
	p.list.SetCurrentRow(cur)
	h := min(len(items), 8)*suggestRow + 8 + 2
	w := min(anchor.Width(), 520)
	at := anchor.MapTo2(p.frame.ParentWidget(), qt.NewQPoint2(0, 0))
	p.frame.SetGeometry(at.X(), at.Y()-h-4, w, h)
	p.frame.Raise()
	p.frame.Show()
	p.applyStyle()
}

func (p *suggestPopup) hide() {
	p.frame.Hide()
	p.items = nil
}

// move — стрелки вверх и вниз по кругу.
func (p *suggestPopup) move(delta int) {
	n := len(p.items)
	if n == 0 {
		return
	}
	p.list.SetCurrentRow(((p.list.CurrentRow()+delta)%n + n) % n)
}

func (p *suggestPopup) current() (Suggestion, bool) {
	row := p.list.CurrentRow()
	if row < 0 || row >= len(p.items) {
		return Suggestion{}, false
	}
	return p.items[row], true
}

func (p *suggestPopup) applyStyle() {
	pal := Pal()
	p.frame.SetStyleSheet(fmt.Sprintf(`QFrame#suggest { background: %s; border: 1px solid %s; border-radius: 6px; }
QListWidget { background: transparent; border: none; outline: none; }`, pal.Surf, pal.Line2))
}

func (p *suggestPopup) paintRow(painter *qt.QPainter, opt *qt.QStyleOptionViewItem, s Suggestion) {
	pal := Pal()
	r := opt.Rect()
	selected := opt.State()&qt.QStyle__State_Selected != 0
	hover := opt.State()&qt.QStyle__State_MouseOver != 0
	painter.Save()
	defer painter.Restore()
	painter.SetRenderHint(qt.QPainter__Antialiasing)
	if selected || hover {
		bg := pal.Hover
		if selected {
			bg = pal.AccW
		}
		painter.SetPenWithStyle(qt.NoPen)
		painter.SetBrush(qt.NewQBrush3(qt.NewQColor6(bg)))
		painter.DrawRoundedRect(qt.NewQRectF4(float64(r.X()), float64(r.Y()), float64(r.Width()), float64(r.Height())), 4, 4)
	}
	x, cy := r.X()+8, r.Y()+r.Height()/2
	if s.Icon != nil {
		painter.DrawPixmap9(x, cy-10, s.Icon)
	}
	x += 28
	titleColor := pal.Tx
	if selected {
		titleColor = pal.SelTx
	}
	tf := pixelFont(14, qt.QFont__DemiBold)
	fm := qt.NewQFontMetrics(tf)
	right := r.X() + r.Width() - 8
	title := fm.ElidedText(s.Title, qt.ElideRight, right-x)
	painter.SetFont(tf)
	painter.SetPen(qt.NewQColor6(titleColor))
	painter.DrawText7(x, r.Y(), right-x, r.Height(), int(qt.AlignVCenter|qt.AlignLeft), title)
	x += fm.HorizontalAdvance(title) + 8
	if s.Sub != "" && x < right {
		sf := pixelFont(13, qt.QFont__Normal)
		painter.SetFont(sf)
		painter.SetPen(qt.NewQColor6(pal.Tx2))
		painter.DrawText7(x, r.Y(), right-x, r.Height(), int(qt.AlignVCenter|qt.AlignLeft),
			qt.NewQFontMetrics(sf).ElidedText(s.Sub, qt.ElideRight, right-x))
	}
}

// emojiPixmap — символ эмодзи картинкой 20 px (цветным шрифтом приложения).
func emojiPixmap(char string) *qt.QPixmap {
	key := "emoji|" + char
	if pm := style.icons[key]; pm != nil {
		return pm
	}
	img := qt.NewQImage3(20*resourceDPR, 20*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
	img.SetDevicePixelRatio(resourceDPR)
	img.Fill2(qt.Transparent)
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetFont(pixelFont(16, qt.QFont__Normal))
	p.DrawText7(0, 0, 20, 20, int(qt.AlignCenter), char)
	p.End()
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	style.icons[key] = pm
	return pm
}

// suggester — источник подсказок для поля ввода канала channel(): сразу то,
// что уже знаем, а через 150 мс после последнего нажатия — с сервера:
// участники канала и остальные, каналы, где нас нет, эмодзи сервера.
func (m *MainWindow) suggester(channel func() string) func(complete.Token, func([]Suggestion)) {
	return func(tok complete.Token, show func([]Suggestion)) {
		chID := channel()
		show(m.suggestions(tok, chID, nil))
		m.suggestTimer.Stop()
		m.suggestPending = func() { m.suggestRemote(tok, chID, show) }
		m.suggestTimer.Start(150)
	}
}

// remote — что сервер добавил к известному локально.
type remote struct {
	inChannel, others []*model.User
	channels          []*model.Channel
	emoji             []string
}

func (m *MainWindow) suggestions(tok complete.Token, chID string, r *remote) []Suggestion {
	if r == nil {
		r = &remote{}
	}
	ch := m.st.Channel(chID)
	var out []Suggestion
	switch tok.Kind {
	case complete.User:
		// Сначала участники канала (их знает сервер), потом все остальные.
		users := complete.Users(r.inChannel, tok.Query, suggestLimit)
		seen := map[string]bool{}
		for _, u := range users {
			seen[u.Id] = true
		}
		rest := slices.DeleteFunc(append(m.st.KnownUsers(), r.others...), func(u *model.User) bool { return seen[u.Id] })
		users = append(users, complete.Users(rest, tok.Query, suggestLimit)...)
		for _, u := range users[:min(len(users), suggestLimit)] {
			sub := displayName(u)
			if sub == u.Username {
				sub = ""
			}
			if u.Id == m.s.Me.Id {
				sub += " (вы)"
			}
			out = append(out, Suggestion{Insert: "@" + u.Username + " ", Title: "@" + u.Username, Sub: sub,
				Icon: avatarPixmap(m.assets, u, u.Username, u.IsBot, 20, nil)})
		}
		if ch != nil && !ch.IsGroupOrDirect() {
			for _, sp := range complete.Specials(tok.Query) {
				out = append(out, Suggestion{Insert: "@" + sp.Name + " ", Title: "@" + sp.Name, Sub: sp.Description,
					Icon: iconPixmap("users", Pal().Tx2, 20)})
			}
		}
	case complete.Channel:
		var mine []*model.Channel
		for _, t := range m.st.Teams() {
			if ch == nil || ch.TeamId == "" || ch.TeamId == t.Id {
				mine = append(mine, m.st.TeamChannels(t.Id)...)
			}
		}
		chs := complete.Channels(append(mine, r.channels...), tok.Query, suggestLimit)
		for _, c := range chs {
			iconName := "hash"
			if c.Type == model.ChannelTypePrivate {
				iconName = "lock"
			}
			out = append(out, Suggestion{Insert: "~" + c.Name + " ", Title: c.DisplayName, Sub: "~" + c.Name,
				Icon: iconPixmap(iconName, Pal().Tx2, 20)})
		}
	case complete.Emoji:
		names := complete.EmojiNames(append(slices.Clone(emoji.Names()), r.emoji...), tok.Query, suggestLimit)
		for _, name := range names {
			s := Suggestion{Insert: ":" + name + ": ", Title: ":" + name + ":"}
			if c, ok := emoji.Char(name); ok {
				s.Icon = emojiPixmap(c)
			} else if path := m.assets.Emoji(name); path != "" {
				pm := qt.NewQPixmap4(path).Scaled3(20*resourceDPR, 20*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
				pm.SetDevicePixelRatio(resourceDPR)
				s.Icon = pm
			} else {
				m.assets.WantEmoji(name, func() {})
			}
			out = append(out, s)
		}
	}
	return out
}

// suggestRemote — спросить сервер и показать всё вместе; show сам отбросит
// ответ, если пользователь уже набрал другое.
func (m *MainWindow) suggestRemote(tok complete.Token, chID string, show func([]Suggestion)) {
	ch := m.st.Channel(chID)
	team := ""
	if ch != nil {
		team = ch.TeamId
	}
	if team == "" && len(m.st.Teams()) > 0 {
		team = m.st.Teams()[0].Id
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		r := &remote{}
		var err error
		switch tok.Kind {
		case complete.User:
			var ua *model.UserAutocomplete
			if ch != nil && ch.TeamId != "" {
				ua, _, err = m.s.Client.AutocompleteUsersInChannel(ctx, team, chID, tok.Query, suggestLimit, "")
			} else {
				ua, _, err = m.s.Client.AutocompleteUsersInTeam(ctx, team, tok.Query, suggestLimit, "")
			}
			if ua != nil {
				r.inChannel, r.others = ua.Users, ua.OutOfChannel
			}
		case complete.Channel:
			if team != "" {
				r.channels, _, err = m.s.Client.AutocompleteChannelsForTeam(ctx, team, tok.Query)
			}
		case complete.Emoji:
			var list []*model.Emoji
			list, _, err = m.s.Client.AutocompleteEmoji(ctx, tok.Query, "")
			for _, e := range list {
				r.emoji = append(r.emoji, e.Name)
			}
		}
		m.ui(func() {
			if err != nil {
				m.log.Debug("autocomplete", "kind", tok.Kind, "query", tok.Query, "err", err)
				return
			}
			m.st.AddUsers(append(slices.Clone(r.inChannel), r.others...))
			show(m.suggestions(tok, chID, r))
		})
	}()
}
