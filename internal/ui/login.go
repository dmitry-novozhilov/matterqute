package ui

import (
	qt "github.com/mappu/miqt/qt6"
)

// LoginForm — что ввёл пользователь.
type LoginForm struct {
	Server, LoginID, Password, MFA string
}

// LoginView — страница входа: форма по центру окна. Сеть не трогает: отдаёт
// ввод в OnSubmit, а владелец переключает состояния (SetResuming, SetBusy,
// AskMFA, SetFieldError, SetUnavailable, SetExpired, SetError).
type LoginView struct {
	w        *qt.QWidget
	form     *qt.QFrame
	notice   *qt.QFrame
	nIcon    *qt.QLabel
	nTitle   *qt.QLabel
	nText    *qt.QLabel
	server   *qt.QLineEdit
	login    *qt.QLineEdit
	password *qt.QLineEdit
	mfa      *qt.QLineEdit
	mfaLabel *qt.QLabel
	mfaHint  *qt.QLabel
	pwError  *qt.QLabel
	mfaError *qt.QLabel
	logo     *qt.QLabel
	retry    *qt.QPushButton
	submit   *qt.QPushButton

	noticeKind  string
	noticeIconN string
	spin        *spinner // крутит иконку плашки «Подключаемся…»
	mfaStage    bool
	unsubscribe func()

	OnSubmit func(LoginForm)
	OnRetry  func() // повторить вход по сохранённому токену
}

func setName(o *qt.QObject, name string) { o.SetObjectName(*qt.NewQAnyStringView3(name)) }

func fieldLabel(text string) *qt.QLabel {
	l := qt.NewQLabel3(text)
	l.SetProperty("role", qt.NewQVariant11("fieldLabel"))
	return l
}

func roleLabel(role string) *qt.QLabel {
	l := qt.NewQLabel2()
	l.SetWordWrap(true)
	l.SetProperty("role", qt.NewQVariant11(role))
	l.SetVisible(false)
	return l
}

func NewLoginView(server, loginID string) *LoginView {
	l := &LoginView{w: qt.NewQWidget2(), form: qt.NewQFrame2()}
	setName(l.form.QObject, "loginForm")
	l.form.SetFixedWidth(360)

	l.logo = qt.NewQLabel2()
	title := qt.NewQLabel3("matterqute")
	title.SetFont(pixelFont(20, qt.QFont__DemiBold))
	sub := qt.NewQLabel3("Вход в Mattermost")
	sub.SetProperty("role", qt.NewQVariant11("hint"))
	heads := qt.NewQVBoxLayout2()
	heads.SetSpacing(0)
	heads.AddWidget(title.QWidget)
	heads.AddWidget(sub.QWidget)
	head := qt.NewQHBoxLayout2()
	head.SetSpacing(12)
	head.AddWidget(l.logo.QWidget)
	head.AddLayout(heads.QLayout)
	head.AddStretch()

	l.notice = qt.NewQFrame2()
	setName(l.notice.QObject, "loginNotice")
	l.nIcon, l.nTitle, l.nText = qt.NewQLabel2(), qt.NewQLabel2(), qt.NewQLabel2()
	l.spin = newSpinner(l.nIcon.QObject, l.nIcon.SetPixmap)
	l.nTitle.SetFont(pixelFont(13, qt.QFont__DemiBold))
	l.nTitle.SetWordWrap(true)
	l.nText.SetWordWrap(true)
	l.nText.SetTextInteractionFlags(qt.TextSelectableByMouse)
	nt := qt.NewQVBoxLayout2()
	nt.SetSpacing(2)
	nt.AddWidget(l.nTitle.QWidget)
	nt.AddWidget(l.nText.QWidget)
	nl := qt.NewQHBoxLayout(l.notice.QWidget)
	nl.SetContentsMargins(10, 8, 10, 8)
	nl.SetSpacing(8)
	nl.AddWidget3(l.nIcon.QWidget, 0, qt.AlignTop)
	nl.AddLayout2(nt.QLayout, 1)
	l.notice.SetVisible(false)

	l.server = qt.NewQLineEdit3(server)
	l.server.SetPlaceholderText("mm.example.com")
	l.login = qt.NewQLineEdit3(loginID)
	l.login.SetPlaceholderText("Email или имя пользователя")
	l.password = qt.NewQLineEdit2()
	l.password.SetEchoMode(qt.QLineEdit__Password)
	l.mfa = qt.NewQLineEdit2()
	l.mfa.SetPlaceholderText("6 цифр из приложения-аутентификатора")
	l.mfaLabel = fieldLabel("Код MFA")
	l.mfaHint = roleLabel("hint")
	l.mfaHint.SetText("Введите код двухфакторной аутентификации.")
	l.pwError = roleLabel("error")
	l.mfaError = roleLabel("error")
	l.mfa.SetVisible(false)
	l.mfaLabel.SetVisible(false)

	l.retry = qt.NewQPushButton3("Повторить")
	l.retry.SetVisible(false)
	l.retry.OnClicked(func() {
		if l.OnRetry != nil {
			l.OnRetry()
		}
	})
	l.submit = qt.NewQPushButton3("Войти")
	l.submit.SetProperty("primary", qt.NewQVariant11("true"))
	l.submit.SetDefault(true)
	l.submit.OnClicked(l.doSubmit)
	buttons := qt.NewQHBoxLayout2()
	buttons.SetSpacing(8)
	buttons.AddStretch()
	buttons.AddWidget(l.retry.QWidget)
	buttons.AddWidget(l.submit.QWidget)

	fl := qt.NewQVBoxLayout(l.form.QWidget)
	fl.SetContentsMargins(24, 24, 24, 20)
	fl.SetSpacing(0)
	fl.AddLayout(head.QLayout)
	fl.AddSpacing(16)
	fl.AddWidget(l.notice.QWidget)
	field := func(label *qt.QLabel, edit *qt.QLineEdit, after ...*qt.QLabel) {
		fl.AddSpacing(14)
		fl.AddWidget(label.QWidget)
		fl.AddSpacing(4)
		fl.AddWidget(edit.QWidget)
		for _, a := range after {
			fl.AddSpacing(4)
			fl.AddWidget(a.QWidget)
		}
	}
	field(fieldLabel("Сервер"), l.server)
	field(fieldLabel("Логин"), l.login)
	field(fieldLabel("Пароль"), l.password, l.pwError)
	field(l.mfaLabel, l.mfa, l.mfaError, l.mfaHint)
	fl.AddSpacing(18)
	fl.AddLayout(buttons.QLayout)

	page := qt.NewQVBoxLayout(l.w)
	page.AddStretch()
	row := qt.NewQHBoxLayout2()
	row.AddStretch()
	row.AddWidget(l.form.QWidget)
	row.AddStretch()
	page.AddLayout(row.QLayout)
	page.AddStretch()

	for _, e := range []*qt.QLineEdit{l.server, l.login, l.password, l.mfa} {
		e.OnReturnPressed(l.doSubmit)
		e.OnTextEdited(func(string) { l.clearFieldErrors() })
	}
	l.unsubscribe = OnThemeChanged(l.restyle)
	l.restyle()
	return l
}

func (l *LoginView) Widget() *qt.QWidget { return l.w }

// Dispose отписывает страницу от смены темы; сам виджет удаляет владелец.
func (l *LoginView) Dispose() { l.unsubscribe() }

// Focus ставит курсор в первое незаполненное поле.
func (l *LoginView) Focus() {
	switch {
	case l.mfaStage:
		l.mfa.SetFocus()
	case l.server.Text() == "":
		l.server.SetFocus()
	case l.login.Text() == "":
		l.login.SetFocus()
	default:
		l.password.SetFocus()
	}
}

func (l *LoginView) restyle() {
	p := Pal()
	l.logo.SetPixmap(appIconPixmap(40))
	l.spin.stop()
	if l.noticeIconN != "" {
		color := map[string]string{"info": p.Tx2, "warn": p.Warn, "danger": p.Dang}[l.noticeKind]
		if l.noticeIconN == "loader-circle" && l.noticeKind != "" {
			l.spin.start(color, 16)
		} else {
			l.nIcon.SetPixmap(iconPixmap(l.noticeIconN, color, 16))
		}
	}
	l.retry.SetIcon(icon("refresh-cw", p.Tx, 16))
	l.nText.SetStyleSheet("color:" + p.Tx2)
}

func (l *LoginView) doSubmit() {
	if !l.submit.IsEnabled() || l.OnSubmit == nil {
		return
	}
	l.OnSubmit(LoginForm{Server: l.server.Text(), LoginID: l.login.Text(), Password: l.password.Text(), MFA: l.mfa.Text()})
}

// showNotice — плашка над полями: kind info/warn/danger; "" — спрятать.
func (l *LoginView) showNotice(kind, iconName, title, text string, mono bool) {
	l.noticeKind, l.noticeIconN = kind, iconName
	l.notice.SetVisible(kind != "")
	if kind == "" {
		l.spin.stop()
		return
	}
	setProp(l.notice.QWidget, "kind", kind)
	l.nTitle.SetText(title)
	l.nText.SetText(text)
	l.nText.SetVisible(text != "")
	f := pixelFont(12, qt.QFont__Normal)
	if mono {
		f.SetFamilies([]string{monoFamily()})
	}
	l.nText.SetFont(f)
	l.restyle()
}

func (l *LoginView) setEditable(on bool) {
	// Принятые сервером логин и пароль на шаге MFA не меняются.
	creds := on && !l.mfaStage
	for _, w := range []*qt.QWidget{l.server.QWidget, l.login.QWidget, l.password.QWidget} {
		w.SetEnabled(creds)
	}
	l.mfa.SetEnabled(on)
	l.submit.SetEnabled(on)
	l.retry.SetEnabled(on)
}

func (l *LoginView) clearFieldErrors() {
	for _, e := range []struct {
		label *qt.QLabel
		edit  *qt.QLineEdit
	}{{l.pwError, l.password}, {l.mfaError, l.mfa}} {
		if !e.label.IsHidden() {
			e.label.SetVisible(false)
			setProp(e.edit.QWidget, "error", "false")
		}
	}
}

// SetResuming — вход по сохранённой сессии: плашка с загрузкой, всё неактивно.
func (l *LoginView) SetResuming(serverURL string) {
	l.showNotice("info", "loader-circle", "Подключаемся к "+hostOf(serverURL)+"…", "", false)
	l.retry.SetVisible(false)
	l.setEditable(false)
}

// SetBusy — запрос входа ушёл на сервер.
func (l *LoginView) SetBusy() {
	l.showNotice("", "", "", "", false)
	l.clearFieldErrors()
	l.retry.SetVisible(false)
	l.setEditable(false)
	l.submit.SetText("Входим…")
}

func (l *LoginView) ready() {
	l.submit.SetText("Войти")
	l.setEditable(true)
	l.Focus()
}

// AskMFA показывает поле кода: логин и пароль сервер уже принял.
func (l *LoginView) AskMFA() {
	l.mfaStage = true
	l.mfaLabel.SetVisible(true)
	l.mfa.SetVisible(true)
	l.mfaHint.SetVisible(true)
	l.mfa.Clear()
	l.ready()
}

// SetFieldError — ошибка под паролем (неверный логин или пароль) или под
// кодом MFA (неверный код).
func (l *LoginView) SetFieldError(msg string) {
	label, edit := l.pwError, l.password
	if l.mfaStage {
		label, edit = l.mfaError, l.mfa
	}
	label.SetText(msg)
	label.SetVisible(true)
	setProp(edit.QWidget, "error", "true")
	l.ready()
	edit.SelectAll()
}

// SetUnavailable — сервер не ответил: причина моноширинным текстом и «Повторить».
func (l *LoginView) SetUnavailable(details string, canRetry bool) {
	l.showNotice("danger", "wifi-off", "Сервер недоступен", details, true)
	l.retry.SetVisible(canRetry)
	l.ready()
}

// SetExpired — сохранённая сессия больше не действует.
func (l *LoginView) SetExpired() {
	l.showNotice("warn", "clock", "Сессия истекла", "Войдите заново.", false)
	l.retry.SetVisible(false)
	l.ready()
}

// SetError — прочие ошибки входа.
func (l *LoginView) SetError(msg string) {
	l.showNotice("danger", "triangle-alert", "Не удалось войти", msg, false)
	l.retry.SetVisible(false)
	l.ready()
}
