package store

import (
	"maps"
	"path/filepath"
	"testing"
	"time"

	"github.com/obk/thicket/internal/feed"
)

func open(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "thicket.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func titles(t *testing.T, db *DB, feeds ...int64) []string {
	t.Helper()
	items, err := db.Items(feeds, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, it := range items {
		s = append(s, it.Title)
	}
	return s
}

func TestSave(t *testing.T) {
	db := open(t)
	feeds, err := db.Feeds([]string{"https://a.example/feed", "https://b.example/feed"})
	if err != nil || len(feeds) != 2 || feeds[0].ID == 0 || feeds[0].ID == feeds[1].ID {
		t.Fatalf("feeds: %+v, %v", feeds, err)
	}
	a := feeds[0].ID
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	oldest := now.AddDate(0, 0, -90)

	got := &feed.Fetched{ETag: `"1"`, Feed: &feed.Feed{Title: "A", Site: "https://a.example/", Items: []feed.Item{
		{Key: "3", Title: "undated"},
		{Key: "2", Title: "future", Date: now.Add(time.Hour)},
		{Key: "1", Title: "yesterday", Date: now.AddDate(0, 0, -1), Content: "<p>words</p>"},
		{Key: "0", Title: "ancient", Date: now.AddDate(-1, 0, 0)},
	}}}
	if err := db.Save(a, now, got, oldest); err != nil {
		t.Fatal(err)
	}
	// Undated and future items are dated when first seen, in the feed's
	// order; ancient ones aren't kept.
	if s := titles(t, db, a); len(s) != 3 || s[0] != "undated" || s[1] != "future" || s[2] != "yesterday" {
		t.Errorf("items: %q", s)
	}
	feeds, _ = db.Feeds([]string{"https://a.example/feed"})
	if f := feeds[0]; f.Title != "A" || f.ETag != `"1"` || !f.Fetched.Equal(now) || f.Items != 3 {
		t.Errorf("feed after save: %+v", f)
	}

	// A changed item changes, but keeps its date.
	later := now.Add(time.Hour)
	got.Feed.Items = []feed.Item{{Key: "1", Title: "yesterday, fixed", Date: now}}
	if err := db.Save(a, later, got, oldest); err != nil {
		t.Fatal(err)
	}
	items, _ := db.Items([]int64{a}, 100, 0)
	it, err := db.Item(items[2].ID)
	if err != nil || it.Title != "yesterday, fixed" || !it.Date.Equal(now.AddDate(0, 0, -1)) || it.Content != "" {
		t.Errorf("changed item: %+v, %v", it, err)
	}

	if err := db.Failed(a, later, "404 Not Found"); err != nil {
		t.Fatal(err)
	}
	feeds, _ = db.Feeds([]string{"https://a.example/feed"})
	if f := feeds[0]; f.Note != "404 Not Found" || !f.Tried.Equal(later) || !f.Fetched.Equal(later) {
		t.Errorf("feed after failing: %+v", f)
	}
	if _, err := db.Item(9999); err != ErrNotFound {
		t.Errorf("missing item: %v", err)
	}
}

// An item goes once it is both old and out of its feed, and a feed that
// hasn't changed still has what it had.
func TestPrune(t *testing.T) {
	db := open(t)
	feeds, _ := db.Feeds([]string{"https://a.example/feed"})
	a := feeds[0].ID
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	keep := 30 * 24 * time.Hour

	got := &feed.Fetched{Feed: &feed.Feed{Items: []feed.Item{{Key: "1", Title: "one", Date: start}}}}
	db.Save(a, start, got, start.Add(-keep))
	got.Feed.Items = []feed.Item{{Key: "2", Title: "two", Date: start.Add(time.Hour)}}
	db.Save(a, start.Add(time.Hour), got, start.Add(-keep))

	// Weeks later, the feed hasn't changed: "two" is still in it.
	later := start.Add(40 * 24 * time.Hour)
	if err := db.Save(a, later, &feed.Fetched{}, later.Add(-keep)); err != nil {
		t.Fatal(err)
	}
	if err := db.Prune(later.Add(-keep)); err != nil {
		t.Fatal(err)
	}
	if s := titles(t, db, a); len(s) != 1 || s[0] != "two" {
		t.Errorf("after pruning: %q", s)
	}
}

func TestPages(t *testing.T) {
	db := open(t)
	feeds, _ := db.Feeds([]string{"https://a.example/feed", "https://b.example/feed"})
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i, f := range feeds {
		var items []feed.Item
		for n := range 5 {
			items = append(items, feed.Item{Key: string(rune('a' + n)), Title: string(rune('a'+n)) + string(rune('1'+i)),
				Date: now.Add(-time.Duration(n*2+i) * time.Hour)})
		}
		db.Save(f.ID, now, &feed.Fetched{Feed: &feed.Feed{Items: items}}, now.AddDate(0, 0, -90))
	}
	page, err := db.Items([]int64{feeds[0].ID, feeds[1].ID}, 3, 3)
	if err != nil || len(page) != 3 || page[0].Title != "b2" || page[2].Title != "c2" {
		t.Errorf("second page of three: %+v, %v", page, err)
	}
	if s := titles(t, db, feeds[1].ID); len(s) != 5 || s[0] != "a2" {
		t.Errorf("one feed: %q", s)
	}
	if s := titles(t, db); s != nil {
		t.Errorf("no feeds: %q", s)
	}
}

func TestPicturesOnly(t *testing.T) {
	db := open(t)
	feeds, _ := db.Feeds([]string{"https://a.example/feed"})
	now := time.Now()
	db.Save(feeds[0].ID, now, &feed.Fetched{Feed: &feed.Feed{Items: []feed.Item{
		{Key: "1", Title: "words and a picture", Content: `<p>Words <img src="a.png"></p>`},
		{Key: "2", Title: "a picture", Content: `<p><IMG SRC="b.png"></p>`},
		{Key: "3", Title: "a link"},
	}}}, now.AddDate(0, 0, -90))
	items, err := db.Items([]int64{feeds[0].ID}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.Title] = it.PicturesOnly
	}
	want := map[string]bool{"words and a picture": false, "a picture": true, "a link": false}
	if !maps.Equal(got, want) {
		t.Errorf("pictures only: %v, want %v", got, want)
	}
}
