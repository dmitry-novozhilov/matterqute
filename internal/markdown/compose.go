package markdown

import (
	"fmt"
	"strings"
)

// Поле ввода — редактор с оформлением (WYSIWYG): он отдаёт текст абзацами, а
// на сервер уходит markdown Mattermost (FromBlocks).

// BlockKind — вид абзаца редактора.
type BlockKind int

const (
	Para    BlockKind = iota // обычная строка
	Quote                    // строка цитаты
	Bullet                   // пункт маркированного списка
	Ordered                  // пункт нумерованного списка
	Code                     // строка блока кода
)

// Block — абзац редактора.
type Block struct {
	Kind  BlockKind
	Level int    // вложенность пункта списка: 0 — верхний уровень
	Start int    // номер пункта нумерованного списка в исходном тексте; 0 — по порядку с 1
	Spans []Span // текст с оформлением; у Code оформление не учитывается
}

// Span — кусок текста с одним оформлением.
type Span struct {
	Text                       string
	Bold, Italic, Strike, Code bool
	Link                       string // адрес ссылки; пусто — не ссылка
}

// Text — текст абзаца без оформления.
func (b Block) Text() string {
	var s strings.Builder
	for _, sp := range b.Spans {
		s.WriteString(sp.Text)
	}
	return s.String()
}

// FromBlocks — markdown Mattermost. Обычный текст уходит как есть, без
// экранирования: набранные руками *звёздочки* и `кавычки` остаются разметкой,
// как в простом поле ввода. Разметку добавляет только оформление. Соседние
// строки — через перевод строки (Mattermost показывает его переносом), пустая
// строка — новый абзац.
func FromBlocks(blocks []Block) string {
	var lines []string
	var listKinds, widths []int // по уровням открытого списка: вид и ширина маркера
	var counters []int
	prev := BlockKind(-1)
	isList := func(k BlockKind) bool { return k == Bullet || k == Ordered }
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		// После списка или цитаты обычная строка без пустой стала бы их
		// продолжением («ленивая» строка markdown).
		if prev >= 0 && prev != b.Kind && (isList(prev) || prev == Quote) && !(isList(prev) && isList(b.Kind)) {
			lines = append(lines, "")
		}
		if !isList(b.Kind) {
			listKinds, widths, counters = nil, nil, nil
		}
		switch b.Kind {
		case Code:
			var code []string
			for ; i < len(blocks) && blocks[i].Kind == Code; i++ {
				code = append(code, blocks[i].Text())
			}
			i--
			fence := strings.Repeat("`", max(3, longestRun(strings.Join(code, "\n"), '`')+1))
			lines = append(lines, fence)
			lines = append(lines, code...)
			lines = append(lines, fence)
		case Quote:
			text := strings.ReplaceAll(Inline(b.Spans), "\n", "\n> ")
			if text == "" {
				lines = append(lines, ">")
			} else {
				lines = append(lines, "> "+text)
			}
		case Bullet, Ordered:
			level := min(max(b.Level, 0), len(listKinds)) // глубже родителя+1 не бывает
			keep := min(level+1, len(listKinds))          // уровни глубже закрываются
			if level < keep && listKinds[level] != int(b.Kind) {
				keep = level // другой вид списка на том же уровне — новый список
			}
			listKinds, widths, counters = listKinds[:keep], widths[:keep], counters[:keep]
			if level == len(listKinds) {
				// Первый пункт уровня задаёт номер (markdown нумерует дальше сам).
				listKinds, widths, counters = append(listKinds, int(b.Kind)), append(widths, 0), append(counters, max(b.Start, 1)-1)
			}
			counters[level]++
			marker := "-"
			if b.Kind == Ordered {
				marker = fmt.Sprintf("%d.", counters[level])
			}
			widths[level] = len(marker) + 1
			indent := 0
			for _, w := range widths[:level] {
				indent += w
			}
			pad := strings.Repeat(" ", indent)
			text := strings.ReplaceAll(Inline(b.Spans), "\n", "\n"+pad+strings.Repeat(" ", widths[level]))
			line := pad + marker
			if text != "" {
				line += " " + text
			}
			lines = append(lines, line)
		default:
			lines = append(lines, Inline(b.Spans))
		}
		prev = b.Kind
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// longestRun — самая длинная серия символа c подряд.
func longestRun(s string, c rune) int {
	best, n := 0, 0
	for _, r := range s {
		if r == c {
			n++
			best = max(best, n)
		} else {
			n = 0
		}
	}
	return best
}

type style uint8

const (
	strike style = 1 << iota
	bold
	italic
)

// Порядок открытия: снаружи внутрь.
var styleOrder = []style{strike, bold, italic}

func (s style) marker() string {
	switch s {
	case strike:
		return "~~"
	case bold:
		return "**"
	}
	return "*"
}

func spanStyle(sp Span) style {
	var s style
	if sp.Strike {
		s |= strike
	}
	if sp.Bold {
		s |= bold
	}
	if sp.Italic {
		s |= italic
	}
	return s
}

// Inline — строка с оформлением в markdown: ссылки — [текст](адрес) (если
// текст и есть адрес — просто адрес, Mattermost сделает ссылку сам),
// оформление — маркерами, вложенными правильно.
func Inline(spans []Span) string {
	var out strings.Builder
	for i := 0; i < len(spans); {
		j := i
		for j < len(spans) && spans[j].Link == spans[i].Link {
			j++
		}
		group, link := spans[i:j], spans[i].Link
		switch {
		case link == "":
			out.WriteString(emphasis(group, false))
		case plainText(group) == link:
			out.WriteString(link)
		default:
			out.WriteString("[" + emphasis(group, true) + "](" + linkURL(link) + ")")
		}
		i = j
	}
	return strings.ReplaceAll(out.String(), " ", "\n")
}

func plainText(spans []Span) string {
	var s strings.Builder
	for _, sp := range spans {
		if sp.Bold || sp.Italic || sp.Strike || sp.Code {
			return ""
		}
		s.WriteString(sp.Text)
	}
	return s.String()
}

var urlEscaper = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E")

func linkURL(u string) string { return urlEscaper.Replace(u) }

var linkTextEscaper = strings.NewReplacer("[", `\[`, "]", `\]`)

// emphasis — куски одной ссылки (или вне ссылок) с маркерами. Маркеры
// закрываются в обратном порядке открытия; пробелы по краям оформленного
// текста выносятся за маркеры: «** жирный**» markdown не поймёт.
func emphasis(spans []Span, inLink bool) string {
	var out strings.Builder
	var stack []style
	pending := "" // пробелы в конце предыдущего куска: после закрывающих маркеров
	for _, sp := range spans {
		text := sp.Text
		if inLink && !sp.Code {
			text = linkTextEscaper.Replace(text)
		}
		lead, core, trail := splitSpace(text)
		if sp.Code {
			lead, core, trail = "", text, ""
		}
		if core == "" {
			pending += text // одни пробелы: оформление не меняем
			continue
		}
		want := spanStyle(sp)
		k := len(stack)
		for idx, s := range stack {
			if want&s == 0 {
				k = idx
				break
			}
		}
		for len(stack) > k {
			out.WriteString(stack[len(stack)-1].marker())
			stack = stack[:len(stack)-1]
		}
		out.WriteString(pending)
		out.WriteString(lead)
		pending = ""
		for _, s := range styleOrder {
			if want&s != 0 && !hasStyle(stack, s) {
				out.WriteString(s.marker())
				stack = append(stack, s)
			}
		}
		if sp.Code {
			out.WriteString(codeSpan(core))
		} else {
			out.WriteString(core)
		}
		pending = trail
	}
	for len(stack) > 0 {
		out.WriteString(stack[len(stack)-1].marker())
		stack = stack[:len(stack)-1]
	}
	out.WriteString(pending)
	return out.String()
}

func hasStyle(stack []style, s style) bool {
	for _, x := range stack {
		if x == s {
			return true
		}
	}
	return false
}

func splitSpace(s string) (lead, core, trail string) {
	core = strings.TrimLeft(s, " \t ")
	lead = s[:len(s)-len(core)]
	t := strings.TrimRight(core, " \t ")
	return lead, t, core[len(t):]
}

// codeSpan — `код`: кавычек вокруг больше, чем подряд внутри; код, который
// начинается или кончается кавычкой, отделяется пробелом.
func codeSpan(s string) string {
	fence := strings.Repeat("`", longestRun(s, '`')+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}
