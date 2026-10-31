package markdown

import "testing"

func TestRender(t *testing.T) {
	for in, want := range map[string]string{
		"hi <b>&":                    "hi &lt;b&gt;&amp;",
		"a\nb":                       "a<br>b",
		"see https://x.io/a?b=1&c=2": `see <a href="https://x.io/a?b=1&amp;c=2">https://x.io/a?b=1&amp;c=2</a>`,
		"(https://x.io/p).":          `(<a href="https://x.io/p">https://x.io/p</a>).`,
		"http://a.b/\"><script>":     `<a href="http://a.b/">http://a.b/</a>&#34;&gt;&lt;script&gt;`,
	} {
		if got := Render(in); got != want {
			t.Errorf("Render(%q)\n got %s\nwant %s", in, got, want)
		}
	}
}
