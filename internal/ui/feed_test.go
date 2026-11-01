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
	settle()
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
	settle()
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
	settle()
	if !f.bar.IsVisible() {
		t.Error("30 image posts fit into 300px: no scroll bar, test is meaningless")
		return
	}
	widen()
	f.Loading()
	settle()

	f.Widget().Resize(600, 300)
	f.SetPosts(withImages(200), true)
	settle()
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
	settle()
	f.RefreshNames()
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
	f.RefreshNames()
	settle()
	if f.stick || f.bar.Value() == f.bar.Maximum() {
		t.Errorf("re-render glued the feed to the bottom: value %d of %d, stick %v", f.bar.Value(), f.bar.Maximum(), f.stick)
	}
	if i := f.index(id); i < 0 || abs(f.postTop(i)-top) > 1 {
		t.Errorf("post %s at the top moved from %d to %d px", id, top, f.postTop(i))
	}
}
