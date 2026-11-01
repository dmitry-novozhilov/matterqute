package ui

import (
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/multimedia"
	"github.com/mattermost/mattermost/server/public/model"
)

// inlineVideo — ролик, который играет прямо в ленте. QTextDocument виджеты не
// встраивает, поэтому это виджет поверх превью в документе, на месте его
// картинки; Feed двигает его при прокрутке и правках. Кадры рисуем сами из
// QVideoSink: обычный виджет честно обрезается краем ленты. Внизу полоса
// управления: пуск/пауза, время, перемотка, звук, «Во весь экран».
type inlineVideo struct {
	f      *Feed
	fi     *model.FileInfo
	w      *qt.QWidget
	mp     *multimedia.QMediaPlayer
	audio  *multimedia.QAudioOutput
	frame  *qt.QImage // последний кадр; nil — ещё не было
	thumb  *qt.QPixmap
	status string // «Загружаем видео…» или ошибка; "" — видео готово
	path   string
	pos    int // позиция картинки превью в документе; -1 — не нашли

	play, mute, full *qt.QToolButton
	time             *qt.QLabel
	seek             *qt.QSlider
}

// playInline — клик по превью: ролик играет на месте; повторный клик — пауза.
func (f *Feed) playInline(fi *model.FileInfo) {
	if v := f.videos[fi.Id]; v != nil {
		v.toggle()
		return
	}
	v := newInlineVideo(f, fi)
	f.videos[fi.Id] = v
	f.placeVideos()
	if f.d.Assets == nil {
		v.fail("Видео недоступно")
		return
	}
	f.d.Assets.Download(fi, func(path string, err error) {
		if f.videos[fi.Id] != v {
			return // ленту уже очистили
		}
		if err != nil {
			v.fail("Не удалось скачать видео: " + err.Error())
			return
		}
		v.load(path)
		if _, ok := f.d.Assets.Video(fi.Id); !ok {
			f.d.Assets.WantVideo(fi, f.refreshSoon) // большой ролик: кадр-превью и длительность — сейчас
		}
	})
}

func newInlineVideo(f *Feed, fi *model.FileInfo) *inlineVideo {
	v := &inlineVideo{f: f, fi: fi, w: qt.NewQWidget(f.view.Viewport()), status: "Загружаем видео…", pos: -1}
	setName(v.w.QObject, "inlineVideo")
	if f.d.Assets != nil {
		if info, ok := f.d.Assets.Video(fi.Id); ok {
			v.thumb = qt.NewQPixmap4(info.Thumb)
		}
	}
	v.mp = multimedia.NewQMediaPlayer2(v.w.QObject)
	v.audio = multimedia.NewQAudioOutput3(v.w.QObject)
	v.mp.SetAudioOutput(v.audio)
	sink := multimedia.NewQVideoSink2(v.w.QObject)
	v.mp.SetVideoSink(sink)
	sink.OnVideoFrameChanged(func(fr *multimedia.QVideoFrame) {
		if fr.IsValid() {
			if img := fr.ToImage(); !img.IsNull() {
				v.frame = img
				v.w.Update()
			}
		}
	})
	v.w.OnPaintEvent(func(_ func(*qt.QPaintEvent), _ *qt.QPaintEvent) { v.paint() })
	v.w.OnMousePressEvent(func(_ func(*qt.QMouseEvent), e *qt.QMouseEvent) {
		if e.Button() == qt.LeftButton {
			v.toggle()
		}
	})

	button := func(name, tip string) *qt.QToolButton {
		b := qt.NewQToolButton2()
		setName(b.QObject, name)
		b.SetFixedSize2(28, 28)
		b.SetIconSize(qt.NewQSize2(16, 16))
		b.SetFocusPolicy(qt.NoFocus)
		b.SetToolTip(tip)
		return b
	}
	v.play = button("inlinePlay", "Пуск / пауза")
	v.play.OnClicked(v.toggle)
	v.mute = button("inlineMute", "Звук")
	v.mute.SetCheckable(true)
	v.mute.OnToggled(func(on bool) {
		v.audio.SetMuted(on)
		v.restyle()
	})
	v.full = button("inlineFull", "Во весь экран")
	v.full.OnClicked(v.fullscreen)
	v.time = qt.NewQLabel3(formatDuration(0) + " / " + formatDuration(0))
	setName(v.time.QObject, "inlineTime")
	v.seek = qt.NewQSlider3(qt.Horizontal)
	setName(v.seek.QObject, "inlineSeek")
	v.seek.SetFocusPolicy(qt.NoFocus)
	v.seek.OnSliderMoved(func(p int) { v.mp.SetPosition(int64(p)) })

	bar := qt.NewQFrame2()
	setName(bar.QObject, "inlineBar")
	bar.SetFixedHeight(36)
	bl := qt.NewQHBoxLayout(bar.QWidget)
	bl.SetContentsMargins(6, 0, 6, 0)
	bl.SetSpacing(8)
	bl.AddWidget(v.play.QWidget)
	bl.AddWidget(v.time.QWidget)
	bl.AddWidget2(v.seek.QWidget, 1)
	bl.AddWidget(v.mute.QWidget)
	bl.AddWidget(v.full.QWidget)
	lay := qt.NewQVBoxLayout(v.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.AddStretch()
	lay.AddWidget(bar.QWidget)

	v.mp.OnDurationChanged(func(d int64) {
		v.seek.SetRange(0, int(d))
		v.showTime()
	})
	v.mp.OnPositionChanged(func(p int64) {
		if !v.seek.IsSliderDown() {
			v.seek.SetValue(int(p))
		}
		v.showTime()
	})
	v.mp.OnPlaybackStateChanged(func(multimedia.QMediaPlayer__PlaybackState) { v.restyle() })
	v.mp.OnErrorOccurred(func(_ multimedia.QMediaPlayer__Error, text string) {
		v.fail("Не удалось воспроизвести видео: " + text)
	})
	v.restyle()
	return v
}

func (v *inlineVideo) load(path string) {
	v.path, v.status = path, ""
	v.mp.SetSource(qt.QUrl_FromLocalFile(path))
	v.f.pauseOtherVideos(v)
	v.mp.Play()
	v.w.Update()
}

func (v *inlineVideo) fail(text string) {
	v.status = text
	v.w.Update()
}

func (v *inlineVideo) toggle() {
	if v.path == "" {
		return
	}
	if v.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState {
		v.mp.Pause()
		return
	}
	v.f.pauseOtherVideos(v)
	v.mp.Play()
}

// fullscreen — отдельное окно во весь экран с той же позиции; когда его
// закроют, ролик продолжится здесь.
func (v *inlineVideo) fullscreen() {
	if v.path == "" || v.f.OnFullscreenVideo == nil {
		return
	}
	playing := v.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState
	v.mp.Pause()
	v.f.OnFullscreenVideo(v.fi, v.f.fileOwner(v.fi.Id), v.path, v.mp.Position(), playing, func(pos int64, playing bool) {
		if v.f.videos[v.fi.Id] != v {
			return
		}
		v.mp.SetPosition(pos)
		if playing {
			v.f.pauseOtherVideos(v)
			v.mp.Play()
		}
	})
}

func (v *inlineVideo) stop() {
	v.mp.Stop()
	v.w.Hide()
	v.w.DeleteLater()
}

func (v *inlineVideo) showTime() {
	v.time.SetText(formatDuration(v.mp.Position()) + " / " + formatDuration(v.mp.Duration()))
}

func (v *inlineVideo) paint() {
	p := qt.NewQPainter2(v.w.QPaintDevice)
	defer p.End()
	r := v.w.Rect()
	p.FillRect5(0, 0, r.Width(), r.Height(), qt.NewQColor6("#141210"))
	p.SetRenderHint(qt.QPainter__SmoothPixmapTransform)
	var img *qt.QImage
	switch {
	case v.frame != nil:
		img = v.frame
	case v.thumb != nil && !v.thumb.IsNull():
		img = v.thumb.ToImage()
	}
	if img != nil {
		s := img.Scaled3(r.Width(), r.Height(), qt.KeepAspectRatio, qt.SmoothTransformation)
		p.DrawImage8(qt.NewQPoint2((r.Width()-s.Width())/2, (r.Height()-s.Height())/2), s)
	}
	if v.status != "" {
		p.FillRect5(0, 0, r.Width(), r.Height(), qt.NewQColor11(0, 0, 0, 120))
		p.SetPen(qt.NewQColor6("#e9e6e1"))
		p.SetFont(pixelFont(13, qt.QFont__Normal))
		p.DrawText6(qt.NewQRect4(12, 0, r.Width()-24, r.Height()-36), int(qt.AlignCenter)|int(qt.TextWordWrap), v.status)
	}
}

// restyle — иконки белые поверх кадра, перемотка — цветом акцента темы.
func (v *inlineVideo) restyle() {
	playIcon := "play"
	if v.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState {
		playIcon = "pause"
	}
	white, dim := "#ffffff", "#a8a49e"
	v.play.SetIcon(icon(playIcon, white, 16))
	v.full.SetIcon(icon("maximize", white, 16))
	if v.mute.IsChecked() {
		v.mute.SetIcon(icon("volume-2", dim, 16))
		v.mute.SetToolTip("Включить звук")
	} else {
		v.mute.SetIcon(icon("volume-2", white, 16))
		v.mute.SetToolTip("Выключить звук")
	}
	acc := Pal().Acc
	v.w.SetStyleSheet(`
QFrame#inlineBar { background: rgba(0, 0, 0, 150); }
QToolButton { background: transparent; border: none; border-radius: 4px; }
QToolButton:hover { background: rgba(255, 255, 255, 40); }
QLabel#inlineTime { font-family: "PT Mono"; font-size: 12px; color: #ffffff; background: transparent; }
QSlider::groove:horizontal { height: 4px; border-radius: 2px; background: rgba(255, 255, 255, 70); }
QSlider::sub-page:horizontal { height: 4px; border-radius: 2px; background: ` + acc + `; }
QSlider::handle:horizontal { width: 12px; height: 12px; margin: -4px 0; border-radius: 6px; background: ` + acc + `; }
`)
}

// pauseOtherVideos — в ленте играет один ролик.
func (f *Feed) pauseOtherVideos(keep *inlineVideo) {
	for _, v := range f.videos {
		if v != keep && v.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState {
			v.mp.Pause()
		}
	}
}

// stopVideos — лента очищается: ролики останавливаются и убираются.
func (f *Feed) stopVideos() {
	for id, v := range f.videos {
		v.stop()
		delete(f.videos, id)
	}
}

// placeVideos ставит виджеты роликов на места их превью в документе: после
// прокрутки — сразу, после правок документа — когда раскладка улеглась.
func (f *Feed) placeVideos() {
	if len(f.videos) == 0 {
		return
	}
	for _, v := range f.videos {
		if v.pos < 0 || !v.at(v.pos) {
			v.pos = v.find()
		}
		if v.pos < 0 {
			if v.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState {
				v.mp.Pause() // пост с роликом исчез из ленты
			}
			v.w.Hide()
			continue
		}
		c := qt.NewQTextCursor2(f.doc)
		c.SetPosition(v.pos)
		img := v.imageAt(v.pos)
		r := f.view.CursorRect(c)
		v.w.SetGeometry(r.Left(), r.Top(), int(img.Width()), int(img.Height()))
		v.w.Show()
	}
}

// imageAt — формат картинки, если символ в позиции pos — картинка (U+FFFC), иначе nil.
func (v *inlineVideo) imageAt(pos int) *qt.QTextImageFormat {
	if ch := v.f.doc.CharacterAt(pos); uint16(ch.Row())<<8|uint16(ch.Cell()) != 0xfffc {
		return nil
	}
	// Курсор после картинки: charFormat — формат символа перед ним. (В начале
	// блока он вернул бы формат символа после — поэтому и проверяем символ.)
	c := qt.NewQTextCursor2(v.f.doc)
	c.SetPosition(pos + 1)
	if f := c.CharFormat(); f.IsImageFormat() {
		return f.ToImageFormat()
	}
	return nil
}

// at — по позиции pos всё ещё превью этого ролика.
func (v *inlineVideo) at(pos int) bool {
	if pos+1 >= v.f.doc.CharacterCount() {
		return false
	}
	img := v.imageAt(pos)
	return img != nil && strings.HasPrefix(img.Name(), "video:"+v.fi.Id+":")
}

// find ищет превью ролика в посте, к которому он приложен; -1 — нет в ленте.
func (v *inlineVideo) find() int {
	f := v.f
	p := f.fileOwner(v.fi.Id)
	if p == nil {
		return -1
	}
	i := f.index(p.Id)
	if i < 0 {
		return -1
	}
	fr := f.items[i].frame
	for pos := fr.FirstPosition(); pos < fr.LastPosition(); pos++ {
		if v.at(pos) {
			return pos
		}
	}
	return -1
}
