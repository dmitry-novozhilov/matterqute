package ui

import (
	"strings"

	qt "github.com/mappu/miqt/qt6"
)

// Картинки ленты по требованию. Вложения, превью роликов и картинки
// предпросмотра ссылок раскодированными — мегабайты каждая (вдвое крупнее
// экранного размера, по 4 байта на точку), а лента каждого канала собрана
// заранее, со всеми последними сообщениями: на настоящем сервере 343
// картинки в 43 лентах — около 500 МБ, хотя на экране из них несколько.
// Раскладке документа они не нужны: размер записан в HTML (width, height).
// Поэтому при вставке поста ресурс — общая заглушка 1×1 (Qt растягивает её
// на место картинки), картинка раскодируется, когда пост виден (paintUnder),
// и отпускается, когда лента ушла с экрана или пост — дальше пары экранов от
// видимого места (releaseFar).

// lazyImage — картинка ресурса: как её получить и что ещё сделать при этом.
type lazyImage struct {
	load      func() *qt.QPixmap // новая картинка или nil (не читается)
	onLoad    func()             // GIF: крутить кадры
	onRelease func()             // GIF: остановить
}

// lazyKeep — сколько экранов выше и ниже видимого держать раскодированными.
const lazyKeep = 2

// lazyDot — заглушка 1×1 цветом поля (общая: не удалять).
func lazyDot() *qt.QPixmap {
	color := Pal().Surf2
	key := "lazy|" + color
	if pm := style.icons[key]; pm != nil {
		return pm
	}
	pm := qt.NewQPixmap2(1, 1)
	pm.FillWithFillColor(qcolor(color))
	style.icons[key] = pm
	return pm
}

// scaledFile — картинка с диска размером w×h (логических px, вдвое крупнее
// в точках); nil — файл не читается.
func scaledFile(path string, w, h int) *qt.QPixmap {
	src := qt.NewQPixmap4(path)
	defer free(src)
	if src.IsNull() {
		return nil
	}
	pm := src.Scaled3(w*resourceDPR, h*resourceDPR, qt.KeepAspectRatio, qt.SmoothTransformation)
	pm.SetDevicePixelRatio(resourceDPR)
	return pm
}

// addLazy — ресурс name w×h: пока заглушка, картинка — когда пост будет виден.
func (f *Feed) addLazy(name string, w, h int, lz lazyImage) {
	f.lazy[name] = lz
	delete(f.loaded, name)
	f.addResource(name, lazyDot(), w, h)
}

// lazyNames — картинки по требованию в HTML поста.
func (f *Feed) lazyNames(html string) []string {
	var out []string
	for _, m := range resourceSrcRe.FindAllStringSubmatch(html, -1) {
		if _, ok := f.lazy[m[1]]; ok {
			out = append(out, m[1])
		}
	}
	return out
}

// loadImages — картинки постов lo…hi (видимых) раскодировать, если ещё нет;
// сдвинулось видимое — через секунду отпустить далёкие.
func (f *Feed) loadImages(lo, hi int) {
	for _, it := range f.items[lo : hi+1] {
		for _, name := range it.images {
			lz, ok := f.lazy[name]
			if !ok || f.loaded[name] {
				continue
			}
			f.loaded[name] = true // и если не читается: не пробовать на каждой отрисовке
			if pm := lz.load(); pm != nil {
				f.setResource(name, pm)
				free(pm) // у документа своя копия
			}
			if lz.onLoad != nil {
				lz.onLoad()
			}
		}
	}
	if lo != f.loadedLo || hi != f.loadedHi {
		f.loadedLo, f.loadedHi = lo, hi
		f.releaseTimer.Start(1000)
	}
}

// releaseFar — отпустить картинки постов дальше lazyKeep экранов от видимого.
func (f *Feed) releaseFar() {
	if !f.shown || len(f.loaded) == 0 {
		return
	}
	h := float64(f.view.Viewport().Height())
	top := float64(f.bar.Value()) - lazyKeep*h
	bottom := float64(f.bar.Value()) + (1+lazyKeep)*h
	lay := f.doc.DocumentLayout()
	keep := map[string]bool{}
	for _, it := range f.items {
		if len(it.images) == 0 {
			continue
		}
		if r := tmp(lay.FrameBoundingRect(it.frame)); r.Bottom() >= top && r.Top() <= bottom {
			for _, name := range it.images {
				keep[name] = true
			}
		}
	}
	f.releaseImages(func(name string) bool { return keep[name] })
}

// releaseImages — раскодированные картинки, кроме keep (nil — все), снова
// заглушкой.
func (f *Feed) releaseImages(keep func(name string) bool) {
	for name := range f.loaded {
		if keep != nil && keep(name) {
			continue
		}
		delete(f.loaded, name)
		lz, ok := f.lazy[name]
		if !ok {
			continue // убран из документа (sweepResources)
		}
		f.setResource(name, lazyDot())
		if lz.onRelease != nil {
			lz.onRelease()
		}
	}
}

// stopMovie — GIF файла id больше не крутить: QMovie прочь.
func (f *Feed) stopMovie(id string) {
	if mv := f.movies[id]; mv != nil {
		mv.Stop()
		mv.DeleteLater()
		delete(f.movies, id)
	}
	delete(f.movieAt, id)
}

// isSizedResource — имя ресурса с размером: картинка, ролик, предпросмотр,
// заглушка (sweepResources).
func isSizedResource(name string) bool {
	for _, p := range []string{"preview:", "link:", "video:", "placeholder:"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
