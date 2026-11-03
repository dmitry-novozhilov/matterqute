package mm

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mattermost/mattermost/server/public/model"
)

// TestSendAfterClose: сервер оборвал соединение — библиотека закрыла канал
// писателя, и отправка в него не должна ронять процесс (send on closed channel).
func TestSendAfterClose(t *testing.T) {
	if raceEnabled {
		// Отправка после закрытия канала писателя — гонка внутри библиотеки
		// (chansend против closechan), детектор её отметит; мы лишь не падаем.
		t.Skip("send after close races inside model.WebSocketClient")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		c.ReadMessage() // auth challenge
		c.Close()
	}))
	defer srv.Close()

	wsc, err := model.NewWebSocketClient4("ws"+strings.TrimPrefix(srv.URL, "http"), "token")
	if err != nil {
		t.Fatal(err)
	}
	wsc.Listen()
	for range wsc.EventChannel { // читатель закрывает каналы, увидев обрыв
	}
	deadline := time.Now().Add(2 * time.Second)
	for sendWS(wsc, wsAction{"user_typing", map[string]any{}}) {
		if time.Now().After(deadline) {
			t.Fatal("sendWS keeps succeeding after the server closed the connection")
		}
		time.Sleep(10 * time.Millisecond)
	}

	s := &Session{}
	if s.Typing("c", "") {
		t.Error("Typing without a connection = true")
	}
	out := startWSOut(wsc)
	s.setWS(out)
	if !s.Typing("c", "") {
		t.Error("Typing with a connection = false")
	}
	out.stop()
	s.setWS(nil)
}
