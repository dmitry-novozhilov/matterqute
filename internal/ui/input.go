package ui

import (
	"sync"
	"time"

	qt "github.com/mappu/miqt/qt6"
)

// Главный поток Qt один: пока он рисует порцию ленты фоном, нажатие клавиши
// или шаг колеса ждут её конца (длинный пост — десятки миллисекунд). Поэтому
// фоновая отрисовка лент ждёт паузы во вводе: buildStep — idleBuild без
// клавиш и мыши, дорисовка ленты на экране — idleDrain без клавиш.

const (
	idleBuild = 600 * time.Millisecond
	idleDrain = 300 * time.Millisecond
)

var (
	lastInput time.Time // клавиша, щелчок, колесо, перетаскивание
	lastKey   time.Time
	inputOnce sync.Once
)

// watchInput ставит на приложение фильтр событий ввода (один раз).
func watchInput() {
	inputOnce.Do(func() {
		filter := qt.NewQObject()
		filter.OnEventFilter(func(super func(*qt.QObject, *qt.QEvent) bool, watched *qt.QObject, e *qt.QEvent) bool {
			switch e.Type() {
			case qt.QEvent__KeyPress:
				lastKey = time.Now()
				lastInput = lastKey
			case qt.QEvent__MouseButtonPress, qt.QEvent__Wheel:
				lastInput = time.Now()
			case qt.QEvent__MouseMove:
				if qt.UnsafeNewQMouseEvent(e.UnsafePointer()).Buttons() != qt.NoButton {
					lastInput = time.Now() // тянут полосу прокрутки, разделитель, выделение
				}
			}
			return super(watched, e)
		})
		qt.QCoreApplication_Instance().InstallEventFilter(filter)
	})
}

// waitInput — сколько ещё ждать, пока ввод (since — время последнего) утихнет
// на d; 0 — уже тихо.
func waitInput(since time.Time, d time.Duration) time.Duration {
	return max(0, d-time.Since(since))
}
