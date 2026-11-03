package markdown

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/theme"
)

func TestRender(t *testing.T) {
	r := New(Options{
		Me:            "alice",
		ChannelExists: func(name string) bool { return name == "town-square" },
		CustomEmoji: func(name string) string {
			switch name {
			case "partyparrot":
				return "/cache/emoji/partyparrot.gif"
			case "dance": // анимированное: ресурс документа, а не файл
				return "emoji:dance"
			}
			return ""
		},
	})
	for _, c := range []struct {
		name, in string
		want     []string // подстроки результата
		not      []string // чего быть не должно
	}{
		{"custom emoji as a document resource", "ура :dance:", []string{`<img src="emoji:dance" width="20" height="20" title=":dance:">`}, []string{"file://"}},
		{"emphasis", "**жир** _курсив_ ~~зачёрк~~", []string{"<strong>жир</strong>", "<em>курсив</em>", "<del>зачёрк</del>"}, nil},
		{"hard wraps", "строка1\nстрока2", []string{"строка1<br>", "строка2"}, nil},
		{"raw html is text", "<script>alert(1)</script> и <b>x</b>", []string{"&lt;script&gt;", "&lt;b&gt;x&lt;/b&gt;"}, []string{"<script>", "<b>x"}},
		{"html block is text", "<div>\nhi\n</div>", []string{"&lt;div&gt;<br>hi<br>&lt;/div&gt;"}, []string{"<div>"}},
		{"code span", "`a <b> & c`", []string{`<span style="font-family:'PT Mono';font-size:13px;background-color:#eeebe6">a &lt;b&gt; &amp; c</span>`}, nil},
		{"code block", "```go\nfunc main() {\n\tx := 1 < 2\n}\n```", []string{"white-space:pre-wrap", "func main() {\n\tx := 1 &lt; 2\n}</p>"}, []string{"<pre>"}},
		{"mentions", "привет @alice и @bob.", []string{`background-color:#f6dd90;color:#4f3005;font-weight:600;text-decoration:none">@alice</a>`, `href="mm-user:bob" style="color:#007255;font-weight:600;text-decoration:none">@bob</a>.`}, nil},
		{"mention case", "@Alice", []string{`background-color:#f6dd90;color:#4f3005;font-weight:600;text-decoration:none">@Alice</a>`}, nil},
		{"special mentions", "@channel @here", []string{`#f6dd90;color:#4f3005;font-weight:600;text-decoration:none">@channel</a>`, `>@here</a>`}, nil},
		{"email is not mention", "пиши bob@example.com", []string{`mailto:bob@example.com`}, []string{"mm-user:"}},
		{"system emoji", ":+1: и :white_check_mark:", []string{"👍 и ✅"}, nil},
		{"custom emoji", "ура :partyparrot:", []string{`<img src="file:///cache/emoji/partyparrot.gif" width="20" height="20" title=":partyparrot:">`}, nil},
		{"unknown emoji stays", ":nope:", []string{":nope:"}, []string{"<img"}},
		{"time is not emoji", "в 12:30:45 :+1:", []string{"12:30:45 👍"}, nil},
		{"channel link", "в ~town-square, не ~missing", []string{`<a href="mm-channel:town-square" style="color:#007255">~town-square</a>`, "не ~missing"}, nil},
		{"autolink trims dot", "см. https://x.io/a?b=1&c=2.", []string{`<a href="https://x.io/a?b=1&amp;c=2">https://x.io/a?b=1&amp;c=2</a>.`}, nil},
		{"no mention inside link", "[@bob](https://x.io)", []string{`<a href="https://x.io">@bob</a>`}, []string{"mm-user:"}},
		{"image is link", "![схема](https://x.io/s.png)", []string{`<a href="https://x.io/s.png">🖼 схема</a>`}, []string{"<img"}},
		{"table", "| a | b |\n|---|---|\n| 1 | 2 |", []string{`<table cellspacing="1" cellpadding="3" bgcolor="#d9d5cf"`, `<th bgcolor="#eeebe6" align="left">a</th>`, `<td bgcolor="#fcfbf9" align="left">2</td>`}, []string{"border-collapse"}},
		{"task list", "- [x] готово\n- [ ] нет", []string{"☑ готово", "☐ нет"}, []string{"<input"}},
		{"blockquote", "> цитата", []string{`<td width="3" bgcolor="#bcb6b0"></td>`, "цитата"}, []string{"<blockquote"}},
		{"heading", "### Релиз", []string{`<p style="font-size:15px;font-weight:600;margin-top:4px;margin-bottom:2px">Релиз</p>`}, []string{"<h3"}},
		{"paragraphs", "раз\n\nдва", []string{`<p style="margin:0">раз</p>`, `<p style="margin-top:6px;margin-bottom:0">два</p>`}, nil},
	} {
		got := r.Render(c.in)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: Render(%q)\n got: %s\nwant substring: %s", c.name, c.in, got, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(got, w) {
				t.Errorf("%s: Render(%q)\n got: %s\nmust not contain: %s", c.name, c.in, got, w)
			}
		}
	}
}

func TestRenderPlain(t *testing.T) {
	// Без Options: ~каналы и кастомные эмодзи остаются текстом, упоминания — ссылки.
	got := Render("~town-square :partyparrot: @bob")
	if !strings.Contains(got, "~town-square :partyparrot: ") || !strings.Contains(got, "mm-user:bob") {
		t.Errorf("Render plain: %s", got)
	}
}

func TestAttachments(t *testing.T) {
	r := New(Options{})
	got := r.Attachments([]*model.MessageAttachment{{
		Color: "danger", Pretext: "Алерт", Title: "DiskSpaceLow", TitleLink: "https://grafana.example.com/d/1",
		Text: "**db-2**: свободно 3%", Footer: "Grafana",
		Fields: []*model.MessageAttachmentField{
			{Title: "host", Value: "db-2", Short: true}, {Title: "severity", Value: "critical", Short: true},
			{Title: "runbook", Value: "https://wiki/disk"},
		},
	}, {Color: `#fff" onclick="x`, Fallback: "только fallback <b>"}}, 640, nil)
	for _, w := range []string{
		"Алерт", `<td width="640">`, `<td width="4" bgcolor="#c13c36">`, `<p style="margin:0;font-weight:600"><a href="https://grafana.example.com/d/1">DiskSpaceLow</a></p>`,
		"<strong>db-2</strong>: свободно 3%", `width="50%" valign="top" style="padding-bottom:4px"><span style="font-size:12px;font-weight:600;color:#5f5a53">host</span>`,
		`<td colspan="2" valign="top" style="padding-bottom:4px"><span style="font-size:12px;font-weight:600;color:#5f5a53">runbook</span>`,
		"Grafana", `<td width="4" bgcolor="#bcb6b0">`, "только fallback &lt;b&gt;",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("Attachments: missing %s\n got: %s", w, got)
		}
	}
	if strings.Contains(got, "onclick") {
		t.Errorf("invalid color leaked into HTML: %s", got)
	}
}

func TestRenderInlineAndDark(t *testing.T) {
	got := RenderInline("Сервисы и API.\nДежурный: **@carol**", theme.Dark)
	if strings.HasPrefix(got, "<p") || !strings.Contains(got, "Сервисы и API. Дежурный:") || !strings.Contains(got, "#5ec4a1") {
		t.Errorf("RenderInline dark: %s", got)
	}
}

func TestPlainText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"**жирный** и _курсив_ :+1:", "жирный и курсив 👍"},
		{"строка\nвторая", "строка\nвторая"},
		{"[ссылка](https://example.com) и https://mm.example.com", "ссылка и https://mm.example.com"},
		{"### Релиз\n- [x] кэш\n- ~~API~~", "Релиз\nкэш\nAPI"},
		{"код `make` и\n```\nline1\nline2\n```", "код make и\nline1\nline2"},
		{"> цитата\n\nабзац", "цитата\nабзац"},
		{"<b>сырой</b> html", "<b>сырой</b> html"},
		{"@alice глянь ~town-square :нет_такого:", "@alice глянь ~town-square :нет_такого:"},
	} {
		if got := PlainText(tc.in); got != tc.want {
			t.Errorf("PlainText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHighlight(t *testing.T) {
	r := New(Options{Highlight: []string{"отчёт", "deploy"}})
	got := r.Render("Где **Отчёты** по Deploy-у? предотчёт и `отчёт` в коде")
	for _, want := range []string{`">Отчёты</span>`, `">Deploy</span>`} {
		if !strings.Contains(got, want) {
			t.Errorf("no highlight %q in %s", want, got)
		}
	}
	if strings.Contains(got, `">предотчёт`) || strings.Count(got, "<span style=\"background-color") != 2 {
		t.Errorf("highlighted inside a word or in code: %s", got)
	}
}
