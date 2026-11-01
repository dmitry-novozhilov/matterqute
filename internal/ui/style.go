package ui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/svg"

	"matterqute/internal/qtx"
	"matterqute/internal/theme"
)

// ThemeMode — выбор в меню «Вид → Тема».
type ThemeMode string

const (
	ThemeSystem ThemeMode = "system"
	ThemeLight  ThemeMode = "light"
	ThemeDark   ThemeMode = "dark"
)

// Плотность картинок, которые рисуем сами: вдвое крупнее, чтобы на HiDPI было чётко.
const resourceDPR = 2

// style — состояние оформления приложения; только из главного потока.
var style = struct {
	app       *qt.QApplication
	mode      ThemeMode
	pal       theme.Palette
	applied   bool
	listeners map[int]func()
	nextID    int
	icons     map[string]*qt.QPixmap
	avatars   map[string]*qt.QPixmap
}{mode: ThemeSystem, pal: theme.Light, listeners: map[int]func(){}, icons: map[string]*qt.QPixmap{}, avatars: map[string]*qt.QPixmap{}}

// Pal — палитра текущей темы.
func Pal() theme.Palette { return style.pal }

// InitStyle регистрирует шрифты и применяет тему. Вызывать после создания QApplication.
func InitStyle(app *qt.QApplication, mode ThemeMode, log *slog.Logger) {
	style.app = app
	for _, f := range theme.Fonts() {
		if qt.QFontDatabase_AddApplicationFontFromData(f.Data) < 0 {
			log.Warn("load font", "file", f.Name)
		}
	}
	font := qt.NewQFont()
	// Эмодзи — запасной шрифт: иначе Qt берёт первый попавшийся, часто чёрно-белый.
	font.SetFamilies([]string{theme.SansFamily, theme.EmojiFamily})
	font.SetPixelSize(14)
	qt.QApplication_SetFont(font)
	qtx.WatchSystemScheme(func(bool) {
		if style.mode == ThemeSystem {
			applyTheme()
		}
	})
	SetThemeMode(mode)
}

func ThemeModeNow() ThemeMode { return style.mode }

func SetThemeMode(mode ThemeMode) {
	switch mode {
	case ThemeLight, ThemeDark:
	default:
		mode = ThemeSystem
	}
	style.mode = mode
	applyTheme()
}

func applyTheme() {
	pal := theme.Light
	switch style.mode {
	case ThemeDark:
		pal = theme.Dark
	case ThemeSystem:
		if dark, known := qtx.SystemDark(); known && dark {
			pal = theme.Dark
		}
	}
	if style.applied && pal.Dark == style.pal.Dark {
		return
	}
	style.pal, style.applied = pal, true
	style.icons = map[string]*qt.QPixmap{}
	style.avatars = map[string]*qt.QPixmap{}

	qp := qt.NewQPalette()
	set := func(role qt.QPalette__ColorRole, c string) { qp.SetColor2(role, qt.NewQColor6(c)) }
	set(qt.QPalette__Window, pal.Win)
	set(qt.QPalette__WindowText, pal.Tx)
	set(qt.QPalette__Base, pal.Surf)
	set(qt.QPalette__Text, pal.Tx)
	set(qt.QPalette__Button, pal.Surf)
	set(qt.QPalette__ButtonText, pal.Tx)
	set(qt.QPalette__Highlight, pal.SelBg)
	set(qt.QPalette__HighlightedText, pal.Tx)
	set(qt.QPalette__Link, pal.Acc)
	set(qt.QPalette__LinkVisited, pal.Acc)
	set(qt.QPalette__PlaceholderText, pal.Tx3)
	set(qt.QPalette__ToolTipBase, pal.Tx)
	qt.QApplication_SetPalette(qp)
	if style.app != nil {
		style.app.SetStyleSheet(pal.QSS())
	}
	for _, f := range style.listeners {
		f()
	}
}

// OnThemeChanged подписывает f на смену темы; вернёт отписку (звать при закрытии окна).
func OnThemeChanged(f func()) (remove func()) {
	id := style.nextID
	style.nextID++
	style.listeners[id] = f
	return func() { delete(style.listeners, id) }
}

// iconPixmap — иконка из дизайн-пакета цветом color размером size×size (логических px).
func iconPixmap(name, color string, size int) *qt.QPixmap {
	key := fmt.Sprintf("%s|%s|%d", name, color, size)
	if pm := style.icons[key]; pm != nil {
		return pm
	}
	img := qt.NewQImage3(size*resourceDPR, size*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
	img.Fill2(qt.Transparent)
	if data, err := theme.Icon(name, color); err == nil {
		r := svg.NewQSvgRenderer3(data)
		p := qt.NewQPainter2(img.QPaintDevice)
		r.Render2(p, qt.NewQRectF4(0, 0, float64(size*resourceDPR), float64(size*resourceDPR)))
		p.End()
		r.Delete()
	}
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	style.icons[key] = pm
	return pm
}

func icon(name, color string, size int) *qt.QIcon { return qt.NewQIcon2(iconPixmap(name, color, size)) }

// dotPixmap — круглая точка статуса.
func dotPixmap(color string, size int) *qt.QPixmap {
	img := qt.NewQImage3(size*resourceDPR, size*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
	img.Fill2(qt.Transparent)
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	p.SetPenWithStyle(qt.NoPen)
	p.SetBrush(qt.NewQBrush3(qt.NewQColor6(color)))
	p.DrawEllipse2(0, 0, size*resourceDPR, size*resourceDPR)
	p.End()
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	return pm
}

// appIconPixmap — иконка приложения, вариант 1b «Канал»: белое облачко с
// зелёной решёткой на зелёной плитке (docs/design_handoff_matterqute/design/app-icon.dc.html).
func appIconPixmap(size int) *qt.QPixmap {
	const tile, glyph = "#008a67", "#fcfbf9"
	s := float64(size * resourceDPR)
	img := qt.NewQImage3(size*resourceDPR, size*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
	img.Fill2(qt.Transparent)
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	p.SetPenWithStyle(qt.NoPen)
	p.SetBrush(qt.NewQBrush3(qt.NewQColor6(tile)))
	p.DrawRoundedRect(qt.NewQRectF4(0, 0, s, s), s*0.22, s*0.22)
	// Облачко: скруглённый прямоугольник с хвостиком снизу слева.
	p.SetBrush(qt.NewQBrush3(qt.NewQColor6(glyph)))
	p.DrawRoundedRect(qt.NewQRectF4(s*0.18, s*0.2, s*0.64, s*0.5), s*0.2, s*0.2)
	p.DrawEllipse(qt.NewQRectF4(s*0.2, s*0.52, s*0.22, s*0.24))
	hash := iconPixmap("hash", tile, size)
	p.DrawPixmap(qt.NewQRectF4(s*0.31, s*0.26, s*0.38, s*0.38), hash, qt.NewQRectF4(0, 0, float64(size*resourceDPR), float64(size*resourceDPR)))
	p.End()
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	return pm
}

func appIcon() *qt.QIcon {
	ic := qt.NewQIcon()
	for _, size := range []int{16, 32, 48, 64, 128} {
		ic.AddPixmap(appIconPixmap(size))
	}
	return ic
}

// pixelFont — шрифт приложения заданного размера и начертания.
func pixelFont(px int, weight qt.QFont__Weight) *qt.QFont {
	f := qt.NewQFont()
	f.SetFamilies([]string{theme.SansFamily, theme.EmojiFamily})
	f.SetPixelSize(px)
	f.SetWeight(weight)
	return f
}

// repolish пересчитывает стиль виджета после смены динамического свойства.
func repolish(w *qt.QWidget) {
	w.Style().Unpolish(w)
	w.Style().Polish(w)
	w.Update()
}

// setProp ставит динамическое свойство для QSS ([error="true"] и т. п.) и перерисовывает.
func setProp(w *qt.QWidget, name string, value string) {
	w.SetProperty(name, qt.NewQVariant11(value))
	repolish(w)
}

// SaveAppIcon пишет иконку приложения в PNG (для уведомлений рабочего стола).
func SaveAppIcon(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("app icon %s: %w", path, err)
	}
	if !appIconPixmap(128).Save(path) {
		return fmt.Errorf("app icon %s: QPixmap::save failed", path)
	}
	return nil
}
