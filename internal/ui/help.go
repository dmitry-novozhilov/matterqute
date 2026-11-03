package ui

import (
	"fmt"
	"html"
	"path"
	"runtime"
	"runtime/debug"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/docs/help"
	"matterqute/internal/markdown"
)

// helpWindow — статья справки (docs/help, встроены в бинарник) тем же
// рендером markdown, что и лента. Ссылки на другие статьи открываются здесь
// же, веб-ссылки — в браузере. Одно окно на приложение.
type helpWindow struct {
	w           *qt.QWidget
	view        *qt.QTextBrowser
	name        string
	closed      bool
	unsubscribe func()
}

// buildHelpMenu — меню «Справка»: статьи и «О программе».
func (s *Shell) buildHelpMenu() {
	menu := s.w.MenuBar().AddMenuWithTitle("Сп&равка")
	keys := menu.QWidget.AddActionWithText("Сочетания клавиш")
	keys.SetShortcut(qt.NewQKeySequence2("Ctrl+/"))
	keys.OnTriggered(s.showShortcuts)
	menu.AddSeparator()
	for _, a := range help.Articles() {
		act := menu.QWidget.AddActionWithText(a.Title + "…")
		act.OnTriggered(func() { s.showHelp(a.Name) })
	}
	menu.AddSeparator()
	about := menu.QWidget.AddActionWithText("О программе…")
	about.OnTriggered(s.showAbout)
}

// showHelp — статья name в окне справки (открытом — поверх).
func (s *Shell) showHelp(name string) {
	h := s.help
	if h == nil || h.closed {
		h = newHelpWindow(s.w.QWidget)
		s.help = h
	}
	h.open(name)
	h.w.Show()
	h.w.Raise()
	h.w.ActivateWindow()
}

func newHelpWindow(parent *qt.QWidget) *helpWindow {
	// Окно с родителем: закроется вместе с главным и не держит приложение.
	h := &helpWindow{w: qt.NewQWidget3(parent, qt.Window), view: qt.NewQTextBrowser2()}
	h.w.SetAttribute(qt.WA_DeleteOnClose)
	h.w.Resize(720, 640)
	h.view.SetOpenLinks(false)
	h.view.SetFrameShape(qt.QFrame__NoFrame)
	h.view.OnAnchorClicked(func(u *qt.QUrl) { h.follow(u.ToString()) })
	lay := qt.NewQVBoxLayout(h.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.AddWidget(h.view.QWidget)
	h.unsubscribe = OnThemeChanged(h.render)
	h.w.OnDestroyed(func() {
		h.closed = true
		h.unsubscribe()
	})
	return h
}

func (h *helpWindow) open(name string) {
	h.name = name
	h.render()
	h.view.VerticalScrollBar().SetValue(0)
}

// render — статья в текущей теме и шрифтах (поля — как у ленты).
func (h *helpWindow) render() {
	md := help.Text(h.name)
	pal := Pal()
	h.w.SetWindowTitle(helpTitle(md) + " — справка " + AppTitle)
	h.view.Document().SetDefaultFont(feedFont())
	h.view.Document().SetDocumentMargin(24)
	h.view.SetStyleSheet(fmt.Sprintf("QTextBrowser { background: %s; color: %s; }", pal.Win, pal.Tx))
	h.view.SetHtml(scaleFonts(markdown.New(markdown.Options{Palette: pal}).Render(md)))
}

// follow — ссылка в статье: другая статья — здесь, веб — в браузере.
func (h *helpWindow) follow(link string) {
	switch {
	case strings.HasPrefix(link, "http://"), strings.HasPrefix(link, "https://"):
		qt.QDesktopServices_OpenUrl(qt.NewQUrl3(link))
	case strings.HasSuffix(link, ".md") && help.Text(path.Base(link)) != "":
		h.open(path.Base(link))
	}
}

func helpTitle(md string) string {
	for _, l := range strings.Split(md, "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return "Справка"
}

// showAbout — «О программе»: версия сборки, Qt, лицензии того, что внутри.
func (s *Shell) showAbout() {
	d := newDialog(s.w.QWidget, "О программе")
	logo := appIconPixmap(48 * resourceDPR)
	logo.SetDevicePixelRatio(resourceDPR)
	icon := qt.NewQLabel2()
	icon.SetPixmap(logo)
	text := qt.NewQLabel2()
	text.SetTextFormat(qt.RichText)
	text.SetWordWrap(true)
	text.SetOpenExternalLinks(true)
	text.SetText(aboutHTML())
	row := qt.NewQHBoxLayout2()
	row.SetSpacing(16)
	row.AddWidget3(icon.QWidget, 0, qt.AlignTop)
	row.AddWidget2(text.QWidget, 1)
	d.lay.AddLayout(row.QLayout)
	d.bottom.AddStretch()
	ok := qt.NewQPushButton3("Закрыть")
	ok.SetProperty("primary", qt.NewQVariant11("true"))
	ok.SetDefault(true)
	ok.OnClicked(func() { d.w.Close() })
	d.bottom.AddWidget(ok.QWidget)
	d.w.SetMinimumWidth(460)
	d.w.Show()
	s.about = d
}

func aboutHTML() string {
	pal := Pal()
	return fmt.Sprintf(`<p style="font-size:18px;font-weight:600;margin:0">%s</p>`+
		`<p style="margin-top:2px;color:%s">Десктопный клиент Mattermost на Go и Qt</p>`+
		`<p style="margin-top:12px">Сборка: %s<br>Qt %s, %s</p>`+
		`<p style="margin-top:12px;font-size:12px;color:%s">Matterqute — MIT. Внутри: шрифты IBM Plex Sans, PT Mono, Noto Color Emoji (OFL), `+
		`значки Lucide (ISC, часть из Feather — MIT), разделы эмодзи из данных Unicode (Unicode License V3), `+
		`клиент Mattermost (Apache-2.0), goldmark и chroma (MIT), Qt (LGPL), MIQT (MIT).</p>`,
		AppTitle, pal.Tx2, html.EscapeString(buildVersion()), qt.QLibraryInfo_Version().ToString(), runtime.Version(), pal.Tx2)
}

// buildVersion — из чего собрано: коммит и время (Go пишет их в бинарник).
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "неизвестна"
	}
	var rev, at string
	dirty := false
	for _, st := range info.Settings {
		switch st.Key {
		case "vcs.revision":
			rev = st.Value
		case "vcs.time":
			at = st.Value
		case "vcs.modified":
			dirty = st.Value == "true"
		}
	}
	if rev == "" {
		return "без сведений о коммите"
	}
	out := rev[:min(12, len(rev))]
	if dirty {
		out += " (с изменениями)"
	}
	if at != "" {
		out += ", " + strings.NewReplacer("T", " ", "Z", " UTC").Replace(at)
	}
	return out
}
