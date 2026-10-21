// Package feed reads RSS 2.0, RSS 1.0 and Atom feeds.
package feed

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"html"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
)

type Feed struct {
	Title string
	Site  string // the website the feed is for
	Items []Item // in the feed's order, usually newest first
}

type Item struct {
	Key     string // what tells it apart in its feed: its guid or id, else its link
	Title   string // plain text
	Link    string // absolute
	Author  string
	Date    time.Time // zero if the feed gives none
	Content string    // HTML, as the feed gives it: clean it before showing it
}

// MaxItems is the most items read from one feed, so that a feed with its
// whole archive in it doesn't fill the database.
const MaxItems = 200

var (
	ErrHTML    = errors.New("an HTML page, not a feed")
	ErrNotFeed = errors.New("not an RSS or Atom feed")
)

// Parse reads a feed. base is its address, for links relative to it.
func Parse(data []byte, base string) (*Feed, error) {
	head := bytes.ToLower(data[:min(len(data), 512)])
	if bytes.Contains(head, []byte("<!doctype html")) || bytes.Contains(head, []byte("<html")) {
		return nil, ErrHTML
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.CharsetReader = charset.NewReaderLabel
	// Feeds are often not quite XML, with HTML entities such as &nbsp;.
	// Not xml.HTMLAutoClose, though: it takes RSS's <link> for HTML's.
	d.Strict = false
	d.Entity = xml.HTMLEntity

	var root xml.StartElement
	for {
		t, err := d.Token()
		if err != nil {
			return nil, ErrNotFeed
		}
		if se, ok := t.(xml.StartElement); ok {
			root = se
			break
		}
	}
	var (
		f   *Feed
		err error
	)
	switch root.Name.Local {
	case "rss":
		var r rssFeed
		err = d.DecodeElement(&r, &root)
		f = r.feed()
	case "RDF":
		var r rdfFeed
		err = d.DecodeElement(&r, &root)
		f = r.feed()
	case "feed":
		var a atomFeed
		err = d.DecodeElement(&a, &root)
		f = a.feed()
	default:
		return nil, ErrNotFeed
	}
	if err != nil {
		return nil, err
	}
	f.tidy(base)
	return f, nil
}

// tidy makes links absolute, and gives every item a key and a title.
func (f *Feed) tidy(base string) {
	b, _ := url.Parse(base)
	f.Site = resolve(b, f.Site)
	if f.Site == "" && b != nil {
		f.Site = (&url.URL{Scheme: b.Scheme, Host: b.Host, Path: "/"}).String()
	}
	if s, err := url.Parse(f.Site); err == nil {
		b = s
	}
	if len(f.Items) > MaxItems {
		f.Items = f.Items[:MaxItems]
	}
	for i := range f.Items {
		it := &f.Items[i]
		it.Link = resolve(b, it.Link)
		if it.Key == "" {
			it.Key = it.Link
		}
		if it.Key == "" {
			sum := sha256.Sum256([]byte(it.Title + "\x00" + it.Content))
			it.Key = hex.EncodeToString(sum[:16])
		}
		if it.Title == "" {
			it.Title = Excerpt(it.Content, 80)
		}
	}
}

func resolve(base *url.URL, link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	// Only links a reader can follow: no javascript: or data:.
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func first(s ...string) string {
	for _, v := range s {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// dateLayouts are the ways feeds write dates: RFC 822 as RSS asks, with
// and without its optional parts, and RFC 3339 for Atom and Dublin Core.
var dateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 2006 15:04 MST",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 MST",
	"Mon, 2 January 2006 15:04:05 -0700",
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// RSS 2.0. Tags without a namespace match any, so link can also catch an
// atom:link, which has no text: hence the slices.

type rssFeed struct {
	Channel struct {
		Title string    `xml:"title"`
		Links []string  `xml:"link"`
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title string   `xml:"title"`
	Links []string `xml:"link"`
	GUID  struct {
		ID        string `xml:",chardata"`
		Permalink string `xml:"isPermaLink,attr"`
	} `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	DCDate      string `xml:"http://purl.org/dc/elements/1.1/ date"`
	Description string `xml:"description"`
	Encoded     string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Author      string `xml:"author"`
	Creator     string `xml:"http://purl.org/dc/elements/1.1/ creator"`
}

func (r *rssFeed) feed() *Feed {
	f := &Feed{Title: PlainText(r.Channel.Title), Site: first(r.Channel.Links...)}
	for _, it := range r.Channel.Items {
		f.Items = append(f.Items, it.item())
	}
	return f
}

func (it *rssItem) item() Item {
	i := Item{
		Key:     strings.TrimSpace(it.GUID.ID),
		Title:   PlainText(it.Title),
		Link:    first(it.Links...),
		Author:  rssAuthor(first(it.Creator, it.Author)),
		Date:    parseDate(first(it.PubDate, it.DCDate)),
		Content: first(it.Encoded, it.Description),
	}
	// A guid is a permalink unless it says it isn't.
	if i.Link == "" && it.GUID.Permalink != "false" && strings.HasPrefix(i.Key, "http") {
		i.Link = i.Key
	}
	return i
}

// rssAuthor takes the name from RSS's "email (Name)".
func rssAuthor(s string) string {
	if open, end := strings.Index(s, "("), strings.LastIndex(s, ")"); open > 0 && end > open {
		return strings.TrimSpace(s[open+1 : end])
	}
	return s
}

// RSS 1.0, in RDF: the items sit beside the channel, not in it.

type rdfFeed struct {
	Channel struct {
		Title string `xml:"title"`
		Link  string `xml:"link"`
	} `xml:"channel"`
	Items []rssItem `xml:"item"`
}

func (r *rdfFeed) feed() *Feed {
	f := &Feed{Title: PlainText(r.Channel.Title), Site: r.Channel.Link}
	for _, it := range r.Items {
		f.Items = append(f.Items, it.item())
	}
	return f
}

// Atom.

type atomFeed struct {
	Title   atomText     `xml:"title"`
	Links   []atomLink   `xml:"link"`
	Authors []atomPerson `xml:"author"`
	Entries []atomEntry  `xml:"entry"`
}

type atomEntry struct {
	ID        string       `xml:"id"`
	Title     atomText     `xml:"title"`
	Links     []atomLink   `xml:"link"`
	Authors   []atomPerson `xml:"author"`
	Published string       `xml:"published"`
	Updated   string       `xml:"updated"`
	Summary   atomText     `xml:"summary"`
	Content   atomText     `xml:"content"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type atomPerson struct {
	Name string `xml:"name"`
}

// atomText is text, HTML or XHTML, as its type says.
type atomText struct {
	Type  string `xml:"type,attr"`
	Text  string `xml:",chardata"`
	Inner string `xml:",innerxml"`
}

func (t atomText) html() string {
	switch t.Type {
	case "html", "text/html":
		return t.Text
	case "xhtml", "application/xhtml+xml":
		return t.Inner
	}
	return html.EscapeString(strings.TrimSpace(t.Text))
}

func (t atomText) plain() string {
	return PlainText(t.html())
}

// alternate is the link to the page itself.
func alternate(links []atomLink) string {
	for _, l := range links {
		if l.Rel == "" || l.Rel == "alternate" {
			return l.Href
		}
	}
	return ""
}

func (a *atomFeed) feed() *Feed {
	f := &Feed{Title: a.Title.plain(), Site: alternate(a.Links)}
	for _, e := range a.Entries {
		authors := e.Authors
		if len(authors) == 0 {
			authors = a.Authors
		}
		var names []string
		for _, p := range authors {
			if n := strings.TrimSpace(p.Name); n != "" {
				names = append(names, n)
			}
		}
		content := e.Content.html()
		if strings.TrimSpace(content) == "" {
			content = e.Summary.html()
		}
		f.Items = append(f.Items, Item{
			Key:     strings.TrimSpace(e.ID),
			Title:   e.Title.plain(),
			Link:    alternate(e.Links),
			Author:  strings.Join(names, ", "),
			Date:    parseDate(first(e.Published, e.Updated)),
			Content: content,
		})
	}
	return f
}
