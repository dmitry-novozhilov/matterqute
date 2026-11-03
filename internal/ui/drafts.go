package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
)

// Черновики переживают перезапуск. Их хранит сервер (/api/v4/drafts) — общие
// с веб-клиентом: начал писать в браузере, продолжил здесь. Если сервер их не
// хранит (выключено AllowSyncedDrafts или пользователь выключил sync_drafts),
// — файл рядом с настройками.
//
// Поле ввода держит черновики абзацами с оформлением по ключу (канал, корень
// треда), здесь — markdown (FromBlocks/ToBlocks). Сохраняем через draftDelay
// после правки и при уходе из канала (SwitchTo сообщает о прежнем ключе), при
// закрытии окна — сразу. Пришедший с сервера (загрузка после сайдбара и после
// переподключения, события draft_* из другого клиента) кладём в поле, только
// если там его не правили с прошлой сверки: правка здесь главнее — она и
// уйдёт на сервер.

const draftDelay = 800 * time.Millisecond

type draftKey struct{ channel, root string }

type drafts struct {
	m       *MainWindow
	server  bool                // хранит сервер; иначе файл path
	path    string              // файл черновиков, если их не хранит сервер
	synced  map[draftKey]string // что в хранилище (сервер подтвердил или записано в файл)
	pending map[draftKey]string // отправлено серверу, ответа ещё нет
	acked   map[draftKey]int64  // UpdateAt сервера у последнего подтверждённого: события старше — запоздавшее эхо
	dirty   map[draftKey]bool
	roots   map[string]string // корень треда → канал: у поля треда ключ — корень
	timer   *qt.QTimer
	saving  bool                   // запрос сохранения в пути: следующий — после него, по порядку
	again   bool                   // пока шёл запрос, набралось ещё
	loaded  bool                   // загружены после первого сайдбара
	reload  bool                   // переподключились: загрузить заново (события за обрыв потеряны)
	modeSet bool                   // server уже выбран по настройкам
	edited  map[draftKey]time.Time // когда черновик правили (здесь или, по серверу, где-то ещё)
}

func newDrafts(m *MainWindow, path string) *drafts {
	d := &drafts{m: m, path: path, synced: map[draftKey]string{}, pending: map[draftKey]string{}, acked: map[draftKey]int64{},
		dirty: map[draftKey]bool{}, roots: map[string]string{}, edited: map[draftKey]time.Time{}}
	d.timer = qt.NewQTimer2(m.w.QObject)
	d.timer.SetSingleShot(true)
	d.timer.SetInterval(int(draftDelay.Milliseconds()))
	d.timer.OnTimeout(func() { d.save(false) })
	return d
}

// keyOf — какой черновик у поля c с ключом key; false — у этого ключа черновиков нет.
func (d *drafts) keyOf(c *Composer, key string) (draftKey, bool) {
	if c == d.m.panel.composer {
		ch := d.roots[key]
		if t := d.m.thread; ch == "" && t != nil && t.rootID == key {
			ch = t.channelID
		}
		return draftKey{ch, key}, ch != "" && key != ""
	}
	return draftKey{channel: key}, d.m.st.Channel(key) != nil
}

func (d *drafts) composer(k draftKey) (*Composer, string) {
	if k.root != "" {
		return d.m.panel.composer, k.root
	}
	return d.m.composer, k.channel
}

func (d *drafts) text(k draftKey) string {
	c, key := d.composer(k)
	return c.DraftText(key)
}

// changed — поле c (ключ key) поменялось или из него ушли: сохранить скоро.
func (d *drafts) changed(c *Composer, key string) {
	k, ok := d.keyOf(c, key)
	if !ok {
		return
	}
	if k.root != "" {
		d.roots[k.root] = k.channel
	}
	d.dirty[k] = true
	d.edited[k] = time.Now()
	d.timer.Start2()
	d.m.draftsChanged()
}

// save — изменившиеся черновики — на сервер или в файл; wait — дождаться
// ответа сервера (окно закрывается). Запросы идут по одному: пока один в
// пути, новое копится (again) — иначе два ответа приходили бы не по порядку.
// synced меняется только по ответу сервера: до него набранное — правка
// здесь, и сверка с сервером (applyAll) его не затрёт.
func (d *drafts) save(wait bool) {
	if d.saving && !wait {
		d.again = true
		return
	}
	me := d.m.s.Me.Id
	var ups []*model.Draft
	var dels []draftKey
	for k := range d.dirty {
		delete(d.dirty, k)
		text := d.text(k)
		if text == d.synced[k] {
			continue
		}
		if text == "" {
			dels = append(dels, k)
		} else {
			ups = append(ups, &model.Draft{UserId: me, ChannelId: k.channel, RootId: k.root, Message: text})
		}
		d.pending[k] = text
	}
	if len(ups)+len(dels) == 0 {
		return
	}
	d.m.refreshItem("") // карандаш у канала в сайдбаре
	if !d.server {
		for k, text := range d.pending {
			if text == "" {
				delete(d.synced, k)
			} else {
				d.synced[k] = text
			}
		}
		clear(d.pending)
		if err := saveDraftFile(d.path, d.synced); err != nil {
			d.m.log.Warn("save drafts", "err", err)
		}
		return
	}
	run := func() {
		ctx, cancel := context.WithTimeout(d.m.ctx, requestTimeout)
		defer cancel()
		var results []draftSaved
		for _, dr := range ups {
			r := draftSaved{k: draftKey{dr.ChannelId, dr.RootId}, text: dr.Message}
			if saved, _, err := d.m.s.Client.UpsertDraft(ctx, dr); err != nil {
				d.m.log.Warn("save draft", "channel", dr.ChannelId, "root", dr.RootId, "err", err)
			} else {
				r.ok = true
				if saved != nil {
					r.at = saved.UpdateAt
				}
			}
			results = append(results, r)
		}
		for _, k := range dels {
			r := draftSaved{k: k}
			if err := d.deleteOnServer(ctx, me, k); err != nil {
				d.m.log.Warn("delete draft", "channel", k.channel, "root", k.root, "err", err)
			} else {
				r.ok = true
			}
			results = append(results, r)
		}
		d.m.ui(func() { d.saved(results) })
	}
	d.saving = true
	if wait {
		run()
	} else {
		go run()
	}
}

// draftSaved — ответ сервера на одно сохранение: text — что отправили ("" —
// удаление), at — UpdateAt сохранённого, ok — сервер принял.
type draftSaved struct {
	k    draftKey
	text string
	at   int64
	ok   bool
}

// saved — ответы сервера: принятое — в synced; не принятое (нет связи)
// остаётся отличием поля от synced, и его отправит следующая правка или
// сверка после переподключения.
func (d *drafts) saved(results []draftSaved) {
	d.saving = false
	for _, r := range results {
		if d.pending[r.k] == r.text {
			delete(d.pending, r.k)
		}
		if !r.ok {
			continue
		}
		if r.text == "" {
			delete(d.synced, r.k)
		} else {
			d.synced[r.k] = r.text
		}
		d.acked[r.k] = max(d.acked[r.k], r.at)
	}
	if d.again {
		d.again = false
		d.save(false)
	}
}

// deleteOnServer — черновик канала или треда прочь; его и не было — не ошибка.
// DeleteDraft клиента не знает про тред (всегда удаляет черновик канала),
// поэтому у треда — путь, как у веб-клиента: …/drafts/{корень}.
func (d *drafts) deleteOnServer(ctx context.Context, me string, k draftKey) error {
	var resp *model.Response
	var err error
	if k.root == "" {
		_, resp, err = d.m.s.Client.DeleteDraft(ctx, me, k.channel, "")
	} else {
		var r *http.Response
		r, err = d.m.s.Client.DoAPIDelete(ctx, "/users/"+me+"/channels/"+k.channel+"/drafts/"+k.root)
		if r != nil {
			r.Body.Close()
			resp = model.BuildResponse(r)
		}
	}
	if err != nil && resp != nil && resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

// sidebarLoaded — сайдбар загружен (команды известны): после первого раза и
// после переподключения — черновики с сервера или из файла.
func (d *drafts) sidebarLoaded(server bool) {
	if !d.modeSet || server != d.server {
		// Хранилище выбрано впервые (до сайдбара набранное шло в файл) или
		// сменилось (настройку переключили): что в synced — не отсюда, и
		// набранное в полях уйдёт в новое хранилище как правка.
		clear(d.synced)
		clear(d.pending)
		clear(d.acked)
		d.loaded = false
	}
	d.server, d.modeSet = server, true
	if d.loaded && !d.reload {
		return
	}
	d.loaded, d.reload = true, false
	if !d.server {
		all, err := loadDraftFile(d.path)
		if err != nil {
			d.m.log.Warn("load drafts", "err", err)
		}
		d.applyAll(all, true)
		return
	}
	teams := d.m.st.Teams()
	me := d.m.s.Me.Id
	go func() {
		ctx, cancel := context.WithTimeout(d.m.ctx, requestTimeout)
		defer cancel()
		all := map[draftKey]string{}
		edited := map[draftKey]time.Time{}
		var errs []error
		for _, t := range teams {
			list, _, err := d.m.s.Client.GetDrafts(ctx, me, t.Id)
			if err != nil {
				errs = append(errs, fmt.Errorf("drafts of team %s: %w", t.Name, err))
				continue
			}
			for _, dr := range list { // лички приходят в каждой команде
				k := draftKey{dr.ChannelId, dr.RootId}
				all[k] = dr.Message
				edited[k] = time.UnixMilli(dr.UpdateAt)
			}
		}
		d.m.ui(func() {
			if len(errs) > 0 {
				d.m.log.Warn("load drafts", "err", errors.Join(errs...))
				d.reload = true // попробуем после следующего переподключения
				if len(errs) == len(teams) {
					return
				}
			}
			for k, at := range edited {
				if _, ok := d.edited[k]; !ok {
					d.edited[k] = at
				}
			}
			// Часть команд не ответила: их черновиков в all нет, но они не удалены.
			d.applyAll(all, len(errs) == 0)
		})
	}()
}

// applyAll — все черновики с сервера (или из файла): пришедшие — в поля, а
// сохранённые раньше, которых там больше нет, — удалены (в другом клиенте);
// complete — список полный, и отсутствие в нём значит удаление.
// Правленное здесь и не сохранённое (не было связи) — сохранить.
func (d *drafts) applyAll(all map[draftKey]string, complete bool) {
	for k, text := range all {
		d.apply(k, text)
	}
	if complete {
		for k := range d.synced {
			if _, ok := all[k]; !ok {
				d.apply(k, "")
			}
		}
	}
	for _, c := range []*Composer{d.m.composer, d.m.panel.composer} {
		for _, key := range c.DraftKeys() {
			if k, ok := d.keyOf(c, key); ok && d.text(k) != d.synced[k] {
				d.dirty[k] = true
			}
		}
	}
	if len(d.dirty) > 0 {
		d.timer.Start2()
	}
	d.m.refreshItem("")
	d.m.draftsChanged()
}

// apply — черновик k с сервера или из файла: в поле, если там его не правили
// с прошлой сверки.
func (d *drafts) apply(k draftKey, text string) {
	if k.root != "" {
		d.roots[k.root] = k.channel
	}
	// Так его прочитает поле: абзацами и обратно (иначе вечное «не то же»).
	norm := strings.TrimSpace(markdown.FromBlocks(markdown.ToBlocks(text)))
	if norm == d.synced[k] {
		return // то же, что сохранено (в том числе своё же эхо)
	}
	if d.text(k) != d.synced[k] {
		return // здесь правили — правка уйдёт на сервер
	}
	if norm == "" {
		delete(d.synced, k)
	} else {
		d.synced[k] = norm
	}
	c, key := d.composer(k)
	c.SetDraft(key, text)
	d.m.refreshItem("")
	d.m.draftsChanged()
}

// remote — событие draft_created/updated/deleted (другой клиент или эхо своего).
func (d *drafts) remote(dr *model.Draft, deleted bool) {
	if !d.server || !d.loaded {
		return
	}
	k := draftKey{dr.ChannelId, dr.RootId}
	if dr.UpdateAt > 0 && dr.UpdateAt < d.acked[k] {
		return // запоздавшее эхо своего же более раннего сохранения
	}
	text := dr.Message
	if deleted {
		text = ""
	} else if dr.UpdateAt > 0 {
		d.edited[k] = time.UnixMilli(dr.UpdateAt)
	}
	d.apply(k, text)
}

// eventDraft — черновик из data["draft"] (JSON-строка).
func eventDraft(data map[string]any) (*model.Draft, error) {
	raw, ok := data["draft"].(string)
	if !ok {
		return nil, fmt.Errorf("draft: want JSON string, got %T", data["draft"])
	}
	var dr model.Draft
	if err := json.Unmarshal([]byte(raw), &dr); err != nil {
		return nil, fmt.Errorf("draft %s: %w", truncate(raw, 200), err)
	}
	return &dr, nil
}

// fileDraft — черновик в файле (когда их не хранит сервер).
type fileDraft struct {
	Channel string `json:"channel_id"`
	Root    string `json:"root_id,omitempty"`
	Message string `json:"message"`
}

func loadDraftFile(path string) (map[draftKey]string, error) {
	all := map[draftKey]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || path == "" {
		return all, nil
	}
	if err != nil {
		return all, fmt.Errorf("load drafts %s: %w", path, err)
	}
	var list []fileDraft
	if err := json.Unmarshal(data, &list); err != nil {
		return all, fmt.Errorf("load drafts %s: %w", path, err)
	}
	for _, f := range list {
		all[draftKey{f.Channel, f.Root}] = f.Message
	}
	return all, nil
}

// saveDraftFile — черновики в файл (0600: там текст сообщений); нет — файла нет.
func saveDraftFile(path string, all map[draftKey]string) error {
	if path == "" {
		return nil
	}
	if len(all) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("save drafts %s: %w", path, err)
		}
		return nil
	}
	list := make([]fileDraft, 0, len(all))
	for k, text := range all {
		list = append(list, fileDraft{Channel: k.channel, Root: k.root, Message: text})
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("save drafts %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("save drafts %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("save drafts %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("save drafts %s: %w", path, err)
	}
	return nil
}
