package ui

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
)

// Qt привязан к потоку, где создан QApplication, а go test гоняет тесты в своих
// горутинах. Поэтому главный поток крутит цикл событий, а тела тестов идут в
// нём через onMain.
func TestMain(m *testing.M) {
	if os.Getenv("QT_QPA_PLATFORM") == "" {
		os.Setenv("QT_QPA_PLATFORM", "offscreen")
	}
	app := qt.NewQApplication(os.Args)
	// Шрифты и стили приложения — чтобы раскладка и скриншоты были как в жизни.
	InitStyle(app, ThemeLight, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Тесты закрывают свои окна; по умолчанию Qt на этом вышел бы из цикла событий.
	qt.QGuiApplication_SetQuitOnLastWindowClosed(false)
	code := make(chan int, 1)
	go func() {
		code <- m.Run()
		mainthread.Start(qt.QCoreApplication_Quit)
	}()
	qt.QApplication_Exec()
	os.Exit(<-code)
}

// onMain выполняет f в главном потоке Qt. t.Fatal внутри f нельзя — он
// завершил бы чужую горутину; f должна выходить через return.
func onMain(f func()) { mainthread.Wait(f) }

// settle даёт Qt пересчитать раскладку и диапазон прокрутки.
func settle() {
	for range 5 {
		qt.QCoreApplication_SendPostedEvents()
		qt.QCoreApplication_ProcessEvents()
	}
}

// drained ждёт, пока лента дорисует посты, отложенные на потом (drain).
func drained(f *Feed) {
	for deadline := time.Now().Add(10 * time.Second); len(f.pending) > 0 && time.Now().Before(deadline); {
		settle()
	}
	settle()
}

func makePosts(from, n int) []*model.Post {
	out := make([]*model.Post, n)
	for i := range n {
		k := from + i
		// Каждый пост — новый автор, чтобы у всех был заголовок и высота была заметной.
		out[i] = &model.Post{Id: fmt.Sprintf("p%04d", k), UserId: fmt.Sprintf("u%d", k%3), CreateAt: int64(k) * 60 * 60 * 1000,
			Message: fmt.Sprintf("сообщение %d", k)}
	}
	return out
}

func TestFeedScrolling(t *testing.T) { onMain(func() { testFeedScrolling(t) }) }

func testFeedScrolling(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
	f.Widget().Resize(500, 300)
	f.Widget().Show()
	defer f.Widget().Close()
	older := 0
	f.OnNeedOlder = func() { older++ }

	f.SetPosts(makePosts(100, 60), true)
	drained(f)
	if f.bar.Maximum() == 0 {
		t.Error("60 posts fit into 300px: scroll range is empty, test is meaningless")
		return
	}
	if f.bar.Value() != f.bar.Maximum() {
		t.Errorf("after SetPosts: value %d, max %d; want stuck to bottom", f.bar.Value(), f.bar.Maximum())
		return
	}

	// Новый пост, когда мы внизу, — остаёмся внизу.
	f.Upsert(makePosts(160, 1)[0])
	settle()
	if f.bar.Value() != f.bar.Maximum() {
		t.Errorf("after append at bottom: value %d, max %d; want bottom", f.bar.Value(), f.bar.Maximum())
	}

	// Прокрутка к верху просит старые посты ровно один раз.
	f.bar.SetValue(0)
	f.bar.SetValue(1)
	f.bar.SetValue(0)
	if older != 1 {
		t.Errorf("OnNeedOlder called %d times, want 1", older)
		return
	}

	// Подгруженные сверху посты не сдвигают то, что было видно.
	fromBottom := f.bar.Maximum() - f.bar.Value()
	f.PrependOlder(makePosts(40, 60), true)
	drained(f)
	if got := f.bar.Maximum() - f.bar.Value(); got != fromBottom {
		t.Errorf("after PrependOlder: %dpx from bottom, want %d (view jumped)", got, fromBottom)
	}
	if f.bar.Value() == 0 {
		t.Error("after PrependOlder view stayed at the very top")
	}
	if len(f.items) != 121 || f.items[0].post.Id != "p0040" || f.items[120].post.Id != "p0160" {
		t.Errorf("items: %d, first %s, last %s", len(f.items), f.items[0].post.Id, f.items[len(f.items)-1].post.Id)
	}

	// Новый пост, когда пользователь читает историю, ленту не дёргает.
	before := f.bar.Value()
	f.Upsert(makePosts(161, 1)[0])
	settle()
	if f.bar.Value() != before {
		t.Errorf("append while scrolled up moved view: %d → %d", before, f.bar.Value())
	}

	// Правка и удаление.
	edited := makePosts(161, 1)[0]
	edited.Message, edited.EditAt = "исправлено", 1
	f.Upsert(edited)
	if len(f.items) != 122 {
		t.Errorf("edit inserted a duplicate: %d items", len(f.items))
	}
	f.Remove("p0161")
	f.Remove("missing")
	if len(f.items) != 121 {
		t.Errorf("after remove: %d items, want 121", len(f.items))
	}
}

func TestContinues(t *testing.T) {
	a := &model.Post{UserId: "u1", CreateAt: 0}
	same := &model.Post{UserId: "u1", CreateAt: groupMillis - 1}
	late := &model.Post{UserId: "u1", CreateAt: groupMillis}
	other := &model.Post{UserId: "u2", CreateAt: 1}
	system := &model.Post{UserId: "u1", CreateAt: 1, Type: model.PostTypeJoinChannel}
	// Вебхуки u1: пишут от его имени, но автор — интеграция.
	hook := func(name string) *model.Post {
		p := &model.Post{UserId: "u1", CreateAt: 2}
		p.AddProp(model.PostPropsFromWebhook, "true")
		p.AddProp(model.PostPropsOverrideUsername, name)
		return p
	}
	grafana, grafana2, ci := hook("Grafana"), hook("Grafana"), hook("CI")
	for _, c := range []struct {
		prev, p *model.Post
		want    bool
	}{{nil, a, false}, {a, same, true}, {a, late, false}, {a, other, false}, {a, system, false},
		{a, grafana, false}, {grafana, same, false}, {grafana, grafana2, true}, {grafana, ci, false}} {
		if got := continues(c.prev, c.p); got != c.want {
			t.Errorf("continues(%+v, %+v) = %v, want %v", c.prev, c.p, got, c.want)
		}
	}
}

func TestFeedSelectionAcrossPosts(t *testing.T) { onMain(func() { testFeedSelectionAcrossPosts(t) }) }

func testFeedSelectionAcrossPosts(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
	f.Widget().Resize(500, 400)
	f.Widget().Show()
	defer f.Widget().Close()
	posts := makePosts(1, 3)
	posts[1].Metadata = &model.PostMetadata{Reactions: []*model.Reaction{{UserId: "u1", PostId: posts[1].Id, EmojiName: "+1"}}}
	f.SetPosts(posts, false)

	// Выделение от первого поста до последнего — одним курсором документа.
	c := qt.NewQTextCursor2(f.doc)
	c.SetPosition(f.items[0].frame.FirstPosition())
	c.SetPosition2(f.items[2].frame.LastPosition(), qt.QTextCursor__KeepAnchor)
	f.view.SetTextCursor(c)
	f.view.Copy()
	got := qt.QGuiApplication_Clipboard().Text()
	for _, want := range []string{"u1", "сообщение 1", "u2", "сообщение 2", "сообщение 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("copied selection lacks %q:\n%s", want, got)
		}
	}
	if strings.ContainsRune(got, '\uFFFC') {
		t.Errorf("copied selection has image placeholders: %q", got)
	}
}

func TestFeedFramesAndAnchors(t *testing.T) { onMain(func() { testFeedFramesAndAnchors(t) }) }

func testFeedFramesAndAnchors(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
	var toggled []string
	f.OnToggleReaction = func(p *model.Post, name string) { toggled = append(toggled, p.Id+" "+name) }
	var threads []string
	f.OnOpenThread = func(id string) { threads = append(threads, id) }

	f.SetPosts(makePosts(10, 3), true)
	f.PrependOlder(makePosts(5, 2), false)
	f.Upsert(makePosts(20, 1)[0])
	edited := makePosts(11, 1)[0]
	edited.Message = "исправлено"
	f.Upsert(edited)
	f.Remove("p0012")
	drained(f)

	// Порядок текста в документе совпадает с порядком постов.
	text := f.doc.ToPlainText()
	last := -1
	for _, want := range []string{"сообщение 5", "сообщение 6", "сообщение 10", "исправлено", "сообщение 20"} {
		i := strings.Index(text, want)
		if i < 0 || i < last {
			t.Errorf("document order broken at %q:\n%s", want, text)
			break
		}
		last = i
	}
	if strings.Contains(text, "сообщение 12") || strings.Contains(text, "сообщение 11") {
		t.Errorf("removed or replaced text still in document:\n%s", text)
	}

	// Ссылки ленты переживают QUrl: «+» в имени эмодзи не теряется.
	f.onAnchor(qt.NewQUrl3(reactLink + "p0010:" + url.PathEscape("+1")).ToString())
	f.onAnchor(qt.NewQUrl3(threadLink + "p0010").ToString())
	if len(toggled) != 1 || toggled[0] != "p0010 +1" || len(threads) != 1 || threads[0] != "p0010" {
		t.Errorf("anchors: toggled %v, threads %v", toggled, threads)
	}
}

// Очистка и удаление поста меняют высоту документа, полоса прокрутки
// пропадает, и Qt шлёт resize прямо из doc.Clear()/RemoveSelectedText:
// обработчики не должны трогать фреймы, которые Qt уже удалил.
func TestFeedClearWithImagesAfterResize(t *testing.T) {
	onMain(func() { testFeedClearWithImagesAfterResize(t) })
}

func testFeedClearWithImagesAfterResize(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
	f.Widget().Resize(600, 300)
	f.Widget().Show()
	defer f.Widget().Close()
	withImages := func(from int) []*model.Post {
		posts := makePosts(from, 30)
		for _, p := range posts {
			p.Metadata = &model.PostMetadata{Files: []*model.FileInfo{{Id: "f" + p.Id, Name: "a.png", Extension: "png", MimeType: "image/png", Width: 800, Height: 200}}}
		}
		return posts
	}

	// Окно шире на величину чуть меньше порога перерисовки (24 px): lastWidth
	// остаётся старым, и исчезнувшая полоса прокрутки добирает разницу до порога.
	widen := func() {
		f.Widget().Resize(f.lastWidth+25, 300)
		settle()
	}
	f.SetPosts(withImages(100), true)
	drained(f)
	settleFor(100 * time.Millisecond) // картинки вписаны в ширину (refit)
	if !f.bar.IsVisible() {
		t.Error("30 image posts fit into 300px: no scroll bar, test is meaningless")
		return
	}
	widen()
	f.Loading()
	settle()

	f.Widget().Resize(600, 300)
	f.SetPosts(withImages(200), true)
	drained(f)
	settleFor(100 * time.Millisecond)
	widen()
	for len(f.items) > 1 {
		f.Remove(f.items[len(f.items)-1].post.Id)
	}
	settleFor(200 * time.Millisecond)
	if len(f.items) != 1 {
		t.Errorf("%d posts left, want 1", len(f.items))
	}
	// Перерисовка отложена, но случается: картинки вписаны в текущую ширину.
	if w := f.view.Viewport().Width(); f.lastWidth != w {
		t.Errorf("images fitted to %d px, viewport is %d", f.lastWidth, w)
	}
}

func TestFeedLoadingSpinner(t *testing.T) { onMain(func() { testFeedLoadingSpinner(t) }) }

func testFeedLoadingSpinner(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
	f.Widget().Resize(500, 300)
	f.Widget().Show()
	defer f.Widget().Close()

	f.Loading()
	if !strings.Contains(f.doc.ToHtml(), spinnerSrc) {
		t.Error("loading placeholder does not show the spinner resource")
	}
	frame := f.spin.frame
	settleFor(200 * time.Millisecond)
	if !f.spin.timer.IsActive() || f.spin.frame == frame {
		t.Errorf("spinner stands still while loading (active=%v, frame %d → %d)", f.spin.timer.IsActive(), frame, f.spin.frame)
	}
	f.SetPosts(makePosts(1, 3), false)
	if f.spin.timer.IsActive() {
		t.Error("spinner runs after posts arrived")
	}
	f.Loading()
	f.Error("load posts: timeout")
	if f.spin.timer.IsActive() {
		t.Error("spinner runs under the error placeholder")
	}
}

// Пользователь отмотал чуть выше низа; перерисовка всех постов (догрузились
// аватары, эмодзи, превью) не должна прилеплять ленту к концу, а пост у
// верхнего края должен остаться на месте. Лента у низа остаётся у низа.
func TestFeedRerenderKeepsScroll(t *testing.T) { onMain(func() { testFeedRerenderKeepsScroll(t) }) }

func testFeedRerenderKeepsScroll(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
	f.Widget().Resize(500, 300)
	f.Widget().Show()
	defer f.Widget().Close()
	posts := makePosts(1, 40)
	for i := 0; i < len(posts); i += 3 {
		posts[i].Message = strings.Repeat("длинное сообщение в несколько строк ", 12)
	}
	f.SetPosts(posts, false)
	drained(f)
	f.renderAll()
	settle()
	if f.bar.Value() != f.bar.Maximum() || !f.stick {
		t.Errorf("feed at the bottom left it after re-render: value %d of %d, stick %v", f.bar.Value(), f.bar.Maximum(), f.stick)
	}

	f.bar.SetValue(f.bar.Maximum() - 150) // как прокрутка колесом: не programmatic
	settle()
	if f.stick {
		t.Fatal("scrolled up but feed still sticks to the bottom")
	}
	id, top := f.topPost()
	f.renderAll()
	settle()
	if f.stick || f.bar.Value() == f.bar.Maximum() {
		t.Errorf("re-render glued the feed to the bottom: value %d of %d, stick %v", f.bar.Value(), f.bar.Maximum(), f.stick)
	}
	if i := f.index(id); i < 0 || abs(f.postTop(i)-top) > 1 {
		t.Errorf("post %s at the top moved from %d to %d px", id, top, f.postTop(i))
	}
	// postTop — там, где Qt ищет пост под мышью (а значит, и рисует): на
	// первой строке — этот пост, сразу над верхом — уже нет. QTextEdit::cursorRect
	// после такой перерисовки сдвинут на старое смещение, и место по нему
	// восстанавливалось с ошибкой.
	x := feedAvatarCol + 8
	for i := range f.items {
		y := f.postTop(i)
		if y < 0 || y > f.view.Viewport().Height()-4 {
			continue
		}
		if f.itemAt(f.view.CursorForPosition(qt.NewQPoint2(x, y+10)).Position()) != f.items[i] {
			t.Errorf("post %d: Qt finds another post 10 px below postTop %d", i, y)
		}
		if f.itemAt(f.view.CursorForPosition(qt.NewQPoint2(x, y-3)).Position()) == f.items[i] && i > 0 {
			t.Errorf("post %d: Qt finds it 3 px above postTop %d", i, y)
		}
		// Первая строка поста — ниже верха фрейма на его поле (у поста без
		// разделителя поля задаёт сам фрейм).
		it := f.items[i]
		want := y
		if it.content == it.frame {
			want += int(it.frame.FrameFormat().TopMargin())
		}
		c := qt.NewQTextCursor2(f.doc)
		c.SetPosition(it.frame.FirstPosition())
		if r := f.cursorRect(c); r.Top() != want {
			t.Errorf("post %d: cursorRect top %d, want %d (postTop %d)", i, r.Top(), want, y)
		}
	}
}

func TestLinkAddress(t *testing.T) {
	for in, want := range map[string]string{
		"https://go.dev/doc":     "https://go.dev/doc",
		"http://x.y":             "http://x.y",
		"mailto:bob@example.com": "bob@example.com",
		"mm-user:bob":            "",
		"mm-thread:abc":          "",
		"":                       "",
	} {
		got, ok := linkAddress(in)
		if got != want || ok != (want != "") {
			t.Errorf("linkAddress(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// Длинные посты (HTML каждого Qt разбирает десятки миллисекунд): SetPosts
// рисует сразу только то, что видно, остальное — порциями, не сдвигая
// видимое; в итоге лента та же, что при отрисовке целиком.
func TestFeedDrawsInParts(t *testing.T) { onMain(func() { testFeedDrawsInParts(t) }) }

func testFeedDrawsInParts(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
	f.Widget().Resize(700, 400)
	f.Widget().Show()
	defer f.Widget().Close()
	posts := makePosts(100, 60)
	for i, p := range posts {
		var b strings.Builder
		for k := range 40 {
			fmt.Fprintf(&b, "- пункт %d: **важно** `код` и [ссылка](https://example.com/%d)\n", k, k)
		}
		p.Message = b.String()
		if i%2 == 1 { // продолжение предыдущего: без заголовка
			p.UserId, p.CreateAt = posts[i-1].UserId, posts[i-1].CreateAt+60*1000
		}
	}
	f.SetPosts(posts, true)
	if len(f.items) == 0 || len(f.items) >= 30 || f.items[len(f.items)-1].post.Id != posts[59].Id {
		t.Errorf("SetPosts drew %d posts at once (last %v), want only the newest few", len(f.items), f.items)
		return
	}
	if !f.Has(posts[0].Id) || f.index(posts[0].Id) >= 0 {
		t.Error("not yet drawn post: Has must see it, index must not")
	}
	settle()
	if f.bar.Value() != f.bar.Maximum() {
		t.Errorf("not at the bottom while drawing: %d of %d", f.bar.Value(), f.bar.Maximum())
	}

	// Пока дорисовывается: правка и удаление неотрисованного, новый пост снизу.
	edited := posts[3].Clone()
	edited.Message = "исправлено"
	f.Upsert(edited)
	f.Remove(posts[5].Id)
	f.Upsert(makePosts(500, 1)[0])
	drained(f)
	if len(f.items) != 60 || f.items[0].post.Id != posts[0].Id || f.items[59].post.Id != "p0500" {
		t.Errorf("after drawing: %d posts, first %s, last %s", len(f.items), f.items[0].post.Id, f.items[len(f.items)-1].post.Id)
		return
	}
	if f.items[3].post.Message != "исправлено" || f.Has(posts[5].Id) {
		t.Error("edit or removal of a not yet drawn post lost")
	}
	if f.bar.Value() != f.bar.Maximum() {
		t.Errorf("drawing above moved the feed off the bottom: %d of %d", f.bar.Value(), f.bar.Maximum())
	}
	// Отрисованное по частям — то же, что нарисовалось бы сразу (заголовки, разделители дней).
	for i, it := range f.items {
		if it.html != f.postHTML(i) {
			t.Errorf("post %d (%s) drawn differently than in place", i, it.post.Id)
		}
	}

	// Подгрузка старых — тоже по частям; пользователь выше низа — видимое не сдвигается.
	f.bar.SetValue(f.bar.Maximum() / 2)
	settle()
	id, top := f.topPost()
	f.PrependOlder(makePosts(10, 60), true)
	drained(f)
	if i := f.index(id); i < 0 || abs(f.postTop(i)-top) > 1 {
		t.Errorf("post %s at the top moved from %d px while older posts were drawn", id, top)
	}
	if len(f.items) != 120 || f.items[0].post.Id != "p0010" {
		t.Errorf("after older: %d posts, first %s", len(f.items), f.items[0].post.Id)
	}

	// Переход к ещё не отрисованному посту дорисовывает всё сразу.
	f.SetPosts(posts, true)
	f.Highlight(posts[1].Id)
	f.ScrollToPost(posts[1].Id)
	if len(f.pending) != 0 || f.index(posts[1].Id) < 0 {
		t.Errorf("ScrollToPost to a not yet drawn post: %d pending", len(f.pending))
	}
}

// Тред по частям: корень рисуется последним, но с верным «N ответов», первый
// ответ после корня — с заголовком; итог — как при отрисовке целиком.
// Перерисовка всего (тема, ширина) рисует сразу только видимое.
func TestFeedThreadInParts(t *testing.T) { onMain(func() { testFeedThreadInParts(t) }) }

func testFeedThreadInParts(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}, Thread: true})
	f.Widget().Resize(400, 400)
	f.Widget().Show()
	defer f.Widget().Close()
	posts := makePosts(100, 30)
	for i, p := range posts {
		p.Message = strings.Repeat("ответ в треде, довольно длинный, чтобы занять место. ", 20)
		p.UserId = "u1" // один автор подряд: заголовок только там, где начинается группа
		if i > 0 {
			p.RootId = posts[0].Id
			p.CreateAt = posts[0].CreateAt + int64(i)*1000
		}
	}
	f.SetPosts(posts, false)
	if len(f.items) == 0 || len(f.items) >= 30 {
		t.Errorf("thread drew %d of 30 posts at once, want only the newest few", len(f.items))
		return
	}
	drained(f)
	if len(f.items) != 30 || f.items[0].post.Id != posts[0].Id {
		t.Errorf("thread: %d posts, first %s", len(f.items), f.items[0].post.Id)
		return
	}
	if !strings.Contains(f.items[0].html, pluralReplies(29)) {
		t.Errorf("root has no «%s» separator", pluralReplies(29))
	}
	if !f.startsGroup(1) || f.startsGroup(2) {
		t.Error("first reply after the root must start a group, the next one must not")
	}
	for i, it := range f.items {
		if it.html != f.postHTML(i) {
			t.Errorf("thread post %d drawn differently than in place", i)
		}
	}

	lo, hi, ok := f.visibleRange()
	if !ok || hi != len(f.items)-1 || lo == 0 {
		t.Errorf("visible range %d..%d (ok %v) at the bottom of %d posts", lo, hi, ok, len(f.items))
	}
	f.invalidateAll()
	if n := len(f.stale); n == 0 || n > len(f.items)-(hi-lo+1) {
		t.Errorf("invalidateAll left %d stale of %d, visible %d..%d: want only the invisible ones queued", n, len(f.items), lo, hi)
	}
	drained(f)
	if len(f.stale) != 0 {
		t.Errorf("%d posts left stale after drawing", len(f.stale))
	}
}

// Аватара нет в документе: лента рисует его сама слева от строки автора;
// клик по нему — как по имени, рядом с ним — нет. Подсветка строки — тоже рисунком.
func TestFeedAvatarOverlay(t *testing.T) { onMain(func() { testFeedAvatarOverlay(t) }) }

func testFeedAvatarOverlay(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
	f.Widget().Resize(500, 400)
	f.Widget().Show()
	defer f.Widget().Close()
	var links []string
	f.OnLink = func(u string) { links = append(links, u) }
	posts := makePosts(1, 3)
	posts[2].Message = "@me глянь"
	f.SetPosts(posts, false)
	settle()
	it := f.items[1]
	if it.avatar == "" || strings.Contains(it.html, "<img src=\"avatar:") {
		t.Errorf("avatar %q, html %s: want avatar drawn by the feed, not in the document", it.avatar, it.html)
		return
	}
	top := f.contentRect(it).Y() + int(it.content.FrameFormat().TopMargin())
	if f.avatarAt(qt.NewQPoint2(30, top+16)) != it || f.avatarAt(qt.NewQPoint2(60, top+16)) != nil || f.avatarAt(qt.NewQPoint2(30, top+40)) == it {
		t.Error("avatar hit area is not the 32×32 square left of the author line")
	}
	press := func(x, y int) {
		e := qt.NewQMouseEvent(qt.QEvent__MouseButtonPress, qt.NewQPointF3(float64(x), float64(y)), qt.LeftButton, qt.LeftButton, qt.NoModifier)
		qt.QCoreApplication_SendEvent(f.view.Viewport().QObject, e.QEvent)
	}
	press(30, top+16)
	if len(links) != 1 || links[0] != markdown.UserLinkScheme+"u2" {
		t.Errorf("click on avatar: links %v, want the author card of u2", links)
	}

	img := f.Widget().Grab().ToImage()
	bg := img.PixelColor(8, top+16).Name()
	if c := img.PixelColor(16+16, top+16).Name(); c == bg {
		t.Errorf("no avatar painted at its place (pixel %s, background %s)", c, bg)
	}
	men := f.items[2]
	if men.bg != Pal().MenRow {
		t.Errorf("post mentioning me: row color %q, want %s", men.bg, Pal().MenRow)
		return
	}
	y := f.contentRect(men).Y() + 2
	if c := img.PixelColor(4, y).Name(); c != Pal().MenRow {
		t.Errorf("mention row not highlighted under the avatar column: pixel %s, want %s", c, Pal().MenRow)
	}
}

// Длинное сообщение свёрнуто: начало и «Показать полностью»; развернуть и
// свернуть — пост у верха окна на месте. Длинный текст вложения — тоже.
func TestFeedLongPosts(t *testing.T) { onMain(func() { testFeedLongPosts(t) }) }

func testFeedLongPosts(t *testing.T) {
	f := NewFeed(FeedDeps{Names: func(id string) string { return id }, Me: &model.User{Id: "me", Username: "me"}})
	f.Widget().Resize(600, 400)
	f.Widget().Show()
	defer f.Widget().Close()
	var b strings.Builder
	for i := range 60 {
		fmt.Fprintf(&b, "строка отчёта %d\n", i)
	}
	posts := makePosts(1, 6)
	posts[3].Message = b.String()
	posts[4].AddProp("attachments", []any{map[string]any{"title": "Алерт", "text": b.String()}})
	f.SetPosts(posts, false)
	drained(f)
	long := f.items[3]
	if strings.Contains(long.html, "строка отчёта 59") || !strings.Contains(long.html, "строка отчёта 0") || !strings.Contains(long.html, moreLink+long.post.Id) {
		t.Errorf("long post is not collapsed:\n%s", long.html)
		return
	}
	if att := f.items[4]; strings.Contains(att.html, "строка отчёта 59") || !strings.Contains(att.html, moreLink+att.post.Id) {
		t.Errorf("long attachment text is not collapsed:\n%s", att.html)
	}

	f.ScrollToPost(long.post.Id)
	settle()
	shotWidget(t, f.Widget(), "long-post")
	id, top := f.topPost()
	f.onAnchor(moreLink + long.post.Id)
	settle()
	shotWidget(t, f.Widget(), "long-post-expanded")
	if !strings.Contains(long.html, "строка отчёта 59") || !strings.Contains(long.html, lessLink+long.post.Id) {
		t.Errorf("«Показать полностью» did not expand the post:\n%s", long.html)
	}
	if got, gotTop := f.topPost(); got != id || abs(gotTop-top) > 1 {
		t.Errorf("expanding moved the view: top post %s at %d, was %s at %d", got, gotTop, id, top)
	}
	f.onAnchor(lessLink + long.post.Id)
	settle()
	if strings.Contains(long.html, "строка отчёта 59") || !strings.Contains(long.html, moreLink+long.post.Id) {
		t.Error("«Свернуть» did not collapse the post back")
	}
}
