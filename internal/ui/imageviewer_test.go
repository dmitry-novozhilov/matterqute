package ui

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Картинка во весь экран открывается в приложении: вложение и картинка по
// ссылке одного поста листаются, масштаб и сдвиг, клик по фону закрывает.
func TestImageViewer(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.Show()
		if !waitFor(t, "sidebar", func() bool { return len(m.items) > 0 }) {
			return
		}
		backend := channelByName(m, "dev", "backend")
		m.selectChannel(backend.Id)
		if !waitFor(t, "backend posts", func() bool { return len(m.feed.items) >= 5 && len(m.feed.pending) == 0 }) {
			return
		}
		var img *model.FileInfo
		for _, it := range m.feed.items {
			if it.post.Metadata != nil && len(it.post.Metadata.Files) > 0 && it.post.Metadata.Files[0].Name == "схема.png" {
				img = it.post.Metadata.Files[0]
			}
		}
		if img == nil {
			t.Error("seeded схема.png not in backend")
			return
		}
		// Пост со вложением и картинкой по ссылке.
		photo := s.ServerURL + "/static/photo.png"
		p := fake.Post(&model.Post{ChannelId: backend.Id, UserId: m.st.UserByName("bob").Id, Message: "Две картинки " + photo,
			Metadata: &model.PostMetadata{Files: []*model.FileInfo{img}, Embeds: []*model.PostEmbed{{Type: model.PostEmbedImage, URL: photo}},
				Images: map[string]*model.PostImage{photo: {Width: 640, Height: 360}}}})
		if !waitFor(t, "post with two images", func() bool {
			i := m.feed.index(p.Id)
			return i >= 0 && strings.Contains(m.feed.items[i].html, imageLink+img.Id) && strings.Contains(m.feed.items[i].html, linkImageLink+p.Id+":0")
		}) {
			return
		}

		m.feed.onAnchor(imageLink + img.Id)
		v := m.viewer
		if v == nil || !v.w.IsVisible() || !v.w.IsFullScreen() {
			t.Error("click on the image did not open the viewer full screen")
			return
		}
		if !waitFor(t, "original loaded", func() bool { return v.path != "" && v.msg == "" && v.img != nil }) {
			t.Logf("viewer message %q", v.msg)
			return
		}
		if v.img.Width() != 640 || v.img.Height() != 360 || v.title.Text() != "схема.png — bob, # backend" || !v.count.IsHidden() {
			t.Errorf("viewer shows %dx%d %q, counter hidden %v", v.img.Width(), v.img.Height(), v.title.Text(), v.count.IsHidden())
		}
		// Меньше экрана — в своём размере, по центру.
		r := v.imageRect()
		if v.scale != 1 || int(r.Width()) != 640 || abs(int(r.X()+r.Width()/2)-v.canvas.Width()/2) > 1 {
			t.Errorf("small image: scale %v, rect %.0f,%.0f %.0fx%.0f in %dx%d", v.scale, r.X(), r.Y(), r.Width(), r.Height(), v.canvas.Width(), v.canvas.Height())
		}
		shotWidget(t, v.w, "image-viewer")

		// Колесо — масштаб у курсора: точка картинки под ним остаётся на месте.
		at := qt.NewQPointF3(float64(v.canvas.Width())/2+30, float64(v.canvas.Height())/2+20)
		v.zoomAt(2, at)
		r = v.imageRect()
		ux, uy := (at.X()-r.X())/2, (at.Y()-r.Y())/2
		qt.QCoreApplication_SendEvent(v.canvas.QObject, qt.NewQWheelEvent(at, v.canvas.MapToGlobal(at), *qt.NewQPoint(), *qt.NewQPoint2(0, 120),
			qt.NoButton, qt.NoModifier, qt.NoScrollPhase, false).QEvent)
		if v.fit || v.scale != 2*viewerZoom {
			t.Errorf("wheel zoom: scale %v fit %v", v.scale, v.fit)
		}
		if r2 := v.imageRect(); abs(int(r2.X()+ux*v.scale)-int(at.X())) > 1 || abs(int(r2.Y()+uy*v.scale)-int(at.Y())) > 1 {
			t.Errorf("point under the cursor moved: image at %.0f,%.0f", r2.X(), r2.Y())
		}
		// Перетаскивание сдвигает, а не закрывает.
		send := func(typ qt.QEvent__Type, x, y float64, buttons qt.MouseButton) {
			btn := qt.LeftButton
			if typ == qt.QEvent__MouseMove {
				btn = qt.NoButton
			}
			ev := qt.NewQMouseEvent(typ, qt.NewQPointF3(x, y), btn, buttons, qt.NoModifier)
			qt.QCoreApplication_SendEvent(v.canvas.QObject, ev.QEvent)
		}
		before := v.panX
		send(qt.QEvent__MouseButtonPress, 300, 300, qt.LeftButton)
		send(qt.QEvent__MouseMove, 340, 300, qt.LeftButton)
		send(qt.QEvent__MouseButtonRelease, 340, 300, qt.NoButton)
		if v.closed || v.panX == before {
			t.Errorf("drag: closed %v, pan %v → %v", v.closed, before, v.panX)
		}
		// 0 — снова по экрану.
		qt.QCoreApplication_SendEvent(v.w.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_0), qt.NoModifier).QEvent)
		if !v.fit || v.scale != 1 || v.panX != 0 {
			t.Errorf("fit: scale %v pan %v", v.scale, v.panX)
		}

		// Пост с двумя картинками: → — картинка по ссылке того же поста.
		m.openImage(feedPost(m.feed, p.Id), img.Id)
		if m.viewer != v || v.count.Text() != "1 / 2" || v.count.IsHidden() {
			t.Errorf("two images: viewer reused %v, counter %q", m.viewer == v, v.count.Text())
		}
		qt.QCoreApplication_SendEvent(v.w.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_Right), qt.NoModifier).QEvent)
		if v.count.Text() != "2 / 2" || v.items[v.cur].src != photo {
			t.Errorf("next image: %q %q", v.count.Text(), v.items[v.cur].src)
		}
		if !waitFor(t, "link image loaded", func() bool { return v.path != "" && v.img != nil && v.img.Width() == 640 }) {
			return
		}

		// Клик по фону — закрыть.
		send(qt.QEvent__MouseButtonPress, 3, 3, qt.LeftButton)
		send(qt.QEvent__MouseButtonRelease, 3, 3, qt.NoButton)
		settle()
		if m.viewer != nil {
			t.Error("click on the background did not close the viewer")
		}

		// GIF крутится и здесь.
		for _, it := range m.feed.items {
			if it.post.Metadata != nil && len(it.post.Metadata.Files) > 0 && it.post.Metadata.Files[0].Name == "deploy.gif" {
				m.openImage(it.post, it.post.Metadata.Files[0].Id)
			}
		}
		if v = m.viewer; v == nil {
			t.Error("deploy.gif did not open")
			return
		}
		var frames []int
		waitFor(t, "GIF animating in the viewer", func() bool {
			if v.movie == nil || v.img == nil || v.img.Width() != 160 {
				return false
			}
			if n := v.movie.CurrentFrameNumber(); len(frames) == 0 || frames[len(frames)-1] != n {
				frames = append(frames, n)
			}
			return len(frames) >= 3
		})
		v.w.Close()
		settle()

		// Картинка по ссылке из ленты открывается тоже здесь, Esc закрывает.
		m.feed.onAnchor(linkImageLink + p.Id + ":0")
		if v = m.viewer; v == nil || v.items[v.cur].src != photo {
			t.Error("click on the link image did not open it in the viewer")
			return
		}
		qt.QCoreApplication_SendEvent(v.w.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_Escape), qt.NoModifier).QEvent)
		settle()
		if m.viewer != nil {
			t.Error("Esc did not close the viewer")
		}
	})
}

// Картинка копируется в буфер обмена оригиналом: из меню ленты (вложение и
// картинка по ссылке), из списка файлов и из просмотра по Ctrl+C.
func TestCopyImage(t *testing.T) {
	fake, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		var msgs []string
		m.OnMessage = func(text string) { msgs = append(msgs, text) }
		m.Show()
		if !waitFor(t, "sidebar", func() bool { return len(m.items) > 0 }) {
			return
		}
		backend := channelByName(m, "dev", "backend")
		m.selectChannel(backend.Id)
		if !waitFor(t, "backend posts", func() bool { return len(m.feed.items) >= 5 && len(m.feed.pending) == 0 }) {
			return
		}
		var img *model.FileInfo
		for _, it := range m.feed.items {
			if it.post.Metadata != nil && len(it.post.Metadata.Files) > 0 && it.post.Metadata.Files[0].Name == "схема.png" {
				img = it.post.Metadata.Files[0]
			}
		}
		if img == nil {
			t.Error("seeded схема.png not in backend")
			return
		}
		photo := s.ServerURL + "/static/photo.png"
		p := fake.Post(&model.Post{ChannelId: backend.Id, UserId: m.st.UserByName("bob").Id, Message: "Две картинки " + photo,
			Metadata: &model.PostMetadata{Files: []*model.FileInfo{img}, Embeds: []*model.PostEmbed{{Type: model.PostEmbedImage, URL: photo}},
				Images: map[string]*model.PostImage{photo: {Width: 640, Height: 360}}}})
		if !waitFor(t, "post with two images", func() bool { return m.feed.index(p.Id) >= 0 }) {
			return
		}
		cb := qt.QGuiApplication_Clipboard()
		clipSize := func() (int, int) {
			i := cb.Image() // освобождает финализатор MIQT
			return i.Width(), i.Height()
		}
		copied := func(w, h int) bool { cw, ch := clipSize(); return cw == w && ch == h }

		// Меню ленты: по ссылке картинки под курсором — пост и ключ.
		if owner, key, ok := m.feed.imageAt(imageLink + img.Id); !ok || owner == nil || key != img.Id {
			t.Errorf("imageAt(attachment) = %v, %q, %v", owner != nil, key, ok)
		}
		if owner, key, ok := m.feed.imageAt(linkImageLink + p.Id + ":0"); !ok || owner == nil || owner.Id != p.Id || key != "link:0" {
			t.Errorf("imageAt(link image) = %v, %q, %v", owner != nil, key, ok)
		}
		if _, _, ok := m.feed.imageAt("https://example.com/a.png"); ok {
			t.Error("imageAt(web link) found an image")
		}
		cb.Clear()
		m.copyImage(p, img.Id)
		if !waitFor(t, "attachment copied", func() bool { return copied(640, 360) }) {
			return
		}
		if len(msgs) == 0 || msgs[len(msgs)-1] != "Картинка скопирована в буфер обмена" {
			t.Errorf("status after copy: %q", msgs)
		}
		cb.Clear()
		m.copyImage(p, "link:0")
		if !waitFor(t, "link image copied", func() bool { return copied(640, 360) }) {
			return
		}
		// Список файлов — тем же путём, без поста.
		cb.Clear()
		m.copyImageItem(viewerItem{key: img.Id, name: img.Name, fi: img})
		if !waitFor(t, "file list image copied", func() bool { return copied(640, 360) }) {
			return
		}

		// Просмотр: Ctrl+C — оригинал; нажали до загрузки — скопируется после.
		cb.Clear()
		m.openImage(feedPost(m.feed, p.Id), img.Id)
		v := m.viewer
		if v == nil {
			t.Error("viewer did not open")
			return
		}
		qt.QCoreApplication_SendEvent(v.w.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_C), qt.ControlModifier).QEvent)
		if !waitFor(t, "viewer copied", func() bool { return copied(640, 360) }) {
			return
		}
		if v.copyWanted || v.msg != "Скопировано в буфер обмена" {
			t.Errorf("viewer after copy: wanted %v, msg %q", v.copyWanted, v.msg)
		}
		if !waitFor(t, "flash gone", func() bool { return v.msg == "" }) {
			return
		}
		// Кнопка — то же.
		cb.Clear()
		v.copy.Click()
		if !copied(640, 360) {
			w, h := clipSize()
			t.Errorf("copy button: clipboard %dx%d", w, h)
		}
		v.w.Close()
		settle()
	})
}
