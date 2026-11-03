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
	me           *model.User
	crt          bool
	noPreviews   bool // пользователь выключил предпросмотр ссылок
	postPriority bool
	nameFormat   string // как показывать людей (model.Show*); "" — имя пользователя
	nameLocked   bool
	maxFileSize  int64
	teams        []*model.Team
	channels     map[string]*model.Channel
	members      map[string]*model.ChannelMember
	users        map[string]*model.User
	timelines    map[string]*Timeline
	threads      map[string]*model.ThreadResponse // CRT: треды, на которые подписан пользователь
	statuses     map[string]string                // присутствие: online, away, dnd, offline
	// Посты, уже учтённые в счётчиках: свой пост приходит дважды — ответом
	// на CreatePost и по WebSocket.
	applied map[string]bool
	// Удалённые посты: удаление применяется по ответу DeletePost и ещё раз
	// приходит по WebSocket.
	deleted map[string]bool
	saved   map[string]bool // сохранённые посты (настройки flagged_post)
	// Категории сайдбара по командам (categories.go).
	categories map[string]*model.OrderedSidebarCategories
	// Непрочитанные — отдельной группой; keepUnread — открытый канал, который
	// был непрочитанным: остаётся в ней, пока его не покинут.
	unreadSection bool
	keepUnread    string
}

func New(me *model.User) *Store {
	return &Store{
		me:        me,
		channels:  map[string]*model.Channel{},
		members:   map[string]*model.ChannelMember{},
		users:     map[string]*model.User{me.Id: me},
		timelines: map[string]*Timeline{},
		threads:   map[string]*model.ThreadResponse{},
		statuses:  map[string]string{},
		applied:   map[string]bool{},
		deleted:   map[string]bool{},
		saved:     map[string]bool{},

		categories: map[string]*model.OrderedSidebarCategories{},
	}
}

func (s *Store) Me() *model.User { return s.me }

// CRT — включены ли свёрнутые треды: ответы живут только в тредах, а
// непрочитанное в канале считается по корневым постам.
func (s *Store) CRT() bool { return s.crt }

// LinkPreviews — показывать ли предпросмотр ссылок (настройка пользователя).
func (s *Store) LinkPreviews() bool { return !s.noPreviews }

// PostPriority — можно ли ставить сообщениям метки «Важно» и «Срочно».
func (s *Store) PostPriority() bool { return s.postPriority }

// MaxFileSize — лимит сервера на загрузку файла в байтах; 0 — неизвестен.
func (s *Store) MaxFileSize() int64 { return s.maxFileSize }

// Sidebar — всё, что нужно для списка каналов; собирается по REST целиком.
type Sidebar struct {
	CRT          bool
	MaxFileSize  int64 // лимит сервера на загрузку файла; 0 — неизвестен
	LinkPreviews bool  // показывать предпросмотр ссылок
	Teams        []*model.Team
	Channels     []*model.Channel // личные и групповые приходят в ответе каждой команды — дубли допустимы
	Members      []*model.ChannelMember
	Users        []*model.User
	Threads      []*model.ThreadResponse // только при CRT; DM-треды приходят в каждой команде
	Statuses     []*model.Status         // присутствие собеседников в личках
	Saved        []string                // сохранённые посты; nil — не загрузились (прежние остаются)
	SyncDrafts   bool                    // черновики хранит сервер (иначе — файл приложения)
	// Категории сайдбара по командам; команды, чьи не загрузились, — без них.
	Categories map[string]*model.OrderedSidebarCategories
	// UnreadSection — непрочитанные отдельной группой вверху сайдбара.
	UnreadSection bool
	PostPriority  bool // метки «Важно», «Срочно» включены на сервере
	// NameFormat — как показывать людей (model.Show*), NameFormatLocked —
	// сервер запретил менять.
	NameFormat       string
	NameFormatLocked bool
}

// SetSidebar заменяет каналы, членства и треды; пользователи и ленты остаются.
func (s *Store) SetSidebar(sb *Sidebar) {
	s.crt = sb.CRT
	s.noPreviews = !sb.LinkPreviews
	s.maxFileSize = sb.MaxFileSize
	s.teams = sb.Teams
	s.unreadSection = sb.UnreadSection
	s.postPriority = sb.PostPriority
	s.nameFormat, s.nameLocked = sb.NameFormat, sb.NameFormatLocked
	s.categories = map[string]*model.OrderedSidebarCategories{}
	for team, c := range sb.Categories {
		s.categories[team] = c
	}
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
	for _, st := range sb.Statuses {
		s.statuses[st.UserId] = st.Status
	}
	if sb.Saved != nil {
		s.saved = map[string]bool{}
		for _, id := range sb.Saved {
			s.saved[id] = true
		}
	}
}

// Saved — пост сохранён (настройка flagged_post, общая с веб-клиентом).
func (s *Store) Saved(postID string) bool { return s.saved[postID] }

// SetSaved — пост сохранили или убрали из сохранённых.
func (s *Store) SetSaved(postID string, on bool) {
	if on {
		s.saved[postID] = true
	} else {
		delete(s.saved, postID)
	}
}

// SetMember — участие в канале с сервера (счётчики после «пометить непрочитанным»).
func (s *Store) SetMember(m *model.ChannelMember) {
	if s.channels[m.ChannelId] != nil {
		s.members[m.ChannelId] = m
	}
}

// Status — присутствие пользователя: online, away, dnd, offline (по умолчанию).
func (s *Store) Status(userID string) string {
	if st := s.statuses[userID]; st != "" {
		return st
	}
	return model.StatusOffline
}

// SetStatus — пришло событие status_change.
func (s *Store) SetStatus(userID, status string) { s.statuses[userID] = status }

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
	name := s.Name(peer)
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

// Muted — канал заглушён: непрочитанным считается только упоминание.
func (s *Store) Muted(channelID string) bool {
	m := s.members[channelID]
	return m != nil && m.NotifyProps[model.MarkUnreadNotifyProp] == model.ChannelMarkUnreadMention
}

// SetMuted — канал заглушили или включили (mark_unread участника), пока
// сервер не прислал обновление сам.
func (s *Store) SetMuted(channelID string, muted bool) {
	m := s.members[channelID]
	if m == nil {
		return
	}
	if m.NotifyProps == nil {
		m.NotifyProps = model.StringMap{}
	}
	m.NotifyProps[model.MarkUnreadNotifyProp] = model.ChannelMarkUnreadAll
	if muted {
		m.NotifyProps[model.MarkUnreadNotifyProp] = model.ChannelMarkUnreadMention
	}
}

// AddChannel — вошли в канал или создали его: в сайдбаре сразу, не дожидаясь
// перезагрузки. member nil — пока без счётчиков (их принесёт перезагрузка).
func (s *Store) AddChannel(ch *model.Channel, member *model.ChannelMember) {
	s.channels[ch.Id] = ch
	if member != nil {
		s.members[ch.Id] = member
	}
}

// RemoveChannel — вышли из канала: из сайдбара сразу, вместе с лентой.
func (s *Store) RemoveChannel(id string) {
	delete(s.channels, id)
	delete(s.members, id)
	delete(s.timelines, id)
}

// NotifyProp — настройка уведомлений участника в канале (desktop,
// desktop_threads, ignore_channel_mentions, mark_unread); "" — не задана.
func (s *Store) NotifyProp(channelID, key string) string {
	if m := s.members[channelID]; m != nil {
		return m.NotifyProps[key]
	}
	return ""
}

// SetNotifyProp — настройку поменяли, пока сервер не прислал участника сам.
func (s *Store) SetNotifyProp(channelID, key, value string) {
	m := s.members[channelID]
	if m == nil {
		return
	}
	if m.NotifyProps == nil {
		m.NotifyProps = model.StringMap{}
	}
	m.NotifyProps[key] = value
}

// LastViewed — когда канал был прочитан в последний раз (мс); 0 — неизвестно.
func (s *Store) LastViewed(channelID string) int64 {
	if m := s.members[channelID]; m != nil {
		return m.LastViewedAt
	}
	return 0
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

// KnownUsers — все пользователи, о которых знаем (для автодополнения @).
func (s *Store) KnownUsers() []*model.User {
	out := make([]*model.User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	return out
}

// Username — имя пользователя или его id, если профиль ещё не загружен.
func (s *Store) Username(id string) string {
	if u := s.users[id]; u != nil {
		return u.Username
	}
	return id
}

// Name — как показывать человека: по настройке «как показывать имена»
// (имя пользователя, псевдоним или «Имя Фамилия»; чего нет — имя
// пользователя). Для @упоминаний и ссылок — Username.
func (s *Store) Name(id string) string {
	if u := s.users[id]; u != nil {
		format := s.nameFormat
		if format == "" {
			format = model.ShowUsername
		}
		return u.GetDisplayName(format)
	}
	return id
}

// NameFormat — настройка «как показывать имена» и запретил ли сервер её менять.
func (s *Store) NameFormat() (format string, locked bool) {
	if s.nameFormat == "" {
		return model.ShowUsername, s.nameLocked
	}
	return s.nameFormat, s.nameLocked
}

// SetNameFormat — настройку поменяли здесь.
func (s *Store) SetNameFormat(format string) { s.nameFormat = format }

func (s *Store) AddUsers(us []*model.User) {
	for _, u := range us {
		s.users[u.Id] = u
	}
}

// UserByName — загруженный профиль по имени пользователя (без учёта регистра) или nil.
func (s *Store) UserByName(username string) *model.User {
	for _, u := range s.users {
		if strings.EqualFold(u.Username, username) {
			return u
		}
	}
	return nil
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

// MarkTimelinesStale — был обрыв связи: загруженные ленты сверить с сервером.
func (s *Store) MarkTimelinesStale() {
	for _, t := range s.timelines {
		if t.Loaded {
			t.Stale = true
		}
	}
}

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

// ApplyDeleted убирает пост из ленты канала (removed — он там был), у ответа
// уменьшает счётчик ответов корня в ленте (root — перерисовать). first=false
// — это удаление уже применяли: оно приходит и ответом на DeletePost, и по
// WebSocket.
func (s *Store) ApplyDeleted(p *model.Post) (removed bool, root *model.Post, first bool) {
	if s.deleted[p.Id] {
		return false, nil, false
	}
	s.deleted[p.Id] = true
	t := s.timelines[p.ChannelId]
	if t == nil {
		return false, nil, true
	}
	removed = t.Remove(p.Id)
	if p.RootId != "" {
		if root = t.Get(p.RootId); root != nil && root.ReplyCount > 0 {
			root.ReplyCount--
		}
	}
	return removed, root, true
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

// RemoveThread — отписались от треда: из списка тредов прочь.
func (s *Store) RemoveThread(id string) { delete(s.threads, id) }

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

// Mentions — сколько упоминаний ждёт пользователя: по каналам (в личках сервер
// считает упоминанием каждое сообщение) и, при CRT, по тредам.
func (s *Store) Mentions() int64 {
	var n int64
	for id := range s.channels {
		_, m := s.Unread(id)
		n += m
	}
	if s.crt {
		_, m := s.ThreadsUnread()
		n += m
	}
	return n
}

// NewMessages — сколько новых сообщений в каналах, не заглушённых (при CRT —
// корневых, плюс новые ответы в тредах, на которые подписан). Для трея: если
// упоминаний нет, а новое есть.
func (s *Store) NewMessages() int64 {
	var n int64
	for id, ch := range s.channels {
		m := s.members[id]
		if m == nil || s.Muted(id) {
			continue
		}
		if s.crt {
			n += max(0, ch.TotalMsgCountRoot-m.MsgCountRoot)
		} else {
			n += max(0, ch.TotalMsgCount-m.MsgCount)
		}
	}
	if s.crt {
		for _, t := range s.threads {
			n += max(0, t.UnreadReplies)
		}
	}
	return n
}

// ThreadsUnread — есть ли непрочитанные треды и сколько в них упоминаний.
func (s *Store) ThreadsUnread() (unread bool, mentions int64) {
	for _, t := range s.threads {
		unread = unread || t.UnreadReplies > 0
		mentions += t.UnreadMentions
	}
	return unread || mentions > 0, mentions
}

// Snapshot — что сохранить на диск для быстрого запуска: сайдбар и хвосты
// загруженных лент (до tail постов, без «только вам»); пользователи — авторы
// этих постов и собеседники по личкам, без себя. Сохраняют снимок в главном
// потоке: в нём те же объекты, что в store.
func (s *Store) Snapshot(tail int) (*Sidebar, map[string]*Timeline) {
	sb := &Sidebar{CRT: s.crt, MaxFileSize: s.maxFileSize, LinkPreviews: !s.noPreviews, Teams: s.teams,
		Categories: s.categories, Saved: []string{}, UnreadSection: s.unreadSection, PostPriority: s.postPriority,
		NameFormat: s.nameFormat, NameFormatLocked: s.nameLocked}
	for _, ch := range s.channels {
		sb.Channels = append(sb.Channels, ch)
	}
	for _, m := range s.members {
		sb.Members = append(sb.Members, m)
	}
	for _, t := range s.threads {
		sb.Threads = append(sb.Threads, t)
	}
	for id := range s.saved {
		sb.Saved = append(sb.Saved, id)
	}
	users := map[string]bool{}
	tls := map[string]*Timeline{}
	for id, t := range s.timelines {
		if !t.Loaded || s.channels[id] == nil {
			continue
		}
		posts, cut := t.Posts, len(t.Posts) > tail
		if cut {
			posts = posts[len(posts)-tail:]
		}
		posts = slices.DeleteFunc(slices.Clone(posts), func(p *model.Post) bool { return p.Type == model.PostTypeEphemeral })
		tls[id] = &Timeline{Posts: posts, HasMore: t.HasMore || cut, Loaded: true}
		for _, p := range posts {
			users[p.UserId] = true
		}
	}
	for _, ch := range s.channels {
		if ch.Type == model.ChannelTypeDirect {
			users[s.DMPeer(ch)] = true
		}
	}
	delete(users, s.me.Id)
	for id := range users {
		if u := s.users[id]; u != nil {
			sb.Users = append(sb.Users, u)
		}
	}
	return sb, tls
}

// Restore — снимок с диска вместо загрузки с сервера. Всё как после обрыва
// связи: ленты загружены, но их сверят с сервером (Stale).
func (s *Store) Restore(sb *Sidebar, tls map[string]*Timeline) {
	s.SetSidebar(sb)
	for id, t := range tls {
		if s.channels[id] == nil || t == nil {
			continue
		}
		t.Loaded, t.Stale = true, true
		s.timelines[id] = t
	}
}

// SetMe — свой профиль пришёл с сервера (окно открыто из снимка со старым).
func (s *Store) SetMe(u *model.User) {
	s.me = u
	s.users[u.Id] = u
}
