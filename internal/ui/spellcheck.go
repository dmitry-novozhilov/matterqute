package ui

import (
	"log/slog"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"matterqute/internal/spell"
)

// Проверка орфографии в поле ввода (hunspell, internal/spell): ошибки
// подчёркнуты красной волной; правый щелчок по слову — варианты замены и
// «Добавить в словарь». Подчёркивает QSyntaxHighlighter: его форматы —
// только в раскладке, в документ (и в markdown, readBlocks) не попадают.
// Не проверяем код, @упоминания, ~каналы, ссылки, слова с цифрами,
// аббревиатуры и слово, которое сейчас набирают (курсор в его конце).
// Словари грузятся фоном при запуске; нет библиотеки или словарей —
// проверки нет, пункт меню «Вид» говорит почему.

const spellSuggestions = 6

var spellState = struct {
	checker   *spell.Checker
	failed    string // словари не загрузились: почему
	on        bool   // «Вид → Проверка орфографии»
	listeners map[int]func()
	next      int
}{on: true, listeners: map[int]func(){}}

// StartSpell — загрузить словари фоном; personal — файл личного словаря.
func StartSpell(personal string, log *slog.Logger) {
	go func() {
		c, err := spell.Open(spell.Langs, personal)
		mainthread.Start(func() {
			switch {
			case c == nil:
				log.Info("spellcheck off", "err", err)
				spellState.failed = "нет словарей hunspell"
			case err != nil:
				log.Warn("spellcheck personal dictionary", "err", err)
				fallthrough
			default:
				log.Info("spellcheck on", "dictionaries", c.Loaded)
				spellState.checker = c
			}
			spellChanged()
		})
	}()
}

// SetSpellcheck — проверять ли орфографию (все поля ввода).
func SetSpellcheck(on bool) {
	spellState.on = on
	spellChanged()
}

// OnSpellcheckChosen — настройку поменяли в меню (сохранить).
var OnSpellcheckChosen func(on bool)

// spellChecker — чем проверять сейчас; nil — не проверяем.
func spellChecker() *spell.Checker {
	if !spellState.on {
		return nil
	}
	return spellState.checker
}

// onSpell — подписка на смену словарей и настройки; вернёт отписку.
func onSpell(f func()) func() {
	id := spellState.next
	spellState.next++
	spellState.listeners[id] = f
	return func() { delete(spellState.listeners, id) }
}

func spellChanged() {
	for _, f := range spellState.listeners {
		f()
	}
}

// setupSpelling — подсветка ошибок и меню вариантов в поле ввода.
func (c *Composer) setupSpelling() {
	c.spell = qt.NewQSyntaxHighlighter2(c.edit.Document())
	c.spell.OnHighlightBlock(c.highlightSpelling)
	// Перекраска сама шлёт contentsChanged — поле решило бы, что текст
	// изменился (черновик, подсказки). Текст не менялся: сигналы документа
	// на это время глушим, перерисовку просим сами.
	c.unsubscribeSpell = onSpell(func() {
		doc := c.edit.Document()
		was := doc.BlockSignals(true)
		c.spell.Rehighlight()
		doc.BlockSignals(was)
		c.edit.Viewport().Update()
	})
	c.edit.OnContextMenuEvent(func(_ func(*qt.QContextMenuEvent), e *qt.QContextMenuEvent) {
		menu := c.edit.CreateStandardContextMenuWithPosition(e.Pos())
		defer menu.DeleteLater()
		// Варианты — над стандартными пунктами: собираем во вспомогательное
		// меню (оно — ребёнок menu и живёт с ним) и переносим действия.
		if items := c.spellItems(e.Pos()); len(items) > 0 {
			sub := qt.NewQMenu(menu.QWidget)
			fillMenu(sub, items)
			if std := menu.Actions(); len(std) > 0 {
				for _, a := range sub.Actions() {
					menu.InsertAction(std[0], a)
				}
				menu.InsertSeparator(std[0])
			}
		}
		menu.ExecWithPos(e.GlobalPos())
	})
}

// highlightSpelling — красная волна под словами с ошибкой в абзаце text.
func (c *Composer) highlightSpelling(text string) {
	sc := spellChecker()
	if sc == nil {
		return
	}
	b := c.spell.CurrentBlock()
	if b.BlockFormat().BoolProperty(propCodeBlock) {
		return
	}
	var code [][2]int // код в строке — не проверяем
	for it := b.Begin(); !it.AtEnd(); it.OperatorPlusPlus() {
		if fr := it.Fragment(); fr.CharFormat().BoolProperty(propCode) {
			code = append(code, [2]int{fr.Position() - b.Position(), fr.Position() - b.Position() + fr.Length()})
		}
	}
	typing := c.edit.TextCursor().Position() - b.Position()
	f := tmp(qt.NewQTextCharFormat())
	f.SetUnderlineStyle(qt.QTextCharFormat__SpellCheckUnderline)
	f.SetUnderlineColor(qcolor(Pal().Dang))
	for _, w := range spell.Words(text) {
		if w.End16 == typing || inRanges(code, w.Start16, w.End16) || sc.Correct(w.Text) {
			continue
		}
		c.spell.SetFormat(w.Start16, w.End16-w.Start16, f)
	}
}

func inRanges(rs [][2]int, start, end int) bool {
	for _, r := range rs {
		if start < r[1] && end > r[0] {
			return true
		}
	}
	return false
}

// spellItems — пункты меню для слова под точкой pos: варианты замены и
// «Добавить в словарь»; пусто — слово верно (или проверки нет).
func (c *Composer) spellItems(pos *qt.QPoint) []menuItem {
	return c.spellItemsAt(c.edit.CursorForPosition(pos))
}

// spellItemsAt — то же для слова у курсора cur.
func (c *Composer) spellItemsAt(cur *qt.QTextCursor) []menuItem {
	sc := spellChecker()
	if sc == nil {
		return nil
	}
	b := cur.Block()
	if b.BlockFormat().BoolProperty(propCodeBlock) || cur.CharFormat().BoolProperty(propCode) {
		return nil
	}
	at := cur.Position() - b.Position()
	for _, w := range spell.Words(b.Text()) {
		if at < w.Start16 || at > w.End16 || sc.Correct(w.Text) {
			continue
		}
		from, to := b.Position()+w.Start16, b.Position()+w.End16
		var out []menuItem
		for _, s := range sc.Suggest(w.Text, spellSuggestions) {
			out = append(out, menuItem{title: s, do: func() { c.replaceRange(from, to, s) }})
		}
		if len(out) == 0 {
			out = append(out, menuItem{title: "Нет вариантов", do: func() {}})
		}
		word := w.Text
		return append(out, menuItem{}, menuItem{title: "Добавить «" + word + "» в словарь", do: func() {
			if err := sc.Add(word); err != nil {
				slog.Default().Warn("spellcheck add word", "err", err)
			}
			spellChanged()
		}})
	}
	return nil
}

// replaceRange — текст поля с from до to (в символах документа) заменить на s.
func (c *Composer) replaceRange(from, to int, s string) {
	tc := tmp(c.edit.TextCursor())
	tc.SetPosition(from)
	tc.SetPosition2(to, qt.QTextCursor__KeepAnchor)
	tc.InsertText(s)
	c.edit.SetTextCursor(tc)
}
