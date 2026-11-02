package ui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/svg"

	"matterqute/internal/config"
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
	app        *qt.QApplication
	mode       ThemeMode
	pal        theme.Palette
	applied    bool
	listeners  map[int]func()
	nextID     int
	icons      map[string]*qt.QPixmap
	avatars    map[string]*qt.QPixmap
	fonts      config.Fonts // выбранные шрифты, умолчания подставлены
	formatBar  bool         // панель оформления над полем ввода
	typingShow bool         // показывать, кто печатает
	typingSend bool         // сообщать, что печатаю я
}{mode: ThemeSystem, pal: theme.Light, listeners: map[int]func(){}, icons: map[string]*qt.QPixmap{}, avatars: map[string]*qt.QPixmap{},
	fonts: defaultFonts, typingShow: true, typingSend: true}

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

// AppTitle — название приложения для людей: заголовок окна, меню, трей,
// уведомления. Служебное имя (бинарник, ярлык, настройки) — matterqute.
const AppTitle = "Matterqute"

// appGreen — плитка значка приложения; им же трей говорит о новых сообщениях.
const appGreen = "#008a67"

// appIconPixmap — иконка приложения size×size физических пикселей: зелёное
// облачко с белой «#», без плитки — как знак в трее, тоже по пиксельной сетке
// (у макета 1b «Канал» с плиткой и «#» шрифтом в мелких размерах решётка
// мылилась).
func appIconPixmap(size int) *qt.QPixmap {
	return bubblePixmap(size, appGreen, "#fcfbf9", "", 0)
}

// appIcon — иконка окна: каждый размер нарисован отдельно.
func appIcon() *qt.QIcon {
	ic := qt.NewQIcon()
	for _, size := range []int{16, 20, 22, 24, 32, 40, 48, 64, 96, 128, 256} {
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

// SaveAppIcon пишет иконку приложения в PNG px×px (уведомления, ярлык в меню).
func SaveAppIcon(path string, px int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("app icon %s: %w", path, err)
	}
	if !appIconPixmap(max(1, px)).Save(path) {
		return fmt.Errorf("app icon %s: QPixmap::save failed", path)
	}
	return nil
}
