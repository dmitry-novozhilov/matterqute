package ui

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Главный поток занят — сторож пишет его стек, с тем, кто его держит.
func TestWatchMainThread(t *testing.T) {
	var out syncBuffer
	stop := watchMainThread(slog.New(slog.NewTextHandler(io.Discard, nil)), &out, 200*time.Millisecond, time.Hour)
	defer stop()
	time.Sleep(100 * time.Millisecond) // сторож узнал горутину главного потока
	onMain(func() { time.Sleep(time.Second) })
	if s := out.String(); !strings.Contains(s, "TestWatchMainThread") || !strings.Contains(s, "time.Sleep") {
		t.Errorf("no stack of the busy main thread:\n%s", s)
	}
}
