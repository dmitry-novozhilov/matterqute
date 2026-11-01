package ui

import (
	"fmt"
	"strings"

	qt "github.com/mappu/miqt/qt6"
)

// ConnState — что показывает строка состояния справа.
type ConnState int

const (
	ConnNone         ConnState = iota // не подключено (окно входа)
	ConnConnecting                    // подключаемся
	ConnOnline                        // в сети
	ConnReconnecting                  // обрыв, переподключаемся
	ConnUnavailable                   // сервер недоступен
)

// Shell — единственное окно приложения: меню, строка состояния, а в центре
// вход или окно сервера.
type Shell struct {
	w      *qt.QMainWindow
	stack  *qt.QStackedWidget
	user   *qt.QLabel
	server *qt.QLabel
	dot    *qt.QLabel
	status *qt.QLabel
	conn   ConnState

	logout     *qt.QAction
	themeActs  map[ThemeMode]*qt.QAction
	serverMenu *qt.QMenu

	OnLogout func()
}

func NewShell() *Shell {
	s := &Shell{w: qt.NewQMainWindow2(), stack: qt.NewQStackedWidget2(), themeActs: map[ThemeMode]*qt.QAction{}}
	s.w.SetWindowTitle("matterqute")
	s.w.SetWindowIcon(appIcon())
	s.w.Resize(1280, 800)
	s.w.SetMinimumSize2(900, 600)
	s.w.SetCentralWidget(s.stack.QWidget)

	file := s.w.MenuBar().AddMenuWithTitle("&Файл")
	s.logout = file.QWidget.AddActionWithText("Выйти из аккаунта")
	s.logout.OnTriggered(func() {
		if s.OnLogout != nil {
			s.OnLogout()
		}
	})
	file.AddSeparator()
	quit := file.QWidget.AddActionWithText("Выход")
	quit.SetShortcut(qt.NewQKeySequence2("Ctrl+Q"))
	quit.OnTriggered(qt.QCoreApplication_Quit)

	view := s.w.MenuBar().AddMenuWithTitle("&Вид")
	themes := view.AddMenuWithTitle("Тема")
	group := qt.NewQActionGroup(s.w.QObject)
	for _, t := range []struct {
		mode  ThemeMode
		title string
	}{{ThemeSystem, "Системная"}, {ThemeLight, "Светлая"}, {ThemeDark, "Тёмная"}} {
		a := themes.QWidget.AddActionWithText(t.title)
		a.SetCheckable(true)
		group.AddAction(a)
		mode := t.mode
		a.OnTriggered(func() {
			SetThemeMode(mode)
			if OnThemeModeChosen != nil {
				OnThemeModeChosen(mode)
			}
		})
		s.themeActs[t.mode] = a
	}
	s.themeActs[ThemeModeNow()].SetChecked(true)

	s.serverMenu = s.w.MenuBar().AddMenuWithTitle("&Сервер")
	s.SetServer("")

	s.user = qt.NewQLabel2()
	s.server = qt.NewQLabel2()
	s.dot = qt.NewQLabel2()
	s.status = qt.NewQLabel2()
	left := qt.NewQWidget2()
	ll := qt.NewQHBoxLayout(left)
	ll.SetContentsMargins(10, 0, 0, 0)
	ll.SetSpacing(8)
	ll.AddWidget(s.user.QWidget)
	ll.AddWidget(s.server.QWidget)
	right := qt.NewQWidget2()
	rl := qt.NewQHBoxLayout(right)
	rl.SetContentsMargins(0, 0, 10, 0)
	rl.SetSpacing(6)
	rl.AddWidget(s.dot.QWidget)
	rl.AddWidget(s.status.QWidget)
	s.w.StatusBar().AddWidget(left)
	s.w.StatusBar().AddPermanentWidget(right)
	s.w.StatusBar().SetSizeGripEnabled(false)
	s.SetUser("", "")
	s.SetConnection(ConnNone)
	OnThemeChanged(s.restyle)
	s.restyle()
	return s
}

// OnThemeModeChosen — пользователь выбрал тему в меню (сохранить в настройках).
var OnThemeModeChosen func(ThemeMode)

func (s *Shell) Show() { s.w.Show() }

// SetPage показывает страницу (вход или окно сервера) в центре окна.
func (s *Shell) SetPage(w *qt.QWidget) {
	if s.stack.IndexOf(w) < 0 {
		s.stack.AddWidget(w)
	}
	s.stack.SetCurrentWidget(w)
}

// RemovePage убирает страницу и удаляет её виджеты.
func (s *Shell) RemovePage(w *qt.QWidget) {
	s.stack.RemoveWidget(w)
	w.DeleteLater()
}

// SetUser — «@alice  mm.example.com» слева в строке состояния; пусто — окно входа.
func (s *Shell) SetUser(username, serverURL string) {
	s.logout.SetEnabled(username != "")
	if username == "" {
		s.user.SetText("")
		s.server.SetText("")
		return
	}
	s.user.SetText("@" + username)
	s.server.SetText(hostOf(serverURL))
}

// SetServer — пункты меню «Сервер»: текущий сервер с галочкой. Несколько
// серверов и «Добавить сервер…» — следующий этап.
func (s *Shell) SetServer(serverURL string) {
	s.serverMenu.Clear()
	if serverURL != "" {
		a := s.serverMenu.QWidget.AddActionWithText(hostOf(serverURL))
		a.SetCheckable(true)
		a.SetChecked(true)
		a.SetShortcut(qt.NewQKeySequence2("Ctrl+1"))
		s.serverMenu.AddSeparator()
	}
	add := s.serverMenu.QWidget.AddActionWithText("Добавить сервер…")
	add.SetEnabled(false)
}

// ShowMessage — короткое сообщение слева в строке состояния (на 10 с); "" — убрать.
func (s *Shell) ShowMessage(text string) {
	if text == "" {
		s.w.StatusBar().ClearMessage()
		return
	}
	s.w.StatusBar().ShowMessage2(text, 10000)
}

func (s *Shell) SetConnection(c ConnState) {
	s.conn = c
	s.restyle()
}

func (s *Shell) restyle() {
	p := Pal()
	text, color := "не подключено", p.Tx3
	switch s.conn {
	case ConnConnecting:
		text, color = "подключаемся…", p.Warn
	case ConnOnline:
		text, color = "в сети", p.Good
	case ConnReconnecting:
		text, color = "нет соединения, переподключаемся…", p.Warn
	case ConnUnavailable:
		text, color = "сервер недоступен", p.Dang
	}
	s.status.SetText(text)
	s.dot.SetPixmap(dotPixmap(color, 8))
	s.user.SetStyleSheet(fmt.Sprintf("font-weight:600;color:%s", p.Tx))
	s.server.SetStyleSheet("color:" + p.Tx2)
	s.status.SetStyleSheet("color:" + p.Tx2)
}

func hostOf(serverURL string) string {
	return strings.TrimPrefix(strings.TrimPrefix(serverURL, "https://"), "http://")
}
