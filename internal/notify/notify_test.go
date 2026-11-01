package notify

import (
	"bufio"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakeServer — сервер уведомлений на своей шине: записывает Notify.
type fakeServer struct {
	caps []string

	mu    sync.Mutex
	calls []call
}

type call struct {
	replaces          uint32
	icon, title, body string
	actions           []string
	hints             map[string]dbus.Variant
}

func (f *fakeServer) GetCapabilities() ([]string, *dbus.Error) { return f.caps, nil }

func (f *fakeServer) Notify(app string, replaces uint32, icon, title, body string, actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{replaces, icon, title, body, actions, hints})
	return uint32(len(f.calls)), nil
}

// privateBus поднимает отдельный dbus-daemon и направляет на него сессионную шину теста.
func privateBus(t *testing.T) string {
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
	addr = strings.TrimSpace(addr)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
	return addr
}

func TestNotifier(t *testing.T) {
	addr := privateBus(t)
	srv, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect fake server: %v", err)
	}
	defer srv.Close()
	fake := &fakeServer{caps: []string{"body", "body-markup", "actions"}}
	if err := srv.Export(fake, path, iface); err != nil {
		t.Fatal(err)
	}
	if reply, err := srv.RequestName(dest, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: %v %v", dest, reply, err)
	}

	n, err := New("matterqute", "/tmp/icon.png")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer n.Close()
	opened := make(chan string, 4)
	id, err := n.Show(Notification{Title: "bob в #backend", Body: "a < b && c > d", Image: "/home/u/.cache/аватар.png",
		OnOpen: func(token string) { opened <- "first " + token }})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	fake.mu.Lock()
	c := fake.calls[0]
	fake.mu.Unlock()
	if c.title != "bob в #backend" || c.body != "a &lt; b &amp;&amp; c &gt; d" || c.icon != "/tmp/icon.png" {
		t.Errorf("Notify got title %q body %q icon %q", c.title, c.body, c.icon)
	}
	if strings.Join(c.actions, ",") != "default,Открыть" {
		t.Errorf("actions %v", c.actions)
	}
	if img, _ := c.hints["image-path"].Value().(string); img != "file:///home/u/.cache/%D0%B0%D0%B2%D0%B0%D1%82%D0%B0%D1%80.png" {
		t.Errorf("image-path %q", img)
	}

	// Клик по уведомлению — OnOpen с токеном активации, если сервер его дал;
	// заменённые и закрытые уведомления не открываются.
	srv.Emit(path, iface+".ActivationToken", id, "tok-123")
	srv.Emit(path, iface+".ActionInvoked", id, "default")
	select {
	case got := <-opened:
		if got != "first tok-123" {
			t.Errorf("opened %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ActionInvoked did not call OnOpen")
	}
	id2, _ := n.Show(Notification{Title: "второе", Replaces: id, OnOpen: func(string) { opened <- "second" }})
	srv.Emit(path, iface+".NotificationClosed", id2, uint32(2))
	srv.Emit(path, iface+".ActionInvoked", id, "default")  // заменено
	srv.Emit(path, iface+".ActionInvoked", id2, "default") // закрыто
	select {
	case got := <-opened:
		t.Errorf("replaced or closed notification opened %s", got)
	case <-time.After(300 * time.Millisecond):
	}
	fake.mu.Lock()
	if r := fake.calls[1].replaces; r != id {
		t.Errorf("second Notify replaces %d, want %d", r, id)
	}
	fake.mu.Unlock()
}

func TestNotifierPlainServer(t *testing.T) {
	addr := privateBus(t)
	srv, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	fake := &fakeServer{caps: []string{"body"}}
	srv.Export(fake, path, iface)
	srv.RequestName(dest, dbus.NameFlagDoNotQueue)
	n, err := New("matterqute", "")
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.Show(Notification{Title: "t", Body: "a < b"})
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if c := fake.calls[0]; c.body != "a < b" || len(c.actions) != 0 {
		t.Errorf("server without markup got body %q actions %v", c.body, c.actions)
	}
}

func TestNoServer(t *testing.T) {
	privateBus(t)
	if n, err := New("matterqute", ""); err == nil || !strings.Contains(err.Error(), "GetCapabilities") {
		t.Errorf("New without a notification server = %v, %v", n, err)
	}
}

func TestLauncher(t *testing.T) {
	addr := privateBus(t)
	dock, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer dock.Close()
	if err := dock.AddMatchSignal(dbus.WithMatchInterface("com.canonical.Unity.LauncherEntry")); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 4)
	dock.Signal(signals)

	l, err := NewLauncher("matterqute.desktop")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	next := func() (string, map[string]dbus.Variant) {
		select {
		case s := <-signals:
			uri, _ := s.Body[0].(string)
			props, _ := s.Body[1].(map[string]dbus.Variant)
			return uri, props
		case <-time.After(2 * time.Second):
			t.Fatal("no LauncherEntry.Update")
			return "", nil
		}
	}
	l.SetCount(3)
	uri, props := next()
	if uri != "application://matterqute.desktop" || props["count"].Value() != int64(3) || props["count-visible"].Value() != true {
		t.Errorf("Update %s %v", uri, props)
	}
	l.SetCount(3) // то же число — без сигнала
	l.SetCount(0)
	if _, props := next(); props["count"].Value() != int64(0) || props["count-visible"].Value() != false {
		t.Errorf("Update for 0: %v", props)
	}
}
