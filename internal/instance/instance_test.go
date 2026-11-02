package instance

import (
	"bufio"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// privateBus — свой dbus-daemon, сессионная шина теста — он.
func privateBus(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon not installed")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("dbus-daemon address: %v", err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", strings.TrimSpace(addr))
}

func TestClaim(t *testing.T) {
	privateBus(t)
	first, err := Claim("", "")
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	defer first.Close()

	// Ссылка до SetHandler не теряется.
	if _, err := Claim("mattermost://mm.example.com/dev/pl/abc", "tok-1"); !errors.Is(err, ErrRunning) {
		t.Fatalf("second Claim: %v, want ErrRunning", err)
	}
	got := make(chan [2]string, 2)
	first.SetHandler(func(link, token string) { got <- [2]string{link, token} })
	if _, err := Claim("", "tok-2"); !errors.Is(err, ErrRunning) {
		t.Fatalf("third Claim: %v", err)
	}
	for _, want := range [][2]string{{"mattermost://mm.example.com/dev/pl/abc", "tok-1"}, {"", "tok-2"}} {
		select {
		case g := <-got:
			if g != want {
				t.Errorf("got %v, want %v", g, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %v", want)
		}
	}
}
