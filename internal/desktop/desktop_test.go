package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallUninstall(t *testing.T) {
	dir := t.TempDir()
	var sizes []int
	written, err := Install(dir, "/home/u/dev/matter qute/matterqute", func(path string, px int) error {
		sizes = append(sizes, px)
		return os.WriteFile(path, []byte("png"), 0o644)
	})
	if err != nil || len(written) != len(IconSizes)+1 || len(sizes) != len(IconSizes) {
		t.Fatalf("Install: %v, %v", written, err)
	}
	entry, err := os.ReadFile(filepath.Join(dir, "applications", ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Name=Matterqute\n", "Exec=\"/home/u/dev/matter qute/matterqute\" %u\n", "Icon=matterqute\n",
		"StartupWMClass=matterqute\n", "MimeType=x-scheme-handler/mattermost;\n"} {
		if !strings.Contains(string(entry), want) {
			t.Errorf("entry lacks %q:\n%s", want, entry)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "icons", "hicolor", "256x256", "apps", "matterqute.png")); err != nil {
		t.Error(err)
	}
	removed, err := Uninstall(dir)
	if err != nil || len(removed) != len(written) {
		t.Errorf("Uninstall: %v, %v", removed, err)
	}
	if removed, err := Uninstall(dir); err != nil || len(removed) != 0 {
		t.Errorf("second Uninstall: %v, %v", removed, err)
	}
}

func TestQuoteExec(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/matterqute": "/usr/bin/matterqute",
		"/a b/c":              `"/a b/c"`,
		`/a $x/"q"`:           `"/a \\$x/\\"q\\""`,
	} {
		if got := quoteExec(in); got != want {
			t.Errorf("quoteExec(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDataDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/x/data")
	if d, _ := DataDir(); d != "/x/data" {
		t.Errorf("DataDir = %s", d)
	}
	t.Setenv("XDG_DATA_HOME", "")
	if d, _ := DataDir(); !strings.HasSuffix(d, "/.local/share") {
		t.Errorf("DataDir = %s", d)
	}
}
