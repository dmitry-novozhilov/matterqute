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
	w        *qt.QMainWindow
	stack    *qt.QStackedWidget
	user     *qt.QLabel
	mood     *qt.QLabel // свой статус: эмодзи рядом с @user
	server   *qt.QLabel
	myStatus *qt.QAction
	dot      *qt.QLabel
	status   *qt.QLabel
	conn     ConnState

	logout     *qt.QAction
	themeActs  map[ThemeMode]*qt.QAction
	serverMenu *qt.QMenu

	// HideOnClose — крестик прячет окно в трей, а не завершает приложение:
	// иначе уведомления перестанут приходить. Включается, когда трей есть.
	HideOnClose bool
	quitting    bool
	quitApp     func() // QCoreApplication::quit; в тестах — подделка

	OnLogout       func()
	OnClose        func()              // окно закрывается или прячется в трей: сохранить, что нужно
	OnSelectServer func(i int)         // выбран сервер в меню «Сервер»
	OnStatus       func(at *qt.QPoint) // открыть панель статуса (клик по @user или «Файл → Статус…»)
	OnAddServer    func()
}

// ServerItem — пункт меню «Сервер».
type ServerItem struct {
	Host     string
	Active   bool
	Mentions int64 // упоминания и непрочитанные лички
}

func NewShell() *Shell {
	s := &Shell{w: qt.NewQMainWindow2(), stack: qt.NewQStackedWidget2(), themeActs: map[ThemeMode]*qt.QAction{},
		quitApp: qt.QCoreApplication_Quit}
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
	s.myStatus = file.QWidget.AddActionWithText("Статус…")
	// Панель статуса — всегда над своим именем в строке состояния, и из меню тоже.
	s.myStatus.OnTriggered(func() {
		if s.OnStatus != nil {
			s.OnStatus(s.user.MapToGlobalWithQPoint(qt.NewQPoint2(0, 0)))
		}
	})
	file.AddSeparator()
	quit := file.QWidget.AddActionWithText("Выход")
	quit.SetShortcut(qt.NewQKeySequence2("Ctrl+Q"))
	quit.OnTriggered(s.Quit)
	s.w.OnCloseEvent(func(super func(*qt.QCloseEvent), e *qt.QCloseEvent) {
		if s.OnClose != nil {
			s.OnClose()
		}
		if s.HideOnClose && !s.quitting {
			e.Ignore()
			s.w.Hide()
			return
		}
		super(e)
	})

	view := s.w.MenuBar().AddMenuWithTitle("&Вид")
	themes := view.AddMenuWithTitle("Тема")
	fontsAct := view.QWidget.AddActionWithText("Шрифты…")
	fontsAct.OnTriggered(func() { showFontsDialog(s.w.QWidget) })
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
	s.SetServers(nil)

	s.user = qt.NewQLabel2()
	s.mood = qt.NewQLabel2()
	s.server = qt.NewQLabel2()
	// Клик по своему имени или статусу — панель статуса, как аватар в веб-клиенте.
	for _, l := range []*qt.QLabel{s.user, s.mood} {
		l.SetCursor(qt.NewQCursor2(qt.PointingHandCursor))
		l.SetToolTip("Статус")
		l.OnMousePressEvent(func(_ func(*qt.QMouseEvent), e *qt.QMouseEvent) {
			if e.Button() == qt.LeftButton && s.OnStatus != nil {
				s.OnStatus(s.user.MapToGlobalWithQPoint(qt.NewQPoint2(0, 0)))
			}
		})
	}
	s.dot = qt.NewQLabel2()
	s.status = qt.NewQLabel2()
	left := qt.NewQWidget2()
	ll := qt.NewQHBoxLayout(left)
	ll.SetContentsMargins(10, 0, 0, 0)
	ll.SetSpacing(8)
	ll.AddWidget(s.user.QWidget)
	ll.AddWidget(s.mood.QWidget)
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
	s.myStatus.SetEnabled(username != "")
	if username == "" {
		s.user.SetText("")
		s.server.SetText("")
		s.SetMood(nil, "")
		return
	}
	s.user.SetText("@" + username)
	s.server.SetText(hostOf(serverURL))
}

// SetMood — свой статус рядом с @user: эмодзи и подсказка; nil — статуса нет.
func (s *Shell) SetMood(pm *qt.QPixmap, tip string) {
	if pm == nil {
		s.mood.Clear()
		s.mood.SetVisible(false)
		return
	}
	s.mood.SetPixmap(pm)
	s.mood.SetToolTip(tip)
	s.mood.SetVisible(true)
}

// SetServers — меню «Сервер» (решение 6d): текущий сервер с галочкой, у
// остальных — счётчик упоминаний и непрочитанных личек, Ctrl+1…9. Пока меню
// закрыто, о новом на другом сервере говорит точка у заголовка «Сервер».
func (s *Shell) SetServers(items []ServerItem) {
	s.serverMenu.Clear()
	news := false
	for i, it := range items {
		text := it.Host
		if !it.Active && it.Mentions > 0 {
			text += "   " + fmt.Sprint(it.Mentions)
			news = true
		}
		a := s.serverMenu.QWidget.AddActionWithText(text)
		a.SetCheckable(true)
		a.SetChecked(it.Active)
		if i < 9 {
			a.SetShortcut(qt.NewQKeySequence2(fmt.Sprintf("Ctrl+%d", i+1)))
		}
		a.OnTriggered(func() {
			if s.OnSelectServer != nil {
				s.OnSelectServer(i)
			}
		})
	}
	if len(items) > 0 {
		s.serverMenu.AddSeparator()
	}
	add := s.serverMenu.QWidget.AddActionWithText("Добавить сервер…")
	add.SetIcon(icon("plus", Pal().Tx2, 16))
	add.OnTriggered(func() {
		if s.OnAddServer != nil {
			s.OnAddServer()
		}
	})
	// Цветную точку в заголовок пункта строки меню Qt не нарисует — ставим «•».
	title := "&Сервер"
	if news {
		title += " •"
	}
	s.serverMenu.SetTitle(title)
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

// Geometry — место и размер окна (и развёрнуто ли) для следующего запуска.
func (s *Shell) Geometry() []byte { return s.w.SaveGeometry() }

// RestoreGeometry возвращает окно туда, где его оставили; звать до Show. Если
// того экрана больше нет, Qt сам вернёт окно на видимый. Пустое — как по умолчанию.
func (s *Shell) RestoreGeometry(g []byte) {
	if len(g) > 0 {
		s.w.RestoreGeometry(g)
	}
}

// Quit завершает приложение. Видимое окно сначала закрывается — так
// срабатывает closeEvent и сохраняется его положение; спрятанное в трей уже
// сохранено, когда пряталось.
func (s *Shell) Quit() {
	s.quitting = true
	if s.w.IsVisible() {
		s.w.Close()
	}
	s.quitApp()
}

// Raise — окно поверх остальных и в фокусе, даже если было свёрнуто или в трее.
func (s *Shell) Raise() {
	s.w.SetWindowState(s.w.WindowState()&^qt.WindowMinimized | qt.WindowActive)
	s.w.Show()
	s.w.Raise()
	s.w.ActivateWindow()
}
