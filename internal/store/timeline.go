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
}

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
		t.Posts[i] = p
		return i, false
	}
	// Обычно пост — самый новый, поэтому ищем с конца.
	i := len(t.Posts)
	for i > 0 && t.Posts[i-1].CreateAt > p.CreateAt {
		i--
	}
	t.Posts = slices.Insert(t.Posts, i, p)
	return i, true
}

// Replace заменяет пост с тем же id, если он загружен.
func (t *Timeline) Replace(p *model.Post) bool {
	i := t.index(p.Id)
	if i < 0 {
		return false
	}
	t.Posts[i] = p
	return true
}

func (t *Timeline) Remove(id string) bool {
	i := t.index(id)
	if i < 0 {
		return false
	}
	t.Posts = slices.Delete(t.Posts, i, i+1)
	return true
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
