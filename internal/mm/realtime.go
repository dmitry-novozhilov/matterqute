package mm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// RealtimeHandler вызывается последовательно из горутины Realtime.Run.
type RealtimeHandler interface {
	// OnConnected — пришёл hello. Зовётся и после каждого переподключения:
	// события за время обрыва потеряны, состояние надо досинхронизировать по REST.
	OnConnected()
	// OnDisconnected — соединение оборвалось; следующая попытка через retryIn
	// (или раньше, если придёт сигнал в канал retry у RunRealtime).
	OnDisconnected(err error, retryIn time.Duration)
	OnEvent(ev *model.WebSocketEvent)
}

const (
	reconnectMinDelay = time.Second
	reconnectMaxDelay = 30 * time.Second
	// Соединение, прожившее дольше, считаем здоровым и сбрасываем backoff.
	stableConnection = time.Minute
)

// RunRealtime держит WebSocket к серверу, переподключаясь с экспоненциальной
// задержкой, пока не отменён ctx. Сигнал в retry («Повторить сейчас»)
// прерывает ожидание.
func (s *Session) RunRealtime(ctx context.Context, h RealtimeHandler, retry <-chan struct{}) {
	wsURL, err := websocketURL(s.ServerURL)
	if err != nil {
		h.OnDisconnected(err, 0)
		return
	}
	delay := reconnectMinDelay
	for ctx.Err() == nil {
		started := time.Now()
		err := listenOnce(ctx, wsURL, s.Token(), h, s.setWS)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > stableConnection {
			delay = reconnectMinDelay
		}
		h.OnDisconnected(err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		case <-retry:
		}
		delay = min(delay*2, reconnectMaxDelay)
	}
}

func listenOnce(ctx context.Context, wsURL, token string, h RealtimeHandler, setWS func(*wsOut)) error {
	wsc, err := model.NewWebSocketClient4(wsURL, token)
	if err != nil {
		return fmt.Errorf("connect %s: %w", wsURL, err)
	}
	wsc.Listen()
	defer func() {
		wsc.Close()
		// Читатель внутри model.WebSocketClient может висеть на записи в полный
		// EventChannel; дочитываем, пока он не закроет каналы.
		go func() {
			for range wsc.EventChannel {
			}
		}()
		for range wsc.ResponseChannel {
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-wsc.EventChannel:
			if !ok {
				if wsc.ListenError != nil {
					return fmt.Errorf("websocket %s: %w", wsURL, wsc.ListenError)
				}
				return fmt.Errorf("websocket %s: closed by server", wsURL)
			}
			if ev.EventType() == model.WebsocketEventHello {
				out := startWSOut(wsc)
				setWS(out)
				defer func() { setWS(nil); out.stop() }() // до wsc.Close (defer выше): после него писать некуда
				h.OnConnected()
				continue
			}
			h.OnEvent(ev)
		case resp, ok := <-wsc.ResponseChannel:
			// Ответы нужны только на auth challenge: ошибку там сервер иначе не сообщит.
			if ok && resp.Error != nil {
				return fmt.Errorf("websocket %s: seq %d: %w", wsURL, resp.SeqReply, resp.Error)
			}
		case <-wsc.PingTimeoutChannel:
			return fmt.Errorf("websocket %s: no ping from server for %ds", wsURL, 60+model.PingTimeoutBufferSeconds)
		}
	}
}

func (s *Session) setWS(ws *wsOut) {
	s.wsMu.Lock()
	s.ws = ws
	s.wsMu.Unlock()
}

// wsOut — писатель одного соединения: действия клиента (user_typing,
// user_update_active_status) уходят в WebSocket из одной горутины по очереди,
// главный поток их только ставит в очередь. SendMessage библиотеки пишет в
// канал, который её читатель закрывает при обрыве, и отправка в этот момент
// паникует — горутина это ловит и с соединением заканчивает: очередь после
// обрыва пропадает вместе с ним (следующее соединение сообщит всё заново).
type wsOut struct {
	queue chan wsAction
	done  chan struct{}
}

type wsAction struct {
	action string
	data   map[string]any
}

const wsOutQueue = 16

func startWSOut(wsc *model.WebSocketClient) *wsOut {
	o := &wsOut{queue: make(chan wsAction, wsOutQueue), done: make(chan struct{})}
	go func() {
		for {
			select {
			case <-o.done:
				return
			case a := <-o.queue:
				if !sendWS(wsc, a) {
					return
				}
			}
		}
	}()
	return o
}

// sendWS — одно действие в соединение; false — канал писателя уже закрыт.
func sendWS(wsc *model.WebSocketClient, a wsAction) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	wsc.SendMessage(a.action, a.data)
	return true
}

func (o *wsOut) stop() { close(o.done) }

// send ставит действие в очередь, не ожидая: очередь полна — соединение
// не пишет (скоро оборвётся), действие пропускаем.
func (s *Session) send(action string, data map[string]any) bool {
	s.wsMu.Lock()
	ws := s.ws
	s.wsMu.Unlock()
	if ws == nil {
		return false
	}
	select {
	case ws.queue <- wsAction{action, data}:
		return true
	default:
		return false
	}
}

// Typing — сообщить серверу, что пользователь набирает текст в канале (в
// треде — parentID): остальные увидят «печатает…». Без соединения — false.
func (s *Session) Typing(channelID, parentID string) bool {
	return s.send("user_typing", map[string]any{"channel_id": channelID, "parent_id": parentID})
}

// SetActive — сообщить серверу, за компьютером ли пользователь (система
// простаивает — нет), как веб-клиент и Mattermost Desktop: действие
// user_update_active_status с manual=false — сервер ставит «Отошёл» или «В
// сети», только если статус не выставлен руками. false — соединения нет.
func (s *Session) SetActive(active bool) bool {
	return s.send("user_update_active_status", map[string]any{"user_is_active": active, "manual": false})
}

// websocketURL: https://host/sub → wss://host/sub (model.WebSocketClient сам добавит /api/v4/websocket).
func websocketURL(serverURL string) (string, error) {
	switch {
	case strings.HasPrefix(serverURL, "https://"):
		return "wss://" + strings.TrimPrefix(serverURL, "https://"), nil
	case strings.HasPrefix(serverURL, "http://"):
		return "ws://" + strings.TrimPrefix(serverURL, "http://"), nil
	}
	return "", errors.New("websocket url: server url must start with http:// or https://, got " + truncate(serverURL, 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
