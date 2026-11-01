package ui

import (
	"io"
	"log/slog"
	"testing"

	"matterqute/internal/config"
)

// Окно закрыли — место и размер в config.json; следующий запуск их восстановил.
func TestAppRemembersWindow(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	onMain(func() {
		cfg, err := config.Load(dir)
		if err != nil {
			t.Error(err)
			return
		}
		a := NewApp(cfg, nil, log) // без серверов: только страница входа, токены не нужны
		a.Start()
		// Экран offscreen — 800×600: окно должно на него помещаться.
		a.shell.w.SetMinimumSize2(100, 100)
		a.shell.w.Resize(640, 480)
		a.shell.w.Move(30, 20)
		settle()
		a.shell.w.Close()

		cfg2, err := config.Load(dir)
		if err != nil || len(cfg2.Window.Geometry) == 0 {
			t.Errorf("geometry not saved on close: %v, %d bytes", err, len(cfg2.Window.Geometry))
			return
		}
		b := NewApp(cfg2, nil, log)
		b.shell.w.SetMinimumSize2(100, 100)
		b.Start()
		defer b.shell.w.Close()
		settle()
		// Рамку окна до показа Qt угадывает, место может съехать на её толщину.
		near := func(got, want int) bool { return abs(got-want) <= 4 }
		if sz, pos := b.shell.w.Size(), b.shell.w.Pos(); sz.Width() != 640 || sz.Height() != 480 || !near(pos.X(), 30) || !near(pos.Y(), 20) {
			t.Errorf("next start: window %dx%d at %d,%d; want 640x480 at 30,20", sz.Width(), sz.Height(), pos.X(), pos.Y())
		}
	})
}
