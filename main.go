// Command thicket is a small feed reader. It fetches RSS and Atom feeds every
// half hour or so, keeps their items in SQLite, and serves pages of the
// newest ones, each feed's, and each item to read. The pages need no
// JavaScript.
//
//	thicket                        # thicket.toml in this folder
//	thicket -config /etc/thicket/thicket.toml -listen 127.0.0.1:9000
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/obk/thicket/internal/config"
	"github.com/obk/thicket/internal/poll"
	"github.com/obk/thicket/internal/server"
	"github.com/obk/thicket/internal/store"
)

func main() {
	configPath := flag.String("config", "thicket.toml", "the config file")
	listen := flag.String("listen", "", "address to serve on (default: listen in the config, or 127.0.0.1:8080)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: thicket [-config thicket.toml] [-listen host:port]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	log.SetFlags(0)
	log.SetPrefix("thicket: ")

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	db, err := store.Open(cfg.Database)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	looped := make(chan struct{})
	go func() {
		poll.New(cfg, db).Loop(ctx)
		close(looped)
	}()

	// Timeouts, so that slow or idle clients can't hold connections open:
	// nothing thicket serves takes long, and no request has a body.
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.New(cfg, db),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	log.Printf("%d feeds every %s; serving on http://%s/", len(cfg.Feeds), cfg.Interval, cfg.Listen)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	// Let fetches in progress save before the database closes.
	<-looped
	if err := db.Close(); err != nil {
		log.Fatal(err)
	}
}
