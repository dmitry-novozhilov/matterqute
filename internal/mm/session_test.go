package mm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestNormalizeServerURL(t *testing.T) {
	for in, want := range map[string]string{
		"chat.example.com":               "https://chat.example.com",
		" https://chat.example.com/ ":    "https://chat.example.com",
		"http://localhost:8065":          "http://localhost:8065",
		"https://example.com/mm/?x=1#f":  "https://example.com/mm",
		"https://example.com/team/path/": "https://example.com/team/path",
	} {
		got, err := NormalizeServerURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeServerURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ftp://example.com", "https://"} {
		if got, err := NormalizeServerURL(in); err == nil {
			t.Errorf("NormalizeServerURL(%q) = %q, want error", in, got)
		}
	}
}

// fakeLogin имитирует POST /api/v4/users/login: без token отвечает так же,
// как сервер с включённым MFA.
func fakeLogin(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/users/login" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode login body: %v", err)
		}
		fail := func(status int, id string) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(model.NewAppError("login", id, nil, "", status))
		}
		switch {
		case body["password"] != "secret":
			fail(http.StatusUnauthorized, "api.user.login.invalid_credentials_email_username")
		case body["token"] == "":
			fail(http.StatusBadRequest, mfaRequiredErrorID)
		case body["token"] != "123456":
			fail(http.StatusUnauthorized, "api.user.check_user_mfa.bad_code.app_error")
		default:
			w.Header().Set(model.HeaderToken, "session-token")
			json.NewEncoder(w).Encode(&model.User{Id: "u1", Username: body["login_id"]})
		}
	}))
}

func TestLoginMFA(t *testing.T) {
	srv := fakeLogin(t)
	defer srv.Close()
	ctx := context.Background()

	if _, err := Login(ctx, srv.URL, "alice", "secret", ""); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("login without mfa: err = %v, want ErrMFARequired", err)
	}

	_, err := Login(ctx, srv.URL, "alice", "secret", "000000")
	var se *ServerError
	if !errors.As(err, &se) || se.Status != http.StatusUnauthorized || se.ID != "api.user.check_user_mfa.bad_code.app_error" {
		t.Fatalf("login with bad mfa: err = %v, want ServerError 401 bad_code", err)
	}
	if !IsUnauthorized(err) {
		t.Errorf("IsUnauthorized(%v) = false", err)
	}
	if msg := err.Error(); !strings.Contains(msg, srv.URL) || !strings.Contains(msg, "401") {
		t.Errorf("error message %q lacks server url or status", msg)
	}

	s, err := Login(ctx, srv.URL, "alice", "secret", "123456")
	if err != nil {
		t.Fatalf("login with mfa: %v", err)
	}
	if s.Token() != "session-token" || s.Me.Username != "alice" {
		t.Errorf("session = token %q user %q, want session-token alice", s.Token(), s.Me.Username)
	}
}

func TestLoginBadPassword(t *testing.T) {
	srv := fakeLogin(t)
	defer srv.Close()

	_, err := Login(context.Background(), srv.URL, "alice", "wrong", "")
	if errors.Is(err, ErrMFARequired) || !IsUnauthorized(err) {
		t.Fatalf("err = %v, want plain 401", err)
	}
}

func TestWebsocketURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/mm": "wss://example.com/mm",
		"http://localhost:8065":  "ws://localhost:8065",
	} {
		if got, err := websocketURL(in); err != nil || got != want {
			t.Errorf("websocketURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := websocketURL("example.com"); err == nil || !strings.Contains(err.Error(), "example.com") {
		t.Errorf("websocketURL without scheme: err = %v, want error mentioning the url", err)
	}
}

func TestUploadFileProgress(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/files" || r.Header.Get(model.HeaderAuth) != "BEARER tok" {
			http.Error(w, "bad request "+r.URL.Path, http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("channel_id") != "ch1" {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		f, fh, _ := r.FormFile("files")
		got, _ = io.ReadAll(f)
		json.NewEncoder(w).Encode(&model.FileUploadResponse{FileInfos: []*model.FileInfo{{Id: "f1", Name: fh.Filename}}})
	}))
	defer srv.Close()
	c := model.NewAPIv4Client(srv.URL)
	c.SetToken("tok")
	s := &Session{ServerURL: srv.URL, Client: c}

	data := strings.Repeat("x", 100000)
	var last atomic.Int64 // прогресс приходит из горутины, которая пишет тело запроса
	fi, err := s.UploadFile(context.Background(), "ch1", "файл.txt", strings.NewReader(data), int64(len(data)), func(sent, total int64) { last.Store(sent) })
	if err != nil || fi.Id != "f1" || fi.Name != "файл.txt" {
		t.Fatalf("UploadFile = %+v, %v", fi, err)
	}
	if string(got) != data || last.Load() != int64(len(data)) {
		t.Errorf("server got %d bytes, progress ended at %d; want %d", len(got), last.Load(), len(data))
	}

	s.Client.SetToken("wrong")
	_, err = s.UploadFile(context.Background(), "ch1", "a.txt", strings.NewReader("a"), 1, nil)
	var se *ServerError
	if !errors.As(err, &se) || se.Status != http.StatusBadRequest {
		t.Errorf("upload with bad token: %v", err)
	}
}
