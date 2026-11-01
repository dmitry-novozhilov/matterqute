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
		err := listenOnce(ctx, wsURL, s.Token(), h)
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

func listenOnce(ctx context.Context, wsURL, token string, h RealtimeHandler) error {
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
