package ui

import (
	"fmt"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/multimedia"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// player — ролик во весь экран (кнопка «Во весь экран» у ролика в ленте):
// кадр на тёмном фоне и панель 44 px по макету — пуск/пауза, время,
// перемотка, громкость, выход из полноэкранного режима. Пробел — пуск/пауза,
// Esc или та же кнопка — обратно в ленту, ролик продолжится там.
type player struct {
	w      *qt.QWidget
	stack  *qt.QStackedWidget // сообщение (ошибка) — либо видео
	msg    *qt.QLabel
	video  *multimedia.QVideoWidget
	mp     *multimedia.QMediaPlayer
	audio  *multimedia.QAudioOutput
	play   *qt.QToolButton
	full   *qt.QToolButton
	vol    *qt.QLabel
	time   *qt.QLabel
	seek   *qt.QSlider
	volume *qt.QSlider
	closed bool

	unsubscribe func()
	onClosed    func()
	back        func(pos int64, playing bool) // окно закрывают: где остановились и играло ли
}

// fullscreenVideo — окно во весь экран с позиции pos; повторный вызов для того
// же ролика поднимает уже открытое.
func (m *MainWindow) fullscreenVideo(fi *model.FileInfo, p *model.Post, path string, pos int64, playing bool, back func(int64, bool)) {
	if pl := m.players[fi.Id]; pl != nil {
		pl.w.Raise()
		pl.w.ActivateWindow()
		return
	}
	pl := newPlayer(m.w, m.videoTitle(fi, p))
	m.players[fi.Id] = pl
	pl.onClosed = func() { delete(m.players, fi.Id) }
	pl.back = back
	pl.w.ShowFullScreen()
	pl.load(path, pos, playing)
}

// videoTitle — «demo.mp4 — bob, # backend».
func (m *MainWindow) videoTitle(fi *model.FileInfo, p *model.Post) string {
	if p == nil {
		return fi.Name
	}
	author := m.st.Username(p.UserId)
	if in, ok := store.PostIntegration(p); ok && in.Name != "" {
		author = in.Name
	}
	title := fi.Name + " — " + author
	if ch := m.st.Channel(p.ChannelId); ch != nil && ch.Type != model.ChannelTypeDirect {
		title += ", " + channelPrefix(ch) + m.st.Title(ch)
	}
	return title
}

func newPlayer(parent *qt.QWidget, title string) *player {
	// Окно с родителем: закроется вместе с окном сервера (выход из аккаунта)
	// и не держит приложение, когда главное окно закрыли.
	p := &player{w: qt.NewQWidget3(parent, qt.Window)}
	setName(p.w.QObject, "player")
	p.w.SetAttribute(qt.WA_DeleteOnClose)
	p.w.SetWindowTitle(title)
	p.w.Resize(960, 584)
	p.w.SetMinimumSize2(480, 314)

	p.msg = qt.NewQLabel2()
	setName(p.msg.QObject, "playerMessage")
	p.msg.SetAlignment(qt.AlignCenter)
	p.msg.SetWordWrap(true)
	p.video = multimedia.NewQVideoWidget2()
	p.stack = qt.NewQStackedWidget2()
	setName(p.stack.QObject, "playerVideo")
	p.stack.AddWidget(p.msg.QWidget)
	p.stack.AddWidget(p.video.QWidget)

	p.mp = multimedia.NewQMediaPlayer2(p.w.QObject)
	p.audio = multimedia.NewQAudioOutput3(p.w.QObject)
	p.mp.SetAudioOutput(p.audio)
	p.mp.SetVideoOutput(p.video.QObject)

	button := func(name string) *qt.QToolButton {
		b := qt.NewQToolButton2()
		setName(b.QObject, name)
		b.SetFixedSize2(32, 32)
		b.SetIconSize(qt.NewQSize2(16, 16))
		b.SetFocusPolicy(qt.NoFocus)
		return b
	}
	p.play = button("playerPlay")
	p.play.SetToolTip("Пуск / пауза (пробел)")
	p.play.OnClicked(p.toggle)
	p.full = button("playerFull")
	p.full.SetToolTip("Выйти из полноэкранного режима (Esc)")
	p.full.OnClicked(func() { p.w.Close() })
	p.time = qt.NewQLabel3(formatDuration(0) + " / " + formatDuration(0))
	setName(p.time.QObject, "playerTime")
	p.seek = qt.NewQSlider3(qt.Horizontal)
	setName(p.seek.QObject, "playerSeek")
	p.seek.SetFocusPolicy(qt.NoFocus)
	p.seek.OnSliderMoved(func(v int) { p.mp.SetPosition(int64(v)) })
	p.vol = qt.NewQLabel2()
	p.volume = qt.NewQSlider3(qt.Horizontal)
	setName(p.volume.QObject, "playerVolume")
	p.volume.SetFixedWidth(56)
	p.volume.SetFocusPolicy(qt.NoFocus)
	p.volume.SetRange(0, 100)
	p.volume.OnValueChanged(func(v int) { p.audio.SetVolume(float32(v) / 100) })
	p.volume.SetValue(70)

	bar := qt.NewQFrame2()
	setName(bar.QObject, "playerBar")
	bar.SetFixedHeight(44)
	bl := qt.NewQHBoxLayout(bar.QWidget)
	bl.SetContentsMargins(12, 0, 12, 0)
	bl.SetSpacing(12)
	bl.AddWidget(p.play.QWidget)
	bl.AddWidget(p.time.QWidget)
	bl.AddWidget2(p.seek.QWidget, 1)
	bl.AddWidget(p.vol.QWidget)
	bl.AddWidget(p.volume.QWidget)
	bl.AddWidget(p.full.QWidget)

	lay := qt.NewQVBoxLayout(p.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.SetSpacing(0)
	lay.AddWidget2(p.stack.QWidget, 1)
	lay.AddWidget(bar.QWidget)

	p.mp.OnDurationChanged(func(d int64) {
		p.seek.SetRange(0, int(d))
		p.showTime()
	})
	p.mp.OnPositionChanged(func(pos int64) {
		if !p.seek.IsSliderDown() {
			p.seek.SetValue(int(pos))
		}
		p.showTime()
	})
	p.mp.OnPlaybackStateChanged(func(multimedia.QMediaPlayer__PlaybackState) { p.restyle() })
	p.mp.OnErrorOccurred(func(_ multimedia.QMediaPlayer__Error, text string) {
		p.fail("Не удалось воспроизвести видео: " + text)
	})
	p.w.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		switch qt.Key(e.Key()) {
		case qt.Key_Space:
			p.toggle()
		case qt.Key_Escape:
			p.w.Close()
		default:
			super(e)
		}
	})
	// Позицию и «играло ли» — до удаления окна: потом плеер уже удалён, а
	// при удалении ещё и сообщит «остановлен».
	p.w.OnCloseEvent(func(super func(*qt.QCloseEvent), e *qt.QCloseEvent) {
		if p.back != nil {
			p.back(p.mp.Position(), p.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState)
			p.back = nil
		}
		super(e)
	})
	p.unsubscribe = OnThemeChanged(p.restyle)
	p.w.OnDestroyed(func() {
		p.closed = true
		p.unsubscribe()
		if p.onClosed != nil {
			p.onClosed()
		}
	})
	p.restyle()
	return p
}

// load — играть с позиции pos; paused — встать на ней на паузе.
func (p *player) load(path string, pos int64, playing bool) {
	p.stack.SetCurrentWidget(p.video.QWidget)
	seeked := false
	p.mp.OnMediaStatusChanged(func(st multimedia.QMediaPlayer__MediaStatus) {
		// Позиция до загрузки ролика не применяется — ставим, когда он загружен.
		if !seeked && (st == multimedia.QMediaPlayer__LoadedMedia || st == multimedia.QMediaPlayer__BufferedMedia) {
			seeked = true
			p.mp.SetPosition(pos)
			if !playing {
				p.mp.Pause()
			}
		}
	})
	p.mp.SetSource(qt.QUrl_FromLocalFile(path))
	p.mp.Play()
}

func (p *player) fail(text string) {
	p.msg.SetText(text)
	p.stack.SetCurrentWidget(p.msg.QWidget)
}

func (p *player) toggle() {
	if p.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState {
		p.mp.Pause()
	} else {
		p.mp.Play()
	}
}

func (p *player) showTime() {
	p.time.SetText(formatDuration(p.mp.Position()) + " / " + formatDuration(p.mp.Duration()))
}

// restyle — иконки и стиль панели из текущей темы; кадр — всегда на тёмном.
func (p *player) restyle() {
	pal := Pal()
	playIcon := "play"
	if p.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState {
		playIcon = "pause"
	}
	p.play.SetIcon(icon(playIcon, pal.Tx, 16))
	p.full.SetIcon(icon("maximize", pal.Tx2, 16))
	p.vol.SetPixmap(iconPixmap("volume-2", pal.Tx2, 16))
	p.w.SetStyleSheet(fmt.Sprintf(`
QStackedWidget#playerVideo, QLabel#playerMessage { background: #141210; color: #a8a49e; }
QFrame#playerBar { background: %[1]s; border-top: 1px solid %[2]s; }
QToolButton#playerPlay { background: %[3]s; border: none; border-radius: 4px; }
QToolButton#playerFull { background: transparent; border: none; border-radius: 4px; }
QToolButton#playerFull:hover { background: %[3]s; }
QLabel#playerTime { font-family: "%[6]s"; font-size: 12px; color: %[4]s; }
QSlider::groove:horizontal { height: 4px; border-radius: 2px; background: %[2]s; }
QSlider#playerSeek::sub-page:horizontal { height: 4px; border-radius: 2px; background: %[5]s; }
QSlider#playerSeek::handle:horizontal { width: 12px; height: 12px; margin: -4px 0; border-radius: 6px; background: %[5]s; }
QSlider#playerVolume::sub-page:horizontal { height: 4px; border-radius: 2px; background: %[4]s; }
QSlider#playerVolume::handle:horizontal { width: 10px; height: 10px; margin: -3px 0; border-radius: 5px; background: %[4]s; }
`, pal.Surf, pal.Line, pal.Hover, pal.Tx2, pal.Acc, monoFamily()))
}
