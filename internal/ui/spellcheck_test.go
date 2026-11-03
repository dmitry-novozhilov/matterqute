package ui

import (
	"path/filepath"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/spell"
)

// Орфография в поле ввода: ошибки подчёркнуты (код и набираемое слово — нет),
// варианты в меню заменяют слово, «в словарь» снимает подчёркивание; текст
// поля от этого не «меняется» (ни черновиков, ни «печатает…»).
func TestSpellcheck(t *testing.T) {
	sc, err := spell.Open(spell.Langs, filepath.Join(t.TempDir(), "words.txt"))
	if sc == nil {
		t.Skip(err)
	}
	if len(sc.Loaded) < len(spell.Langs) {
		t.Skipf("dictionaries loaded %v, want %v (hunspell-ru, hunspell-en-us)", sc.Loaded, spell.Langs)
	}
	onMain(func() {
		defer func() {
			spellState.checker = nil
			spellChanged()
			sc.Close()
		}()
		c := NewComposer()
		defer c.dispose()
		changes := 0
		c.edit.OnTextChanged(func() { changes++ })
		spellState.checker, spellState.on = sc, true
		c.edit.SetPlainText("Превет мир, helo `превет` @alise пешуу")
		// Курсор — в конце «пешуу»: его ещё набирают, не подчёркиваем.
		cur := c.edit.TextCursor()
		cur.MovePosition(qt.QTextCursor__End)
		c.edit.SetTextCursor(cur)
		spellChanged()
		marked := func() []string {
			b := c.edit.Document().FirstBlock()
			text := []rune(b.Text())
			var out []string
			for _, r := range b.Layout().Formats() {
				if r.Format().UnderlineStyle() == qt.QTextCharFormat__SpellCheckUnderline {
					out = append(out, string(text[r.Start():r.Start()+r.Length()]))
				}
			}
			return out
		}
		before := changes
		spellChanged()
		if changes != before {
			t.Errorf("rehighlight changed the text %d times", changes-before)
		}
		// «превет» в обратных кавычках набран руками, а не форматом кода, —
		// проверяется; формат кода проверяет richedit, здесь — слова.
		if got := strings.Join(marked(), ","); got != "Превет,helo,превет" {
			t.Errorf("marked %q", got)
		}
		// Дописали пробел — слово набрано, теперь подчёркнуто.
		c.edit.TextCursor().InsertText(" ")
		if got := strings.Join(marked(), ","); got != "Превет,helo,превет,пешуу" {
			t.Errorf("after space: marked %q", got)
		}
		c.w.Resize(560, 120)
		c.w.Show()
		settle()
		shotWidget(t, c.w, "spellcheck")

		// Меню у слова: варианты и «в словарь».
		// Курсор внутри слова (меню по правому щелчку берёт его из точки).
		at := func(word string) *qt.QTextCursor {
			i := strings.Index(c.edit.ToPlainText(), word)
			cur := c.edit.TextCursor()
			cur.SetPosition(len([]rune(c.edit.ToPlainText()[:i])) + 1)
			return cur
		}
		items := c.spellItemsAt(at("Превет"))
		fix := -1
		var titles []string
		for i, x := range items {
			titles = append(titles, x.title)
			if x.title == "Привет" {
				fix = i
			}
		}
		// Порядок вариантов — hunspell («Ревет», «Преет»… «Привет»), как в LibreOffice.
		if fix < 0 || !strings.HasPrefix(items[len(items)-1].title, "Добавить «Превет»") {
			t.Errorf("menu %q", titles) // не Fatal: внутри onMain он вешает тесты
			return
		}
		items[fix].do()
		if !strings.HasPrefix(c.edit.ToPlainText(), "Привет мир") {
			t.Errorf("after replace: %q", c.edit.ToPlainText())
		}
		if items := c.spellItemsAt(at("мир")); len(items) != 0 {
			t.Errorf("menu for a correct word: %d items", len(items))
		}
		if items = c.spellItemsAt(at("helo")); len(items) == 0 {
			t.Error("no menu for helo")
			return
		}
		items[len(items)-1].do()
		if got := strings.Join(marked(), ","); strings.Contains(got, "helo") {
			t.Errorf("added word still marked: %q", got)
		}

		// Выключили — подчёркиваний нет.
		SetSpellcheck(false)
		if got := marked(); len(got) != 0 {
			t.Errorf("spellcheck off, marked %q", got)
		}
		SetSpellcheck(true)
	})
}
