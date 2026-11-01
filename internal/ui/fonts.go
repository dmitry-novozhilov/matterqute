package ui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/config"
	"matterqute/internal/theme"
)

// Шрифты по дизайну; меняются в «Вид → Шрифты…».
var defaultFonts = config.Fonts{
	Sidebar: config.Font{Family: theme.SansFamily, Size: 14},
	Feed:    config.Font{Family: theme.SansFamily, Size: 14},
	Mono:    config.Font{Family: theme.MonoFamily, Size: 13},
}

const minFontSize, maxFontSize = 9, 32

// OnFontsChosen — шрифты выбрали в окне (сохранить в настройках).
var OnFontsChosen func(config.Fonts)

// SetFonts применяет шрифты сразу: всё, что подписано на смену темы
// (OnThemeChanged), перерисуется. Пустое — по дизайну.
func SetFonts(f config.Fonts) {
	fill := func(f, def config.Font) config.Font {
		if f.Family == "" {
			f.Family = def.Family
		}
		if f.Size == 0 {
			f.Size = def.Size
		}
		f.Size = max(minFontSize, min(f.Size, maxFontSize))
		return f
	}
	style.fonts = config.Fonts{
		Sidebar: fill(f.Sidebar, defaultFonts.Sidebar),
		Feed:    fill(f.Feed, defaultFonts.Feed),
		Mono:    fill(f.Mono, defaultFonts.Mono),
	}
	for _, l := range style.listeners {
		l()
	}
}

func font(f config.Font, px int, weight qt.QFont__Weight) *qt.QFont {
	q := qt.NewQFont()
	// Эмодзи — запасным цветным шрифтом, как у всего приложения.
	q.SetFamilies([]string{f.Family, theme.EmojiFamily})
	q.SetPixelSize(max(minFontSize-2, px))
	q.SetWeight(weight)
	return q
}

// sidebarFont — шрифт сайдбара; delta — к размеру (подписи групп мельче на 2).
func sidebarFont(delta int, weight qt.QFont__Weight) *qt.QFont {
	return font(style.fonts.Sidebar, style.fonts.Sidebar.Size+delta, weight)
}

// feedFont — шрифт текста сообщений и поля ввода.
func feedFont() *qt.QFont { return font(style.fonts.Feed, style.fonts.Feed.Size, qt.QFont__Normal) }

// monoFamily — моноширинное семейство (время сообщений, код, плеер).
func monoFamily() string { return style.fonts.Mono.Family }

// feedPx — размер из макета (там лента 14 px) при выбранном размере ленты.
func feedPx(n int) int {
	return int(math.Round(float64(n) * float64(style.fonts.Feed.Size) / float64(defaultFonts.Feed.Size)))
}

// fontSizeRe — размеры в HTML ленты: моноширинный (код, время) и остальной текст.
var fontSizeRe = regexp.MustCompile(`(font-family:'PT Mono';)?font-size:(\d+)px`)

// scaleFonts — HTML ленты пишется в пикселях макета (лента 14 px, моно 13 px):
// перед вставкой в документ размеры пересчитываются под выбранные шрифты, а
// моноширинное семейство подменяется выбранным. Одно место вместо правки
// каждой строки разметки.
func scaleFonts(html string) string {
	f := style.fonts
	if f == defaultFonts {
		return html
	}
	return fontSizeRe.ReplaceAllStringFunc(html, func(m string) string {
		sub := fontSizeRe.FindStringSubmatch(m)
		n, _ := strconv.Atoi(sub[2])
		if sub[1] != "" {
			return fmt.Sprintf("font-family:'%s';font-size:%dpx", f.Mono.Family, max(minFontSize-2, n-defaultFonts.Mono.Size+f.Mono.Size))
		}
		return fmt.Sprintf("font-size:%dpx", feedPx(n))
	})
}

// showFontsDialog — «Вид → Шрифты…»: семейство и размер для сайдбара, ленты и
// моноширинного; меняются сразу, «Сбросить» — по дизайну.
func showFontsDialog(parent *qt.QWidget) {
	d := newDialog(parent, "Шрифты")
	type row struct {
		family *qt.QFontComboBox
		size   *qt.QSpinBox
	}
	var rows []row
	current := func() config.Fonts {
		get := func(r row) config.Font {
			return config.Font{Family: r.family.CurrentFont().Family(), Size: r.size.Value()}
		}
		return config.Fonts{Sidebar: get(rows[0]), Feed: get(rows[1]), Mono: get(rows[2])}
	}
	applying := false
	apply := func() {
		if applying {
			return
		}
		f := current()
		SetFonts(f)
		if OnFontsChosen != nil {
			OnFontsChosen(f)
		}
	}
	set := func(f config.Fonts) {
		applying = true
		for i, v := range []config.Font{f.Sidebar, f.Feed, f.Mono} {
			qf := qt.NewQFont()
			qf.SetFamily(v.Family)
			rows[i].family.SetCurrentFont(qf)
			rows[i].size.SetValue(v.Size)
		}
		applying = false
	}
	grid := qt.NewQGridLayout2()
	grid.SetHorizontalSpacing(8)
	grid.SetVerticalSpacing(8)
	for i, title := range []string{"Сайдбар", "Лента и поле ввода", "Моноширинный: код, время"} {
		fam := qt.NewQFontComboBox2()
		if i == 2 {
			fam.SetFontFilters(qt.QFontComboBox__MonospacedFonts)
		}
		size := qt.NewQSpinBox2()
		size.SetRange(minFontSize, maxFontSize)
		size.SetSuffix(" px")
		fam.OnCurrentFontChanged(func(*qt.QFont) { apply() })
		size.OnValueChanged(func(int) { apply() })
		grid.AddWidget2(fieldLabel(title).QWidget, i, 0)
		grid.AddWidget2(fam.QWidget, i, 1)
		grid.AddWidget2(size.QWidget, i, 2)
		rows = append(rows, row{fam, size})
	}
	grid.SetColumnStretch(1, 1)
	d.lay.AddLayout(grid.QLayout)
	set(style.fonts)

	reset := qt.NewQPushButton3("Сбросить")
	reset.OnClicked(func() {
		set(defaultFonts)
		SetFonts(config.Fonts{})
		if OnFontsChosen != nil {
			OnFontsChosen(config.Fonts{})
		}
	})
	d.bottom.AddWidget(reset.QWidget)
	d.bottom.AddStretch()
	done := qt.NewQPushButton3("Готово")
	done.SetProperty("primary", qt.NewQVariant11("true"))
	done.SetDefault(true)
	done.OnClicked(func() { d.w.Close() })
	d.bottom.AddWidget(done.QWidget)
	d.w.SetMinimumWidth(520)
	d.w.Show()
}
