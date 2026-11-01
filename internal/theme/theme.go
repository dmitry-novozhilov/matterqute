// Package theme — визуальная система из дизайн-пакета
// (docs/design_handoff_matterqute): палитры светлой и тёмной темы, таблицы
// стилей, иконки, шрифты. Без Qt: применяет всё это internal/ui.
package theme

import (
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
)

//go:embed tokens.json qss/*.qss icons/*.svg fonts/*.ttf
var files embed.FS

// Palette — цвета одной темы (tokens.json → color.light / color.dark).
type Palette struct {
	Dark bool

	Win, Side, Surf, Surf2, Hover string
	Line, Line2                   string
	Tx, Tx2, Tx3                  string
	Acc, AccH, AccW, SelTx, OnAcc string
	SelBg                         string
	MenBg, MenTx, MenRow          string
	Good, Warn, Dang              string
	GoodBg, WarnBg, DangBg        string
	Avatars                       [][2]string // [фон, инициалы]; индекс — hash(username) % len
}

var Light, Dark Palette

func init() {
	data, err := files.ReadFile("tokens.json")
	if err != nil {
		panic(err)
	}
	var t struct {
		Color struct {
			Light, Dark map[string]string
			Avatar      struct{ Light, Dark [][2]string }
		}
	}
	if err := json.Unmarshal(data, &t); err != nil {
		panic(fmt.Sprintf("theme: parse tokens.json: %v", err))
	}
	Light = palette(t.Color.Light, t.Color.Avatar.Light, false)
	Dark = palette(t.Color.Dark, t.Color.Avatar.Dark, true)
}

func palette(c map[string]string, avatars [][2]string, dark bool) Palette {
	get := func(k string) string {
		v, ok := c[k]
		if !ok {
			panic(fmt.Sprintf("theme: tokens.json: no color %q", k))
		}
		return v
	}
	return Palette{
		Dark: dark,
		Win:  get("win"), Side: get("side"), Surf: get("surf"), Surf2: get("surf2"), Hover: get("hover"),
		Line: get("line"), Line2: get("line2"),
		Tx: get("tx"), Tx2: get("tx2"), Tx3: get("tx3"),
		Acc: get("acc"), AccH: get("accH"), AccW: get("accW"), SelTx: get("selTx"), OnAcc: get("onAcc"),
		SelBg: get("selBg"),
		MenBg: get("menBg"), MenTx: get("menTx"), MenRow: get("menRow"),
		Good: get("good"), Warn: get("warn"), Dang: get("dang"),
		GoodBg: get("goodBg"), WarnBg: get("warnBg"), DangBg: get("dangBg"),
		Avatars: avatars,
	}
}

// QSS — таблица стилей виджетов для темы.
func (p Palette) QSS() string {
	name := "qss/light.qss"
	if p.Dark {
		name = "qss/dark.qss"
	}
	data, err := files.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// AttachmentColor — цвет полосы вложения интеграции: именованные цвета —
// семантические цвета темы, свой HEX — как есть.
func (p Palette) AttachmentColor(c string) string {
	switch c {
	case "good":
		return p.Good
	case "warning":
		return p.Warn
	case "danger":
		return p.Dang
	}
	return ""
}

// Avatar — цвета аватара-инициалов [фон, текст]: последняя пара — у ботов
// (tokens.json: «index 4 is used for bots»), люди делят остальные.
func (p Palette) Avatar(username string, bot bool) (bg, fg string) {
	i := len(p.Avatars) - 1
	if !bot {
		h := fnv.New32a()
		h.Write([]byte(username))
		i = int(h.Sum32() % uint32(len(p.Avatars)-1))
	}
	return p.Avatars[i][0], p.Avatars[i][1]
}

// Icon — SVG иконки из дизайн-пакета с цветом color вместо currentColor.
func Icon(name, color string) ([]byte, error) {
	data, err := files.ReadFile("icons/" + name + ".svg")
	if err != nil {
		return nil, fmt.Errorf("icon %q: %w", name, err)
	}
	return []byte(strings.ReplaceAll(string(data), "currentColor", color)), nil
}

// Font — встроенный шрифт.
type Font struct {
	Name string
	Data []byte
}

// Fonts — IBM Plex Sans (400, 500, 600, курсив), PT Mono и Noto Color Emoji.
func Fonts() []Font {
	entries, err := files.ReadDir("fonts")
	if err != nil {
		panic(err)
	}
	var out []Font
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".ttf") {
			data, _ := files.ReadFile("fonts/" + e.Name())
			out = append(out, Font{Name: e.Name(), Data: data})
		}
	}
	return out
}

// Семейства шрифтов; эмодзи — запасной после основного.
const (
	SansFamily  = "IBM Plex Sans"
	MonoFamily  = "PT Mono"
	EmojiFamily = "Noto Color Emoji"
)
