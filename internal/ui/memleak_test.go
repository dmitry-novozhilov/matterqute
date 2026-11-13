package ui

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/multimedia"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/memstat"
	"matterqute/internal/mmfake"
)

// Память объектов Qt (qtmem.go): то, что повторяется всё время работы, —
// кадры GIF, ролика, анимированного эмодзи, отрисовка ленты и списков,
// перерисовка постов — не копит память. Меряем кучу C (malloc), а не RSS:
// освобождённое glibc держит у себя, RSS не опускается; и сборщик мусора Go
// между замерами не зовём — картинка, которую удалил бы только финализатор,
// до сборки мусора тоже занимает память (из-за этого mq съел 28 ГБ).

// needMalloc — без статистики malloc (не glibc) тестам нечего мерить.
func needMalloc(t *testing.T) {
	if _, ok := memstat.MallocInUse(); !ok {
		t.Skip("malloc statistics unavailable on this platform")
	}
}

// mallocGrowth — на сколько выросла куча C за do. До замера — сборка мусора
// и цикл событий (временные объекты tmp), после — только цикл событий.
func mallocGrowth(do func()) int64 {
	for range 2 {
		runtime.GC()
		settle()
	}
	before, _ := memstat.MallocInUse()
	do()
	settle()
	after, _ := memstat.MallocInUse()
	return int64(after) - int64(before)
}

// leastGrowth — наименьший рост из трёх замеров: утечка растёт каждый раз,
// а разовое (кэши шрифтов и глифов Qt, которые наполнили другие тесты) — нет.
func leastGrowth(do func()) int64 {
	least := mallocGrowth(do)
	for range 2 {
		least = min(least, mallocGrowth(do))
	}
	return least
}

func mb(n int64) float64 { return float64(n) / (1 << 20) }

// Анимированное эмодзи в реакции: плашка рисуется заново на каждом кадре.
func TestMemoryAnimatedEmoji(t *testing.T) {
	needMalloc(t)
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		src := "reaction:" + root.Id + ":partyparrot"
		if !waitFor(t, "animated partyparrot pill", func() bool {
			_, sub := m.feed.animSubs["partyparrot"]
			return sub && m.feed.pills[src].render != nil
		}) {
			return
		}
		frames := func(n int) {
			for range n {
				m.feed.emojiFrame("partyparrot")
			}
		}
		frames(50) // прогрев
		const n = 2000
		if grew := mallocGrowth(func() { frames(n) }); grew > 2<<20 {
			t.Errorf("%d frames of an animated reaction: malloc grew by %.1f MB", n, mb(grew))
		}
	})
}

// GIF в ленте: каждый кадр — новая картинка в ресурсе документа.
func TestMemoryGIFFrames(t *testing.T) {
	needMalloc(t)
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.Show()
		if !waitFor(t, "sidebar", func() bool { return len(m.items) > 0 }) {
			return
		}
		m.selectChannel(channelByName(m, "dev", "backend").Id)
		var gif string
		var mv *qt.QMovie
		if !waitFor(t, "GIF animating", func() bool {
			for _, it := range m.feed.items {
				if it.post.Metadata != nil && len(it.post.Metadata.Files) > 0 && it.post.Metadata.Files[0].MimeType == "image/gif" {
					gif = it.post.Metadata.Files[0].Id
				}
			}
			mv = m.feed.movies[gif]
			_, placed := m.feed.movieAt[gif]
			return mv != nil && mv.IsValid() && mv.State() == qt.QMovie__Running && placed
		}) {
			return
		}
		frames := func(n int) {
			for range n {
				m.feed.putFrame(gif, mv)
			}
		}
		frames(20)
		const n = 300
		if grew := mallocGrowth(func() { frames(n) }); grew > 2<<20 {
			t.Errorf("%d GIF frames: malloc grew by %.1f MB", n, mb(grew))
		}
	})
}

// Ролик в ленте: кадр 1280×720 — 3,5 МБ, их десятки в секунду.
func TestMemoryVideoFrames(t *testing.T) {
	needMalloc(t)
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.Show()
		if !waitFor(t, "sidebar", func() bool { return len(m.items) > 0 }) {
			return
		}
		backend := channelByName(m, "dev", "backend")
		fis, _, err := s.Client.UploadFile(context.Background(), mmfake.DemoAVI(1280, 720, 30, 30), backend.Id, "big.avi")
		if err != nil {
			t.Errorf("upload video: %v", err)
			return
		}
		fi := fis.FileInfos[0]
		if _, _, err := s.Client.CreatePost(context.Background(), &model.Post{ChannelId: backend.Id, Message: "big video", FileIds: []string{fi.Id}}); err != nil {
			t.Errorf("post video: %v", err)
			return
		}
		m.selectChannel(backend.Id)
		if !waitFor(t, "video post", func() bool {
			for _, it := range m.feed.items {
				if it.post.Message == "big video" {
					return true
				}
			}
			return false
		}) {
			return
		}
		// Разобран (кадр-превью, длительность) — до щелчка: после разбора пост
		// перерисовывается, и играющий ролик встаёт на паузу.
		if !waitFor(t, "video probed", func() bool { _, ok := m.assets.Video(fi.Id); return ok }) {
			return
		}
		drained(m.feed)
		m.feed.onAnchor(videoLink + fi.Id)
		v := m.feed.videos[fi.Id]
		if v == nil {
			t.Error("no inline video after a click on the preview")
			return
		}
		v.audio.SetMuted(true)
		v.mp.SetLoops(int(multimedia.QMediaPlayer__Infinite))
		if !waitFor(t, "video playing", func() bool {
			return v.mp.PlaybackState() == multimedia.QMediaPlayer__PlayingState && v.frame != nil
		}) {
			return
		}
		settleFor(time.Second) // декодер и буферы — на месте
		frames, last := 0, v.frame
		grew := mallocGrowth(func() {
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				settleFor(10 * time.Millisecond)
				if v.frame != last {
					frames, last = frames+1, v.frame
				}
			}
		})
		if frames < 5 {
			t.Errorf("only %d video frames in 3 s: nothing to judge by", frames)
		}
		if grew > 16<<20 {
			t.Errorf("%d frames of 1280x720 video: malloc grew by %.1f MB", frames, mb(grew))
		}
	})
}

// Отрисовка ленты и сайдбара, перерисовка постов: каждый раз — художник,
// цвета, шрифты, курсоры документа.
func TestMemoryRepaint(t *testing.T) {
	needMalloc(t)
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		m.feed.stopEmojiAnimations() // кадры эмодзи — отдельный тест
		drained(m.feed)
		paint := func(n int) {
			for range n {
				m.feed.view.Viewport().Repaint()
				m.sidebar.Viewport().Repaint()
			}
		}
		paint(20)
		const n = 500
		if grew := leastGrowth(func() { paint(n) }); grew > 1<<20 {
			t.Errorf("%d repaints of the feed and sidebar: malloc grew by %.1f MB", n, mb(grew))
		}
		// Перерисовка постов. Qt держит в документе фреймы и таблицы, стёртые
		// из текста (пост с разделителем — свой фрейм текста), до его очистки:
		// это его доля, ~2 КБ на фрейм; наша — курсоры, форматы, плашки — ноль.
		render := func(n int) {
			for range n {
				m.feed.renderAll()
			}
		}
		render(2)
		const r = 20
		frames := len(m.feed.doc.Children())
		grew := mallocGrowth(func() { render(r) })
		orphans := len(m.feed.doc.Children()) - frames
		if allow := int64(orphans)*3<<10 + 1<<20; grew > allow {
			t.Errorf("%d re-renders of %d posts: malloc grew by %.1f MB, allowed %.1f MB for %d frames Qt keeps",
				r, len(m.feed.items), mb(grew), mb(allow), orphans)
		}
	})
}

// Окно шире — картинки перерисованы в новом размере, а прежние (имя ресурса с
// размером) из документа убраны: иначе каждая ширина оставляла бы мегабайты.
func TestSweepResources(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		m.w.Resize(900, 600)
		m.Show()
		if !waitFor(t, "sidebar", func() bool { return len(m.items) > 0 }) {
			return
		}
		m.selectChannel(channelByName(m, "dev", "backend").Id)
		previews := func() []string {
			var out []string
			for name := range m.feed.resources {
				if strings.HasPrefix(name, "preview:") {
					out = append(out, name)
				}
			}
			return out
		}
		if !waitFor(t, "image previews in the feed", func() bool { return len(previews()) > 0 }) {
			return
		}
		drained(m.feed)
		old := previews()
		m.w.Resize(1400, 600)
		// Крупная картинка выросла вместе с колонкой, мелкая (GIF 160×90) — нет.
		if !waitFor(t, "a preview in the new size", func() bool {
			return slices.ContainsFunc(previews(), func(n string) bool { return !slices.Contains(old, n) })
		}) {
			t.Logf("before %v, after %v", old, previews())
			return
		}
		drained(m.feed)
		var now []string // на что ссылаются посты
		for _, it := range m.feed.items {
			for _, g := range resourceSrcRe.FindAllStringSubmatch(it.html, -1) {
				if strings.HasPrefix(g[1], "preview:") {
					now = append(now, g[1])
				}
			}
		}
		for _, name := range old {
			if slices.Contains(now, name) {
				continue // того же размера
			}
			_, kept := m.feed.resources[name]
			if v := m.feed.doc.Resource(int(qt.QTextDocument__ImageResource), tmp(qt.NewQUrl3(name))); v.IsValid() || kept {
				t.Errorf("resource %s of the old width still in the document", name)
			}
		}
		for _, name := range now {
			if v := m.feed.doc.Resource(int(qt.QTextDocument__ImageResource), tmp(qt.NewQUrl3(name))); !v.IsValid() {
				t.Errorf("resource %s of the new width missing", name)
			}
		}
	})
}

// Документ скрытой ленты пересобирается раз в час, если в нём набрались
// фреймы перерисованных постов: их больше нет, посты и место — те же. Ленту
// на экране и свежую не трогаем.
func TestFeedRebuild(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		town := m.feed
		townID := channelByName(m, "dev", "town-square").Id
		ids := func(f *Feed) []string {
			var out []string
			for _, p := range append(slices.Clone(f.pending), f.posts()...) {
				out = append(out, p.Id)
			}
			return out
		}
		// Лента не у низа: пост у края — его место должно вернуться.
		town.ScrollToPost(town.items[20].post.Id)
		settle()
		anchor, top := town.topPost()
		m.selectChannel(channelByName(m, "dev", "backend").Id)
		if !waitFor(t, "backend on screen", func() bool { return m.feed != town && m.feed.ready }) {
			return
		}
		orphansAfter := func(f *Feed, renders int) int {
			for range renders {
				f.renderAll()
			}
			drained(f)
			return f.orphans()
		}
		want := ids(town)
		if n := orphansAfter(town, 3); n < feedRebuildMin {
			t.Errorf("3 re-renders of %d posts left %d frames, want at least %d", len(town.items), n, feedRebuildMin)
			return
		}

		// Собрана меньше часа назад — не трогаем.
		before := town.orphans()
		m.rebuildFeeds()
		if len(town.pending) > 0 || town.orphans() != before {
			t.Errorf("feed built %v ago rebuilt", time.Since(town.built).Round(time.Second))
		}
		// На экране — тоже, сколько бы ни прошло.
		shown := m.feed
		orphansAfter(shown, 2)
		shown.built = time.Now().Add(-2 * feedRebuildEvery)
		onScreen := shown.orphans()

		town.built = time.Now().Add(-2 * feedRebuildEvery)
		m.rebuildFeeds()
		if len(shown.pending) > 0 || shown.orphans() != onScreen {
			t.Error("feed on screen rebuilt")
		}
		if len(town.pending) == 0 {
			t.Error("hidden feed with old frames not rebuilt")
			return
		}
		drained(town)
		if n := town.orphans(); n != 0 || !slices.Equal(ids(town), want) || !town.ready {
			t.Errorf("after rebuild: %d frames left, ready %v, posts the same %v", n, town.ready, slices.Equal(ids(town), want))
		}

		// Вернулись — пост у края на своём месте; и ещё раз, не дождавшись
		// фоновой дорисовки: пост у края — в очереди.
		for _, wait := range []bool{true, false} {
			if !wait {
				m.selectChannel(channelByName(m, "dev", "backend").Id)
				settle()
				orphansAfter(town, 2)
				town.built = time.Now().Add(-2 * feedRebuildEvery)
				m.rebuildFeeds()
				if town.pendingIndex(anchor) < 0 {
					t.Errorf("post at the top already drawn: nothing to check")
				}
			}
			m.selectChannel(townID)
			settle()
			if id, got := town.topPost(); id != anchor || abs(got-top) > 1 {
				t.Errorf("back in town-square (drained before %v): top post %s at %d, was %s at %d", wait, id, got, anchor, top)
			}
		}
	})
}
