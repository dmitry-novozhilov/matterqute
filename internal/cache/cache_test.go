package cache

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/mm"
	"matterqute/internal/mmfake"
)

func newCache(t *testing.T) (*Cache, *mm.Session, string) {
	fake := mmfake.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	fake.AddEmoji("blob", color.RGBA{0, 128, 255, 255})
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s, err := mm.Login(context.Background(), srv.URL, "alice", mmfake.Password, "")
	if err != nil {
		t.Fatalf("login to fake: %v", err)
	}
	dir := t.TempDir()
	return New(dir, srv.URL, s.Client), s, dir
}

func TestEmoji(t *testing.T) {
	c, _, dir := newCache(t)
	ctx := context.Background()

	if p := c.EmojiPath("blob"); p != "" {
		t.Errorf("EmojiPath before fetch = %q", p)
	}
	p, err := c.FetchEmoji(ctx, "blob")
	if err != nil || !strings.HasSuffix(p, ".png") || !strings.HasPrefix(p, dir) {
		t.Fatalf("FetchEmoji(blob) = %q, %v; want .png under %s", p, err, dir)
	}
	if got := c.EmojiPath("blob"); got != p {
		t.Errorf("EmojiPath after fetch = %q, want %q", got, p)
	}
	for _, name := range []string{"nope", "nope", "../../etc/passwd", ""} {
		if _, err := c.FetchEmoji(ctx, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("FetchEmoji(%q): err = %v, want ErrNotFound", name, err)
		}
	}
}

func upload(t *testing.T, s *mm.Session, name string, data []byte) *model.FileInfo {
	teams, _, err := s.Client.GetTeamsForUser(context.Background(), s.Me.Id, "")
	if err != nil {
		t.Fatalf("teams: %v", err)
	}
	chs, _, err := s.Client.GetChannelsForTeamForUser(context.Background(), teams[0].Id, s.Me.Id, false, "")
	if err != nil {
		t.Fatalf("channels: %v", err)
	}
	resp, _, err := s.Client.UploadFile(context.Background(), data, chs[0].Id, name)
	if err != nil {
		t.Fatalf("upload %s: %v", name, err)
	}
	return resp.FileInfos[0]
}

func TestFiles(t *testing.T) {
	c, s, dir := newCache(t)
	ctx := context.Background()
	fi := upload(t, s, "../../../evil.sh", []byte("#!/bin/sh\necho hi\n"))

	p, err := c.FetchFile(ctx, fi)
	if err != nil {
		t.Fatalf("FetchFile: %v", err)
	}
	if rel, err := filepath.Rel(dir, p); err != nil || strings.HasPrefix(rel, "..") || filepath.Base(p) != "evil.sh" {
		t.Errorf("file saved to %s, want evil.sh inside %s", p, dir)
	}
	if data, _ := os.ReadFile(p); string(data) != "#!/bin/sh\necho hi\n" {
		t.Errorf("file content %q", data)
	}

	// Пять одновременных запросов превью — одна загрузка, у всех один путь.
	img := upload(t, s, "pic.png", []byte("\x89PNG\r\n\x1a\nnot really"))
	var wg sync.WaitGroup
	paths, errs := make([]string, 5), make([]error, 5)
	for i := range paths {
		wg.Go(func() {
			paths[i], errs[i] = c.FetchPreview(ctx, img.Id)
		})
	}
	wg.Wait()
	for i, got := range paths {
		if errs[i] != nil || got == "" || got != paths[0] || !strings.HasSuffix(got, ".png") {
			t.Errorf("concurrent previews: %v, errors %v", paths, errs)
			break
		}
	}
	if c.PreviewPath(img.Id) != paths[0] {
		t.Errorf("PreviewPath = %q, want %q", c.PreviewPath(img.Id), paths[0])
	}
	if _, err := c.FetchPreview(ctx, "../x"); err == nil {
		t.Error("FetchPreview accepted invalid id")
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"отчёт.pdf":          "отчёт.pdf",
		"../../etc/passwd":   "_.._etc_passwd",
		".bashrc":            "bashrc",
		"a\x00b\nc.txt":      "a_b_c.txt",
		"   ":                "fallback",
		"C:\\Windows\\x.exe": "C__Windows_x.exe",
	} {
		if got := SafeName(in, "fallback"); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("я", 150) + ".tar.gz"
	if got := SafeName(long, "f"); len(got) > 200 || !strings.HasSuffix(got, ".gz") {
		t.Errorf("SafeName(long) = %d bytes %q", len(got), got[len(got)-10:])
	}
}

func TestIcon(t *testing.T) {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 16, 16)))
	icon := buf.Bytes()
	var auth sync.Map
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.URL.Path, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/icon.png":
			w.Write(icon)
		case "/page":
			w.Write([]byte("<html>not an icon</html>"))
		case "/big":
			w.Write(append(icon, make([]byte, maxIconSize)...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ext.Close()
	c, _, _ := newCache(t)
	ctx := context.Background()

	path, err := c.FetchIcon(ctx, ext.URL+"/icon.png")
	if err != nil || path != c.IconPath(ext.URL+"/icon.png") || !strings.HasSuffix(path, ".png") {
		t.Errorf("FetchIcon = %q, %v; IconPath = %q", path, err, c.IconPath(ext.URL+"/icon.png"))
	}
	if a, _ := auth.Load("/icon.png"); a != "" {
		t.Errorf("Mattermost token sent to the integration's host: %q", a)
	}
	for _, u := range []string{ext.URL + "/page", ext.URL + "/big", ext.URL + "/missing", "file:///etc/passwd", "/relative.png"} {
		if p, err := c.FetchIcon(ctx, u); !errors.Is(err, ErrNotFound) || p != "" || c.IconPath(u) != "" {
			t.Errorf("FetchIcon(%s) = %q, %v; want ErrNotFound and nothing on disk", u, p, err)
		}
	}
}
