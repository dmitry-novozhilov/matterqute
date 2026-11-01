package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/complete"
)

// attachment — файл в поле ввода: грузится (percent), загружен (fileID) или
// не загрузился (err). path или data — откуда грузить повторно.
type attachment struct {
	id      int // локальный, уникален в пределах Composer
	name    string
	path    string
	data    []byte
	size    int64
	percent int
	fileID  string
	err     error
}

// draft — недописанное сообщение канала или треда.
type draft struct {
	key   string
	text  string
	files []*attachment
}

// Строк в поле ввода, после которых появляется прокрутка.
const composerMaxLines = 7

// Composer — поле ввода (QFrame#composer): Enter отправляет, Shift+Enter
// переносит строку; файлы — кнопкой 📎, перетаскиванием или вставкой картинки.
// Черновики хранятся по ключу (канал, тред): SwitchTo переключает.
type Composer struct {
	w        *qt.QWidget // обёртка с полями вокруг рамки
	frame    *qt.QFrame
	edit     *qt.QTextEdit
	attach   *qt.QToolButton
	chips    *qt.QWidget
	chipsLay *qt.QHBoxLayout
	chipWs   []*qt.QWidget
	hint     *qt.QLabel

	placeholder string
	offline     bool
	key         string
	files       []*attachment
	drafts      map[string]*draft
	lastID      int
	sent        *draft // отправляется: вернуть в черновик, если отправка не удалась
	unsubscribe func()

	// Suggest — подсказки для слова под курсором (@, #, ~, :); show можно
	// звать несколько раз (сначала известное, потом с сервера). nil — без подсказок.
	Suggest   func(tok complete.Token, show func([]Suggestion))
	popup     *suggestPopup
	tok       complete.Token // слово, для которого показан список
	tokGen    int            // ответы для прежних слов отбрасываются
	dismissed string         // Esc: для этого слова список больше не открывать

	OnSend func(text string, fileIDs []string)
	// OnUpload — загрузить файл (path с диска или data — вставленная картинка);
	// progress(0…100) по ходу, результат — Uploaded или UploadFailed.
	OnUpload func(id int, name, path string, data []byte, progress func(percent int))
}

func NewComposer() *Composer {
	c := &Composer{w: qt.NewQWidget2(), frame: qt.NewQFrame2(), edit: qt.NewQTextEdit2(), drafts: map[string]*draft{}}
	setName(c.frame.QObject, "composer")
	setName(c.edit.QObject, "composerEdit")
	c.edit.SetAcceptRichText(false)
	c.edit.SetFrameShape(qt.QFrame__NoFrame)
	c.edit.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	c.edit.SetToolTip("Enter — отправить, Shift+Enter — новая строка")
	c.edit.Document().SetDocumentMargin(4)
	c.edit.Document().OnContentsChanged(c.fitHeight)
	c.edit.OnTextChanged(c.updateSuggest)
	c.edit.OnCursorPositionChanged(c.updateSuggest)
	c.edit.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		k := qt.Key(e.Key())
		if c.suggestKey(k) {
			return
		}
		if (k == qt.Key_Return || k == qt.Key_Enter) && e.Modifiers()&qt.ShiftModifier == 0 {
			c.send()
			return
		}
		super(e)
	})
	c.edit.OnFocusInEvent(func(super func(*qt.QFocusEvent), e *qt.QFocusEvent) {
		super(e)
		setProp(c.frame.QWidget, "focus", "true")
	})
	c.edit.OnFocusOutEvent(func(super func(*qt.QFocusEvent), e *qt.QFocusEvent) {
		super(e)
		setProp(c.frame.QWidget, "focus", "false")
		c.hideSuggest() // список не берёт фокус: клик по нему фокус не уводит
	})
	// Перетаскивание и вставка: файлы и картинки — во вложения, остальное — текстом.
	c.edit.OnCanInsertFromMimeData(func(super func(*qt.QMimeData) bool, m *qt.QMimeData) bool {
		return m.HasUrls() || m.HasImage() || super(m)
	})
	c.edit.OnInsertFromMimeData(func(super func(*qt.QMimeData), m *qt.QMimeData) {
		if m.HasUrls() {
			var paths []string
			for _, u := range m.Urls() {
				if u.IsLocalFile() {
					paths = append(paths, u.ToLocalFile())
				}
			}
			if len(paths) > 0 {
				c.AddFiles(paths)
				return
			}
		}
		if m.HasImage() && c.pasteImage() {
			return
		}
		super(m)
	})

	c.attach = qt.NewQToolButton2()
	setName(c.attach.QObject, "attach")
	c.attach.SetToolTip("Прикрепить файлы")
	c.attach.SetFixedSize2(32, 32)
	c.attach.SetIconSize(qt.NewQSize2(18, 18))
	c.attach.OnClicked(func() {
		c.AddFiles(qt.QFileDialog_GetOpenFileNames2(c.w, "Прикрепить файлы"))
	})

	c.chips = qt.NewQWidget2()
	c.chipsLay = qt.NewQHBoxLayout(c.chips)
	c.chipsLay.SetContentsMargins(0, 0, 0, 0)
	c.chipsLay.SetSpacing(6)
	c.chipsLay.AddStretch()
	c.chips.SetVisible(false)

	row := qt.NewQHBoxLayout2()
	row.SetContentsMargins(0, 0, 0, 0)
	row.SetSpacing(2)
	row.AddWidget3(c.attach.QWidget, 0, qt.AlignBottom)
	row.AddWidget(c.edit.QWidget)
	fl := qt.NewQVBoxLayout(c.frame.QWidget)
	fl.SetContentsMargins(4, 3, 8, 3)
	fl.SetSpacing(4)
	fl.AddWidget(c.chips)
	fl.AddLayout(row.QLayout)

	c.hint = qt.NewQLabel3("Дождитесь загрузки файлов. Сообщение осталось в поле ввода.")
	c.hint.SetProperty("role", qt.NewQVariant11("hint"))
	c.hint.SetVisible(false)

	lay := qt.NewQVBoxLayout(c.w)
	lay.SetContentsMargins(16, 12, 16, 12)
	lay.SetSpacing(4)
	lay.AddWidget(c.frame.QWidget)
	lay.AddWidget(c.hint.QWidget)

	c.unsubscribe = OnThemeChanged(c.restyle)
	c.restyle()
	c.fitHeight()
	return c
}

func (c *Composer) Widget() *qt.QWidget { return c.w }

func (c *Composer) dispose() { c.unsubscribe() }

func (c *Composer) restyle() {
	c.attach.SetIcon(icon("paperclip", Pal().Tx2, 18))
	c.renderChips()
}

// fitHeight — поле растёт с текстом от одной строки до семи, дальше прокрутка.
func (c *Composer) fitHeight() {
	line := qt.NewQFontMetrics(c.edit.Font()).LineSpacing()
	docH := int(math.Ceil(c.edit.Document().Size().Height()))
	maxH := line*composerMaxLines + 8
	h := min(max(docH, line+8), maxH)
	c.edit.SetFixedHeight(h)
	if docH > maxH {
		c.edit.SetVerticalScrollBarPolicy(qt.ScrollBarAsNeeded)
	} else {
		c.edit.SetVerticalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	}
}

// SetPlaceholder — «Сообщение в # Town Square», «Ответить в треде».
func (c *Composer) SetPlaceholder(text string) {
	c.placeholder = text
	if !c.offline {
		c.edit.SetPlaceholderText(text)
	}
}

func (c *Composer) SetEnabled(on bool) { c.w.SetEnabled(on) }

// SetOffline — нет соединения: поле неактивно и говорит об этом.
func (c *Composer) SetOffline(offline bool) {
	c.offline = offline
	c.frame.SetEnabled(!offline)
	if offline {
		c.edit.SetPlaceholderText("Нет соединения — отправка недоступна")
	} else {
		c.edit.SetPlaceholderText(c.placeholder)
	}
}

func (c *Composer) Focus() { c.edit.SetFocus() }

// SwitchTo сохраняет черновик текущего ключа и показывает черновик key.
func (c *Composer) SwitchTo(key string) {
	if key == c.key {
		return
	}
	if text := c.edit.ToPlainText(); text != "" || len(c.files) > 0 {
		c.drafts[c.key] = &draft{key: c.key, text: text, files: c.files}
	}
	d := c.drafts[key]
	delete(c.drafts, key)
	c.key, c.files = key, nil
	c.edit.Clear()
	c.hint.SetVisible(false)
	if d != nil {
		c.edit.SetPlainText(d.text)
		c.files = d.files
	}
	c.renderChips()
}

// AddFiles прикрепляет файлы с диска к текущему черновику.
func (c *Composer) AddFiles(paths []string) {
	for _, p := range paths {
		var size int64
		if st, err := os.Stat(p); err == nil {
			size = st.Size()
		}
		c.add(&attachment{name: filepath.Base(p), path: p, size: size})
	}
}

// pasteImage прикрепляет картинку из буфера обмена как PNG.
func (c *Composer) pasteImage() bool {
	img := qt.QGuiApplication_Clipboard().Image()
	if img.IsNull() {
		return false
	}
	tmp, err := os.CreateTemp("", "matterqute-paste-*.png")
	if err != nil {
		return false
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if !img.Save2(tmp.Name(), "PNG") {
		return false
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		return false
	}
	c.add(&attachment{name: "Вставка " + time.Now().Format("2006-01-02 15-04-05") + ".png", data: data, size: int64(len(data))})
	return true
}

func (c *Composer) add(a *attachment) {
	c.lastID++
	a.id = c.lastID
	c.files = append(c.files, a)
	c.renderChips()
	c.upload(a)
}

func (c *Composer) upload(a *attachment) {
	a.err, a.percent = nil, 0
	if c.OnUpload == nil {
		return
	}
	id := a.id
	c.OnUpload(a.id, a.name, a.path, a.data, func(percent int) {
		if a := c.find(id); a != nil && a.fileID == "" && a.percent != percent {
			a.percent = percent
			c.renderChips()
		}
	})
}

// Uploaded — файл загружен на сервер; он может быть уже в черновике другого канала.
func (c *Composer) Uploaded(id int, fileID string) {
	if a := c.find(id); a != nil {
		a.fileID, a.percent = fileID, 100
		c.renderChips()
		if !c.uploading() {
			c.hint.SetVisible(false)
		}
	}
}

func (c *Composer) UploadFailed(id int, err error) {
	if a := c.find(id); a != nil {
		a.err = err
		c.renderChips()
	}
}

func (c *Composer) find(id int) *attachment {
	lists := [][]*attachment{c.files}
	for _, d := range c.drafts {
		lists = append(lists, d.files)
	}
	if c.sent != nil {
		lists = append(lists, c.sent.files)
	}
	for _, l := range lists {
		if i := slices.IndexFunc(l, func(a *attachment) bool { return a.id == id }); i >= 0 {
			return l[i]
		}
	}
	return nil
}

func (c *Composer) remove(id int) {
	c.files = slices.DeleteFunc(c.files, func(a *attachment) bool { return a.id == id })
	c.renderChips()
	if !c.uploading() {
		c.hint.SetVisible(false)
	}
}

// renderChips — плашки файлов над полем (QFrame#fileChip, 36 px).
func (c *Composer) renderChips() {
	for _, w := range c.chipWs {
		c.chipsLay.RemoveWidget(w)
		w.DeleteLater()
	}
	c.chipWs = c.chipWs[:0]
	for _, a := range c.files {
		chip := c.chip(a)
		c.chipsLay.InsertWidget(len(c.chipWs), chip) // перед растяжкой
		c.chipWs = append(c.chipWs, chip)
	}
	c.chips.SetVisible(len(c.files) > 0)
}

func (c *Composer) chip(a *attachment) *qt.QWidget {
	p := Pal()
	f := qt.NewQFrame2()
	setName(f.QObject, "fileChip")
	if a.err != nil {
		f.SetProperty("error", qt.NewQVariant11("true"))
		f.SetToolTip(a.err.Error())
	}
	ic := qt.NewQLabel2()
	color := p.Tx2
	if a.err != nil {
		color = p.Dang
	}
	ic.SetPixmap(iconPixmap("file-text", color, 18))
	name := qt.NewQLabel3(a.name)
	name.SetFont(pixelFont(13, qt.QFont__DemiBold))
	meta := qt.NewQLabel2()
	meta.SetTextFormat(qt.RichText)
	switch {
	case a.err != nil:
		meta.SetText(fmt.Sprintf(`<span style="color:%s">не загрузился</span> · <a href="retry" style="color:%s">Повторить</a>`, p.Dang, p.Acc))
		id := a.id
		meta.OnLinkActivated(func(string) {
			if a := c.find(id); a != nil {
				c.upload(a)
				c.renderChips()
			}
		})
	case a.fileID == "":
		meta.SetText(fmt.Sprintf(`<span style="color:%s">загружается · %d%%</span>`, p.Tx2, a.percent))
	default:
		meta.SetText(fmt.Sprintf(`<span style="color:%s">%s</span>`, p.Tx2, formatSize(a.size)))
	}
	meta.SetFont(pixelFont(12, qt.QFont__Normal))
	texts := qt.NewQVBoxLayout2()
	texts.SetSpacing(0)
	texts.AddWidget(name.QWidget)
	texts.AddWidget(meta.QWidget)
	closeBtn := qt.NewQToolButton2()
	closeBtn.SetIcon(icon("x", p.Tx2, 14))
	closeBtn.SetFixedSize2(20, 20)
	closeBtn.SetToolTip("Убрать")
	id := a.id
	closeBtn.OnClicked(func() { c.remove(id) })
	row := qt.NewQHBoxLayout2()
	row.SetContentsMargins(8, 2, 6, 2)
	row.SetSpacing(8)
	row.AddWidget(ic.QWidget)
	row.AddLayout(texts.QLayout)
	row.AddWidget3(closeBtn.QWidget, 0, qt.AlignTop)
	col := qt.NewQVBoxLayout(f.QWidget)
	col.SetContentsMargins(0, 0, 0, 0)
	col.SetSpacing(0)
	col.AddLayout(row.QLayout)
	if a.fileID == "" && a.err == nil {
		bar := qt.NewQProgressBar2()
		setName(bar.QObject, "fileProgress")
		bar.SetTextVisible(false)
		bar.SetRange(0, 100)
		bar.SetValue(a.percent)
		bar.SetFixedHeight(2)
		col.AddWidget(bar.QWidget)
	}
	return f.QWidget
}

func (c *Composer) uploading() bool {
	return slices.ContainsFunc(c.files, func(a *attachment) bool { return a.fileID == "" && a.err == nil })
}

func (c *Composer) send() {
	if c.OnSend == nil {
		return
	}
	if c.uploading() {
		c.hint.SetVisible(true)
		return
	}
	text := strings.TrimSpace(c.edit.ToPlainText())
	var ids []string
	var sent []*attachment
	for _, a := range c.files {
		if a.fileID != "" {
			ids = append(ids, a.fileID)
			sent = append(sent, a)
		}
	}
	if text == "" && len(ids) == 0 {
		return
	}
	c.sent = &draft{key: c.key, text: text, files: sent}
	c.edit.Clear()
	c.files = slices.DeleteFunc(c.files, func(a *attachment) bool { return a.fileID != "" })
	c.renderChips()
	c.hint.SetVisible(false)
	c.OnSend(text, ids)
}

// Restore возвращает неотправленное в черновик того канала (треда), откуда
// отправляли, — даже если пользователь уже ушёл в другой.
func (c *Composer) Restore() {
	s := c.sent
	c.sent = nil
	if s == nil {
		return
	}
	if s.key != c.key {
		d := c.drafts[s.key]
		if d == nil {
			d = &draft{key: s.key}
			c.drafts[s.key] = d
		}
		if strings.TrimSpace(d.text) == "" {
			d.text = s.text
		}
		d.files = append(s.files, d.files...)
		return
	}
	if strings.TrimSpace(c.edit.ToPlainText()) == "" {
		c.edit.SetPlainText(s.text)
	}
	c.files = append(s.files, c.files...)
	c.renderChips()
}

// Sent — отправка прошла, возвращать нечего.
func (c *Composer) Sent() { c.sent = nil }

// token — слово под курсором для подсказок и позиция его начала.
func (c *Composer) token() (complete.Token, int, bool) {
	cur := c.edit.TextCursor()
	if cur.HasSelection() {
		return complete.Token{}, 0, false
	}
	pos := cur.Position()
	cur.MovePosition2(qt.QTextCursor__StartOfBlock, qt.QTextCursor__KeepAnchor)
	tok, ok := complete.At(cur.SelectedText())
	return tok, pos - tok.Len, ok
}

// updateSuggest — текст или курсор изменились: открыть, обновить или закрыть список.
func (c *Composer) updateSuggest() {
	if c.Suggest == nil {
		return
	}
	tok, start, ok := c.token()
	if !ok {
		c.dismissed = ""
		c.hideSuggest()
		return
	}
	key := fmt.Sprintf("%d:%d", tok.Kind, start)
	if key == c.dismissed {
		return
	}
	if tok == c.tok && c.popup != nil && c.popup.visible() {
		return
	}
	c.tok = tok
	c.tokGen++
	gen := c.tokGen
	c.Suggest(tok, func(items []Suggestion) {
		if gen != c.tokGen {
			return // уже набрали другое
		}
		c.suggestPopup().show(items, c.frame.QWidget)
	})
}

// suggestPopup — список живёт в окне поля ввода, поверх всего над полем.
func (c *Composer) suggestPopup() *suggestPopup {
	win := c.w.Window()
	if c.popup == nil || c.popup.frame.ParentWidget().UnsafePointer() != win.UnsafePointer() {
		if c.popup != nil {
			c.popup.frame.DeleteLater()
		}
		c.popup = newSuggestPopup(win)
		c.popup.onPick = c.acceptSuggestion
	}
	return c.popup
}

func (c *Composer) hideSuggest() {
	c.tokGen++
	c.tok = complete.Token{}
	if c.popup != nil {
		c.popup.hide()
	}
}

// suggestKey — клавиши, пока открыт список: стрелки, Enter и Tab — выбрать, Esc — закрыть.
func (c *Composer) suggestKey(k qt.Key) bool {
	if c.popup == nil || !c.popup.visible() {
		return false
	}
	switch k {
	case qt.Key_Up:
		c.popup.move(-1)
	case qt.Key_Down:
		c.popup.move(1)
	case qt.Key_Return, qt.Key_Enter, qt.Key_Tab:
		s, ok := c.popup.current()
		if !ok {
			return false
		}
		c.acceptSuggestion(s)
	case qt.Key_Escape:
		if tok, start, ok := c.token(); ok {
			c.dismissed = fmt.Sprintf("%d:%d", tok.Kind, start)
		}
		c.hideSuggest()
	default:
		return false
	}
	return true
}

// acceptSuggestion заменяет набранное слово выбранным.
func (c *Composer) acceptSuggestion(s Suggestion) {
	tok, _, ok := c.token()
	c.hideSuggest()
	if !ok {
		return
	}
	cur := c.edit.TextCursor()
	cur.MovePosition3(qt.QTextCursor__PreviousCharacter, qt.QTextCursor__KeepAnchor, tok.Len)
	cur.InsertText(s.Insert)
	c.edit.SetTextCursor(cur)
	c.edit.SetFocus()
}
