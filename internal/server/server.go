// Package server serves thicket's pages: the newest items of every feed, the
// feeds, each feed's items, and each item.
package server

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/obk/thicket/internal/config"
	"github.com/obk/thicket/internal/feed"
	"github.com/obk/thicket/internal/store"
)

//go:embed templates static
var assets embed.FS

var (
	pages = template.Must(template.New("").Funcs(template.FuncMap{
		"ago":  ago,
		"host": host,
	}).ParseFS(assets, "templates/*.html"))
	style = template.CSS(must(assets.ReadFile("static/style.css")))
)

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// PerPage is how many items a list shows before "Older".
var PerPage = 50

type server struct {
	cfg *config.Config
	db  *store.DB
}

// New is the handler for the whole site.
func New(cfg *config.Config, db *store.DB) http.Handler {
	s := &server{cfg: cfg, db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.serveLatest)
	mux.HandleFunc("GET /feeds/{$}", s.serveFeeds)
	mux.HandleFunc("GET /feeds/{id}/{$}", s.serveFeed)
	mux.HandleFunc("GET /items/{id}/{$}", s.serveItem)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Images in items come from their sites; nothing else may load,
		// and no script runs. The sites aren't told where their readers
		// came from.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src https: data:")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		mux.ServeHTTP(w, r)
	})
}

// view is what every page has.
type view struct {
	Site  string // the config's title
	Title string // the page's
	Style template.CSS
	Here  string // which link in the bar is this page: latest or feeds
	Now   time.Time
}

type listView struct {
	view
	Feed  *feedView // for one feed's page
	Days  []dayView
	Newer string // links to the next pages, if any
	Older string
	Empty string // what to say when there are no items
}

type dayView struct {
	Name  string // "Today", "Monday 19 October"
	Items []itemView
}

type itemView struct {
	ID       int64
	Title    string
	Link     string // the original
	Here     bool   // whether it has content to read on thicket
	Feed     string
	FeedID   int64
	Author   string
	Time     string // "14:05"
	Summary  string
	Date     string // in full, on the item's page
	Content  template.HTML
	Original string // the original's site, "go.dev"
}

type feedView struct {
	ID      int64
	Name    string
	URL     string
	Site    string
	Items   int
	Fetched time.Time
	Tried   time.Time
	Note    string
}

// feeds are the config's feeds, by id, and their ids in the config's
// order.
func (s *server) feeds() (map[int64]feedView, []int64, error) {
	urls := make([]string, len(s.cfg.Feeds))
	for i, f := range s.cfg.Feeds {
		urls[i] = f.URL
	}
	rows, err := s.db.Feeds(urls)
	if err != nil {
		return nil, nil, err
	}
	byID := map[int64]feedView{}
	ids := make([]int64, len(rows))
	for i, f := range rows {
		name := s.cfg.Feeds[i].Name
		if name == "" {
			name = f.Title
		}
		if name == "" {
			name = host(f.URL)
		}
		byID[f.ID] = feedView{ID: f.ID, Name: name, URL: f.URL, Site: f.Site, Items: f.Items,
			Fetched: f.Fetched, Tried: f.Tried, Note: f.Note}
		ids[i] = f.ID
	}
	return byID, ids, nil
}

func (s *server) base(title, here string) view {
	if title == "" {
		title = s.cfg.Title
	}
	return view{Site: s.cfg.Title, Title: title, Style: style, Here: here, Now: time.Now()}
}

func (s *server) serveLatest(w http.ResponseWriter, r *http.Request) {
	byID, ids, err := s.feeds()
	if err != nil {
		s.fail(w, err)
		return
	}
	v := listView{view: s.base("", "latest"), Empty: "Nothing yet: thicket fetches the feeds every " + short(s.cfg.Interval) + "."}
	if err := s.list(&v, r, byID, ids); err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "list.html", v)
}

func (s *server) serveFeed(w http.ResponseWriter, r *http.Request) {
	byID, _, err := s.feeds()
	if err != nil {
		s.fail(w, err)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	f, ok := byID[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	v := listView{view: s.base(f.Name, "feeds"), Feed: &f, Empty: "No items yet."}
	if err := s.list(&v, r, byID, []int64{id}); err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "list.html", v)
}

// list fills in a page of items from feeds, grouped by day.
func (s *server) list(v *listView, r *http.Request, byID map[int64]feedView, feeds []int64) error {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	// One more than a page, to know whether there is an older one.
	items, err := s.db.Items(feeds, PerPage+1, (page-1)*PerPage)
	if err != nil {
		return err
	}
	if len(items) > PerPage {
		items = items[:PerPage]
		v.Older = "?page=" + strconv.Itoa(page+1)
	}
	if page == 2 {
		v.Newer = r.URL.Path
	} else if page > 2 {
		v.Newer = "?page=" + strconv.Itoa(page-1)
	}
	now := v.Now.In(s.cfg.Location)
	for _, it := range items {
		t := it.Date.In(s.cfg.Location)
		name := dayName(t, now)
		if len(v.Days) == 0 || v.Days[len(v.Days)-1].Name != name {
			v.Days = append(v.Days, dayView{Name: name})
		}
		d := &v.Days[len(v.Days)-1]
		d.Items = append(d.Items, itemView{
			ID: it.ID, Title: it.Title, Link: it.Link, Here: it.Summary != "" || it.PicturesOnly || it.Link == "",
			Feed: byID[it.FeedID].Name, FeedID: it.FeedID, Author: it.Author,
			Time: t.Format("15:04"), Summary: it.Summary,
		})
	}
	return nil
}

func (s *server) serveItem(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	it, err := s.db.Item(id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	byID, _, err := s.feeds()
	if err != nil {
		s.fail(w, err)
		return
	}
	f, ok := byID[it.FeedID]
	if !ok {
		http.NotFound(w, r) // its feed has left the config
		return
	}
	base := it.Link
	if base == "" {
		base = f.Site
	}
	v := struct {
		view
		Item itemView
	}{
		view: s.base(it.Title, ""),
		Item: itemView{
			ID: it.ID, Title: it.Title, Link: it.Link, Feed: f.Name, FeedID: f.ID, Author: it.Author,
			Date:     it.Date.In(s.cfg.Location).Format("Monday 2 January 2006, 15:04"),
			Content:  template.HTML(feed.Clean(it.Content, base)),
			Original: host(it.Link),
		},
	}
	s.render(w, "item.html", v)
}

func (s *server) serveFeeds(w http.ResponseWriter, r *http.Request) {
	byID, ids, err := s.feeds()
	if err != nil {
		s.fail(w, err)
		return
	}
	v := struct {
		view
		Feeds    []feedView
		Interval string
	}{view: s.base("Feeds", "feeds"), Interval: short(s.cfg.Interval)}
	for _, id := range ids {
		v.Feeds = append(v.Feeds, byID[id])
	}
	s.render(w, "feeds.html", v)
}

func (s *server) render(w http.ResponseWriter, name string, v any) {
	var b bytes.Buffer
	if err := pages.ExecuteTemplate(&b, name, v); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b.Bytes())
}

// fail logs an error and says only that there was one: the log has the
// rest.
func (s *server) fail(w http.ResponseWriter, err error) {
	log.Print(err)
	http.Error(w, "Something went wrong; see thicket's log.", http.StatusInternalServerError)
}

func dayName(t, now time.Time) string {
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "Today"
	case t.Year() == now.AddDate(0, 0, -1).Year() && t.YearDay() == now.AddDate(0, 0, -1).YearDay():
		return "Yesterday"
	case y1 == y2:
		return t.Format("Monday 2 January")
	}
	return t.Format("Monday 2 January 2006")
}

// ago is how long ago t was, roughly: "5 minutes", "3 hours".
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour")
	}
	return plural(int(d.Hours()/24), "day")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// short is a duration as "30m" or "1h", not "30m0s".
func short(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

// host is a link's site, without "www.".
func host(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	h := u.Hostname()
	if len(h) > 4 && h[:4] == "www." {
		h = h[4:]
	}
	return h
}
