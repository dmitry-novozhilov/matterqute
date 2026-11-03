package markdown

import (
	"html"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlightMax — код длиннее подсвечивать не стоит: каждый цветной кусок —
// свой span, а разбор rich text в Qt медленный.
const highlightMax = 30000

// highlight — код на языке lang в HTML с подсветкой синтаксиса (chroma:
// цвета стиля github, в тёмной теме github-dark; фон блока — наш) и
// название языка для подписи. ok=false — язык не знаем (или код слишком
// длинный): тогда код как есть. Как веб-клиент, язык не угадываем: без
// него после ``` — без подсветки.
func highlight(lang, code string, dark bool) (out, name string, ok bool) {
	if lang == "" || len(code) > highlightMax {
		return "", "", false
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return "", "", false
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return "", "", false
	}
	style := styles.Get("github")
	if dark {
		style = styles.Get("github-dark")
	}
	// Цвета «обычного текста» стиля (у github — тёмно-серый, у github-dark —
	// светлый) не берём: такой код — цветом текста нашей темы.
	plain := map[chroma.Colour]bool{}
	for _, t := range []chroma.TokenType{chroma.Background, chroma.Text, chroma.Punctuation, chroma.Name, chroma.NameOther} {
		if c := style.Get(t).Colour; c.IsSet() {
			plain[c] = true
		}
	}
	var b strings.Builder
	css, text := "", ""
	flush := func() {
		switch {
		case text == "":
		case css == "":
			b.WriteString(html.EscapeString(text))
		default:
			b.WriteString(`<span style="` + css + `">` + html.EscapeString(text) + "</span>")
		}
	}
	// Соседние куски одного вида — одним span: их у Qt разбирать меньше.
	for tok := it(); tok != chroma.EOF; tok = it() {
		if strings.TrimSpace(tok.Value) == "" {
			text += tok.Value // пробелы — к соседнему куску, цвет им не нужен
			continue
		}
		e := style.Get(tok.Type)
		if plain[e.Colour] {
			e.Colour = 0
		}
		c := tokenCSS(e)
		if c != css {
			flush()
			css, text = c, ""
		}
		text += tok.Value
	}
	flush()
	return b.String(), lexer.Config().Name, true
}

func tokenCSS(e chroma.StyleEntry) string {
	var s string
	if e.Colour.IsSet() {
		s += "color:" + e.Colour.String() + ";"
	}
	if e.Bold == chroma.Yes {
		s += "font-weight:600;"
	}
	if e.Italic == chroma.Yes {
		s += "font-style:italic;"
	}
	return s
}
