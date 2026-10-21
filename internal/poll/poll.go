// Package poll fetches each feed every interval and saves what it finds.
package poll

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/obk/thicket/internal/config"
	"github.com/obk/thicket/internal/feed"
	"github.com/obk/thicket/internal/store"
)

// Parallel is how many feeds are fetched at once.
var Parallel = 4

// fetch is feed.Fetch, and something else in tests.
var fetch = feed.Fetch

type Poller struct {
	cfg  *config.Config
	db   *store.DB
	urls []string
}

func New(cfg *config.Config, db *store.DB) *Poller {
	p := &Poller{cfg: cfg, db: db}
	for _, f := range cfg.Feeds {
		p.urls = append(p.urls, f.URL)
	}
	return p
}

// Loop looks every minute for feeds that are due, until ctx is done. A
// feed is due interval after it was last tried, so a restart doesn't
// fetch everything again.
func (p *Poller) Loop(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := p.Round(ctx, time.Now()); err != nil {
			log.Print(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Round fetches the feeds that are due, and deletes the items too old to
// keep.
func (p *Poller) Round(ctx context.Context, now time.Time) error {
	feeds, err := p.db.Feeds(p.urls)
	if err != nil {
		return err
	}
	oldest := now.Add(-p.cfg.Keep())
	sem := make(chan struct{}, Parallel)
	var wg sync.WaitGroup
	for _, f := range feeds {
		if !f.Tried.IsZero() && now.Sub(f.Tried) < p.cfg.Interval {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			p.one(ctx, f, oldest)
		}()
	}
	wg.Wait()
	return p.db.Prune(oldest)
}

func (p *Poller) one(ctx context.Context, f store.Feed, oldest time.Time) {
	got, err := fetch(ctx, f.URL, f.ETag, f.Modified)
	if ctx.Err() != nil {
		return // shutting down: not the feed's fault
	}
	now := time.Now()
	if err != nil {
		note := "no answer"
		var fe *feed.Error
		if errors.As(err, &fe) {
			note = fe.Note
		}
		// Once each time it starts failing or fails differently, not
		// every interval while it stays down.
		if note != f.Note {
			log.Printf("%s: %v", f.URL, err)
		}
		if err := p.db.Failed(f.ID, now, note); err != nil {
			log.Print(err)
		}
		return
	}
	if f.Note != "" {
		log.Printf("%s: working again", f.URL)
	}
	if err := p.db.Save(f.ID, now, got, oldest); err != nil {
		log.Printf("%s: %v", f.URL, err)
	}
}
