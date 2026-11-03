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
	"matterqute/internal/emoji"
	"matterqute/internal/markdown"
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

// draft — недописанное сообщение канала или треда: абзацы с оформлением.
type draft struct {
	key      string
	blocks   []markdown.Block
	files    []*attachment
	priority string // метка «Важно»/«Срочно» (priority.go)
}

// Строк в поле ввода, после которых появляется прокрутка.
const composerMaxLines = 7

// Composer — поле ввода (QFrame#composer) с оформлением (richedit.go): Enter
// отправляет, Shift+Enter — новая строка (в списке — пункт), в блоке кода
// Enter — новая строка, Ctrl+Enter — отправить; файлы — кнопкой 📎,
// перетаскиванием или вставкой картинки. Черновики хранятся по ключу (канал,
// тред): SwitchTo переключает.
type Composer struct {
	w                *qt.QWidget // обёртка с полями вокруг рамки
	frame            *qt.QFrame
	edit             *qt.QTextEdit
	spell            *qt.QSyntaxHighlighter // проверка орфографии (spellcheck.go)
	unsubscribeSpell func()
	attach           *qt.QToolButton
	fmtBar           *qt.QWidget     // панель оформления над полем
	fmtShow          *qt.QToolButton // «T» — показать или спрятать панель
	acts             formatActions
	styleKey         string // тема и шрифты, которыми нарисован документ поля
	typing           bool   // идёт набор с клавиатуры: курсор сдвинул ввод, а не переход

	editID   string     // правится своё сообщение с этим id; пусто — пишется новое
	editOrig string     // текст правимого сообщения, каким его загрузили в поле
	stash    *draft     // черновик, отложенный на время правки
	editBar  *qt.QFrame // «Редактирование сообщения» над полем
	editText *qt.QLabel
	chips    *qt.QWidget
	chipsLay *qt.QHBoxLayout
	chipWs   []*qt.QWidget
	hint     *qt.QLabel

	placeholder string
	offline     bool
	archived    bool // канал в архиве: только чтение
	key         string
	files       []*attachment
	drafts      map[string]*draft
	lastID      int
	unsubscribe func()

	// Suggest — подсказки для слова под курсором (@, #, ~, :); show можно
	// звать несколько раз (сначала известное, потом с сервера). nil — без подсказок.
	Suggest   func(tok complete.Token, show func([]Suggestion))
	popup     *suggestPopup
	tok       complete.Token // слово, для которого показан список
	tokGen    int            // ответы для прежних слов отбрасываются
	dismissed string         // Esc: для этого слова список больше не открывать

	// OnSend — отправить; priority — метка сообщения (priority.go), "" — без неё.
	// OnSend — отправить; d — что ушло из поля: не вышло — Restore(d).
	OnSend func(text string, fileIDs []string, priority string, d *draft)
	// OnEdit — сохранить правку сообщения id (text пустой — сообщение стёрли).
	OnEdit func(id, text string, blocks []markdown.Block)
	// OnEditLast — ↑ в пустом поле: править последнее своё сообщение.
	OnEditLast func()
	// OnCommand — сообщение — слэш-команда («/away»): выполнить на сервере.
	// Не вышло — Restore; команды нет — OfferAsText.
	OnCommand func(text string, d *draft)
	// OnLater — «Отправить позже»: выбрать время (потом — ScheduleAt).
	OnLater func()
	// OnSchedule — отложить отправку того, что было в поле, на at.
	OnSchedule func(text string, fileIDs []string, at time.Time, d *draft)
	// OnShowScheduled — «показать» в подсказке об отложенных.
	OnShowScheduled func()
	// OnEmoji — кнопка эмодзи: окно выбора у точки at (глобальной), выбор — InsertEmoji.
	OnEmoji  func(at *qt.QPoint)
	emojiBtn *qt.QToolButton
	later           *qt.QToolButton // «Отправить позже»
	prioBtn         *qt.QToolButton // метка «Важно»/«Срочно»
	prioNote        *qt.QLabel      // метка над текстом
	priority        string          // метка следующего сообщения
	priorities      bool            // сервер разрешает метки (только поле канала)
	// OnDraft — черновик key поменялся (набор, вставка, ушли из канала):
	// сохранить (drafts.go). Правка своего сообщения — не черновик.
	OnDraft func(key string)
	// OnTyping — набирают текст (не правка своего): сообщить серверу.
	OnTyping  func()
	schedNote *qt.QLabel // «2 отложенных сообщения здесь · показать»
	asText    string     // это сообщение — уже не команда: сервер такой не знает, отправить текстом
	// OnUpload — загрузить файл (path с диска или data — вставленная картинка);
	// progress(0…100) по ходу, результат — Uploaded или UploadFailed.
	OnUpload func(id int, name, path string, data []byte, progress func(percent int))
}

func NewComposer() *Composer {
	c := &Composer{w: qt.NewQWidget2(), frame: qt.NewQFrame2(), edit: qt.NewQTextEdit2(), drafts: map[string]*draft{}}
	setName(c.frame.QObject, "composer")
	setName(c.edit.QObject, "composerEdit")
	c.edit.SetAcceptRichText(false) // вставка — простым текстом: чужое оформление в markdown не переложить
	c.edit.SetFrameShape(qt.QFrame__NoFrame)
	c.edit.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	c.edit.SetToolTip("Enter — отправить, Shift+Enter — новая строка")
	c.edit.OnCurrentCharFormatChanged(func(*qt.QTextCharFormat) { c.syncFormat() })
	c.edit.OnCursorPositionChanged(func() {
		if !c.typing {
			c.leaveCodeAndLink()
		}
		c.syncFormat()
	})
	c.edit.OnPaintEvent(func(super func(*qt.QPaintEvent), e *qt.QPaintEvent) {
		super(e)
		c.paintQuotes()
	})
	c.edit.Document().SetDocumentMargin(4)
	c.edit.Document().SetIndentWidth(22) // уровень списка; у Qt по умолчанию 40
	c.edit.Document().OnContentsChanged(func() {
		c.fitHeight()
		c.updatePlaceholder()
	})
	c.edit.OnTextChanged(c.updateSuggest)
	c.edit.OnTextChanged(func() {
		if c.asText != "" && strings.TrimSpace(markdown.FromBlocks(readBlocks(c.edit.Document()))) != c.asText {
			c.asText = "" // текст поменяли — снова команда
			c.hint.SetVisible(false)
		}
		if c.editID == "" && c.OnDraft != nil {
			c.OnDraft(c.key)
		}
	})
	c.edit.OnCursorPositionChanged(c.updateSuggest)
	c.edit.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		k := qt.Key(e.Key())
		if c.suggestKey(k) {
			return
		}
		mods := e.Modifiers()
		switch {
		case k == qt.Key_Escape && c.editID != "":
			c.CancelEdit()
			return
		case k == qt.Key_Up && mods == qt.NoModifier && c.editID == "" && c.empty() && len(c.files) == 0 && c.OnEditLast != nil:
			c.OnEditLast()
			return
		case (k == qt.Key_Return || k == qt.Key_Enter) && mods&qt.ControlModifier != 0 && mods&qt.ShiftModifier != 0:
			if c.editID == "" && c.OnLater != nil {
				c.OnLater()
			}
			return
		case k == qt.Key_Return || k == qt.Key_Enter:
			switch {
			case mods&qt.ControlModifier != 0:
				c.send()
			case mods&qt.ShiftModifier != 0 || blockKind(c.edit.TextCursor().Block()) == markdown.Code:
				c.newLine()
			default:
				c.send()
			}
			return
		case k == qt.Key_Tab && c.indentList(1), k == qt.Key_Backtab && c.indentList(-1):
			return
		case k == qt.Key_Backspace && mods == qt.NoModifier && c.backspaceAtStart():
			return
		case mods&(qt.ControlModifier|qt.ShiftModifier|qt.AltModifier) == qt.ControlModifier|qt.ShiftModifier && c.blockShortcut(e):
			return
		}
		c.typing = e.Text() != ""
		super(e)
		c.typing = false
		if c.editID == "" && c.OnTyping != nil && (e.Text() != "" || k == qt.Key_Backspace || k == qt.Key_Delete) {
			c.OnTyping()
		}
		c.autoformat(e.Text())
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
		if m.HasText() {
			c.pasteText(m.Text())
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

	c.buildFormatBar()
	c.buildPriority()

	row := qt.NewQHBoxLayout2()
	row.SetContentsMargins(0, 0, 0, 0)
	row.SetSpacing(2)
	row.AddWidget3(c.attach.QWidget, 0, qt.AlignBottom)
	row.AddWidget(c.edit.QWidget)
	c.later = qt.NewQToolButton2()
	setName(c.later.QObject, "later")
	c.later.SetFixedSize2(32, 32)
	c.later.SetIconSize(qt.NewQSize2(18, 18))
	c.later.SetFocusPolicy(qt.NoFocus)
	c.later.SetToolTip("Отправить позже (Ctrl+Shift+Enter)")
	c.later.OnClicked(func() {
		if c.editID == "" && c.OnLater != nil {
			c.OnLater()
		}
	})
	c.emojiBtn = qt.NewQToolButton2()
	setName(c.emojiBtn.QObject, "emoji")
	c.emojiBtn.SetFixedSize2(32, 32)
	c.emojiBtn.SetIconSize(qt.NewQSize2(18, 18))
	c.emojiBtn.SetFocusPolicy(qt.NoFocus)
	c.emojiBtn.SetToolTip("Эмодзи")
	c.emojiBtn.OnClicked(func() {
		if c.OnEmoji != nil {
			c.OnEmoji(c.emojiBtn.MapToGlobalWithQPoint(qt.NewQPoint2(0, 0)))
		}
	})
	row.AddWidget3(c.emojiBtn.QWidget, 0, qt.AlignBottom)
	row.AddWidget3(c.prioBtn.QWidget, 0, qt.AlignBottom)
	row.AddWidget3(c.later.QWidget, 0, qt.AlignBottom)
	row.AddWidget3(c.fmtShow.QWidget, 0, qt.AlignBottom)
	fl := qt.NewQVBoxLayout(c.frame.QWidget)
	fl.SetContentsMargins(4, 3, 4, 3)
	fl.SetSpacing(4)
	fl.AddWidget(c.prioNote.QWidget)
	fl.AddWidget(c.chips)
	fl.AddWidget(c.fmtBar)
	fl.AddLayout(row.QLayout)

	c.hint = qt.NewQLabel3("Дождитесь загрузки файлов. Сообщение осталось в поле ввода.")
	c.hint.SetProperty("role", qt.NewQVariant11("hint"))
	c.hint.SetVisible(false)

	c.buildEditBar()

	lay := qt.NewQVBoxLayout(c.w)
	lay.SetContentsMargins(16, 12, 16, 12)
	lay.SetSpacing(4)
	c.schedNote = qt.NewQLabel2()
	c.schedNote.SetTextFormat(qt.RichText)
	c.schedNote.SetProperty("role", qt.NewQVariant11("hint"))
	c.schedNote.OnLinkActivated(func(string) {
		if c.OnShowScheduled != nil {
			c.OnShowScheduled()
		}
	})
	c.schedNote.SetVisible(false)
	lay.AddWidget(c.schedNote.QWidget)
	lay.AddWidget(c.editBar.QWidget)
	lay.AddWidget(c.frame.QWidget)
	lay.AddWidget(c.hint.QWidget)

	c.unsubscribe = OnThemeChanged(c.restyle)
	c.setupSpelling()
	c.restyle()
	c.fitHeight()
	return c
}

func (c *Composer) Widget() *qt.QWidget { return c.w }

func (c *Composer) dispose() {
	c.unsubscribe()
	c.unsubscribeSpell()
}

func (c *Composer) restyle() {
	c.attach.SetIcon(icon("paperclip", Pal().Tx2, 18))
	c.later.SetIcon(icon("clock", Pal().Tx2, 18))
	c.emojiBtn.SetIcon(icon("smile", Pal().Tx2, 18))
	c.updatePriority()
	c.restyleEditBar()
	c.edit.SetFont(feedFont())
	c.edit.Viewport().SetFont(feedFont()) // плейсхолдер Qt рисует шрифтом viewport
	c.restyleFormatBar()
	// Цвета и шрифты оформления записаны в документ — перерисовать его,
	// если сменились тема или шрифты (история правок при этом сбрасывается).
	if key := Pal().Surf2 + Pal().Acc + monoFamily(); key != c.styleKey {
		c.styleKey = key
		if !c.empty() {
			pos := c.edit.TextCursor().Position()
			c.setBlocks(readBlocks(c.edit.Document()))
			cur := c.edit.TextCursor()
			cur.SetPosition(min(pos, c.edit.Document().CharacterCount()-1))
			c.edit.SetTextCursor(cur)
		}
	}
	c.fitHeight()
	c.renderChips()
}

// empty — в поле нет текста (пустые пункты и строки не в счёт).
func (c *Composer) empty() bool { return strings.TrimSpace(c.edit.ToPlainText()) == "" }

// setBlocks — поле с абзацами blocks, курсор в конце.
func (c *Composer) setBlocks(blocks []markdown.Block) {
	c.edit.Clear()
	cur := c.edit.TextCursor()
	writeBlocks(cur, blocks)
	c.edit.SetTextCursor(cur)
	c.leaveCodeAndLink()
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
	c.updatePlaceholder()
}

// updatePlaceholder — подсказка в пустом поле. Qt рисует её, пока в документе
// нет текста, — и поверх пустого пункта списка («1.»), цитаты или блока кода;
// тогда её убираем.
func (c *Composer) updatePlaceholder() {
	text := c.placeholder
	switch {
	case c.archived:
		text = "Канал в архиве — новые сообщения сюда не отправить"
	case c.offline:
		text = "Нет связи — сообщение уйдёт, когда она появится"
	}
	if doc := c.edit.Document(); doc.BlockCount() == 1 && blockKind(doc.FirstBlock()) != markdown.Para {
		text = ""
	}
	if c.edit.PlaceholderText() != text {
		c.edit.SetPlaceholderText(text)
	}
}

func (c *Composer) SetEnabled(on bool) { c.w.SetEnabled(on) }

// SetOffline — нет соединения: писать можно, сообщение уйдёт, когда связь
// появится (очередь отправки, outbox.go); подсказка говорит об этом.
func (c *Composer) SetOffline(offline bool) {
	c.offline = offline
	c.updatePlaceholder()
}

// SetArchived — канал в архиве: писать нельзя, поле неактивно и говорит об этом.
func (c *Composer) SetArchived(archived bool) {
	c.archived = archived
	c.frame.SetEnabled(!c.archived)
	c.updatePlaceholder()
}

func (c *Composer) Focus() { c.edit.SetFocus() }

// SwitchTo сохраняет черновик текущего ключа и показывает черновик key.
func (c *Composer) SwitchTo(key string) {
	if key == c.key {
		return
	}
	c.CancelEdit()
	if !c.empty() || len(c.files) > 0 || c.priority != "" {
		c.drafts[c.key] = &draft{key: c.key, blocks: readBlocks(c.edit.Document()), files: c.files, priority: c.priority}
	}
	d := c.drafts[key]
	delete(c.drafts, key)
	old := c.key
	c.key, c.files = key, nil
	c.setBlocks(nil)
	c.hint.SetVisible(false)
	c.setPriority("")
	if d != nil {
		c.setBlocks(d.blocks)
		c.files = d.files
		if c.priorities {
			c.setPriority(d.priority)
		}
	}
	c.renderChips()
	if c.OnDraft != nil && old != "" {
		c.OnDraft(old) // черновик прежнего — окончательный
	}
}

// DraftText — черновик key markdown'ом ("" — нет): в поле, отложенный на
// время правки или запасённый для другого канала. Файлы — не в нём.
func (c *Composer) DraftText(key string) string {
	var blocks []markdown.Block
	switch {
	case key == c.key && c.editID == "":
		blocks = readBlocks(c.edit.Document())
	case key == c.key:
		if c.stash != nil {
			blocks = c.stash.blocks
		}
	default:
		if d := c.drafts[key]; d != nil {
			blocks = d.blocks
		}
	}
	return strings.TrimSpace(markdown.FromBlocks(blocks))
}

// SetDraft — черновик key пришёл с сервера или из файла: в поле, если key
// открыт (идёт правка — после неё), иначе — в запас; "" — черновика нет.
func (c *Composer) SetDraft(key, text string) {
	var blocks []markdown.Block
	if strings.TrimSpace(text) != "" {
		blocks = markdown.ToBlocks(text)
	}
	switch d := c.drafts[key]; {
	case key == c.key && c.editID != "":
		if c.stash != nil {
			c.stash.blocks = blocks
		}
	case key == c.key:
		c.setBlocks(blocks)
	case blocks != nil:
		if d == nil {
			d = &draft{key: key}
			c.drafts[key] = d
		}
		d.blocks = blocks
	case d != nil:
		d.blocks = nil
		if len(d.files) == 0 {
			delete(c.drafts, key)
		}
	}
}

// DraftNonEmpty — у key есть черновик (у открытого — что-то в поле; идёт
// правка — отложенный под неё). Без разбора текста: зовут на каждую правку.
func (c *Composer) DraftNonEmpty(key string) bool {
	if key == c.key {
		if c.editID != "" {
			return c.stash != nil && (!blocksEmpty(c.stash.blocks) || len(c.stash.files) > 0)
		}
		return !c.empty() || len(c.files) > 0
	}
	return c.HasDraft(key)
}

// DraftFiles — у черновика key есть файлы.
func (c *Composer) DraftFiles(key string) bool {
	if key == c.key && c.editID == "" {
		return len(c.files) > 0
	}
	d := c.drafts[key]
	return d != nil && len(d.files) > 0
}

// ClearDraft — черновик key прочь: открыт — поле очищается (идёт правка —
// отложенный под неё), иначе — из запаса.
func (c *Composer) ClearDraft(key string) {
	switch {
	case key == c.key && c.editID != "":
		c.stash = nil
	case key == c.key:
		c.setBlocks(nil)
		c.files = nil
		c.renderChips()
	default:
		delete(c.drafts, key)
	}
}

// HasDraft — у key есть черновик, а key не открыт (карандаш в сайдбаре).
func (c *Composer) HasDraft(key string) bool {
	d := c.drafts[key]
	return key != c.key && d != nil && (!blocksEmpty(d.blocks) || len(d.files) > 0)
}

// DraftKeys — ключи, у которых что-то есть в поле или в запасе.
func (c *Composer) DraftKeys() []string {
	keys := make([]string, 0, len(c.drafts)+1)
	for k := range c.drafts {
		keys = append(keys, k)
	}
	if c.key != "" {
		keys = append(keys, c.key)
	}
	return keys
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
	// В отправленном (commit) незагруженных файлов нет: pending их не пропускает.
	lists := [][]*attachment{c.files}
	for _, d := range c.drafts {
		lists = append(lists, d.files)
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
	if c.editID != "" {
		blocks := readBlocks(c.edit.Document())
		text := strings.TrimSpace(markdown.FromBlocks(blocks))
		if !c.edit.Document().IsModified() && text == c.editOrig {
			// Ничего не меняли — нечего и сохранять: разметка, которой редактор
			// не умеет (заголовок ссылки и т. п.), при пересборке текста изменилась бы.
			c.CancelEdit()
			return
		}
		id := c.editID
		c.CancelEdit()
		if c.OnEdit != nil {
			c.OnEdit(id, text, blocks)
		}
		return
	}
	if c.OnSend == nil {
		return
	}
	text, ids, blocks, files, ok := c.pending()
	if !ok {
		return
	}
	if strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "//") && len(ids) == 0 && c.OnCommand != nil && text != c.asText {
		d := &draft{key: c.key, blocks: blocks}
		c.setBlocks(nil)
		c.hint.SetVisible(false)
		c.OnCommand(text, d)
		return
	}
	c.asText = ""
	priority := c.priority
	c.OnSend(text, ids, priority, c.commit(blocks, files))
}

// pending — что отправить: текст (markdown Mattermost) и загруженные файлы.
// ok=false — нечего или файлы ещё грузятся (тогда подсказка под полем).
func (c *Composer) pending() (text string, ids []string, blocks []markdown.Block, files []*attachment, ok bool) {
	if c.uploading() {
		c.ShowHint("Дождитесь загрузки файлов. Сообщение осталось в поле ввода.")
		return "", nil, nil, nil, false
	}
	blocks = readBlocks(c.edit.Document())
	text = strings.TrimSpace(markdown.FromBlocks(blocks))
	for _, a := range c.files {
		if a.fileID != "" {
			ids = append(ids, a.fileID)
			files = append(files, a)
		}
	}
	return text, ids, blocks, files, text != "" || len(ids) > 0
}

// commit — отправляемое ушло из поля: поле очистить, а ушедшее отдать —
// для Restore, если отправка не удастся.
func (c *Composer) commit(blocks []markdown.Block, files []*attachment) *draft {
	d := &draft{key: c.key, blocks: blocks, files: files, priority: c.priority}
	c.setPriority("")
	c.setBlocks(nil)
	c.files = slices.DeleteFunc(c.files, func(a *attachment) bool { return a.fileID != "" })
	c.renderChips()
	c.hint.SetVisible(false)
	return d
}

// ScheduleAt — «Отправить позже» на время at: то, что в поле, уходит в
// OnSchedule (не вышло — Restore). false — нечего откладывать.
func (c *Composer) ScheduleAt(at time.Time) bool {
	if c.editID != "" || c.OnSchedule == nil {
		return false
	}
	text, ids, blocks, files, ok := c.pending()
	if !ok {
		return false
	}
	c.OnSchedule(text, ids, at, c.commit(blocks, files))
	return true
}


// SetScheduledNote — «N отложенных» над полем (n=0 — прячется).
func (c *Composer) SetScheduledNote(n int) {
	c.schedNote.SetVisible(n > 0)
	if n > 0 {
		c.schedNote.SetText(fmt.Sprintf(`%s здесь · <a href="show" style="color:%s">показать</a>`,
			plural(int64(n), "отложенное сообщение", "отложенных сообщения", "отложенных сообщений"), Pal().Acc))
	}
}

// Restore возвращает неотправленное s (из commit) в черновик того канала
// (треда), откуда отправляли, — даже если пользователь уже ушёл в другой.
// Если там уже что-то набрали (или идёт правка — тогда в отложенный
// черновик), неотправленное встаёт перед набранным: текст не теряется.
func (c *Composer) Restore(s *draft) {
	if s == nil {
		return
	}
	if s.key != c.key || c.editID != "" {
		d := c.drafts[s.key]
		if c.editID != "" && s.key == c.key {
			if c.stash == nil {
				c.stash = &draft{key: c.key}
			}
			d = c.stash
		} else if d == nil {
			d = &draft{key: s.key}
			c.drafts[s.key] = d
		}
		d.blocks = joinBlocks(s.blocks, d.blocks)
		if d.priority == "" {
			d.priority = s.priority
		}
		d.files = append(s.files, d.files...)
		return
	}
	if !blocksEmpty(s.blocks) {
		c.setBlocks(joinBlocks(s.blocks, readBlocks(c.edit.Document())))
	}
	if c.priority == "" {
		c.setPriority(s.priority)
	}
	c.files = append(s.files, c.files...)
	c.renderChips()
}

// joinBlocks — first, пустая строка, then; пустое с любой стороны — другое как есть.
func joinBlocks(first, then []markdown.Block) []markdown.Block {
	switch {
	case blocksEmpty(then):
		return first
	case blocksEmpty(first):
		return then
	}
	return append(append(slices.Clone(first), markdown.Block{}), then...)
}

// token — слово под курсором для подсказок и позиция его начала.
func (c *Composer) token() (complete.Token, int, bool) {
	cur := c.edit.TextCursor()
	if cur.HasSelection() {
		return complete.Token{}, 0, false
	}
	pos := cur.Position()
	first := cur.Block().Position() == 0
	cur.MovePosition2(qt.QTextCursor__StartOfBlock, qt.QTextCursor__KeepAnchor)
	before := cur.SelectedText()
	if tok, ok := complete.CommandAt(before); ok && first {
		return tok, pos - tok.Len, true
	}
	tok, ok := complete.At(before)
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

// InsertEmoji — эмодзи в текст у курсора: системное — символом (в поле оно
// видно цветным), эмодзи сервера — «:имя: », как из подсказки по «:».
func (c *Composer) InsertEmoji(name string) {
	text := ":" + name + ": "
	if ch, ok := emoji.Char(name); ok {
		text = ch
	}
	cur := c.edit.TextCursor()
	cur.InsertText(text)
	c.edit.SetTextCursor(cur)
	c.edit.SetFocus()
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

// buildEditBar — плашка над полем на время правки: что правим и «отменить».
func (c *Composer) buildEditBar() {
	c.editBar = qt.NewQFrame2()
	setName(c.editBar.QObject, "editBar")
	row := qt.NewQHBoxLayout(c.editBar.QWidget)
	row.SetContentsMargins(10, 4, 4, 4)
	row.SetSpacing(8)
	pencil := qt.NewQLabel2()
	setName(pencil.QObject, "editIcon")
	c.editText = qt.NewQLabel2()
	c.editText.SetTextFormat(qt.PlainText)
	setName(c.editText.QObject, "editText")
	cancel := qt.NewQToolButton2()
	setName(cancel.QObject, "editCancel")
	cancel.SetFixedSize2(24, 24)
	cancel.SetIconSize(qt.NewQSize2(14, 14))
	cancel.SetFocusPolicy(qt.NoFocus)
	cancel.SetToolTip("Отменить правку (Esc)")
	cancel.OnClicked(func() {
		c.CancelEdit()
		c.edit.SetFocus()
	})
	row.AddWidget(pencil.QWidget)
	row.AddWidget2(c.editText.QWidget, 1)
	row.AddWidget(cancel.QWidget)
	c.editBar.Hide()
}

func (c *Composer) restyleEditBar() {
	pal := Pal()
	for _, o := range c.editBar.Children() {
		switch {
		case o.ObjectName() == "editIcon":
			qt.UnsafeNewQLabel(o.UnsafePointer()).SetPixmap(iconPixmap("pencil", pal.Acc, 14))
		case o.ObjectName() == "editCancel":
			qt.UnsafeNewQToolButton(o.UnsafePointer()).SetIcon(icon("x", pal.Tx2, 14))
		}
	}
	c.editBar.SetStyleSheet(fmt.Sprintf(`QFrame#editBar { background: %[1]s; border-left: 3px solid %[2]s; border-radius: 4px; }
QLabel#editText { color: %[3]s; }
QToolButton { border: none; border-radius: 4px; background: transparent; min-width: 0; min-height: 0; }
QToolButton:hover { background: %[4]s; }`, pal.Surf2, pal.Acc, pal.Tx2, pal.Hover))
}

// StartEdit — править своё сообщение id в поле (preview — его начало для
// плашки): черновик откладывается до конца правки, Enter сохраняет, Esc и
// крестик отменяют.
func (c *Composer) StartEdit(id, preview string, blocks []markdown.Block) {
	c.StartEditWith(id, "Редактирование: "+preview, blocks)
}

// StartEditWith — правка с подписью label на плашке (отложенное — со временем).
func (c *Composer) StartEditWith(id, label string, blocks []markdown.Block) {
	if c.editID == "" {
		c.stash = &draft{key: c.key, blocks: readBlocks(c.edit.Document()), files: c.files}
		c.files = nil
		c.renderChips()
	}
	c.editID, c.editOrig = id, strings.TrimSpace(markdown.FromBlocks(blocks))
	c.setBlocks(blocks)
	c.edit.Document().SetModifiedWithBool(false) // Enter без правок ничего не отправит
	c.editText.SetText(label)
	c.editBar.Show()
	c.attach.SetEnabled(false)
	c.hint.SetVisible(false)
	c.updatePriority()
	c.edit.SetFocus()
}

// CancelEdit — конец правки: в поле — отложенный черновик.
func (c *Composer) CancelEdit() {
	if c.editID == "" {
		return
	}
	c.editID = ""
	c.editBar.Hide()
	c.attach.SetEnabled(true)
	c.updatePriority()
	st := c.stash
	c.stash = nil
	c.setBlocks(nil)
	c.files = nil
	if st != nil {
		c.setBlocks(st.blocks)
		c.files = st.files
	}
	c.renderChips()
}

// ShowHint — строка под полем: почему не отправилось и что делать.
func (c *Composer) ShowHint(text string) {
	c.hint.SetText(text)
	c.hint.SetVisible(true)
}

// OfferAsText — команды text на сервере нет: она снова в поле, и следующий
// Enter отправит её обычным сообщением.
func (c *Composer) OfferAsText(text string) {
	c.asText = text
	cmd, _, _ := strings.Cut(text, " ")
	c.ShowHint("Команды «" + cmd + "» на сервере нет. Enter ещё раз — отправить это сообщением.")
}

// Editing — id сообщения, которое правится; пусто — пишется новое.
func (c *Composer) Editing() string { return c.editID }
