package ui

import (
	"fmt"
	"math"
	"net/url"
	"path"
	"path/filepath"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/cache"
	"matterqute/internal/store"
)

// viewerItem — картинка для просмотра: вложение или картинка по ссылке.
type viewerItem struct {
	key     string          // id файла или «link:N» — N-я картинка по ссылке в посте
	name    string          // имя файла или адрес картинки
	fi      *model.FileInfo // вложение; nil — картинка по ссылке
	src     string          // картинка по ссылке: адрес самой картинки
	link    string          // картинка по ссылке: куда ведёт ссылка
	preview string          // уже скачанное превью: показать, пока грузится оригинал
}

// imageViewer — картинка во весь экран внутри приложения (клик по картинке в
// ленте): на тёмном фоне, вписана в экран, мелкая — в своём размере. Колесо и
// +/− — масштаб у курсора, двойной клик или 1 — 1:1 и обратно, 0 — по экрану,
// перетаскивание — сдвиг, ←/→ — соседние картинки сообщения, Esc, клик по
// фону или ✕ — закрыть. Сверху — подпись, «Копировать» (Ctrl+C), «Сохранить…»
// и «Открыть в приложении» (вложение) или «Открыть в браузере» (картинка по
// ссылке).
type imageViewer struct {
	m      *MainWindow
	w      *qt.QWidget
	canvas *qt.QWidget
	title  *qt.QLabel
	count  *qt.QLabel
	copy   *qt.QToolButton
	save   *qt.QToolButton
	open   *qt.QToolButton
	close  *qt.QToolButton

	post  *model.Post
	items []viewerItem
	cur   int
	gen   int // номер показа: ответ загрузки для прежней картинки отбрасываем

	img          *qt.QImage // что рисуем: превью, оригинал или кадр GIF; nil — ещё нечего
	movie        *qt.QMovie
	path         string  // оригинал на диске (для «Сохранить…» картинки по ссылке)
	baseW, baseH float64 // размер оригинала в px: в нём считается масштаб и для превью
	scale        float64 // логических px на px оригинала
	fit          bool    // масштаб «по экрану»: пересчитывается при смене размера окна
	panX, panY   float64 // сдвиг центра картинки от центра окна
	msg          string  // «Загружаем…» или ошибка — поверх картинки
	copyWanted   bool    // Ctrl+C, пока оригинал грузится: скопировать, когда загрузится

	pressAt              *qt.QPointF // где нажали левую кнопку; nil — не нажата
	pressPanX, pressPanY float64     // сдвиг в момент нажатия
	dragged              bool

	closed      bool
	unsubscribe func()
	flashTimer  *qt.QTimer // убирает «Скопировано…»
	flashText   string     // что показал flash: убрать только её, не «Загружаем…» следующей картинки
}

const (
	viewerBg     = "#141210"
	viewerMargin = 24 // поле вокруг вписанной картинки
	viewerZoom   = 1.25
	viewerFlash  = 1500 * time.Millisecond // сколько видна подпись «Скопировано…»
)

// openImage — клик по картинке поста: просмотр с этой картинки (key — id файла
// или «link:N»); листаются все картинки поста.
func (m *MainWindow) openImage(p *model.Post, key string) {
	items := m.viewerItems(p)
	for i, it := range items {
		if it.key == key {
			if m.viewer == nil {
				m.viewer = newImageViewer(m)
				m.viewer.w.ShowFullScreen()
			}
			m.viewer.w.Raise()
			m.viewer.w.ActivateWindow()
			m.viewer.post, m.viewer.items = p, items
			m.viewer.show(i)
			return
		}
	}
}

// viewerItems — картинки поста в порядке ленты: вложения, потом картинки по
// ссылкам (как их показывает embedsHTML).
func (m *MainWindow) viewerItems(p *model.Post) []viewerItem {
	var items []viewerItem
	if p.Metadata != nil {
		for _, fi := range p.Metadata.Files {
			if isImage(fi) && !isVideo(fi) {
				items = append(items, viewerItem{key: fi.Id, name: fi.Name, fi: fi, preview: m.assets.Preview(fi.Id)})
			}
		}
	}
	n := 0
	for _, pv := range store.PostPreviews(p, m.st.LinkPreviews()) {
		if pv.Kind != model.PostEmbedImage {
			continue
		}
		items = append(items, viewerItem{key: fmt.Sprintf("link:%d", n), name: pv.URL, src: pv.Image, link: pv.URL,
			preview: m.assets.LinkImage(pv.Image)})
		n++
	}
	return items
}

// original — оригинал картинки на диске: вложение качается (если ещё нет),
// картинка по ссылке — из кэша или догружается. done — в главном потоке.
func (m *MainWindow) original(it viewerItem, done func(path string, err error)) {
	if it.fi != nil {
		m.assets.Download(it.fi, done)
		return
	}
	if p := m.assets.LinkImage(it.src); p != "" {
		done(p, nil)
		return
	}
	m.assets.WantLinkImage(it.src, func() {
		if p := m.assets.LinkImage(it.src); p != "" {
			done(p, nil)
		} else {
			done("", fmt.Errorf("no image at %s", truncate(it.src, 200)))
		}
	})
}

// copyImage — «Копировать картинку» в меню ленты (key — id файла или
// «link:N», как у openImage): оригинал в буфер обмена, когда загрузится.
func (m *MainWindow) copyImage(p *model.Post, key string) {
	for _, it := range m.viewerItems(p) {
		if it.key == key {
			m.copyImageItem(it)
			return
		}
	}
}

// copyImageItem — оригинал картинки в буфер обмена; итог — в строке состояния.
func (m *MainWindow) copyImageItem(it viewerItem) {
	m.original(it, func(path string, err error) {
		if err == nil {
			err = copyImageFile(path)
		}
		if err != nil {
			m.fail("copy image", err)
			return
		}
		m.message("Картинка скопирована в буфер обмена")
	})
}

// copyImageFile — картинка с диска в буфер обмена: с поворотом по EXIF,
// GIF — первым кадром. Картинку из Read (по значению, с финализатором MIQT)
// удаляет free: снимает финализатор, иначе удаление второй раз.
func copyImageFile(path string) error {
	r := qt.NewQImageReader3(path)
	defer free(r)
	r.SetAutoTransform(true)
	img := r.Read()
	defer free(img)
	if img.IsNull() {
		return fmt.Errorf("read %s: %s", path, r.ErrorString())
	}
	qt.QGuiApplication_Clipboard().SetImage(img)
	return nil
}

func newImageViewer(m *MainWindow) *imageViewer {
	// Окно с родителем: закроется вместе с окном сервера и не держит приложение.
	v := &imageViewer{m: m, w: qt.NewQWidget3(m.w, qt.Window), fit: true, scale: 1}
	setName(v.w.QObject, "imageViewer")
	v.w.SetAttribute(qt.WA_DeleteOnClose)
	v.w.Resize(960, 640)

	button := func(name, tip string) *qt.QToolButton {
		b := qt.NewQToolButton2()
		setName(b.QObject, name)
		b.SetFixedSize2(32, 32)
		b.SetIconSize(tmp(qt.NewQSize2(16, 16)))
		b.SetFocusPolicy(qt.NoFocus)
		b.SetToolTip(tip)
		return b
	}
	v.title = qt.NewQLabel2()
	setName(v.title.QObject, "viewerTitle")
	v.title.SetTextFormat(qt.PlainText)
	v.count = qt.NewQLabel2()
	setName(v.count.QObject, "viewerCount")
	v.copy = button("viewerCopy", "Копировать в буфер обмена (Ctrl+C)")
	v.copy.OnClicked(v.copyCurrent)
	v.save = button("viewerSave", "Сохранить…")
	v.save.OnClicked(v.saveCurrent)
	v.open = button("viewerOpen", "")
	v.open.OnClicked(v.openCurrent)
	v.close = button("viewerClose", "Закрыть (Esc)")
	v.close.OnClicked(func() { v.w.Close() })

	bar := qt.NewQFrame2()
	setName(bar.QObject, "viewerBar")
	bar.SetFixedHeight(44)
	bl := qt.NewQHBoxLayout(bar.QWidget)
	bl.SetContentsMargins(16, 0, 8, 0)
	bl.SetSpacing(8)
	bl.AddWidget2(v.title.QWidget, 1)
	bl.AddWidget(v.count.QWidget)
	bl.AddWidget(v.copy.QWidget)
	bl.AddWidget(v.save.QWidget)
	bl.AddWidget(v.open.QWidget)
	bl.AddWidget(v.close.QWidget)

	v.canvas = qt.NewQWidget2()
	setName(v.canvas.QObject, "viewerCanvas")
	v.canvas.OnPaintEvent(func(_ func(*qt.QPaintEvent), _ *qt.QPaintEvent) { v.paint() })
	v.canvas.OnResizeEvent(func(super func(*qt.QResizeEvent), e *qt.QResizeEvent) {
		super(e)
		v.relayout()
	})
	v.canvas.OnWheelEvent(func(_ func(*qt.QWheelEvent), e *qt.QWheelEvent) {
		switch d := e.AngleDelta().Y(); {
		case d > 0:
			v.zoomAt(viewerZoom, e.Position())
		case d < 0:
			v.zoomAt(1/viewerZoom, e.Position())
		}
	})
	v.canvas.OnMousePressEvent(func(super func(*qt.QMouseEvent), e *qt.QMouseEvent) {
		if e.Button() != qt.LeftButton {
			super(e)
			return
		}
		v.pressAt, v.dragged = e.Position(), false
		v.pressPanX, v.pressPanY = v.panX, v.panY
	})
	v.canvas.OnMouseMoveEvent(func(super func(*qt.QMouseEvent), e *qt.QMouseEvent) {
		if v.pressAt == nil {
			super(e)
			return
		}
		dx, dy := e.Position().X()-v.pressAt.X(), e.Position().Y()-v.pressAt.Y()
		if !v.dragged && math.Hypot(dx, dy) < 4 {
			return
		}
		v.dragged = true
		v.canvas.SetCursor(tmp(qt.NewQCursor2(qt.ClosedHandCursor)))
		v.panX, v.panY = v.pressPanX+dx, v.pressPanY+dy
		v.clampPan()
		v.canvas.Update()
	})
	v.canvas.OnMouseReleaseEvent(func(super func(*qt.QMouseEvent), e *qt.QMouseEvent) {
		if e.Button() != qt.LeftButton || v.pressAt == nil {
			super(e)
			return
		}
		v.pressAt = nil
		v.canvas.UnsetCursor()
		if !v.dragged && !v.imageRect().ContainsWithQPointF(e.Position()) {
			v.w.Close() // клик по фону
		}
	})
	v.canvas.OnMouseDoubleClickEvent(func(_ func(*qt.QMouseEvent), e *qt.QMouseEvent) {
		if v.imageRect().ContainsWithQPointF(e.Position()) {
			v.toggleActual(e.Position())
		}
	})

	lay := qt.NewQVBoxLayout(v.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.SetSpacing(0)
	lay.AddWidget(bar.QWidget)
	lay.AddWidget2(v.canvas, 1)

	v.w.OnKeyPressEvent(func(super func(*qt.QKeyEvent), e *qt.QKeyEvent) {
		if e.Matches(qt.QKeySequence__Copy) {
			v.copyCurrent()
			return
		}
		center := tmp(qt.NewQPointF3(float64(v.canvas.Width())/2, float64(v.canvas.Height())/2))
		switch qt.Key(e.Key()) {
		case qt.Key_Escape:
			v.w.Close()
		case qt.Key_Left:
			v.step(-1)
		case qt.Key_Right, qt.Key_Space:
			v.step(1)
		case qt.Key_Plus, qt.Key_Equal:
			v.zoomAt(viewerZoom, center)
		case qt.Key_Minus:
			v.zoomAt(1/viewerZoom, center)
		case qt.Key_0:
			v.fitToScreen()
		case qt.Key_1:
			v.toggleActual(center)
		default:
			super(e)
		}
	})
	v.flashTimer = qt.NewQTimer2(v.w.QObject)
	v.flashTimer.SetSingleShot(true)
	v.flashTimer.OnTimeout(func() {
		if !v.closed && v.msg == v.flashText {
			v.msg = ""
			v.canvas.Update()
		}
	})
	v.unsubscribe = OnThemeChanged(v.restyle)
	// Окно удаляется после текущих событий: новый клик по картинке до этого
	// должен открыть новое окно, а не закрывающееся.
	v.w.OnCloseEvent(func(super func(*qt.QCloseEvent), e *qt.QCloseEvent) {
		super(e)
		if m.viewer == v {
			m.viewer = nil
		}
	})
	v.w.OnDestroyed(func() {
		v.closed = true
		v.unsubscribe()
		v.stopMovie()
		v.setImage(nil)
		if m.viewer == v {
			m.viewer = nil
		}
	})
	v.restyle()
	return v
}

// show — i-я картинка поста: сразу превью (если есть), оригинал догружается.
func (v *imageViewer) show(i int) {
	v.cur = i
	v.gen++
	gen := v.gen
	it := v.items[i]
	v.stopMovie()
	v.setImage(nil)
	v.path, v.msg = "", "Загружаем…"
	v.copyWanted = false
	v.fit, v.panX, v.panY = true, 0, 0
	v.baseW, v.baseH = 0, 0
	if it.fi != nil && it.fi.Width > 0 && it.fi.Height > 0 {
		v.baseW, v.baseH = float64(it.fi.Width), float64(it.fi.Height)
	}
	if it.preview != "" {
		if img := qt.NewQImage8(it.preview); !img.IsNull() {
			v.setImage(img)
			if v.baseW == 0 {
				v.baseW, v.baseH = float64(img.Width()), float64(img.Height())
			}
		} else {
			free(img)
		}
	}
	v.title.SetText(v.m.mediaTitle(it.name, v.post))
	v.w.SetWindowTitle(it.name)
	v.count.SetVisible(len(v.items) > 1)
	v.count.SetText(fmt.Sprintf("%d / %d", i+1, len(v.items)))
	switch {
	case it.fi != nil:
		v.open.SetToolTip("Открыть в приложении")
		v.open.SetVisible(canOpen(it.fi))
	default:
		v.open.SetToolTip("Открыть в браузере")
		v.open.SetVisible(true)
	}
	v.relayout()

	v.m.original(it, func(path string, err error) {
		if v.closed || v.gen != gen {
			return
		}
		if err != nil {
			v.msg = "Не удалось загрузить картинку: " + err.Error()
			v.m.log.Warn("image viewer", "image", truncate(it.name, 200), "err", err)
			v.canvas.Update()
			return
		}
		v.loadFull(path)
	})
}

// loadFull — оригинал с диска: GIF — анимацией, остальное — с поворотом по EXIF.
func (v *imageViewer) loadFull(path string) {
	r := qt.NewQImageReader3(path)
	defer free(r)
	r.SetAutoTransform(true)
	if r.SupportsAnimation() && r.ImageCount() != 1 {
		mv := qt.NewQMovie3(path)
		if mv.IsValid() {
			v.movie = mv
			mv.OnFrameChanged(func(int) {
				if v.closed || v.movie != mv {
					return
				}
				v.setImage(mv.CurrentImage())
				v.canvas.Update()
			})
			mv.Start()
			v.loaded(path, mv.CurrentImage())
			return
		}
	}
	img := r.Read()
	if img.IsNull() {
		free(img)
		v.msg = "Не удалось открыть картинку: " + r.ErrorString()
		v.canvas.Update()
		return
	}
	v.loaded(path, img)
}

func (v *imageViewer) loaded(path string, img *qt.QImage) {
	w, h := float64(img.Width()), float64(img.Height())
	if !v.fit && v.baseW > 0 {
		v.scale *= v.baseW / w // тот же размер на экране, что у превью
	}
	v.setImage(img)
	v.path, v.msg = path, ""
	v.baseW, v.baseH = w, h
	v.relayout()
	if v.copyWanted {
		v.copyCurrent()
	}
}

// copyCurrent — «Копировать» (Ctrl+C): оригинал в буфер обмена, GIF — текущим
// кадром. Оригинал ещё грузится — скопируется, когда загрузится.
func (v *imageViewer) copyCurrent() {
	if v.path == "" || v.img == nil {
		v.copyWanted = true
		return
	}
	v.copyWanted = false
	qt.QGuiApplication_Clipboard().SetImage(v.img)
	v.flash("Скопировано в буфер обмена")
}

// flash — подпись поверх картинки на viewerFlash.
func (v *imageViewer) flash(text string) {
	v.msg, v.flashText = text, text
	v.canvas.Update()
	v.flashTimer.Start(int(viewerFlash / time.Millisecond))
}

// setImage — картинка на экране; прежняя (кадр GIF — на каждом шаге, мегабайты)
// удаляется сразу.
func (v *imageViewer) setImage(img *qt.QImage) {
	if v.img != img {
		free(v.img)
	}
	v.img = img
}

func (v *imageViewer) stopMovie() {
	if v.movie != nil {
		v.movie.Stop()
		v.movie.DeleteLater()
		v.movie = nil
	}
}

func (v *imageViewer) step(d int) {
	if n := len(v.items); n > 1 {
		v.show((v.cur + d + n) % n)
	}
}

// fitScale — масштаб, при котором картинка целиком в окне; больше 1 не бывает.
func (v *imageViewer) fitScale() float64 {
	if v.baseW <= 0 || v.baseH <= 0 {
		return 1
	}
	aw := float64(max(1, v.canvas.Width()-2*viewerMargin))
	ah := float64(max(1, v.canvas.Height()-2*viewerMargin))
	return min(1, aw/v.baseW, ah/v.baseH)
}

func (v *imageViewer) relayout() {
	if v.fit {
		v.scale, v.panX, v.panY = v.fitScale(), 0, 0
	}
	v.clampPan()
	v.canvas.Update()
}

func (v *imageViewer) fitToScreen() {
	v.fit = true
	v.relayout()
}

// zoomAt — масштаб в factor раз; точка картинки под at остаётся на месте.
func (v *imageViewer) zoomAt(factor float64, at *qt.QPointF) {
	v.zoomTo(v.scale*factor, at)
}

func (v *imageViewer) zoomTo(scale float64, at *qt.QPointF) {
	if v.baseW <= 0 {
		return
	}
	scale = max(min(scale, 16), min(v.fitScale(), 0.1))
	cx, cy := float64(v.canvas.Width())/2+v.panX, float64(v.canvas.Height())/2+v.panY
	ux, uy := (at.X()-cx)/v.scale, (at.Y()-cy)/v.scale
	v.scale, v.fit = scale, false
	v.panX = at.X() - ux*scale - float64(v.canvas.Width())/2
	v.panY = at.Y() - uy*scale - float64(v.canvas.Height())/2
	v.clampPan()
	v.canvas.Update()
}

// toggleActual — 1:1 у точки at, а если уже 1:1 — по экрану.
func (v *imageViewer) toggleActual(at *qt.QPointF) {
	if !v.fit && v.scale == 1 {
		v.fitToScreen()
		return
	}
	v.zoomTo(1, at)
}

// clampPan — картинка меньше окна стоит по центру, больше — не отъезжает от края.
func (v *imageViewer) clampPan() {
	lim := func(pan, size, view float64) float64 {
		if size <= view {
			return 0
		}
		d := (size - view) / 2
		return max(-d, min(d, pan))
	}
	v.panX = lim(v.panX, v.baseW*v.scale, float64(v.canvas.Width()))
	v.panY = lim(v.panY, v.baseH*v.scale, float64(v.canvas.Height()))
}

// imageRect — где картинка в окне.
func (v *imageViewer) imageRect() *qt.QRectF {
	w, h := v.baseW*v.scale, v.baseH*v.scale
	return tmp(qt.NewQRectF4(float64(v.canvas.Width())/2+v.panX-w/2, float64(v.canvas.Height())/2+v.panY-h/2, w, h))
}

func (v *imageViewer) paint() {
	p := qt.NewQPainter2(v.canvas.QPaintDevice)
	defer free(p)
	p.FillRect5(0, 0, v.canvas.Width(), v.canvas.Height(), qcolor(viewerBg))
	if v.img != nil && v.baseW > 0 {
		p.SetRenderHint(qt.QPainter__SmoothPixmapTransform)
		p.DrawImage5(v.imageRect(), v.img)
	}
	if v.msg != "" {
		p.SetPen(qcolor("#a8a49e"))
		p.SetFont(feedFont())
		r := tmp(qt.NewQRect4(viewerMargin, 0, max(1, v.canvas.Width()-2*viewerMargin), v.canvas.Height()-viewerMargin))
		align := qt.AlignHCenter | qt.AlignBottom
		if v.img == nil {
			align = qt.AlignCenter
		}
		p.DrawText6(r, int(align)|int(qt.TextWordWrap), v.msg)
	}
}

// saveCurrent — «Сохранить…»: вложение — как из ленты; картинку по ссылке —
// из кэша, когда она загружена.
func (v *imageViewer) saveCurrent() {
	it := v.items[v.cur]
	if it.fi != nil {
		v.m.saveFile(it.fi)
		return
	}
	if v.path == "" {
		return
	}
	name := "image"
	if u, err := url.Parse(it.src); err == nil && path.Base(u.Path) != "/" && path.Base(u.Path) != "." {
		name = path.Base(u.Path)
	}
	target := qt.QFileDialog_GetSaveFileName3(v.w, "Сохранить картинку", filepath.Join(downloadsDir(), cache.SafeName(name, "image")))
	if target == "" {
		return
	}
	if err := copyFile(v.path, target); err != nil {
		v.m.fail("save image", err)
		return
	}
	v.m.message("Сохранено: " + target)
}

// openCurrent — вложение системным приложением (если тип можно), картинку по
// ссылке — её страницей в браузере.
func (v *imageViewer) openCurrent() {
	it := v.items[v.cur]
	if it.fi != nil {
		v.m.openFile(it.fi)
		return
	}
	v.m.handleLink(it.link)
}

// restyle — панель из текущей темы; сама картинка — всегда на тёмном.
func (v *imageViewer) restyle() {
	pal := Pal()
	v.copy.SetIcon(icon("copy", pal.Tx2, 16))
	v.save.SetIcon(icon("download", pal.Tx2, 16))
	v.open.SetIcon(icon("external-link", pal.Tx2, 16))
	v.close.SetIcon(icon("x", pal.Tx2, 16))
	v.w.SetStyleSheet(fmt.Sprintf(`
QWidget#imageViewer { background: %[6]s; }
QFrame#viewerBar { background: %[1]s; border-bottom: 1px solid %[2]s; }
QLabel#viewerTitle { color: %[4]s; }
QLabel#viewerCount { font-family: "%[5]s"; font-size: 12px; color: %[3]s; }
QToolButton { background: transparent; border: none; border-radius: 4px; }
QToolButton:hover { background: %[7]s; }
`, pal.Surf, pal.Line, pal.Tx2, pal.Tx, monoFamily(), viewerBg, pal.Hover))
}
