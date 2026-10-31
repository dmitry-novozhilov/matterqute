package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
)

// attachment — файл в поле ввода: грузится, загружен (fileID) или не загрузился.
type attachment struct {
	id     int // локальный, уникален в пределах Composer
	name   string
	fileID string
	err    error
}

// draft — недописанное сообщение канала или треда.
type draft struct {
	key   string
	text  string
	files []*attachment
}

// Composer — поле ввода: Enter отправляет, Shift+Enter переносит строку;
// файлы — кнопкой 📎, перетаскиванием или вставкой картинки из буфера.
// Черновики хранятся по ключу (канал, тред): SwitchTo переключает.
type Composer struct {
	w        *qt.QWidget
	edit     *qt.QPlainTextEdit
	attach   *qt.QPushButton
	chips    *qt.QWidget
	chipsLay *qt.QHBoxLayout
	chipBtns []*qt.QPushButton

	key    string
	files  []*attachment
	drafts map[string]*draft
	lastID int
	sent   *draft // отправляется: вернуть в черновик, если отправка не удалась

	OnSend func(text string, fileIDs []string)
	// OnUpload — загрузить файл (path с диска или data — вставленная картинка);
	// результат сообщить через Uploaded или UploadFailed.
	OnUpload func(id int, name, path string, data []byte)
}

func NewComposer() *Composer {
	c := &Composer{w: qt.NewQWidget2(), edit: qt.NewQPlainTextEdit2(), drafts: map[string]*draft{}}
	c.edit.SetPlaceholderText("Сообщение… Enter — отправить, Shift+Enter — новая строка")
	c.edit.SetFixedHeight(64)
	c.edit.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		k := qt.Key(e.Key())
		if (k == qt.Key_Return || k == qt.Key_Enter) && e.Modifiers()&qt.ShiftModifier == 0 {
			c.send()
			return
		}
		super(e)
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

	c.attach = qt.NewQPushButton3("📎")
	c.attach.SetToolTip("Прикрепить файлы")
	c.attach.SetFixedSize2(32, 32)
	c.attach.OnClicked(func() {
		c.AddFiles(qt.QFileDialog_GetOpenFileNames2(c.w, "Прикрепить файлы"))
	})

	c.chips = qt.NewQWidget2()
	c.chipsLay = qt.NewQHBoxLayout(c.chips)
	c.chipsLay.SetContentsMargins(0, 0, 0, 0)
	c.chipsLay.AddStretch()
	c.chips.SetVisible(false)

	row := qt.NewQHBoxLayout2()
	row.SetContentsMargins(0, 0, 0, 0)
	row.AddWidget3(c.attach.QWidget, 0, qt.AlignTop)
	row.AddWidget(c.edit.QWidget)
	lay := qt.NewQVBoxLayout(c.w)
	lay.SetContentsMargins(4, 2, 0, 0)
	lay.SetSpacing(2)
	lay.AddWidget(c.chips)
	lay.AddLayout(row.QLayout)
	return c
}

func (c *Composer) Widget() *qt.QWidget { return c.w }

func (c *Composer) SetEnabled(on bool) { c.w.SetEnabled(on) }

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
	if d != nil {
		c.edit.SetPlainText(d.text)
		c.files = d.files
	}
	c.renderChips()
}

// AddFiles прикрепляет файлы с диска к текущему черновику.
func (c *Composer) AddFiles(paths []string) {
	for _, p := range paths {
		c.add(filepath.Base(p), p, nil)
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
	c.add("Вставка "+time.Now().Format("2006-01-02 15-04-05")+".png", "", data)
	return true
}

func (c *Composer) add(name, path string, data []byte) {
	c.lastID++
	a := &attachment{id: c.lastID, name: name}
	c.files = append(c.files, a)
	c.renderChips()
	if c.OnUpload != nil {
		c.OnUpload(a.id, name, path, data)
	}
}

// Uploaded — файл загружен на сервер; он может быть уже в черновике другого канала.
func (c *Composer) Uploaded(id int, fileID string) {
	if a := c.find(id); a != nil {
		a.fileID = fileID
		c.renderChips()
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
}

func (c *Composer) renderChips() {
	for _, b := range c.chipBtns {
		c.chipsLay.RemoveWidget(b.QWidget)
		b.DeleteLater()
	}
	c.chipBtns = c.chipBtns[:0]
	for _, a := range c.files {
		var text, tip string
		switch {
		case a.err != nil:
			text, tip = "⚠ "+a.name+"  ✕", a.err.Error()
		case a.fileID == "":
			text, tip = "⏳ "+a.name, "загружается"
		default:
			text, tip = "📎 "+a.name+"  ✕", "убрать"
		}
		b := qt.NewQPushButton3(text)
		b.SetFlat(true)
		b.SetToolTip(tip)
		id := a.id
		b.OnClicked(func() { c.remove(id) })
		c.chipsLay.InsertWidget(len(c.chipBtns), b.QWidget) // перед растяжкой
		c.chipBtns = append(c.chipBtns, b)
	}
	c.chips.SetVisible(len(c.files) > 0)
}

func (c *Composer) uploading() bool {
	return slices.ContainsFunc(c.files, func(a *attachment) bool { return a.fileID == "" && a.err == nil })
}

func (c *Composer) send() {
	if c.OnSend == nil {
		return
	}
	if c.uploading() {
		c.edit.SetPlaceholderText("Дождитесь загрузки файлов…")
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
	c.edit.SetPlaceholderText("Сообщение… Enter — отправить, Shift+Enter — новая строка")
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
