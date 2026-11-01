package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// Состояния окна входа: форма, ожидание, код MFA, ошибка с повтором.
func TestLoginDialogStates(t *testing.T) {
	onMain(func() {
		l := NewLoginDialog("mm.example.com", "alice")
		defer l.Close()
		l.Show()
		save := func(name string) {
			if dir := os.Getenv("MATTERQUTE_TEST_SHOTS"); dir != "" {
				settleFor(100e6)
				l.d.Grab().Save(filepath.Join(dir, name+".png"))
			}
		}
		save("login-form")

		l.SetBusy("Входим на https://mm.example.com…")
		if l.submit.IsEnabled() || !l.status.IsVisible() {
			t.Error("busy: submit enabled or status hidden")
		}
		l.AskMFA()
		if !l.form.IsRowVisibleWithWidget(l.mfa.QWidget) || !l.submit.IsEnabled() {
			t.Error("mfa: code row hidden or submit disabled")
		}
		save("login-mfa")

		l.SetError("resume session https://mm.example.com: dial tcp: connection refused", true)
		if !l.retry.IsVisible() {
			t.Error("error with retry: retry button hidden")
		}
		save("login-error")
	})
}
