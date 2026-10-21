package poll

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/obk/thicket/internal/config"
	"github.com/obk/thicket/internal/feed"
	"github.com/obk/thicket/internal/store"
)

func TestRound(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "thicket.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := &config.Config{Interval: 30 * time.Minute, KeepDays: 90, Feeds: []config.Feed{
		{URL: "https://up.example/feed"},
		{URL: "https://down.example/feed"},
	}}

	var (
		mu      sync.Mutex
		fetched []string
	)
	fetch = func(ctx context.Context, url, etag, modified string) (*feed.Fetched, error) {
		mu.Lock()
		fetched = append(fetched, url)
		mu.Unlock()
		if url == "https://down.example/feed" {
			return nil, &feed.Error{Note: "connection refused", Err: errors.New("dial tcp 10.0.0.1:443: connection refused")}
		}
		return &feed.Fetched{Feed: &feed.Feed{Title: "Up", Items: []feed.Item{{Key: "1", Title: "hello"}}}}, nil
	}
	defer func() { fetch = feed.Fetch }()

	p := New(cfg, db)
	now := time.Now()
	if err := p.Round(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(fetched) != 2 {
		t.Errorf("first round fetched %q", fetched)
	}
	feeds, _ := db.Feeds(p.urls)
	if feeds[0].Title != "Up" || feeds[0].Items != 1 || feeds[0].Note != "" {
		t.Errorf("up: %+v", feeds[0])
	}
	if feeds[1].Note != "connection refused" || feeds[1].Tried.IsZero() || !feeds[1].Fetched.IsZero() {
		t.Errorf("down: %+v", feeds[1])
	}

	// Nothing is due again until the interval has passed.
	fetched = nil
	p.Round(context.Background(), now.Add(10*time.Minute))
	if len(fetched) != 0 {
		t.Errorf("fetched %q before the interval", fetched)
	}
	p.Round(context.Background(), now.Add(31*time.Minute))
	if len(fetched) != 2 {
		t.Errorf("fetched %q after the interval", fetched)
	}
}
