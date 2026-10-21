package feed

import (
	"bytes"
	"net/url"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// PlainText is the text of some HTML, with the tags taken out and the
// spaces collapsed.
func PlainText(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return strings.Join(strings.Fields(s), " ")
	}
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(s))
	skip := 0 // inside a script or style
	for {
		switch tt := z.Next(); tt {
		case html.ErrorToken:
			return strings.Join(strings.Fields(b.String()), " ")
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			a := atom.Lookup(name)
			switch {
			case a == atom.Script || a == atom.Style:
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
			case inline[a]:
				continue
			}
			// Other tags part the words either side, as in <p>one</p><p>two</p>.
			b.WriteByte(' ')
		}
	}
}

// inline are the tags that can sit inside a word: <em>Go</em>'s.
var inline = map[atom.Atom]bool{
	atom.A: true, atom.Abbr: true, atom.B: true, atom.Cite: true, atom.Code: true,
	atom.Em: true, atom.I: true, atom.Kbd: true, atom.Mark: true, atom.Q: true,
	atom.S: true, atom.Small: true, atom.Span: true, atom.Strong: true,
	atom.Sub: true, atom.Sup: true, atom.Time: true, atom.U: true, atom.Var: true,
}

// Excerpt is the start of some HTML's text, cut at a word to at most n
// characters.
func Excerpt(s string, n int) string {
	r := []rune(PlainText(s))
	if len(r) <= n {
		return string(r)
	}
	r = r[:n-1]
	if i := strings.LastIndex(string(r), " "); i > 0 {
		return strings.TrimRight(string(r)[:i], " ,.;:") + "…"
	}
	return string(r) + "…"
}

// policy is what HTML from a feed may keep: text, links, images, lists,
// tables and code, but no scripts, styles, forms or frames.
var policy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.RequireNoReferrerOnLinks(true)
	return p
}()

// Clean makes a feed's HTML safe to show, with its links made absolute
// against base, the item's address.
func Clean(s, base string) string {
	b, err := url.Parse(base)
	if err != nil || base == "" {
		return policy.Sanitize(s)
	}
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return policy.Sanitize(s)
	}
	var out bytes.Buffer
	for _, n := range nodes {
		absolute(n, b)
		html.Render(&out, n)
	}
	return policy.Sanitize(out.String())
}

func absolute(n *html.Node, base *url.URL) {
	if n.Type == html.ElementNode {
		for i, a := range n.Attr {
			if a.Key != "href" && a.Key != "src" {
				continue
			}
			if u, err := url.Parse(strings.TrimSpace(a.Val)); err == nil {
				n.Attr[i].Val = base.ResolveReference(u).String()
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		absolute(c, base)
	}
}
