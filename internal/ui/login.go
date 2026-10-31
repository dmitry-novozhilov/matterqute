package ui

import (
	qt "github.com/mappu/miqt/qt6"
)

// LoginForm — что ввёл пользователь.
type LoginForm struct {
	Server, LoginID, Password, MFA string
}

// LoginDialog — окно входа. Сеть не трогает: отдаёт ввод в OnSubmit,
// а владелец переключает состояния через SetBusy/AskMFA/SetError.
type LoginDialog struct {
	d        *qt.QDialog
	form     *qt.QFormLayout
	server   *qt.QLineEdit
	login    *qt.QLineEdit
	password *qt.QLineEdit
	mfa      *qt.QLineEdit
	status   *qt.QLabel
	submit   *qt.QPushButton
	retry    *qt.QPushButton

	OnSubmit func(LoginForm)
	OnRetry  func() // повторить вход по сохранённому токену
	OnClosed func() // окно закрыто: крестиком, Esc или через Close
}

func NewLoginDialog(server, loginID string) *LoginDialog {
	l := &LoginDialog{d: qt.NewQDialog2()}
	l.d.SetWindowTitle("Вход — matterqute")
	l.d.SetAttribute(qt.WA_DeleteOnClose)
	l.d.SetMinimumWidth(420)
	l.d.OnFinished(func(int) {
		if l.OnClosed != nil {
			l.OnClosed()
		}
	})

	l.server = qt.NewQLineEdit3(server)
	l.server.SetPlaceholderText("mm.example.com")
	l.login = qt.NewQLineEdit3(loginID)
	l.login.SetPlaceholderText("email или имя пользователя")
	l.password = qt.NewQLineEdit2()
	l.password.SetEchoMode(qt.QLineEdit__Password)
	l.mfa = qt.NewQLineEdit2()
	l.mfa.SetPlaceholderText("6 цифр из приложения-аутентификатора")

	l.form = qt.NewQFormLayout2()
	l.form.AddRow3("Сервер:", l.server.QWidget)
	l.form.AddRow3("Логин:", l.login.QWidget)
	l.form.AddRow3("Пароль:", l.password.QWidget)
	l.form.AddRow3("Код MFA:", l.mfa.QWidget)
	l.form.SetRowVisible2(l.mfa.QWidget, false)

	l.status = qt.NewQLabel2()
	l.status.SetWordWrap(true)
	l.status.SetTextFormat(qt.PlainText)
	l.status.SetTextInteractionFlags(qt.TextSelectableByMouse)
	l.status.SetVisible(false)

	l.retry = qt.NewQPushButton3("Повторить")
	l.retry.SetVisible(false)
	l.retry.OnClicked(func() {
		if l.OnRetry != nil {
			l.OnRetry()
		}
	})
	l.submit = qt.NewQPushButton3("Войти")
	l.submit.SetDefault(true)
	l.submit.OnClicked(l.doSubmit)

	buttons := qt.NewQHBoxLayout2()
	buttons.AddStretch()
	buttons.AddWidget(l.retry.QWidget)
	buttons.AddWidget(l.submit.QWidget)

	root := qt.NewQVBoxLayout(l.d.QWidget)
	root.AddLayout(l.form.QLayout)
	root.AddWidget(l.status.QWidget)
	root.AddLayout(buttons.QLayout)

	for _, e := range []*qt.QLineEdit{l.server, l.login, l.password, l.mfa} {
		e.OnReturnPressed(l.doSubmit)
	}
	return l
}

func (l *LoginDialog) Show() {
	l.d.Show()
	switch {
	case l.server.Text() == "":
		l.server.SetFocus()
	case l.login.Text() == "":
		l.login.SetFocus()
	default:
		l.password.SetFocus()
	}
}

func (l *LoginDialog) Close() { l.d.Close() }

func (l *LoginDialog) doSubmit() {
	if !l.submit.IsEnabled() || l.OnSubmit == nil {
		return
	}
	l.OnSubmit(LoginForm{
		Server:   l.server.Text(),
		LoginID:  l.login.Text(),
		Password: l.password.Text(),
		MFA:      l.mfa.Text(),
	})
}

// SetBusy блокирует ввод на время запроса и показывает, что происходит.
func (l *LoginDialog) SetBusy(msg string) {
	l.setInputEnabled(false)
	l.retry.SetVisible(false)
	l.showStatus(msg)
}

// AskMFA показывает поле кода: логин и пароль сервер уже принял.
func (l *LoginDialog) AskMFA() {
	l.setInputEnabled(true)
	l.form.SetRowVisible2(l.mfa.QWidget, true)
	l.mfa.Clear()
	l.mfa.SetFocus()
	l.showStatus("Введите код двухфакторной аутентификации.")
}

// SetError возвращает форму в редактируемое состояние; canRetry — показать
// кнопку повтора входа по сохранённому токену (сеть недоступна, а не токен плох).
func (l *LoginDialog) SetError(msg string, canRetry bool) {
	l.setInputEnabled(true)
	l.retry.SetVisible(canRetry)
	l.showStatus(msg)
	if l.form.IsRowVisibleWithWidget(l.mfa.QWidget) {
		l.mfa.SetFocus()
	} else {
		l.password.SetFocus()
	}
}

func (l *LoginDialog) setInputEnabled(on bool) {
	for _, w := range []*qt.QWidget{l.server.QWidget, l.login.QWidget, l.password.QWidget, l.mfa.QWidget, l.submit.QWidget, l.retry.QWidget} {
		w.SetEnabled(on)
	}
}

func (l *LoginDialog) showStatus(msg string) {
	l.status.SetText(msg)
	l.status.SetVisible(msg != "")
	// Перенесённый по словам текст сам окно не растягивает — поля сплющиваются.
	l.d.AdjustSize()
}
