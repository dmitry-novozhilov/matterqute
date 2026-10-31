package mmfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/mattermost/mattermost/server/public/model"
)

// threadMember — подписка пользователя на тред.
type threadMember struct {
	unreadReplies  int64
	unreadMentions int64
}

func (s *Server) post(id string) *model.Post {
	for _, ps := range s.posts {
		for _, p := range ps {
			if p.Id == id {
				return p
			}
		}
	}
	return nil
}

// addReply учитывает ответ: счётчики корня, подписки, непрочитанное у подписчиков.
// Возвращает подписчиков, кроме автора, — им уходит thread_updated.
func (s *Server) addReply(p *model.Post, mentioned []string) []string {
	root := s.post(p.RootId)
	if root == nil {
		return nil
	}
	root.ReplyCount++
	root.LastReplyAt = p.CreateAt
	if s.followers[root.Id] == nil {
		s.followers[root.Id] = map[string]*threadMember{root.UserId: {}}
	}
	f := s.followers[root.Id]
	for _, id := range append([]string{p.UserId}, mentioned...) {
		if f[id] == nil {
			f[id] = &threadMember{}
		}
	}
	var notify []string
	for id, m := range f {
		if id == p.UserId {
			continue
		}
		m.unreadReplies++
		if slices.Contains(mentioned, id) {
			m.unreadMentions++
		}
		notify = append(notify, id)
	}
	return notify
}

func (s *Server) threadFor(rootID, userID string) *model.ThreadResponse {
	root, m := s.post(rootID), s.followers[rootID][userID]
	if root == nil || m == nil {
		return nil
	}
	var participants []*model.User
	for id := range s.followers[rootID] {
		participants = append(participants, &model.User{Id: id})
	}
	return &model.ThreadResponse{
		PostId: root.Id, ReplyCount: root.ReplyCount, LastReplyAt: root.LastReplyAt,
		Participants: participants, Post: root,
		UnreadReplies: m.unreadReplies, UnreadMentions: m.unreadMentions,
	}
}

// sendThreadUpdated шлёт подписчику свежее состояние треда (под s.mu).
func (s *Server) sendThreadUpdated(rootID, userID string) {
	t := s.threadFor(rootID, userID)
	if t == nil || s.CollapsedThreads == model.CollapsedThreadsDisabled {
		return
	}
	raw, _ := json.Marshal(t)
	ev := model.NewWebSocketEvent(model.WebsocketEventThreadUpdated, "", "", userID, nil, "")
	ev.Add("thread", string(raw))
	s.sendTo(userID, ev)
}

func (s *Server) sendTo(userID string, ev *model.WebSocketEvent) {
	data, _ := ev.ToJSON()
	for c := range s.conns {
		if c.userID == userID {
			if err := c.write(data); err != nil {
				s.log.Warn("websocket write", "user", userID, "err", err)
			}
		}
	}
}

func (s *Server) sendToChannel(channelID string, ev *model.WebSocketEvent) {
	data, _ := ev.ToJSON()
	for _, c := range s.connsIn(channelID) {
		if err := c.write(data); err != nil {
			s.log.Warn("websocket write", "user", c.userID, "err", err)
		}
	}
}

func (s *Server) clientConfig(w http.ResponseWriter, _ *http.Request, _ *model.User) {
	s.json(w, map[string]string{"Version": "mmfake", "CollapsedThreads": s.CollapsedThreads})
}

func (s *Server) preferences(w http.ResponseWriter, _ *http.Request, _ *model.User) {
	s.json(w, model.Preferences{})
}

// postThread: корень и все ответы, Order — от новых к старым, как у сервера.
func (s *Server) postThread(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := s.post(r.PathValue("pid"))
	if root == nil || s.members[me.Id][root.ChannelId] == nil {
		s.fail(w, http.StatusNotFound, "app.post.get.app_error")
		return
	}
	list := model.NewPostList()
	thread := []*model.Post{root}
	for _, p := range s.posts[root.ChannelId] {
		if p.RootId == root.Id {
			thread = append(thread, p)
		}
	}
	for i := len(thread) - 1; i >= 0; i-- {
		list.AddPost(thread[i])
		list.AddOrder(thread[i].Id)
	}
	s.json(w, list)
}

func (s *Server) userThreads(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	team := r.PathValue("tid")
	out := &model.Threads{Threads: []*model.ThreadResponse{}}
	for rootID := range s.followers {
		root := s.post(rootID)
		if root == nil {
			continue
		}
		if ch := s.channels[root.ChannelId]; ch.TeamId != team && !ch.IsGroupOrDirect() {
			continue
		}
		if t := s.threadFor(rootID, me.Id); t != nil {
			out.Threads = append(out.Threads, t)
			out.Total++
			out.TotalUnreadMentions += t.UnreadMentions
			if t.UnreadReplies > 0 {
				out.TotalUnreadThreads++
			}
		}
	}
	s.json(w, out)
}

func (s *Server) threadRead(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("thid")
	m := s.followers[id][me.Id]
	if m == nil {
		s.fail(w, http.StatusNotFound, "app.thread.get_membership_for_user.app_error")
		return
	}
	m.unreadReplies, m.unreadMentions = 0, 0
	ts, _ := strconv.ParseInt(r.PathValue("ts"), 10, 64)
	ev := model.NewWebSocketEvent(model.WebsocketEventThreadReadChanged, r.PathValue("tid"), "", me.Id, nil, "")
	ev.Add("thread_id", id)
	ev.Add("timestamp", ts)
	ev.Add("unread_mentions", 0)
	ev.Add("unread_replies", 0)
	s.sendTo(me.Id, ev)
	s.json(w, s.threadFor(id, me.Id))
}

func (s *Server) saveReaction(w http.ResponseWriter, r *http.Request, me *model.User) {
	var re model.Reaction
	if err := json.NewDecoder(r.Body).Decode(&re); err != nil {
		s.fail(w, http.StatusBadRequest, "api.context.invalid_body_param.app_error")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.post(re.PostId)
	if p == nil || re.UserId != me.Id {
		s.fail(w, http.StatusBadRequest, "api.reaction.save_reaction.invalid.app_error")
		return
	}
	re.CreateAt, re.ChannelId = model.GetMillis(), p.ChannelId
	s.react(p, &re, true)
	s.json(w, &re)
}

func (s *Server) deleteReaction(w http.ResponseWriter, r *http.Request, me *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.post(r.PathValue("pid"))
	if p == nil || r.PathValue("uid") != me.Id {
		s.fail(w, http.StatusBadRequest, "api.reaction.delete_reaction.invalid.app_error")
		return
	}
	s.react(p, &model.Reaction{UserId: me.Id, PostId: p.Id, EmojiName: r.PathValue("emoji"), ChannelId: p.ChannelId}, false)
	s.json(w, map[string]string{"status": "OK"})
}

// react меняет реакции поста и рассылает событие (под s.mu).
func (s *Server) react(p *model.Post, re *model.Reaction, added bool) {
	if p.Metadata == nil {
		p.Metadata = &model.PostMetadata{}
	}
	i := slices.IndexFunc(p.Metadata.Reactions, func(x *model.Reaction) bool {
		return x.UserId == re.UserId && x.EmojiName == re.EmojiName
	})
	switch {
	case added && i < 0:
		p.Metadata.Reactions = append(p.Metadata.Reactions, re)
	case !added && i >= 0:
		p.Metadata.Reactions = slices.Delete(p.Metadata.Reactions, i, i+1)
	default:
		return
	}
	p.HasReactions = len(p.Metadata.Reactions) > 0
	typ := model.WebsocketEventReactionRemoved
	if added {
		typ = model.WebsocketEventReactionAdded
	}
	raw, _ := json.Marshal(re)
	ev := model.NewWebSocketEvent(typ, "", p.ChannelId, "", nil, "")
	ev.Add("reaction", string(raw))
	s.sendToChannel(p.ChannelId, ev)
}

// React ставит или снимает реакцию от имени пользователя — для тестов.
func (s *Server) React(userID, postID, emojiName string, added bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.post(postID); p != nil {
		s.react(p, &model.Reaction{UserId: userID, PostId: postID, EmojiName: emojiName, ChannelId: p.ChannelId, CreateAt: model.GetMillis()}, added)
	}
}
