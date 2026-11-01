package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// Состояния страницы входа: форма, вход по сессии, код MFA, ошибки.
func TestLoginViewStates(t *testing.T) {
	onMain(func() {
		l := NewLoginView("mm.example.com", "alice")
		defer l.Dispose()
		w := l.Widget()
		w.Resize(900, 600)
		w.Show()
		defer w.Close()
		save := func(name string) {
			if dir := os.Getenv("MATTERQUTE_TEST_SHOTS"); dir != "" {
				settleFor(100e6)
				w.Grab().Save(filepath.Join(dir, name+".png"))
			}
		}
		save("login-form")

		l.SetResuming("https://mm.example.com")
		if l.submit.IsEnabled() || l.notice.IsHidden() || l.server.IsEnabled() {
			t.Error("resuming: form editable or notice hidden")
		}
		frame := l.spin.frame
		settleFor(200e6)
		if !l.spin.timer.IsActive() || l.spin.frame == frame {
			t.Errorf("resuming: spinner stands still (active=%v, frame %d → %d)", l.spin.timer.IsActive(), frame, l.spin.frame)
		}
		l.SetBusy()
		if l.submit.IsEnabled() || l.submit.Text() != "Входим…" {
			t.Errorf("busy: submit enabled=%v text=%q", l.submit.IsEnabled(), l.submit.Text())
		}
		l.AskMFA()
		if l.mfa.IsHidden() || !l.submit.IsEnabled() || l.password.IsEnabled() || l.server.IsEnabled() {
			t.Error("mfa: code hidden, submit disabled or accepted fields still editable")
		}
		save("login-mfa")
		l.SetFieldError("Неверный код. Проверьте время на телефоне и введите новый код.")
		if l.mfaError.IsHidden() || !l.pwError.IsHidden() {
			t.Error("mfa error shown under the wrong field")
		}

		l2 := NewLoginView("mm.example.com", "alice")
		defer l2.Dispose()
		l2.SetFieldError("Неверный логин или пароль.")
		if l2.pwError.IsHidden() {
			t.Error("credentials error not shown under password")
		}
		l2.SetUnavailable("resume session https://mm.example.com: dial tcp: connection refused", true)
		if l2.retry.IsHidden() || l2.notice.IsHidden() {
			t.Error("unavailable: retry or notice hidden")
		}
		l.SetUnavailable("resume session https://mm.example.com: dial tcp: connection refused", true)
		if l.spin.timer.IsActive() {
			t.Error("spinner still runs under «сервер недоступен»")
		}
		save("login-error")
	})
}
