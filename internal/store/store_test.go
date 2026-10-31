package store

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func post(id, channel, user string, at int64) *model.Post {
	return &model.Post{Id: id, ChannelId: channel, UserId: user, CreateAt: at}
}

func ids(ps []*model.Post) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Id
	}
	return out
}

func TestPostsOfOrdersOldestFirst(t *testing.T) {
	list := &model.PostList{
		Order: []string{"c", "b", "a"},
		Posts: map[string]*model.Post{
			"a": post("a", "ch", "u", 1), "b": post("b", "ch", "u", 2), "c": post("c", "ch", "u", 3),
			"root": post("root", "ch", "u", 0), // корень треда для контекста, не в Order
		},
	}
	if got := ids(PostsOf(list)); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("PostsOf = %v, want [a b c]", got)
	}
}

func TestTimeline(t *testing.T) {
	tl := &Timeline{Posts: []*model.Post{post("b", "ch", "u", 20), post("c", "ch", "u", 30)}}

	added := tl.Prepend([]*model.Post{post("a", "ch", "u", 10), post("b", "ch", "u", 20)}, false)
	if got := ids(added); len(got) != 1 || got[0] != "a" {
		t.Errorf("Prepend added %v, want [a]", got)
	}
	if i, isNew := tl.Upsert(post("d", "ch", "u", 40)); i != 3 || !isNew {
		t.Errorf("Upsert newest = %d, %v; want 3, true", i, isNew)
	}
	// Пост, пришедший по WebSocket раньше соседа, встаёт по времени.
	if i, isNew := tl.Upsert(post("bb", "ch", "u", 25)); i != 2 || !isNew {
		t.Errorf("Upsert middle = %d, %v; want 2, true", i, isNew)
	}
	edited := post("c", "ch", "u", 30)
	edited.Message = "edited"
	if i, isNew := tl.Upsert(edited); i != 3 || isNew || tl.Posts[3].Message != "edited" {
		t.Errorf("Upsert existing = %d, %v, %q; want 3, false, edited", i, isNew, tl.Posts[3].Message)
	}
	if !tl.Remove("bb") || tl.Remove("missing") {
		t.Error("Remove: want true for bb, false for missing")
	}
	if got := ids(tl.Posts); len(got) != 4 || got[0] != "a" || got[3] != "d" {
		t.Errorf("posts = %v, want [a b c d]", got)
	}
}

func newStore() (*Store, *model.Channel, *model.ChannelMember) {
	me := &model.User{Id: "me", Username: "alice"}
	s := New(me)
	ch := &model.Channel{Id: "ch", TeamId: "t", Type: model.ChannelTypeOpen, DisplayName: "dev", TotalMsgCount: 10}
	m := &model.ChannelMember{ChannelId: "ch", UserId: "me", MsgCount: 10, NotifyProps: model.StringMap{}}
	s.SetSidebar(&Sidebar{Channels: []*model.Channel{ch}, Members: []*model.ChannelMember{m}})
	return s, ch, m
}

func TestUnreadAndMentions(t *testing.T) {
	s, _, m := newStore()
	if unread, _ := s.Unread("ch"); unread {
		t.Fatal("fresh channel is unread")
	}

	r := s.ApplyPosted(post("p1", "ch", "bob", 100), false)
	if !r.KnownChannel || r.Timeline {
		t.Errorf("ApplyPosted = %+v, want known channel without timeline", r)
	}
	if unread, mentions := s.Unread("ch"); !unread || mentions != 0 {
		t.Errorf("after foreign post: unread=%v mentions=%d, want true 0", unread, mentions)
	}

	s.ApplyPosted(post("p2", "ch", "bob", 110), true)
	if _, mentions := s.Unread("ch"); mentions != 1 {
		t.Errorf("after mention: mentions=%d, want 1", mentions)
	}

	m.NotifyProps[model.MarkUnreadNotifyProp] = model.ChannelMarkUnreadMention
	if unread, _ := s.Unread("ch"); !unread {
		t.Error("muted channel with mention must be unread")
	}

	s.MarkViewed("ch")
	if unread, mentions := s.Unread("ch"); unread || mentions != 0 {
		t.Errorf("after MarkViewed: unread=%v mentions=%d", unread, mentions)
	}

	s.ApplyPosted(post("p3", "ch", "bob", 120), false)
	if unread, _ := s.Unread("ch"); unread {
		t.Error("muted channel without mention must not be unread")
	}
}

func TestOwnPostKeepsChannelRead(t *testing.T) {
	s, ch, _ := newStore()
	s.SetTimeline("ch", &Timeline{})
	r := s.ApplyPosted(post("p1", "ch", "me", 100), false)
	if !r.Timeline || len(s.Timeline("ch").Posts) != 1 {
		t.Errorf("own post not in timeline: %+v", r)
	}
	if unread, _ := s.Unread("ch"); unread {
		t.Error("own post made channel unread")
	}
	if ch.LastPostAt != 100 || ch.TotalMsgCount != 11 {
		t.Errorf("channel last_post_at=%d total=%d, want 100 11", ch.LastPostAt, ch.TotalMsgCount)
	}
}

func TestUnknownChannel(t *testing.T) {
	s, _, _ := newStore()
	if r := s.ApplyPosted(post("p1", "new-dm", "bob", 100), false); r.KnownChannel {
		t.Error("post in unknown channel reported as known")
	}
}

func TestTitles(t *testing.T) {
	s, _, _ := newStore()
	s.AddUsers([]*model.User{{Id: "bob", Username: "bob"}})
	dm := &model.Channel{Id: "dm", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds("me", "bob")}
	self := &model.Channel{Id: "self", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds("me", "me")}
	unknown := &model.Channel{Id: "dm2", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds("me", "zed")}
	for ch, want := range map[*model.Channel]string{dm: "bob", self: "alice (вы)", unknown: "zed"} {
		if got := s.Title(ch); got != want {
			t.Errorf("Title(%s) = %q, want %q", ch.Name, got, want)
		}
	}
	if got := s.MissingUsers([]string{"bob", "zed", "zed", ""}); len(got) != 1 || got[0] != "zed" {
		t.Errorf("MissingUsers = %v, want [zed]", got)
	}
}

func TestPostedTwiceCountsOnce(t *testing.T) {
	s, ch, _ := newStore()
	s.SetTimeline("ch", &Timeline{Loaded: true})
	p := post("p1", "ch", "bob", 100)
	s.ApplyPosted(p, true)
	s.ApplyPosted(p, true)
	if ch.TotalMsgCount != 11 {
		t.Errorf("total_msg_count = %d after the same post twice, want 11", ch.TotalMsgCount)
	}
	if _, mentions := s.Unread("ch"); mentions != 1 {
		t.Errorf("mentions = %d after the same mention twice, want 1", mentions)
	}
	if n := len(s.Timeline("ch").Posts); n != 1 {
		t.Errorf("timeline has %d posts, want 1", n)
	}
}

func TestReplies(t *testing.T) {
	for _, crt := range []bool{false, true} {
		s, ch, _ := newStore()
		s.crt = crt
		root := post("root", "ch", "bob", 100)
		s.SetTimeline("ch", &Timeline{Loaded: true, Posts: []*model.Post{root}})

		reply := post("r1", "ch", "carol", 200)
		reply.RootId = "root"
		r := s.ApplyPosted(reply, true)
		if r.Root != root || root.ReplyCount != 1 || root.LastReplyAt != 200 {
			t.Errorf("crt=%v: root not updated: %+v, reply_count=%d", crt, r, root.ReplyCount)
		}
		if r.Timeline == crt {
			t.Errorf("crt=%v: reply in channel timeline = %v, want %v", crt, r.Timeline, !crt)
		}
		s.ApplyPosted(reply, true) // второй раз из WebSocket
		if root.ReplyCount != 1 {
			t.Errorf("crt=%v: reply counted twice: reply_count=%d", crt, root.ReplyCount)
		}
		// При CRT ответ не делает канал непрочитанным и упоминание считается в треде.
		unread, mentions := s.Unread("ch")
		if crt && (unread || mentions != 0) {
			t.Errorf("crt: reply made channel unread=%v mentions=%d", unread, mentions)
		}
		if !crt && (!unread || mentions != 1) {
			t.Errorf("no crt: unread=%v mentions=%d, want true 1", unread, mentions)
		}
		if ch.TotalMsgCountRoot != 0 || ch.TotalMsgCount != 11 {
			t.Errorf("crt=%v: totals root=%d all=%d, want 0 11", crt, ch.TotalMsgCountRoot, ch.TotalMsgCount)
		}
	}
}

func TestReactions(t *testing.T) {
	s, _, _ := newStore()
	p := post("p1", "ch", "bob", 100)
	s.SetTimeline("ch", &Timeline{Loaded: true, Posts: []*model.Post{p}})
	r := &model.Reaction{UserId: "me", PostId: "p1", EmojiName: "+1"}

	if got, changed := s.ApplyReaction("ch", r, true); got != p || !changed || !p.HasReactions || len(p.Metadata.Reactions) != 1 {
		t.Fatalf("add reaction: got %v changed=%v, reactions %v", got, changed, p.Metadata)
	}
	if got, changed := s.ApplyReaction("ch", r, true); got != p || changed || len(p.Metadata.Reactions) != 1 {
		t.Errorf("same reaction twice: got %v changed=%v, %d reactions", got, changed, len(p.Metadata.Reactions))
	}
	if got, _ := s.ApplyReaction("other", r, true); got != nil {
		t.Error("reaction in unloaded channel returned a post")
	}
	if got, changed := s.ApplyReaction("ch", r, false); got != p || !changed || p.HasReactions || len(p.Metadata.Reactions) != 0 {
		t.Errorf("remove reaction: got %v changed=%v, has_reactions=%v, %d left", got, changed, p.HasReactions, len(p.Metadata.Reactions))
	}
}

func TestThreads(t *testing.T) {
	s, _, _ := newStore()
	s.SetSidebar(&Sidebar{CRT: true, Threads: []*model.ThreadResponse{
		{PostId: "t1", LastReplyAt: 100},
		{PostId: "t2", LastReplyAt: 300, UnreadReplies: 2, UnreadMentions: 1},
		{PostId: "t1", LastReplyAt: 100}, // DM-тред пришёл во второй команде
		{PostId: "t3", LastReplyAt: 200, DeleteAt: 5},
	}})
	if got := s.Threads(); len(got) != 2 || got[0].PostId != "t2" {
		t.Errorf("threads = %d, first %v; want t2, t1", len(got), got)
	}
	if unread, mentions := s.ThreadsUnread(); !unread || mentions != 1 {
		t.Errorf("ThreadsUnread = %v %d, want true 1", unread, mentions)
	}
	if !s.ApplyThreadRead("t2", 0, 0) || s.ApplyThreadRead("nope", 0, 0) {
		t.Error("ApplyThreadRead: want true for t2, false for unknown")
	}
	if unread, _ := s.ThreadsUnread(); unread {
		t.Error("threads still unread after read")
	}
	s.ApplyThread(&model.ThreadResponse{PostId: "t1", DeleteAt: 1})
	if s.Thread("t1") != nil {
		t.Error("deleted thread still present")
	}
}
