package ui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"time"

	"github.com/mappu/miqt/qt6/mainthread"
)

// Сторож главного потока (MATTERQUTE_DEBUG=1): главный поток не берёт задачи
// дольше stuckAfter — окно заметно не отвечает. Тогда пишем, чем он занят:
// стек его горутины (вызовы Go до ухода в Qt и обратно), и снова раз в
// stuckEvery, пока не освободится.

const (
	stuckAfter = 300 * time.Millisecond // заметная пауза: окно не отвечает
	stuckEvery = time.Second
	pingEvery  = 100 * time.Millisecond
)

// WatchMainThread запускает сторожа (после создания QApplication). Строки — в
// log, многострочные стеки — в w. Возвращает функцию остановки.
func WatchMainThread(log *slog.Logger, w io.Writer) (stop func()) {
	return watchMainThread(log, w, stuckAfter, stuckEvery)
}

func watchMainThread(log *slog.Logger, w io.Writer, after, every time.Duration) func() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Горутина, в которой Qt зовёт Go: её стек и ищем.
		var head string
		mainthread.Wait(func() { head = goroutineHead() })
		for {
			done := make(chan struct{})
			start := time.Now()
			mainthread.Start(func() { close(done) })
			stuck := false
			for wait := after; ; wait = every {
				select {
				case <-ctx.Done():
					return
				case <-done:
				case <-time.After(wait):
					stuck = true
					log.Debug("main thread stuck", "for", time.Since(start).Round(time.Millisecond), "stack", "below")
					fmt.Fprintf(w, "%s\n\n", goroutineStack(head))
					continue
				}
				break
			}
			if stuck {
				log.Debug("main thread free", "after", time.Since(start).Round(time.Millisecond))
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(pingEvery):
			}
		}
	}()
	return cancel
}

// goroutineHead — «goroutine N» текущей горутины.
func goroutineHead() string {
	buf := make([]byte, 64)
	buf = buf[:runtime.Stack(buf, false)]
	if i := bytes.Index(buf, []byte(" [")); i > 0 {
		return string(buf[:i])
	}
	return ""
}

// goroutineStack — стек горутины head из дампа всех горутин.
func goroutineStack(head string) string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	for g := range bytes.SplitSeq(buf, []byte("\n\n")) {
		if head != "" && bytes.HasPrefix(g, []byte(head+" [")) {
			return string(g)
		}
	}
	return "stack of the main thread goroutine (" + head + ") not found"
}
