// Package store — состояние одного сервера в памяти: каналы, членства
// (непрочитанные), пользователи, загруженные ленты.
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
	me        *model.User
	teams     []*model.Team
	channels  map[string]*model.Channel
	members   map[string]*model.ChannelMember
	users     map[string]*model.User
	timelines map[string]*Timeline
}

func New(me *model.User) *Store {
	s := &Store{
		me:        me,
		channels:  map[string]*model.Channel{},
		members:   map[string]*model.ChannelMember{},
		users:     map[string]*model.User{me.Id: me},
		timelines: map[string]*Timeline{},
	}
	return s
}

func (s *Store) Me() *model.User { return s.me }

// Sidebar — всё, что нужно для списка каналов; собирается по REST целиком.
type Sidebar struct {
	Teams    []*model.Team
	Channels []*model.Channel // личные и групповые приходят в ответе каждой команды — дубли допустимы
	Members  []*model.ChannelMember
	Users    []*model.User
}

// SetSidebar заменяет каналы и членства; пользователи и ленты остаются.
func (s *Store) SetSidebar(sb *Sidebar) {
	s.teams = sb.Teams
	s.channels = make(map[string]*model.Channel, len(sb.Channels))
	for _, ch := range sb.Channels {
		s.channels[ch.Id] = ch
	}
	s.members = make(map[string]*model.ChannelMember, len(sb.Members))
	for _, m := range sb.Members {
		s.members[m.ChannelId] = m
	}
	s.AddUsers(sb.Users)
}

func (s *Store) Teams() []*model.Team { return s.teams }

func (s *Store) Channel(id string) *model.Channel { return s.channels[id] }

// TeamChannels — открытые и приватные каналы команды по алфавиту.
func (s *Store) TeamChannels(teamID string) []*model.Channel {
	var out []*model.Channel
	for _, ch := range s.channels {
		if ch.TeamId == teamID && !ch.IsGroupOrDirect() {
			out = append(out, ch)
		}
	}
	s.sortByTitle(out)
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

func (s *Store) sortByTitle(chs []*model.Channel) {
	slices.SortFunc(chs, func(a, b *model.Channel) int {
		return cmp.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
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

// Unread — есть ли непрочитанное и сколько упоминаний. В заглушённом канале
// (mark_unread=mention) непрочитанным считается только упоминание.
func (s *Store) Unread(channelID string) (unread bool, mentions int64) {
	ch, m := s.channels[channelID], s.members[channelID]
	if ch == nil || m == nil {
		return false, 0
	}
	unread = ch.TotalMsgCount > m.MsgCount
	if m.NotifyProps[model.MarkUnreadNotifyProp] == model.ChannelMarkUnreadMention {
		unread = m.MentionCount > 0
	}
	return unread, m.MentionCount
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
	KnownChannel bool // канал есть в сайдбаре; иначе сайдбар пора перезагрузить
	Timeline     bool // пост лёг в загруженную ленту
}

// ApplyPosted учитывает новый пост: счётчики канала, упоминание, ленту.
func (s *Store) ApplyPosted(p *model.Post, mentioned bool) Posted {
	var r Posted
	if t := s.timelines[p.ChannelId]; t != nil {
		t.Upsert(p)
		r.Timeline = true
	}
	ch := s.channels[p.ChannelId]
	if ch == nil {
		return r
	}
	r.KnownChannel = true
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
