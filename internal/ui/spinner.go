package ui

import (
	"fmt"

	qt "github.com/mappu/miqt/qt6"
)

const (
	spinFrames   = 24 // кадров на оборот
	spinInterval = 40 // мс на кадр: оборот примерно за секунду
)

// spinner крутит иконку loader-circle: по таймеру отдаёт в set очередной кадр.
// Таймер — ребёнок owner: удаляется вместе с ним и после этого не срабатывает.
type spinner struct {
	timer *qt.QTimer
	dead  bool // owner удалён, а с ним и таймер
	frame int
	color string
	size  int
	set   func(*qt.QPixmap)
}

func newSpinner(owner *qt.QObject, set func(*qt.QPixmap)) *spinner {
	s := &spinner{timer: qt.NewQTimer2(owner), set: set}
	s.timer.SetInterval(spinInterval)
	s.timer.OnTimeout(func() {
		s.frame = (s.frame + 1) % spinFrames
		s.set(spinnerFrame(s.color, s.size, s.frame))
	})
	s.timer.OnDestroyed(func() { s.dead = true })
	return s
}

// start — крутить иконку цвета color размером size; кадр отдаётся и сразу.
func (s *spinner) start(color string, size int) {
	if s.dead {
		return
	}
	s.color, s.size = color, size
	s.set(spinnerFrame(color, size, s.frame))
	if !s.timer.IsActive() {
		s.timer.Start2()
	}
}

func (s *spinner) stop() {
	if !s.dead {
		s.timer.Stop()
	}
}

// spinnerFrame — loader-circle, повёрнутая на frame/spinFrames оборота.
func spinnerFrame(color string, size, frame int) *qt.QPixmap {
	key := fmt.Sprintf("spin|%s|%d|%d", color, size, frame)
	if pm := style.icons[key]; pm != nil {
		return pm
	}
	img := qt.NewQImage3(size*resourceDPR, size*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
	img.SetDevicePixelRatio(resourceDPR)
	img.Fill2(qt.Transparent)
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	p.SetRenderHint(qt.QPainter__SmoothPixmapTransform)
	c := float64(size) / 2
	p.Translate2(c, c)
	p.Rotate(float64(frame) * 360 / spinFrames)
	p.Translate2(-c, -c)
	p.DrawPixmap9(0, 0, iconPixmap("loader-circle", color, size))
	p.End()
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	style.icons[key] = pm
	return pm
}
