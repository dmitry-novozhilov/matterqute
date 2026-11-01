package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("load missing config: %v", err)
	}
	if c.ActiveServer() != nil {
		t.Fatalf("empty config has active server %+v", c.ActiveServer())
	}

	c.Upsert(Server{URL: "https://a.example", LoginID: "alice"})
	c.Upsert(Server{URL: "https://b.example", LoginID: "bob"})
	c.Upsert(Server{URL: "https://a.example", LoginID: "alice@example.com", Username: "alice"})
	c.Window = Window{Geometry: []byte{0x01, 0xd9, 0xd0, 0xcb, 0x00}, Sidebar: 300, Thread: 420}
	if err := c.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	c2, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(c2.Servers) != 2 {
		t.Fatalf("servers = %+v, want 2", c2.Servers)
	}
	if a := c2.ActiveServer(); a == nil || a.URL != "https://a.example" || a.LoginID != "alice@example.com" {
		t.Errorf("active = %+v, want updated https://a.example", a)
	}
	if w := c2.Window; string(w.Geometry) != string(c.Window.Geometry) || w.Sidebar != 300 || w.Thread != 420 {
		t.Errorf("window = %+v, want %+v", w, c.Window)
	}
}

func TestConfigParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte("{not json"), 0o644)
	if _, err := Load(dir); err == nil {
		t.Fatal("want parse error")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q lacks file path", err)
	}
}

func TestFileTokens(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tokens.json")
	tk := &Tokens{Backend: file, file: file}

	if _, err := tk.Get("https://a.example"); !errors.Is(err, ErrNoToken) {
		t.Fatalf("get before set: err = %v, want ErrNoToken", err)
	}
	if err := tk.Set("https://a.example", "t1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, err := tk.Get("https://a.example"); err != nil || got != "t1" {
		t.Errorf("get = %q, %v; want t1", got, err)
	}
	st, err := os.Stat(file)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("tokens file mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
	if err := tk.Delete("https://a.example"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := tk.Get("https://a.example"); !errors.Is(err, ErrNoToken) {
		t.Errorf("get after delete: err = %v, want ErrNoToken", err)
	}
}
