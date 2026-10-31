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
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	log.Info("listening", "url", "http://"+*addr)
	if err := http.ListenAndServe(*addr, mmfake.New(log)); err != nil {
		log.Error("serve", "addr", *addr, "err", err)
		os.Exit(1)
	}
}
