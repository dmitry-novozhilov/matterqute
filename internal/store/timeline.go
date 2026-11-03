package store

import (
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
)

// Timeline — загруженный хвост ленты канала.
type Timeline struct {
	Posts   []*model.Post // от старых к новым
	HasMore bool          // на сервере есть посты старше Posts[0]
	Loaded  bool          // первая страница пришла; до этого в Posts только посты из WebSocket
	Stale   bool          // был обрыв связи: события могли потеряться, сверить с сервером (Sync)

	// Вставки по одной (Upsert: WebSocket, свои отправленные) — по номеру:
	// сверка (Sync) не удаляет вставленное после своего запроса — его в
	// странице сервера быть не могло.
	gen   int
	added map[string]int
}

// Gen — номер последней вставки: запомнить перед запросом страницы для Sync.
func (t *Timeline) Gen() int { return t.gen }

// PostsOf раскладывает PostList (Order — от новых к старым) в порядке от
// старых к новым. Посты из Posts, которых нет в Order (корни тредов для
// контекста), не берём.
func PostsOf(list *model.PostList) []*model.Post {
	out := make([]*model.Post, 0, len(list.Order))
	for i := len(list.Order) - 1; i >= 0; i-- {
		if p := list.Posts[list.Order[i]]; p != nil {
			out = append(out, p)
		}
	}
	return out
}

// Prepend добавляет более старые посты; возвращает реально добавленные
// (без тех, что уже есть).
func (t *Timeline) Prepend(older []*model.Post, hasMore bool) []*model.Post {
	t.HasMore = hasMore
	added := make([]*model.Post, 0, len(older))
	for _, p := range older {
		if t.index(p.Id) < 0 {
			added = append(added, p)
		}
	}
	t.Posts = append(added, t.Posts...)
	return added
}

// Upsert вставляет пост по времени создания или заменяет пост с тем же id.
// Возвращает позицию и признак новой вставки.
func (t *Timeline) Upsert(p *model.Post) (int, bool) {
	if i := t.index(p.Id); i >= 0 {
		keepReplies(t.Posts[i], p)
		t.Posts[i] = p
		return i, false
	}
	// Обычно пост — самый новый, поэтому ищем с конца.
	i := len(t.Posts)
	for i > 0 && t.Posts[i-1].CreateAt > p.CreateAt {
		i--
	}
	t.Posts = slices.Insert(t.Posts, i, p)
	if t.added == nil {
		t.added = map[string]int{}
	}
	t.gen++
	t.added[p.Id] = t.gen
	return i, true
}

// Replace заменяет пост с тем же id, если он загружен.
func (t *Timeline) Replace(p *model.Post) bool {
	i := t.index(p.Id)
	if i < 0 {
		return false
	}
	keepReplies(t.Posts[i], p)
	t.Posts[i] = p
	return true
}

// keepReplies переносит счётчик ответов со старой версии поста: в событии
// post_edited его может не быть, а уменьшаться от правки он не может.
func keepReplies(old, p *model.Post) {
	if p.ReplyCount < old.ReplyCount {
		p.ReplyCount = old.ReplyCount
	}
	p.LastReplyAt = max(p.LastReplyAt, old.LastReplyAt)
}

func (t *Timeline) Remove(id string) bool {
	i := t.index(id)
	if i < 0 {
		return false
	}
	t.Posts = slices.Delete(t.Posts, i, i+1)
	return true
}

// Get — загруженный пост по id или nil.
func (t *Timeline) Get(id string) *model.Post {
	if i := t.index(id); i >= 0 {
		return t.Posts[i]
	}
	return nil
}

func (t *Timeline) Oldest() *model.Post {
	if len(t.Posts) == 0 {
		return nil
	}
	return t.Posts[0]
}

func (t *Timeline) index(id string) int {
	for i := len(t.Posts) - 1; i >= 0; i-- {
		if t.Posts[i].Id == id {
			return i
		}
	}
	return -1
}

// Sync сверяет загруженное со свежей последней страницей с сервера (после
// обрыва связи): новые и изменённые посты заменяются, а те, что в её
// промежутке времени, но в ней нет, — удалены. Старше страницы — не трогаем,
// и вставленное после запроса страницы (since — Gen перед ним: пришло по
// WebSocket, своё отправленное) — тоже. Возвращает, что поменялось: ленте на
// экране перерисовать только это.
func (t *Timeline) Sync(page []*model.Post, hasMore bool, since int) (changed, removed []*model.Post) {
	fresh := make(map[string]bool, len(page))
	for _, p := range page {
		fresh[p.Id] = true
		if old := t.Get(p.Id); old == nil || differs(old, p) {
			t.Upsert(p)
			changed = append(changed, p)
		}
	}
	from := int64(0) // пустая страница — в канале ничего не осталось
	if len(page) > 0 {
		from = page[0].CreateAt
	}
	for _, p := range slices.Clone(t.Posts) {
		if p.CreateAt >= from && !fresh[p.Id] && t.added[p.Id] <= since { // пришедшее после запроса — не трогаем
			t.Remove(p.Id)
			removed = append(removed, p)
		}
	}
	if len(t.Posts) == 0 || (len(page) > 0 && t.Posts[0].Id == page[0].Id) {
		t.HasMore = hasMore // старше страницы ничего не загружено
	}
	t.Stale = false
	return changed, removed
}

// differs — пост на сервере не тот, что у нас: правка, реакции, ответы, закрепление.
func differs(a, b *model.Post) bool {
	reactions := func(p *model.Post) int {
		if p.Metadata == nil {
			return 0
		}
		return len(p.Metadata.Reactions)
	}
	return a.UpdateAt != b.UpdateAt || a.EditAt != b.EditAt || a.Message != b.Message || a.IsPinned != b.IsPinned ||
		a.ReplyCount != b.ReplyCount || a.LastReplyAt != b.LastReplyAt || reactions(a) != reactions(b)
}
