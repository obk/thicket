package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/obk/thicket/internal/config"
	"github.com/obk/thicket/internal/feed"
	"github.com/obk/thicket/internal/store"
)

func setup(t *testing.T) (*httptest.Server, []store.Feed) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "thicket.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := &config.Config{Title: "My feeds", Interval: 30 * time.Minute, KeepDays: 90, Location: time.UTC,
		Feeds: []config.Feed{{URL: "https://a.example/feed", Name: "Ay"}, {URL: "https://b.example/feed"}}}
	feeds, err := db.Feeds([]string{cfg.Feeds[0].URL, cfg.Feeds[1].URL})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	db.Save(feeds[0].ID, now, &feed.Fetched{Feed: &feed.Feed{Title: "A's own title", Items: []feed.Item{
		{Key: "1", Title: "Older", Link: "https://a.example/older", Date: now.AddDate(0, 0, -3), Content: `<p>An <a href="/x">older</a> one.</p><script>alert(1)</script>`},
		{Key: "2", Title: "Link only", Link: "https://a.example/link", Date: now.Add(-time.Minute)},
		{Key: "3", Title: "Picture", Link: "https://a.example/picture", Date: now.Add(-2 * time.Minute), Content: `<img src="/comic.png" alt="A comic">`},
	}}}, now.AddDate(0, 0, -90))
	db.Failed(feeds[1].ID, now, "404 Not Found")
	srv := httptest.NewServer(New(cfg, db))
	t.Cleanup(srv.Close)
	return srv, feeds
}

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestPages(t *testing.T) {
	srv, feeds := setup(t)

	code, body, h := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("/: %d", code)
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") || h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers: %v", h)
	}
	for _, want := range []string{
		"<h2>Today</h2>",
		`<a href="https://a.example/link">Link only</a>`, // nothing to read here: straight to it
		">Ay</a>", // the config's name over the feed's
		"An older one.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/: no %s in\n%s", want, body)
		}
	}

	_, body, _ = get(t, srv.URL+"/feeds/")
	for _, want := range []string{"Ay", "b.example", "Failing: 404 Not Found", "Fetched under a minute ago"} {
		if !strings.Contains(body, want) {
			t.Errorf("/feeds/: no %s", want)
		}
	}

	code, body, _ = get(t, srv.URL+"/feeds/"+strconv.FormatInt(feeds[1].ID, 10)+"/")
	if code != 200 || !strings.Contains(body, "No items yet.") || !strings.Contains(body, "Failing: 404 Not Found") {
		t.Errorf("b's page: %d\n%s", code, body)
	}

	item := itemPage(t, srv, "Older")
	code, body, _ = get(t, srv.URL+item)
	if code != 200 {
		t.Fatalf("%s: %d", item, code)
	}
	if !strings.Contains(body, `href="https://a.example/x"`) || strings.Contains(body, "<script>alert") {
		t.Errorf("item content not cleaned:\n%s", body)
	}
	if !strings.Contains(body, "Read it on a.example") {
		t.Errorf("no link to the original:\n%s", body)
	}

	// A picture with no words is still read here, not on its site.
	code, body, _ = get(t, srv.URL+itemPage(t, srv, "Picture"))
	if code != 200 || !strings.Contains(body, `src="https://a.example/comic.png"`) {
		t.Errorf("the picture's page: %d\n%s", code, body)
	}

	for _, path := range []string{"/items/999/", "/feeds/999/", "/feeds/x/", "/nothing"} {
		if code, _, _ := get(t, srv.URL+path); code != 404 {
			t.Errorf("%s: %d, want 404", path, code)
		}
	}
}

// itemPage finds an item's page from the list, by its title.
func itemPage(t *testing.T, srv *httptest.Server, title string) string {
	t.Helper()
	_, body, _ := get(t, srv.URL+"/")
	m := regexp.MustCompile(`href="(/items/\d+/)">` + regexp.QuoteMeta(title) + `</a>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no page for %q in\n%s", title, body)
	}
	return m[1]
}

func TestPaging(t *testing.T) {
	srv, _ := setup(t)
	old := PerPage
	PerPage = 1
	defer func() { PerPage = old }()

	_, body, _ := get(t, srv.URL+"/")
	if !strings.Contains(body, `href="?page=2">Older →`) || strings.Contains(body, "← Newer") {
		t.Errorf("first page:\n%s", body)
	}
	_, body, _ = get(t, srv.URL+"/?page=2")
	if !strings.Contains(body, `href="/">← Newer`) || !strings.Contains(body, `href="?page=3">Older →`) {
		t.Errorf("second page:\n%s", body)
	}
	_, body, _ = get(t, srv.URL+"/?page=3")
	if !strings.Contains(body, `href="?page=2">← Newer`) || strings.Contains(body, "Older →") {
		t.Errorf("last page:\n%s", body)
	}
}

func TestDayName(t *testing.T) {
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for when, want := range map[time.Time]string{
		now.Add(-time.Hour):      "Today",
		now.Add(-10 * time.Hour): "Yesterday",
		now.AddDate(0, 0, -2):    "Tuesday 30 December 2025",
		now.AddDate(0, 0, 5):     "Tuesday 6 January",
	} {
		if got := dayName(when, now); got != want {
			t.Errorf("dayName(%v) = %q, want %q", when, got, want)
		}
	}
}
