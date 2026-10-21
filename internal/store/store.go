// Package store keeps the feeds and their items in SQLite.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go, so thicket still builds without cgo

	"github.com/obk/thicket/internal/feed"
)

// An item stays while it is in its feed, and for the time thicket keeps items
// after that: seen is when a fetch last listed it. Times are Unix
// milliseconds.
const schema = `
CREATE TABLE IF NOT EXISTS feeds (
	id       INTEGER PRIMARY KEY,
	url      TEXT NOT NULL UNIQUE,
	title    TEXT NOT NULL DEFAULT '', -- the feed's own
	site     TEXT NOT NULL DEFAULT '',
	etag     TEXT NOT NULL DEFAULT '',
	modified TEXT NOT NULL DEFAULT '',
	fetched  INTEGER NOT NULL DEFAULT 0, -- the last fetch that worked
	tried    INTEGER NOT NULL DEFAULT 0, -- the last fetch
	note     TEXT NOT NULL DEFAULT ''    -- why it failed, as the pages show it
);
CREATE TABLE IF NOT EXISTS items (
	id      INTEGER PRIMARY KEY,
	feed_id INTEGER NOT NULL REFERENCES feeds (id),
	key     TEXT NOT NULL,
	title   TEXT NOT NULL,
	link    TEXT NOT NULL,
	author  TEXT NOT NULL,
	date    INTEGER NOT NULL, -- published, or when first seen if that is earlier
	seen    INTEGER NOT NULL,
	summary TEXT NOT NULL,    -- the start of its text, for lists
	content TEXT NOT NULL,    -- HTML as the feed gives it, cleaned when shown
	UNIQUE (feed_id, key)
);
CREATE INDEX IF NOT EXISTS items_by_date ON items (date);
CREATE INDEX IF NOT EXISTS items_by_feed ON items (feed_id, date);
PRAGMA user_version = 1;
`

// schemaVersion is the user_version of the schema above; a database with a
// later one was made by a newer thicket.
const schemaVersion = 1

// SummaryLength is how many characters of an item's text the lists show.
const SummaryLength = 280

type DB struct{ db *sql.DB }

type Feed struct {
	ID       int64
	URL      string
	Title    string // the feed's own
	Site     string
	ETag     string
	Modified string
	Fetched  time.Time // the last fetch that worked, zero if none has
	Tried    time.Time
	Note     string // why the last fetch failed; "" if it didn't
	Items    int
}

type Item struct {
	ID      int64
	FeedID  int64
	Title   string
	Link    string
	Author  string
	Date    time.Time
	Summary string
	// PicturesOnly is an item with no text but an image, such as a comic:
	// still something to read on its own page.
	PicturesOnly bool
	Content      string // only from Item, not lists
}

var ErrNotFound = errors.New("no such item")

func Open(name string) (*DB, error) {
	db, err := sql.Open("sqlite", name+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// One connection: thicket is the only writer, and SQLite takes one at a
	// time anyway.
	db.SetMaxOpenConns(1)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if version > schemaVersion {
		db.Close()
		return nil, fmt.Errorf("%s: made by a newer thicket (schema %d; this one knows %d)", name, version, schemaVersion)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &DB{db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

// Feeds returns a row for each of urls, in their order, adding those that
// are new.
func (d *DB) Feeds(urls []string) ([]Feed, error) {
	for _, u := range urls {
		if _, err := d.db.Exec(`INSERT OR IGNORE INTO feeds (url) VALUES (?)`, u); err != nil {
			return nil, err
		}
	}
	rows, err := d.db.Query(`SELECT f.id, f.url, f.title, f.site, f.etag, f.modified, f.fetched, f.tried, f.note,
		(SELECT count(*) FROM items WHERE feed_id = f.id) FROM feeds f`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byURL := map[string]Feed{}
	for rows.Next() {
		var (
			f              Feed
			fetched, tried int64
		)
		if err := rows.Scan(&f.ID, &f.URL, &f.Title, &f.Site, &f.ETag, &f.Modified, &fetched, &tried, &f.Note, &f.Items); err != nil {
			return nil, err
		}
		f.Fetched, f.Tried = when(fetched), when(tried)
		byURL[f.URL] = f
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	feeds := make([]Feed, len(urls))
	for i, u := range urls {
		feeds[i] = byURL[u]
	}
	return feeds, nil
}

func when(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// Save records a fetch that worked. Items dated before oldest are left
// out, unless they are already kept.
func (d *DB) Save(id int64, now time.Time, f *feed.Fetched, oldest time.Time) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ms := now.UnixMilli()
	if f.Feed == nil {
		// Not changed: what the last fetch listed is still there.
		if _, err := tx.Exec(`UPDATE items SET seen = ? WHERE feed_id = ? AND seen = (SELECT fetched FROM feeds WHERE id = ?)`, ms, id, id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`UPDATE feeds SET title = ?, site = ? WHERE id = ?`, f.Feed.Title, f.Feed.Site, id); err != nil {
			return err
		}
		// Oldest first, so that items with the same date list in the
		// feed's order.
		for i := len(f.Feed.Items) - 1; i >= 0; i-- {
			if err := saveItem(tx, id, &f.Feed.Items[i], now, oldest); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`UPDATE feeds SET etag = ?, modified = ?, fetched = ?, tried = ?, note = '' WHERE id = ?`,
		f.ETag, f.Modified, ms, ms, id); err != nil {
		return err
	}
	return tx.Commit()
}

func saveItem(tx *sql.Tx, id int64, it *feed.Item, now, oldest time.Time) error {
	ms := now.UnixMilli()
	if !it.Date.IsZero() && it.Date.Before(oldest) {
		_, err := tx.Exec(`UPDATE items SET seen = ? WHERE feed_id = ? AND key = ?`, ms, id, it.Key)
		return err
	}
	// A date in the future would keep an item at the top.
	date := it.Date
	if date.IsZero() || date.After(now) {
		date = now
	}
	// An item changed in its feed is changed here too, but keeps its date:
	// a corrected typo isn't news.
	_, err := tx.Exec(`INSERT INTO items (feed_id, key, title, link, author, date, seen, summary, content)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (feed_id, key) DO UPDATE SET title = excluded.title, link = excluded.link,
			author = excluded.author, seen = excluded.seen, summary = excluded.summary, content = excluded.content`,
		id, it.Key, it.Title, it.Link, it.Author, date.UnixMilli(), ms, feed.Excerpt(it.Content, SummaryLength), it.Content)
	return err
}

// Failed records a fetch that didn't work, and why.
func (d *DB) Failed(id int64, now time.Time, note string) error {
	_, err := d.db.Exec(`UPDATE feeds SET tried = ?, note = ? WHERE id = ?`, now.UnixMilli(), note, id)
	return err
}

// Prune deletes the items that are older than oldest and no longer in
// their feed.
func (d *DB) Prune(oldest time.Time) error {
	ms := oldest.UnixMilli()
	_, err := d.db.Exec(`DELETE FROM items WHERE date < ? AND seen < ?`, ms, ms)
	return err
}

// Items lists the items of the feeds given, newest first, skipping the
// first offset.
func (d *DB) Items(feeds []int64, limit, offset int) ([]Item, error) {
	if len(feeds) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(feeds)+2)
	for _, id := range feeds {
		args = append(args, id)
	}
	args = append(args, limit, offset)
	// Only items without text have their content looked into, so that a
	// list of long posts doesn't read them all.
	rows, err := d.db.Query(`SELECT id, feed_id, title, link, author, date, summary,
		CASE WHEN summary = '' THEN content LIKE '%<img%' ELSE 0 END FROM items
		WHERE feed_id IN (?`+strings.Repeat(", ?", len(feeds)-1)+`)
		ORDER BY date DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var (
			it Item
			ms int64
		)
		if err := rows.Scan(&it.ID, &it.FeedID, &it.Title, &it.Link, &it.Author, &ms, &it.Summary, &it.PicturesOnly); err != nil {
			return nil, err
		}
		it.Date = when(ms)
		items = append(items, it)
	}
	return items, rows.Err()
}

// Item is one item, with its content.
func (d *DB) Item(id int64) (*Item, error) {
	var (
		it Item
		ms int64
	)
	err := d.db.QueryRow(`SELECT id, feed_id, title, link, author, date, summary, content FROM items WHERE id = ?`, id).
		Scan(&it.ID, &it.FeedID, &it.Title, &it.Link, &it.Author, &ms, &it.Summary, &it.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	it.Date = when(ms)
	return &it, nil
}
