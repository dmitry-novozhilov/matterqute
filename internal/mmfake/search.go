package mmfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// searchPosts — поиск, похожий на серверный: все слова (без учёта регистра,
// слово* — начало слова), "фраза", -исключить, from:имя, in:канал; в
// каналах пользователя, новые первыми, не больше per_page.
func (s *Server) searchPosts(w http.ResponseWriter, r *http.Request, me *model.User) {
	var params model.SearchParameter
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil || params.Terms == nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	var words, phrases, without []string
	from, in := "", ""
	q := *params.Terms
	for {
		i := strings.Index(q, `"`)
		j := strings.Index(q[i+1:], `"`)
		if i < 0 || j < 0 {
			break
		}
		phrases = append(phrases, strings.ToLower(q[i+1:i+1+j]))
		q = q[:i] + " " + q[i+2+j:]
	}
	for _, tok := range strings.Fields(strings.ToLower(q)) {
		switch {
		case strings.HasPrefix(tok, "from:"):
			from = strings.TrimPrefix(strings.TrimPrefix(tok, "from:"), "@")
		case strings.HasPrefix(tok, "in:"):
			in = strings.TrimPrefix(strings.TrimPrefix(tok, "in:"), "~")
		case strings.HasPrefix(tok, "-"):
			without = append(without, strings.TrimPrefix(tok, "-"))
		default:
			words = append(words, strings.TrimPrefix(tok, "@")) // как сервер: «@» — не часть слова
		}
	}
	match := func(text string) bool {
		text = strings.ToLower(text)
		fields := strings.FieldsFunc(text, func(r rune) bool {
			return !(r == '_' || r == '-' || 'a' <= r && r <= 'z' || '0' <= r && r <= '9' || r >= 0x400 && r <= 0x4ff)
		})
		has := func(word string) bool {
			prefix := strings.HasSuffix(word, "*")
			word = strings.TrimSuffix(word, "*")
			return slices.ContainsFunc(fields, func(f string) bool { return f == word || prefix && strings.HasPrefix(f, word) })
		}
		or := params.IsOrSearch != nil && *params.IsOrSearch
		any := false
		for _, wd := range words {
			switch {
			case has(wd):
				any = true
			case !or:
				return false
			}
		}
		if or && len(words) > 0 && !any {
			return false
		}
		for _, ph := range phrases {
			if !strings.Contains(text, ph) {
				return false
			}
		}
		for _, wd := range without {
			if has(wd) {
				return false
			}
		}
		return len(words)+len(phrases) > 0 || from != "" || in != ""
	}
	s.mu.Lock()
	var found []*model.Post
	for cid, ps := range s.posts {
		c := s.channels[cid]
		if s.members[me.Id][cid] == nil || in != "" && c.Name != in {
			continue
		}
		for _, p := range ps {
			if p.DeleteAt > 0 || p.IsSystemMessage() || from != "" && s.users[p.UserId].Username != from || !match(p.Message) {
				continue
			}
			found = append(found, p)
		}
	}
	s.mu.Unlock()
	slices.SortFunc(found, func(a, b *model.Post) int { return int(b.CreateAt - a.CreateAt) })
	if params.PerPage != nil && *params.PerPage > 0 && len(found) > *params.PerPage {
		found = found[:*params.PerPage]
	}
	list := model.NewPostList()
	for _, p := range found {
		list.AddPost(p)
		list.AddOrder(p.Id)
	}
	s.json(w, list)
}

// searchFiles — POST /files/search и /teams/{tid}/files/search: файлы
// постов в каналах пользователя; in:канал (или in:@имя — личка), ext:,
// слова — в имени файла; новые первыми.
func (s *Server) searchFiles(w http.ResponseWriter, r *http.Request, me *model.User) {
	var params model.SearchParameter
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil || params.Terms == nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	var words []string
	in, ext := "", ""
	for _, tok := range strings.Fields(strings.ToLower(*params.Terms)) {
		switch {
		case strings.HasPrefix(tok, "in:"):
			in = strings.TrimPrefix(tok, "in:")
		case strings.HasPrefix(tok, "ext:"):
			ext = strings.TrimPrefix(tok, "ext:")
		default:
			words = append(words, tok)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inChannel := func(cid string) bool {
		c := s.channels[cid]
		switch {
		case in == "":
			return true
		case strings.HasPrefix(in, "@"):
			u := s.byName(strings.TrimPrefix(in, "@"))
			return u != nil && c.Type == model.ChannelTypeDirect && c.Name == model.GetDMNameFromIds(me.Id, u.Id)
		}
		return c.Name == in
	}
	var found []*model.FileInfo
	for _, f := range s.files {
		fi := f.info
		if fi.PostId == "" || fi.DeleteAt > 0 || s.members[me.Id][fi.ChannelId] == nil || !inChannel(fi.ChannelId) ||
			ext != "" && strings.ToLower(fi.Extension) != ext {
			continue
		}
		name := strings.ToLower(fi.Name)
		if slices.ContainsFunc(words, func(wd string) bool { return !strings.Contains(name, strings.TrimSuffix(wd, "*")) }) {
			continue
		}
		found = append(found, fi)
	}
	slices.SortFunc(found, func(a, b *model.FileInfo) int { return int(b.CreateAt - a.CreateAt) })
	if params.PerPage != nil && *params.PerPage > 0 && len(found) > *params.PerPage {
		found = found[:*params.PerPage]
	}
	list := &model.FileInfoList{FileInfos: map[string]*model.FileInfo{}, Order: []string{}}
	for _, fi := range found {
		list.FileInfos[fi.Id] = fi
		list.Order = append(list.Order, fi.Id)
	}
	s.json(w, list)
}
