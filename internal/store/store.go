// Package store — состояние одного сервера в памяти: каналы, членства
// (непрочитанные), пользователи, загруженные ленты, треды.
//
// Однопоточный: им владеет главный поток UI. Сеть приносит данные через
// mainthread.Start, поэтому мьютексов нет.
package store

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

type Store struct {
	me          *model.User
	crt         bool
	maxFileSize int64
	teams       []*model.Team
	channels    map[string]*model.Channel
	members     map[string]*model.ChannelMember
	users       map[string]*model.User
	timelines   map[string]*Timeline
	threads     map[string]*model.ThreadResponse // CRT: треды, на которые подписан пользователь
	// Посты, уже учтённые в счётчиках: свой пост приходит дважды — ответом
	// на CreatePost и по WebSocket.
	applied map[string]bool
}

func New(me *model.User) *Store {
	return &Store{
		me:        me,
		channels:  map[string]*model.Channel{},
		members:   map[string]*model.ChannelMember{},
		users:     map[string]*model.User{me.Id: me},
		timelines: map[string]*Timeline{},
		threads:   map[string]*model.ThreadResponse{},
		applied:   map[string]bool{},
	}
}

func (s *Store) Me() *model.User { return s.me }

// CRT — включены ли свёрнутые треды: ответы живут только в тредах, а
// непрочитанное в канале считается по корневым постам.
func (s *Store) CRT() bool { return s.crt }

// MaxFileSize — лимит сервера на загрузку файла в байтах; 0 — неизвестен.
func (s *Store) MaxFileSize() int64 { return s.maxFileSize }

// Sidebar — всё, что нужно для списка каналов; собирается по REST целиком.
type Sidebar struct {
	CRT         bool
	MaxFileSize int64 // лимит сервера на загрузку файла; 0 — неизвестен
	Teams       []*model.Team
	Channels    []*model.Channel // личные и групповые приходят в ответе каждой команды — дубли допустимы
	Members     []*model.ChannelMember
	Users       []*model.User
	Threads     []*model.ThreadResponse // только при CRT; DM-треды приходят в каждой команде
}

// SetSidebar заменяет каналы, членства и треды; пользователи и ленты остаются.
func (s *Store) SetSidebar(sb *Sidebar) {
	s.crt = sb.CRT
	s.maxFileSize = sb.MaxFileSize
	s.teams = sb.Teams
	s.channels = make(map[string]*model.Channel, len(sb.Channels))
	for _, ch := range sb.Channels {
		s.channels[ch.Id] = ch
	}
	s.members = make(map[string]*model.ChannelMember, len(sb.Members))
	for _, m := range sb.Members {
		s.members[m.ChannelId] = m
	}
	s.threads = make(map[string]*model.ThreadResponse, len(sb.Threads))
	for _, t := range sb.Threads {
		s.ApplyThread(t)
	}
	s.AddUsers(sb.Users)
}

func (s *Store) Teams() []*model.Team { return s.teams }

func (s *Store) Channel(id string) *model.Channel { return s.channels[id] }

// TeamFor — команда, от имени которой работать с тредами канала: у личных
// каналов своей команды нет, сервер принимает любую из команд пользователя.
func (s *Store) TeamFor(channelID string) string {
	if ch := s.channels[channelID]; ch != nil && ch.TeamId != "" {
		return ch.TeamId
	}
	if len(s.teams) > 0 {
		return s.teams[0].Id
	}
	return ""
}

// ChannelByName — открытый или приватный канал по имени (как в ~ссылке);
// при совпадении имён в разных командах — из preferTeam.
func (s *Store) ChannelByName(name, preferTeam string) *model.Channel {
	var found *model.Channel
	for _, ch := range s.channels {
		if ch.Name == name && !ch.IsGroupOrDirect() {
			if ch.TeamId == preferTeam {
				return ch
			}
			found = ch
		}
	}
	return found
}

// TeamChannels — открытые и приватные каналы команды по алфавиту.
func (s *Store) TeamChannels(teamID string) []*model.Channel {
	var out []*model.Channel
	for _, ch := range s.channels {
		if ch.TeamId == teamID && !ch.IsGroupOrDirect() {
			out = append(out, ch)
		}
	}
	slices.SortFunc(out, func(a, b *model.Channel) int {
		return cmp.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	return out
}

// DirectChannels — личные и групповые, свежие сверху.
func (s *Store) DirectChannels() []*model.Channel {
	var out []*model.Channel
	for _, ch := range s.channels {
		if ch.IsGroupOrDirect() {
			out = append(out, ch)
		}
	}
	slices.SortFunc(out, func(a, b *model.Channel) int {
		if c := cmp.Compare(b.LastPostAt, a.LastPostAt); c != 0 {
			return c
		}
		return cmp.Compare(s.Title(a), s.Title(b))
	})
	return out
}

// DMPeer — собеседник в личном канале; для канала с самим собой — свой id
// (GetOtherUserIdForDM в этом случае возвращает "").
func (s *Store) DMPeer(ch *model.Channel) string {
	if id := ch.GetOtherUserIdForDM(s.me.Id); id != "" {
		return id
	}
	return s.me.Id
}

// Title — имя канала для показа, без префиксов типа.
func (s *Store) Title(ch *model.Channel) string {
	if ch.Type != model.ChannelTypeDirect {
		return ch.DisplayName // у групповых сервер сам кладёт сюда список участников
	}
	peer := s.DMPeer(ch)
	name := s.Username(peer)
	if peer == s.me.Id {
		return name + " (вы)"
	}
	return name
}

// Unread — есть ли непрочитанное и сколько упоминаний; при CRT — только по
// корневым постам (calculateUnreadCount веб-клиента). В заглушённом канале
// (mark_unread=mention) непрочитанным считается только упоминание.
func (s *Store) Unread(channelID string) (unread bool, mentions int64) {
	ch, m := s.channels[channelID], s.members[channelID]
	if ch == nil || m == nil {
		return false, 0
	}
	if s.crt {
		unread, mentions = ch.TotalMsgCountRoot > m.MsgCountRoot, m.MentionCountRoot
	} else {
		unread, mentions = ch.TotalMsgCount > m.MsgCount, m.MentionCount
	}
	if m.NotifyProps[model.MarkUnreadNotifyProp] == model.ChannelMarkUnreadMention {
		unread = mentions > 0
	}
	return unread, mentions
}

// MarkViewed — локально то же, что делает сервер на ViewChannel.
func (s *Store) MarkViewed(channelID string) {
	ch, m := s.channels[channelID], s.members[channelID]
	if ch == nil || m == nil {
		return
	}
	m.MsgCount = ch.TotalMsgCount
	m.MsgCountRoot = ch.TotalMsgCountRoot
	m.MentionCount = 0
	m.MentionCountRoot = 0
	m.UrgentMentionCount = 0
	m.LastViewedAt = model.GetMillisForTime(time.Now())
}

func (s *Store) User(id string) *model.User { return s.users[id] }

// Username — имя пользователя или его id, если профиль ещё не загружен.
func (s *Store) Username(id string) string {
	if u := s.users[id]; u != nil {
		return u.Username
	}
	return id
}

func (s *Store) AddUsers(us []*model.User) {
	for _, u := range us {
		s.users[u.Id] = u
	}
}

// MissingUsers — id из списка, чьих профилей нет, без повторов.
func (s *Store) MissingUsers(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && s.users[id] == nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// DMPeers — собеседники во всех личных каналах.
func (s *Store) DMPeers() []string {
	var out []string
	for _, ch := range s.channels {
		if ch.Type == model.ChannelTypeDirect {
			out = append(out, s.DMPeer(ch))
		}
	}
	return out
}

// Timeline — загруженная лента канала или nil.
func (s *Store) Timeline(channelID string) *Timeline { return s.timelines[channelID] }

func (s *Store) SetTimeline(channelID string, t *Timeline) { s.timelines[channelID] = t }

// DropTimelines забывает ленты: после обрыва WebSocket в них могут быть дыры.
func (s *Store) DropTimelines() { s.timelines = map[string]*Timeline{} }

// Posted — итог применения события posted.
type Posted struct {
	KnownChannel bool        // канал есть в сайдбаре; иначе сайдбар пора перезагрузить
	Timeline     bool        // пост лёг в загруженную ленту канала
	Root         *model.Post // у корня из ленты канала вырос счётчик ответов
}

// ApplyPosted учитывает новый пост: счётчики канала, упоминание, ленту.
// Повторно пришедший пост только обновляется в ленте.
func (s *Store) ApplyPosted(p *model.Post, mentioned bool) Posted {
	var r Posted
	again := s.applied[p.Id]
	s.applied[p.Id] = true
	t := s.timelines[p.ChannelId]
	if t != nil && (p.RootId == "" || !s.crt) {
		t.Upsert(p)
		r.Timeline = true
	}
	ch := s.channels[p.ChannelId]
	r.KnownChannel = ch != nil
	if again {
		return r
	}
	if t != nil && p.RootId != "" {
		if root := t.Get(p.RootId); root != nil {
			root.ReplyCount++
			root.LastReplyAt = max(root.LastReplyAt, p.CreateAt)
			r.Root = root
		}
	}
	if ch == nil {
		return r
	}
	ch.TotalMsgCount++
	if p.RootId == "" {
		ch.TotalMsgCountRoot++
	}
	ch.LastPostAt = max(ch.LastPostAt, p.CreateAt)
	m := s.members[p.ChannelId]
	switch {
	case m == nil:
	case p.UserId == s.me.Id:
		// Свой пост сервер сразу считает прочитанным.
		s.MarkViewed(p.ChannelId)
	case mentioned:
		m.MentionCount++
		if p.RootId == "" {
			m.MentionCountRoot++ // упоминание в ответе при CRT считается в треде
		}
	}
	return r
}

// ApplyEdited обновляет пост в ленте; false — ленты или поста в ней нет.
func (s *Store) ApplyEdited(p *model.Post) bool {
	t := s.timelines[p.ChannelId]
	return t != nil && t.Replace(p)
}

// ApplyDeleted убирает пост из ленты; false — ленты или поста в ней нет.
func (s *Store) ApplyDeleted(p *model.Post) bool {
	t := s.timelines[p.ChannelId]
	return t != nil && t.Remove(p.Id)
}

// ApplyReaction меняет реакции поста в ленте канала. Возвращает пост, если он
// загружен (перерисовать его надо, даже если changed=false: тот же объект мог
// уже обновить кто-то другой, например панель треда).
func (s *Store) ApplyReaction(channelID string, r *model.Reaction, added bool) (p *model.Post, changed bool) {
	t := s.timelines[channelID]
	if t == nil {
		return nil, false
	}
	if p = t.Get(r.PostId); p == nil {
		return nil, false
	}
	return p, WithReaction(p, r, added)
}

// WithReaction добавляет или убирает реакцию в метаданных поста; false —
// ничего не изменилось (та же реакция пришла второй раз).
func WithReaction(p *model.Post, r *model.Reaction, added bool) bool {
	if p.Metadata == nil {
		p.Metadata = &model.PostMetadata{}
	}
	i := slices.IndexFunc(p.Metadata.Reactions, func(x *model.Reaction) bool {
		return x.UserId == r.UserId && x.EmojiName == r.EmojiName
	})
	switch {
	case added && i < 0:
		p.Metadata.Reactions = append(p.Metadata.Reactions, r)
	case !added && i >= 0:
		p.Metadata.Reactions = slices.Delete(p.Metadata.Reactions, i, i+1)
	default:
		return false
	}
	p.HasReactions = len(p.Metadata.Reactions) > 0
	return true
}

// Треды (CRT)

func (s *Store) Thread(id string) *model.ThreadResponse { return s.threads[id] }

// Threads — треды, свежие сверху.
func (s *Store) Threads() []*model.ThreadResponse {
	out := make([]*model.ThreadResponse, 0, len(s.threads))
	for _, t := range s.threads {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b *model.ThreadResponse) int {
		if c := cmp.Compare(b.LastReplyAt, a.LastReplyAt); c != 0 {
			return c
		}
		return cmp.Compare(a.PostId, b.PostId)
	})
	return out
}

// ApplyThread добавляет или обновляет тред; удалённый — убирает.
func (s *Store) ApplyThread(t *model.ThreadResponse) {
	if t.DeleteAt > 0 || (t.Post != nil && t.Post.DeleteAt > 0) {
		delete(s.threads, t.PostId)
		return
	}
	s.threads[t.PostId] = t
}

// ApplyThreadRead выставляет непрочитанное треда; false — такого треда нет.
func (s *Store) ApplyThreadRead(id string, unreadReplies, unreadMentions int64) bool {
	t := s.threads[id]
	if t == nil {
		return false
	}
	t.UnreadReplies, t.UnreadMentions = unreadReplies, unreadMentions
	return true
}

// ThreadsUnread — есть ли непрочитанные треды и сколько в них упоминаний.
func (s *Store) ThreadsUnread() (unread bool, mentions int64) {
	for _, t := range s.threads {
		unread = unread || t.UnreadReplies > 0
		mentions += t.UnreadMentions
	}
	return unread || mentions > 0, mentions
}
