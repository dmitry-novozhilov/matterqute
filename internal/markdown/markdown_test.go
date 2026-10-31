package markdown

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestRender(t *testing.T) {
	r := New(Options{
		Me:            "alice",
		ChannelExists: func(name string) bool { return name == "town-square" },
		CustomEmoji: func(name string) string {
			if name == "partyparrot" {
				return "/cache/emoji/partyparrot.gif"
			}
			return ""
		},
	})
	for _, c := range []struct {
		name, in string
		want     []string // подстроки результата
		not      []string // чего быть не должно
	}{
		{"emphasis", "**жир** _курсив_ ~~зачёрк~~", []string{"<strong>жир</strong>", "<em>курсив</em>", "<del>зачёрк</del>"}, nil},
		{"hard wraps", "строка1\nстрока2", []string{"строка1<br>", "строка2"}, nil},
		{"raw html is text", "<script>alert(1)</script> и <b>x</b>", []string{"&lt;script&gt;", "&lt;b&gt;x&lt;/b&gt;"}, []string{"<script>", "<b>x"}},
		{"html block is text", "<div>\nhi\n</div>", []string{"&lt;div&gt;<br>hi<br>&lt;/div&gt;"}, []string{"<div>"}},
		{"code span", "`a <b> & c`", []string{`<code style="background-color:#ececec">a &lt;b&gt; &amp; c</code>`}, nil},
		{"code block", "```go\nfunc main() {\n\tx := 1 < 2\n}\n```", []string{"white-space:pre-wrap", "func main() {\n\tx := 1 &lt; 2\n}</p>"}, []string{"<pre>"}},
		{"mentions", "привет @alice и @bob.", []string{`background-color:#ffe58f"><b>@alice</b>`, `href="mm-user:bob"`, "<b>@bob</b></a>."}, nil},
		{"mention case", "@Alice", []string{`background-color:#ffe58f"><b>@Alice</b>`}, nil},
		{"special mentions", "@channel @here", []string{`<b>@channel</b></span>`, `<b>@here</b></span>`}, nil},
		{"email is not mention", "пиши bob@example.com", []string{`mailto:bob@example.com`}, []string{"mm-user:"}},
		{"system emoji", ":+1: и :white_check_mark:", []string{"👍 и ✅"}, nil},
		{"custom emoji", "ура :partyparrot:", []string{`<img src="file:///cache/emoji/partyparrot.gif" width="20" height="20" title=":partyparrot:">`}, nil},
		{"unknown emoji stays", ":nope:", []string{":nope:"}, []string{"<img"}},
		{"time is not emoji", "в 12:30:45 :+1:", []string{"12:30:45 👍"}, nil},
		{"channel link", "в ~town-square, не ~missing", []string{`<a href="mm-channel:town-square">~town-square</a>`, "не ~missing"}, nil},
		{"autolink trims dot", "см. https://x.io/a?b=1&c=2.", []string{`<a href="https://x.io/a?b=1&amp;c=2">https://x.io/a?b=1&amp;c=2</a>.`}, nil},
		{"no mention inside link", "[@bob](https://x.io)", []string{`<a href="https://x.io">@bob</a>`}, []string{"mm-user:"}},
		{"image is link", "![схема](https://x.io/s.png)", []string{`<a href="https://x.io/s.png">🖼 схема</a>`}, []string{"<img"}},
		{"table", "| a | b |\n|---|---|\n| 1 | 2 |", []string{`<table border="1"`, "<th>a</th>", "<td>2</td>"}, nil},
		{"task list", "- [x] готово\n- [ ] нет", []string{"☑ готово", "☐ нет"}, []string{"<input"}},
		{"blockquote", "> цитата", []string{"<blockquote", "цитата"}, nil},
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
	}, {Color: `#fff" onclick="x`, Fallback: "только fallback <b>"}})
	for _, w := range []string{
		"Алерт", `bgcolor="#a30200"`, `<b><a href="https://grafana.example.com/d/1">DiskSpaceLow</a></b>`,
		"<strong>db-2</strong>: свободно 3%", `<td colspan="1" valign="top"><b>host</b>`, `<td colspan="2" valign="top"><b>runbook</b>`,
		"Grafana", `bgcolor="#c8c8c8"`, "только fallback &lt;b&gt;",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("Attachments: missing %s\n got: %s", w, got)
		}
	}
	if strings.Contains(got, "onclick") {
		t.Errorf("invalid color leaked into HTML: %s", got)
	}
}
