package ui

import (
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/multimedia"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/cache"
)

// autoProbeVideo — ролики до этого размера качаем сразу, ради кадра-превью и
// длительности в ленте; большие — только когда их откроют.
const autoProbeVideo = 20 << 20

var videoExts = map[string]bool{"mp4": true, "m4v": true, "mov": true, "webm": true, "mkv": true, "avi": true, "ogv": true}

// isVideo — вложение показываем превью ролика и открываем в плеере.
func isVideo(fi *model.FileInfo) bool {
	return strings.HasPrefix(fi.MimeType, "video/") || videoExts[strings.ToLower(fi.Extension)]
}

// Video — ролик уже разобран: кадр-превью, длительность, размер.
func (a *Assets) Video(fileID string) (cache.VideoInfo, bool) { return a.c.Video(fileID) }

// WantVideo качает ролик и разбирает его (первый кадр, длительность, размер);
// done — когда кадр на диске.
func (a *Assets) WantVideo(fi *model.FileInfo, done func()) {
	key := "video:" + fi.Id
	if a.failed[key] != nil {
		return
	}
	if ws, ok := a.waiting[key]; ok {
		a.waiting[key] = append(ws, done)
		return
	}
	a.waiting[key] = []func(){done}
	finish := func(err error) {
		ws := a.waiting[key]
		delete(a.waiting, key)
		if err != nil {
			a.failed[key] = err
			a.log.Warn("probe video", "file", fi.Id, "name", fi.Name, "err", err)
			return
		}
		for _, f := range ws {
			f()
		}
	}
	a.Download(fi, func(path string, err error) {
		if err != nil {
			finish(err)
			return
		}
		probeVideo(path, func(img *qt.QImage, duration int64, err error) {
			if a.ctx.Err() != nil {
				return // окно закрыто, пока разбирали: в кэш больше не пишем
			}
			if err != nil {
				finish(fmt.Errorf("%s: %w", fi.Name, err))
				return
			}
			thumb := a.c.VideoThumbPath(fi.Id, duration, img.Width(), img.Height())
			if err := os.MkdirAll(filepath.Dir(thumb), 0o700); err != nil {
				finish(err)
				return
			}
			if !img.Save(thumb) {
				finish(fmt.Errorf("save video thumbnail %s: QImage::save failed", thumb))
				return
			}
			finish(nil)
		})
	})
}

// probeVideo проигрывает ролик без звука до первого кадра: кадр, длительность
// (мс). Всё — в главном потоке, done зовётся один раз.
func probeVideo(path string, done func(img *qt.QImage, duration int64, err error)) {
	mp := multimedia.NewQMediaPlayer()
	sink := multimedia.NewQVideoSink2(mp.QObject)
	mp.SetVideoSink(sink)
	timeout := qt.NewQTimer2(mp.QObject)
	finished := false
	finish := func(img *qt.QImage, err error) {
		if finished {
			return
		}
		finished = true
		duration := mp.Duration()
		timeout.Stop()
		mp.Stop()
		mp.DeleteLater()
		done(img, duration, err)
	}
	sink.OnVideoFrameChanged(func(f *multimedia.QVideoFrame) {
		if !f.IsValid() {
			return
		}
		if img := f.ToImage(); !img.IsNull() {
			finish(img, nil)
		}
	})
	mp.OnErrorOccurred(func(_ multimedia.QMediaPlayer__Error, text string) {
		finish(nil, errors.New(text))
	})
	mp.OnMediaStatusChanged(func(st multimedia.QMediaPlayer__MediaStatus) {
		switch st {
		case multimedia.QMediaPlayer__InvalidMedia:
			finish(nil, errors.New("not a playable video"))
		case multimedia.QMediaPlayer__EndOfMedia:
			finish(nil, errors.New("no video frames"))
		}
	})
	timeout.SetSingleShot(true)
	timeout.OnTimeout(func() { finish(nil, errors.New("no frame in 15 s")) })
	timeout.Start(15000)
	mp.SetSource(qt.QUrl_FromLocalFile(path))
	mp.Play()
}

// formatDuration — «0:07», «1:04», «1:02:03».
func formatDuration(ms int64) string {
	s := ms / 1000
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// videoSize — размер превью: как у картинок (без увеличения, в колонку, до
// 400 по высоте), пока размер неизвестен — 16:9.
func videoSize(info cache.VideoInfo, probed bool, maxW int) (int, int) {
	if probed && info.Width > 0 && info.Height > 0 {
		w, h := fitSize(info.Width, info.Height, maxW, previewMaxH)
		return max(w, 160), max(h, 90)
	}
	w := min(480, maxW)
	return w, w * 9 / 16
}

// videoPixmap — превью ролика: кадр на тёмном фоне, по центру кнопка
// воспроизведения 48 px, внизу слева длительность (пока не знаем — размер файла).
func videoPixmap(thumb string, w, h int, label string) *qt.QPixmap {
	img := qt.NewQImage3(w*resourceDPR, h*resourceDPR, qt.QImage__Format_ARGB32_Premultiplied)
	img.SetDevicePixelRatio(resourceDPR)
	img.FillWithColor(qt.NewQColor6("#141210"))
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	p.SetRenderHint(qt.QPainter__SmoothPixmapTransform)
	if thumb != "" {
		if pm := qt.NewQPixmap4(thumb); !pm.IsNull() {
			pm = pm.Scaled3(w*resourceDPR, h*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
			pm.SetDevicePixelRatio(resourceDPR)
			pw, ph := pm.Width()/resourceDPR, pm.Height()/resourceDPR
			p.DrawPixmap9((w-pw)/2, (h-ph)/2, pm)
		}
	}
	cx, cy := float64(w)/2, float64(h)/2
	p.SetPenWithStyle(qt.NoPen)
	p.SetBrush(qt.NewQBrush3(qt.NewQColor11(0, 0, 0, 140)))
	p.DrawEllipse(qt.NewQRectF4(cx-24, cy-24, 48, 48))
	tri := qt.NewQPainterPath()
	tri.MoveTo2(cx-7, cy-11)
	tri.LineTo2(cx-7, cy+11)
	tri.LineTo2(cx+12, cy)
	tri.CloseSubpath()
	p.SetBrush(qt.NewQBrush3(qt.NewQColor6("#ffffff")))
	p.DrawPath(tri)

	f := pixelFont(12, qt.QFont__Normal)
	f.SetFamilies([]string{monoFamily()})
	lw := float64(qt.NewQFontMetrics(f).HorizontalAdvance(label)) + 12
	r := qt.NewQRectF4(8, float64(h)-8-20, lw, 20)
	p.SetBrush(qt.NewQBrush3(qt.NewQColor11(0, 0, 0, 150)))
	p.DrawRoundedRect(r, 4, 4)
	p.SetFont(f)
	p.SetPen(qt.NewQColor6("#ffffff"))
	p.DrawText5(r, int(qt.AlignCenter), label)
	p.End()
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	return pm
}

// videoHTML — превью ролика в ленте; клик открывает плеер. Под превью — имя,
// размер и «Сохранить…».
func (f *Feed) videoHTML(fi *model.FileInfo) string {
	pal := Pal()
	maxW := f.view.Viewport().Width() - feedAvatarCol - feedPadRight
	if maxW < 100 {
		maxW = 600
	}
	var info cache.VideoInfo
	probed := false
	if f.d.Assets != nil {
		info, probed = f.d.Assets.Video(fi.Id)
	}
	w, h := videoSize(info, probed, maxW)
	src := fmt.Sprintf("video:%s:%dx%d:%t", fi.Id, w, h, probed)
	if _, ok := f.resources[src]; !ok {
		label, thumb := formatSize(fi.Size), ""
		if probed {
			label, thumb = formatDuration(info.Duration), info.Thumb
		}
		f.addResource(src, videoPixmap(thumb, w, h, label), w, h)
		if !probed && f.d.Assets != nil && fi.Size <= autoProbeVideo {
			f.d.Assets.WantVideo(fi, f.refreshLater())
		}
	}
	return fmt.Sprintf(`<p style="margin-top:6px;margin-bottom:0"><a href="%s%s"><img src="%s" width="%d" height="%d" title="%s"></a></p>`+
		`<p style="margin:0;font-size:12px;color:%s">%s · %s · <a href="%s%s" style="color:%s">Сохранить…</a></p>`,
		videoLink, fi.Id, src, w, h, html.EscapeString(fi.Name+" — смотреть"),
		pal.Tx2, html.EscapeString(fi.Name), formatSize(fi.Size), saveLink, fi.Id, pal.Acc)
}
