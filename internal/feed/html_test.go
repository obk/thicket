package feed

import (
	"strings"
	"testing"
)

func TestPlainText(t *testing.T) {
	for in, want := range map[string]string{
		"just  words\n here":                              "just words here",
		"<p>one</p><p>two</p>":                            "one two",
		"<em>Go</em>'s <a href=x>own</a>":                 "Go's own",
		"a <script>alert(1)</script>b<style>p{}</style>c": "a b c",
		"fish &amp; chips&nbsp;&#8212; 1 < 2":             "fish & chips — 1 < 2",
	} {
		if got := PlainText(in); got != want {
			t.Errorf("PlainText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExcerpt(t *testing.T) {
	if got := Excerpt("<p>short</p>", 10); got != "short" {
		t.Errorf("short: %q", got)
	}
	if got := Excerpt("one two three four", 12); got != "one two…" {
		t.Errorf("cut at a word: %q", got)
	}
	if got := Excerpt("ééééééééééé", 5); got != "éééé…" {
		t.Errorf("no spaces: %q", got)
	}
}

func TestClean(t *testing.T) {
	in := `<p onclick="x()">Hi <a href="/post">there</a></p>` +
		`<img src="img/a.png" alt="a"><script>alert(1)</script>` +
		`<iframe src="https://evil.example/"></iframe><a href="javascript:alert(1)">no</a>`
	got := Clean(in, "https://example.com/blog/entry")
	for _, want := range []string{
		`<a href="https://example.com/post"`,
		`src="https://example.com/blog/img/a.png"`,
		`noreferrer`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Clean: no %s in %s", want, got)
		}
	}
	for _, bad := range []string{"onclick", "<script", "<iframe", "javascript:"} {
		if strings.Contains(got, bad) {
			t.Errorf("Clean kept %s: %s", bad, got)
		}
	}
}
