package feed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const rss = `<?xml version="1.0"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"
	xmlns:content="http://purl.org/rss/1.0/modules/content/"
	xmlns:dc="http://purl.org/dc/elements/1.1/">
<channel>
	<title>An &amp; example</title>
	<atom:link href="https://example.com/feed.xml" rel="self"/>
	<link>https://example.com/</link>
	<item>
		<title>Second &nbsp;post</title>
		<link>/posts/second/</link>
		<guid isPermaLink="false">post-2</guid>
		<pubDate>Mon, 19 Oct 2026 14:05:00 +0100</pubDate>
		<description>&lt;p&gt;Short&lt;/p&gt;</description>
		<content:encoded><![CDATA[<p>The <em>whole</em> post.</p>]]></content:encoded>
		<dc:creator>obk</dc:creator>
	</item>
	<item>
		<title>First post</title>
		<guid>https://example.com/posts/first/</guid>
		<pubDate>Sun, 4 Oct 2026 09:00 GMT</pubDate>
		<description>Plain words</description>
		<author>obk@example.com (obk)</author>
	</item>
</channel>
</rss>`

func TestRSS(t *testing.T) {
	f, err := Parse([]byte(rss), "https://example.com/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "An & example" || f.Site != "https://example.com/" || len(f.Items) != 2 {
		t.Fatalf("feed: %q %q, %d items", f.Title, f.Site, len(f.Items))
	}
	second, first := f.Items[0], f.Items[1]
	if second.Key != "post-2" || second.Title != "Second post" || second.Link != "https://example.com/posts/second/" {
		t.Errorf("second: %+v", second)
	}
	if !second.Date.Equal(time.Date(2026, 10, 19, 13, 5, 0, 0, time.UTC)) {
		t.Errorf("second's date: %v", second.Date)
	}
	if second.Content != "<p>The <em>whole</em> post.</p>" || second.Author != "obk" {
		t.Errorf("second's content %q, author %q", second.Content, second.Author)
	}
	// A guid is a permalink unless it says it isn't.
	if first.Link != "https://example.com/posts/first/" || first.Author != "obk" {
		t.Errorf("first: %+v", first)
	}
	if first.Date.IsZero() {
		t.Error("first has no date")
	}
}

const atomXML = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
	<title type="text">Atom &lt;example&gt;</title>
	<link href="https://example.org/atom.xml" rel="self"/>
	<link href="https://example.org/"/>
	<author><name>The spinney authors</name></author>
	<entry>
		<id>tag:example.org,2026:1</id>
		<title type="html">&lt;b&gt;Bold&lt;/b&gt; claims</title>
		<link rel="alternate" href="entries/1"/>
		<updated>2026-10-18T10:00:00Z</updated>
		<summary>A summary</summary>
		<content type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml"><p>Hello <a href="/x">there</a></p></div></content>
	</entry>
	<entry>
		<id>tag:example.org,2026:2</id>
		<title>Just a summary</title>
		<published>2026-10-17T10:00:00+02:00</published>
		<summary type="text">1 &lt; 2</summary>
		<author><name>someone</name></author>
	</entry>
</feed>`

func TestAtom(t *testing.T) {
	f, err := Parse([]byte(atomXML), "https://example.org/atom.xml")
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Atom <example>" || f.Site != "https://example.org/" || len(f.Items) != 2 {
		t.Fatalf("feed: %q %q, %d items", f.Title, f.Site, len(f.Items))
	}
	a, b := f.Items[0], f.Items[1]
	if a.Key != "tag:example.org,2026:1" || a.Title != "Bold claims" || a.Link != "https://example.org/entries/1" {
		t.Errorf("first: %+v", a)
	}
	if !strings.Contains(a.Content, `<p>Hello <a href="/x">there</a></p>`) {
		t.Errorf("xhtml content: %q", a.Content)
	}
	if a.Author != "The spinney authors" || b.Author != "someone" {
		t.Errorf("authors: %q, %q", a.Author, b.Author)
	}
	if b.Content != "1 &lt; 2" {
		t.Errorf("text summary as content: %q", b.Content)
	}
	if !b.Date.Equal(time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("published: %v", b.Date)
	}
}

const rdf = `<?xml version="1.0" encoding="ISO-8859-1"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/"
	xmlns:dc="http://purl.org/dc/elements/1.1/">
	<channel rdf:about="https://example.net/">
		<title>RDF</title>
		<link>https://example.net/</link>
	</channel>
	<item rdf:about="https://example.net/1">
		<title>Caf` + "\xe9" + `</title>
		<link>https://example.net/1</link>
		<dc:date>2026-10-16T12:00:00Z</dc:date>
	</item>
</rdf:RDF>`

func TestRDF(t *testing.T) {
	f, err := Parse([]byte(rdf), "https://example.net/rss")
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "RDF" || len(f.Items) != 1 {
		t.Fatalf("feed: %q, %d items", f.Title, len(f.Items))
	}
	it := f.Items[0]
	if it.Title != "Café" || it.Key != "https://example.net/1" || it.Date.IsZero() {
		t.Errorf("item: %+v", it)
	}
}

func TestNotAFeed(t *testing.T) {
	for data, want := range map[string]error{
		"<!DOCTYPE html><html><head><title>x</title>": ErrHTML,
		`<?xml version="1.0"?><opml></opml>`:          ErrNotFeed,
		"":                                            ErrNotFeed,
	} {
		if _, err := Parse([]byte(data), "https://example.com/"); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", data, err, want)
		}
	}
}

func TestTidy(t *testing.T) {
	data := `<rss><channel><title>t</title>` +
		`<item><description>&lt;p&gt;No title or link, just words that go on.&lt;/p&gt;</description></item>` +
		`<item><title>x</title><link>javascript:alert(1)</link></item>` +
		strings.Repeat(`<item><title>more</title></item>`, MaxItems) +
		`</channel></rss>`
	f, err := Parse([]byte(data), "https://example.com/feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != MaxItems {
		t.Errorf("%d items, want %d", len(f.Items), MaxItems)
	}
	if f.Site != "https://example.com/" {
		t.Errorf("site from the feed's address: %q", f.Site)
	}
	a, b := f.Items[0], f.Items[1]
	if a.Title != "No title or link, just words that go on." || a.Key == "" || a.Link != "" {
		t.Errorf("first: %+v", a)
	}
	if b.Link != "" {
		t.Errorf("javascript: link kept: %q", b.Link)
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			w.Write([]byte(rss))
		case "/moved":
			http.Redirect(w, r, "/feed", http.StatusMovedPermanently)
		case "/page":
			w.Write([]byte("<!doctype html><p>hello"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	f, err := Fetch(ctx, srv.URL+"/moved", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if f.Feed == nil || len(f.Feed.Items) != 2 || f.ETag != `"v1"` {
		t.Fatalf("got %+v", f)
	}
	f, err = Fetch(ctx, srv.URL+"/feed", `"v1"`, "")
	if err != nil || f.Feed != nil || f.ETag != `"v1"` {
		t.Fatalf("not modified: %+v, %v", f, err)
	}

	for path, note := range map[string]string{
		"/gone": "404 Not Found",
		"/page": "an HTML page, not a feed",
	} {
		_, err := Fetch(ctx, srv.URL+path, "", "")
		var fe *Error
		if !errors.As(err, &fe) || fe.Note != note {
			t.Errorf("%s: %v, want %q", path, err, note)
		}
	}
}
