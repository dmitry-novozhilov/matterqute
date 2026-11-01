package ui

import (
	"strings"

	qt "github.com/mappu/miqt/qt6"
)

// emojiAnim — анимированное кастомное эмодзи (GIF): один QMovie на окно
// сервера, кадры получают подписчики — плашки реакций, текст сообщений, окно
// выбора. Пока подписчиков нет, QMovie стоит. movie == nil — эмодзи не
// анимированное (запомнили, чтобы не открывать файл снова).
type emojiAnim struct {
	movie *qt.QMovie
	subs  map[int]func()
}

// EmojiMovie — QMovie анимированного эмодзи или nil: эмодзи не GIF, в нём
// один кадр или его ещё нет на диске.
func (a *Assets) EmojiMovie(name string) *qt.QMovie {
	if an, ok := a.anims[name]; ok {
		return an.movie
	}
	path := a.Emoji(name)
	if path == "" {
		return nil // ещё не скачано: решим, когда появится
	}
	an := &emojiAnim{subs: map[int]func(){}}
	a.anims[name] = an
	if !strings.HasSuffix(path, ".gif") {
		return nil
	}
	mv := qt.NewQMovie3(path)
	if !mv.IsValid() || mv.FrameCount() < 2 {
		mv.DeleteLater()
		return nil
	}
	mv.JumpToFrame(0)
	mv.OnFrameChanged(func(int) {
		for _, f := range an.subs {
			f()
		}
	})
	an.movie = mv
	return mv
}

// EmojiFrame — текущий кадр анимированного эмодзи или nil.
func (a *Assets) EmojiFrame(name string) *qt.QPixmap {
	if mv := a.EmojiMovie(name); mv != nil {
		return mv.CurrentPixmap()
	}
	return nil
}

// OnEmojiFrame — f на каждый кадр анимированного эмодзи; вернёт отписку. Для
// неанимированного — ничего не делает.
func (a *Assets) OnEmojiFrame(name string, f func()) (cancel func()) {
	mv := a.EmojiMovie(name)
	if mv == nil {
		return func() {}
	}
	an := a.anims[name]
	a.nextSub++
	id := a.nextSub
	an.subs[id] = f
	if mv.State() != qt.QMovie__Running {
		mv.Start()
	}
	return func() {
		delete(an.subs, id)
		if len(an.subs) == 0 {
			mv.Stop()
		}
	}
}

// stopAnimations — окно сервера закрывается.
func (a *Assets) stopAnimations() {
	for name, an := range a.anims {
		if an.movie != nil {
			an.movie.Stop()
			an.movie.DeleteLater()
		}
		delete(a.anims, name)
	}
}
