package ui

import (
	"context"
	"encoding/json"
	"fmt"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/emoji"
	"matterqute/internal/theme"
)

const (
	pickerCols   = 9
	pickerCell   = 34
	pickerIcon   = 24
	pickerPad    = 8
	pickerHeader = 26
)

// pickerSection — раздел сетки: «Недавние», разделы Unicode, «Эмодзи сервера».
type pickerSection struct {
	title string
	tab   *qt.QPixmap // значок кнопки раздела
	names []string
	top   int // y заголовка в сетке
}

// emojiPicker — окно выбора эмодзи (реакции): поиск, кнопки разделов, сетка с
// заголовками, внизу — эмодзи под курсором. Сетка — один виджет, рисуется
// только видимая часть: эмодзи почти две тысячи. Эмодзи — картинками цветного
// шрифта, а не текстом виджетов: так они цветные при любом системном шрифте.
type emojiPicker struct {
	w        *qt.QFrame
	search   *qt.QLineEdit
	tabs     *qt.QHBoxLayout
	scroll   *qt.QScrollArea
	grid     *qt.QWidget
	preview  *qt.QLabel
	name     *qt.QLabel
	sections []*pickerSection
	all      []*pickerSection // без поиска
	hover    string
	closed   bool

	image  func(name string, size int) *qt.QPixmap
	onPick func(name string)
}

func newEmojiPicker(parent *qt.QWidget, image func(string, int) *qt.QPixmap) *emojiPicker {
	p := &emojiPicker{w: qt.NewQFrame3(parent, qt.Popup|qt.FramelessWindowHint), image: image}
	setName(p.w.QObject, "emojiPicker")
	p.w.SetAttribute(qt.WA_DeleteOnClose)
	p.w.OnDestroyed(func() { p.closed = true })

	p.search = qt.NewQLineEdit2()
	p.search.SetPlaceholderText("Поиск эмодзи")
	p.search.SetClearButtonEnabled(true)
	p.search.OnTextChanged(func(string) { p.filter() })
	p.search.OnReturnPressed(func() {
		if len(p.sections) > 0 && len(p.sections[0].names) > 0 {
			p.pick(p.sections[0].names[0])
		}
	})

	tabs := qt.NewQWidget2()
	p.tabs = qt.NewQHBoxLayout(tabs)
	p.tabs.SetContentsMargins(0, 0, 0, 0)
	p.tabs.SetSpacing(2)

	p.grid = qt.NewQWidget2()
	p.grid.SetMouseTracking(true)
	p.grid.OnPaintEvent(func(_ func(*qt.QPaintEvent), e *qt.QPaintEvent) { p.paint(e.Rect()) })
	p.grid.OnMouseMoveEvent(func(_ func(*qt.QMouseEvent), e *qt.QMouseEvent) {
		p.setHover(p.at(e.Pos().X(), e.Pos().Y()))
	})
	p.grid.OnLeaveEvent(func(_ func(*qt.QEvent), _ *qt.QEvent) { p.setHover("") })
	p.grid.OnMousePressEvent(func(_ func(*qt.QMouseEvent), e *qt.QMouseEvent) {
		if name := p.at(e.Pos().X(), e.Pos().Y()); name != "" && e.Button() == qt.LeftButton {
			p.pick(name)
		}
	})
	p.scroll = qt.NewQScrollArea2()
	p.scroll.SetWidget(p.grid)
	p.scroll.SetFrameShape(qt.QFrame__NoFrame)
	p.scroll.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	p.scroll.SetFixedHeight(6*pickerCell + pickerHeader)

	p.preview = qt.NewQLabel2()
	p.preview.SetFixedSize2(28, 28)
	p.name = qt.NewQLabel2()
	setName(p.name.QObject, "emojiName")
	foot := qt.NewQHBoxLayout2()
	foot.SetSpacing(8)
	foot.AddWidget(p.preview.QWidget)
	foot.AddWidget2(p.name.QWidget, 1)

	lay := qt.NewQVBoxLayout(p.w.QWidget)
	lay.SetContentsMargins(pickerPad, pickerPad, pickerPad, pickerPad)
	lay.SetSpacing(6)
	lay.AddWidget(p.search.QWidget)
	lay.AddWidget(tabs)
	lay.AddWidget(p.scroll.QWidget)
	lay.AddLayout(foot.QLayout)

	pal := Pal()
	p.w.SetStyleSheet(fmt.Sprintf(`QFrame#emojiPicker { background: %[1]s; border: 1px solid %[2]s; border-radius: 6px; }
QScrollArea, QScrollArea > QWidget > QWidget { background: %[1]s; }
QToolButton { border: none; border-radius: 4px; background: transparent; }
QToolButton:hover { background: %[3]s; }
QLabel#emojiName { font-family: "%[5]s"; font-size: 12px; color: %[4]s; }`, pal.Surf, pal.Line2, pal.Hover, pal.Tx2, monoFamily()))
	p.w.SetFixedWidth(pickerCols*pickerCell + 2*pickerPad + 2*pickerPad + 10)
	return p
}

// setSections — разделы без поиска; кнопки разделов — по ним.
func (p *emojiPicker) setSections(sections []*pickerSection) {
	p.all = sections
	for p.tabs.Count() > 0 {
		if it := p.tabs.TakeAt(0); it != nil && it.Widget() != nil {
			it.Widget().DeleteLater()
		}
	}
	for _, s := range sections {
		if len(s.names) == 0 {
			continue
		}
		b := qt.NewQToolButton2()
		b.SetFixedSize2(30, 28)
		b.SetIcon(qt.NewQIcon2(s.tab))
		b.SetIconSize(qt.NewQSize2(18, 18))
		b.SetToolTip(s.title)
		b.SetFocusPolicy(qt.NoFocus)
		b.OnClicked(func() {
			p.search.Clear()
			p.scroll.VerticalScrollBar().SetValue(s.top)
		})
		p.tabs.AddWidget(b.QWidget)
	}
	p.tabs.AddStretch()
	p.filter()
}

// filter — строка поиска: пусто — все разделы, иначе один раздел с найденным.
func (p *emojiPicker) filter() {
	q := p.search.Text()
	if q == "" {
		p.sections = p.all
	} else {
		var custom []string
		for _, s := range p.all {
			if s.title == "Эмодзи сервера" {
				custom = s.names
			}
		}
		title := "Найдено"
		found := emoji.Search(q, custom, 200)
		if len(found) == 0 {
			title = "Ничего не нашлось"
		}
		p.sections = []*pickerSection{{title: title, names: found}}
	}
	y := 0
	for _, s := range p.sections {
		s.top = y
		y += pickerHeader + (len(s.names)+pickerCols-1)/pickerCols*pickerCell
	}
	p.grid.SetFixedSize2(pickerCols*pickerCell+2*pickerPad, max(y+pickerPad, p.scroll.Height()))
	p.scroll.VerticalScrollBar().SetValue(0)
	p.setHover("")
	p.grid.Update()
}

// cell — где в сетке i-й эмодзи раздела.
func (p *emojiPicker) cell(s *pickerSection, i int) (x, y int) {
	return pickerPad + i%pickerCols*pickerCell, s.top + pickerHeader + i/pickerCols*pickerCell
}

func (p *emojiPicker) at(x, y int) string {
	for _, s := range p.sections {
		if y < s.top+pickerHeader || y >= s.top+pickerHeader+(len(s.names)+pickerCols-1)/pickerCols*pickerCell {
			continue
		}
		col, row := (x-pickerPad)/pickerCell, (y-s.top-pickerHeader)/pickerCell
		if x < pickerPad || col >= pickerCols {
			return ""
		}
		if i := row*pickerCols + col; i < len(s.names) {
			return s.names[i]
		}
	}
	return ""
}

func (p *emojiPicker) setHover(name string) {
	if name == p.hover {
		return
	}
	p.hover = name
	if name == "" {
		p.preview.Clear()
		p.name.SetText("")
	} else {
		p.preview.SetPixmap(p.image(name, 28))
		p.name.SetText(":" + name + ":")
	}
	p.grid.Update()
}

func (p *emojiPicker) paint(clip *qt.QRect) {
	pal := Pal()
	painter := qt.NewQPainter2(p.grid.QPaintDevice)
	defer painter.End()
	painter.SetRenderHint(qt.QPainter__Antialiasing)
	head := pixelFont(12, qt.QFont__DemiBold)
	for _, s := range p.sections {
		bottom := s.top + pickerHeader + (len(s.names)+pickerCols-1)/pickerCols*pickerCell
		if bottom < clip.Top() || s.top > clip.Bottom() {
			continue
		}
		painter.SetFont(head)
		painter.SetPen(qt.NewQColor6(pal.Tx2))
		painter.DrawText7(pickerPad+2, s.top, pickerCols*pickerCell, pickerHeader, int(qt.AlignVCenter|qt.AlignLeft), s.title)
		for i, name := range s.names {
			x, y := p.cell(s, i)
			if y+pickerCell < clip.Top() || y > clip.Bottom() {
				continue
			}
			if name == p.hover {
				painter.SetPenWithStyle(qt.NoPen)
				painter.SetBrush(qt.NewQBrush3(qt.NewQColor6(pal.Hover)))
				painter.DrawRoundedRect(qt.NewQRectF4(float64(x), float64(y), pickerCell, pickerCell), 4, 4)
			}
			off := (pickerCell - pickerIcon) / 2
			painter.DrawPixmap9(x+off, y+off, p.image(name, pickerIcon))
		}
	}
}

func (p *emojiPicker) pick(name string) {
	if p.onPick != nil {
		p.onPick(name)
	}
	p.w.Close()
}

// emojiImage — эмодзи картинкой size px: системное — цветным шрифтом
// (явно Noto Color Emoji, не через запасные шрифты системы), кастомное — с
// сервера; пока не загружено — пусто, done перерисует.
func (m *MainWindow) emojiImage(name string, size int, done func()) *qt.QPixmap {
	if c, ok := emoji.Char(name); ok {
		return emojiPixmapSized(c, size)
	}
	if frame := m.assets.EmojiFrame(name); frame != nil { // анимированное — текущий кадр
		pm := frame.Scaled3(size*resourceDPR, size*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
		pm.SetDevicePixelRatio(resourceDPR)
		return pm
	}
	key := fmt.Sprintf("custom|%s|%d", name, size)
	if pm := style.icons[key]; pm != nil {
		return pm
	}
	path := m.assets.Emoji(name)
	if path == "" {
		m.assets.WantEmoji(name, done)
		return emptyPixmap(size)
	}
	pm := qt.NewQPixmap4(path).Scaled3(size*resourceDPR, size*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
	pm.SetDevicePixelRatio(resourceDPR)
	style.icons[key] = pm
	return pm
}

func emptyPixmap(size int) *qt.QPixmap {
	img := qt.NewQImage3(size*resourceDPR, size*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
	img.Fill2(qt.Transparent)
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	return pm
}

// emojiPixmapSized — символ эмодзи картинкой size px шрифтом Noto Color Emoji.
func emojiPixmapSized(char string, size int) *qt.QPixmap {
	key := fmt.Sprintf("emoji|%s|%d", char, size)
	if pm := style.icons[key]; pm != nil {
		return pm
	}
	img := qt.NewQImage3(size*resourceDPR, size*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
	img.SetDevicePixelRatio(resourceDPR)
	img.Fill2(qt.Transparent)
	p := qt.NewQPainter2(img.QPaintDevice)
	f := qt.NewQFont()
	f.SetFamilies([]string{theme.EmojiFamily})
	f.SetPixelSize(size * 4 / 5)
	p.SetFont(f)
	p.DrawText7(0, 0, size, size, int(qt.AlignCenter), char)
	p.End()
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	style.icons[key] = pm
	return pm
}

// pickReaction — окно выбора у курсора; выбранное эмодзи ставится реакцией
// (если уже стоит наша — ничего не меняем).
func (m *MainWindow) pickReaction(p *model.Post) {
	m.pickEmoji(m.w, qt.QCursor_Pos(), func(name string) {
		if !HasReaction(p, m.s.Me.Id, name) {
			m.toggleReaction(p, name)
		}
	})
}

// pickEmoji — окно выбора эмодзи у точки pos (глобальной); parent — чьё это
// всплывающее окно (из модального диалога — сам диалог, иначе ввод заблокирован).
func (m *MainWindow) pickEmoji(parent *qt.QWidget, pos *qt.QPoint, onPick func(name string)) {
	var picker *emojiPicker
	var stop []func()
	update := func() {
		if !picker.closed {
			picker.grid.Update()
		}
	}
	picker = newEmojiPicker(parent, func(name string, size int) *qt.QPixmap {
		return m.emojiImage(name, size, func() {
			// Докачалось при открытом окне: если анимированное — пусть крутится сразу.
			if !picker.closed {
				stop = append(stop, m.assets.OnEmojiFrame(name, update))
			}
			update()
		})
	})
	m.picker = picker
	picker.onPick = onPick
	picker.setSections(m.pickerSections())
	// Анимированные эмодзи сервера крутятся, пока окно открыто.
	animate := func() {
		for _, name := range m.customEmoji {
			stop = append(stop, m.assets.OnEmojiFrame(name, update))
		}
	}
	animate()
	picker.w.OnDestroyed(func() {
		for _, f := range stop {
			f()
		}
	})
	picker.w.AdjustSize()
	// В пределах экрана: окно выше и левее курсора, если не помещается.
	scr := m.w.Screen().AvailableGeometry()
	x := min(pos.X(), scr.X()+scr.Width()-picker.w.Width())
	y := pos.Y() + 8
	if y+picker.w.Height() > scr.Y()+scr.Height() {
		y = pos.Y() - picker.w.Height() - 8
	}
	picker.w.Move(max(x, scr.X()), max(y, scr.Y()))
	picker.w.Show()
	picker.search.SetFocus()
	m.loadEmojiData(func() {
		if !picker.closed {
			picker.setSections(m.pickerSections())
			animate()
		}
	})
}

func (m *MainWindow) pickerSections() []*pickerSection {
	recent := m.recent.Names()
	if len(recent) == 0 {
		recent = emoji.Quick
	}
	out := []*pickerSection{{title: "Недавние", tab: iconPixmap("clock", Pal().Tx2, 18), names: recent}}
	tabs := []string{"😀", "🧑", "🐻", "🍔", "✈️", "⚽", "💡", "🔣", "🏁"}
	for i, c := range emoji.Categories() {
		out = append(out, &pickerSection{title: c.Title, tab: emojiPixmapSized(tabs[i], 18), names: c.Names})
	}
	if len(m.customEmoji) > 0 {
		out = append(out, &pickerSection{title: "Эмодзи сервера", tab: iconPixmap("smile-plus", Pal().Tx2, 18), names: m.customEmoji})
	}
	return out
}

// loadEmojiData — недавние (настройка recent_emojis, как у веб-клиента) и
// кастомные эмодзи сервера, один раз за сессию; done — когда пришли.
func (m *MainWindow) loadEmojiData(done func()) {
	if m.emojiLoaded {
		return
	}
	m.emojiLoaded = true
	me := m.s.Me.Id
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var recent emoji.Recent
		prefs, _, err := m.s.Client.GetPreferencesByCategory(ctx, me, emoji.RecentCategory)
		if err != nil {
			m.log.Warn("load recent emoji", "err", err)
		}
		for _, p := range prefs {
			if p.Name == me {
				if err := json.Unmarshal([]byte(p.Value), &recent); err != nil {
					m.log.Warn("recent emoji preference", "value", truncate(p.Value, 200), "err", err)
				}
			}
		}
		var custom []string
		for page := 0; page < 20; page++ {
			list, _, err := m.s.Client.GetSortedEmojiList(ctx, page, 200, model.EmojiSortByName)
			if err != nil {
				m.log.Warn("load custom emoji", "page", page, "err", err)
				break
			}
			for _, e := range list {
				custom = append(custom, e.Name)
			}
			if len(list) < 200 {
				break
			}
		}
		m.ui(func() {
			if len(m.recent) == 0 {
				m.recent = recent
			}
			m.customEmoji = custom
			done()
		})
	}()
}

// quickReactions — эмодзи панели реакций у сообщения; недавние приходят с
// сервера при первом показе панели — тогда она перерисуется.
func (m *MainWindow) quickReactions() []string {
	m.loadEmojiData(func() {
		m.feed.quick.refresh()
		m.panel.feed.quick.refresh()
	})
	return quickReactions(m.recent)
}

// useEmoji — эмодзи поставили реакцией: в недавние и на сервер (общие с веб-клиентом).
func (m *MainWindow) useEmoji(name string) {
	m.recent = m.recent.Add(name)
	value, _ := json.Marshal(m.recent)
	me := m.s.Me.Id
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		pref := model.Preference{UserId: me, Category: emoji.RecentCategory, Name: me, Value: string(value)}
		if _, err := m.s.Client.UpdatePreferences(ctx, me, model.Preferences{pref}); err != nil {
			m.log.Warn("save recent emoji", "err", err)
		}
	}()
}
