// mmfake — фейковый сервер Mattermost для разработки UI без настоящих учёток.
// Учётки: alice/secret, bob/secret (MFA-код 123456).
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"matterqute/internal/mmfake"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8065", "listen address")
	debug := flag.Bool("debug", false, "log every request")
	chatter := flag.Duration("chatter", 0, "post a message from bob to dev/town-square every interval (0 — off)")
	crt := flag.String("crt", "always_on", "ServiceSettings.CollapsedThreads: always_on, default_on, default_off, disabled")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	srv := mmfake.New(log)
	srv.CollapsedThreads = *crt
	if *chatter > 0 {
		go srv.Chatter(*chatter)
	}
	log.Info("listening", "url", "http://"+*addr)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Error("serve", "addr", *addr, "err", err)
		os.Exit(1)
	}
}
