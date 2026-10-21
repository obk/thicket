package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, text string) (*Config, error) {
	t.Helper()
	name := filepath.Join(t.TempDir(), "thicket.toml")
	if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(name)
}

func TestDefaults(t *testing.T) {
	c, err := load(t, `
[[feed]]
url = "https://example.com/feed.xml"
name = "Example"
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "Feeds" || c.Interval != 30*time.Minute || c.KeepDays != 90 || c.Location != time.Local {
		t.Errorf("defaults: %+v", c)
	}
	if filepath.Base(c.Database) != "thicket.db" || filepath.Dir(c.Database) == "." {
		t.Errorf("database beside the config: %q", c.Database)
	}
	if len(c.Feeds) != 1 || c.Feeds[0].Name != "Example" {
		t.Errorf("feeds: %+v", c.Feeds)
	}
}

func TestTimezone(t *testing.T) {
	c, err := load(t, "timezone = \"Europe/London\"\n[[feed]]\nurl = \"https://example.com/\"\n")
	if err != nil || c.Location.String() != "Europe/London" {
		t.Errorf("timezone: %v, %v", c, err)
	}
}

func TestErrors(t *testing.T) {
	feed := "\n[[feed]]\nurl = \"https://example.com/\"\n"
	for text, want := range map[string]string{
		"":                                       "no feeds",
		"colour = \"red\"" + feed:                "unknown setting colour",
		"interval = \"1m\"" + feed:               "at least 5m",
		"keep_days = 0" + feed:                   "at least 1",
		"timezone = \"Mars/Olympus\"" + feed:     "not a known zone",
		"[[feed]]\nurl = \"ftp://example.com/\"": "is not http",
		"[[feed]]\nname = \"no url\"":            "is not http",
		feed + feed:                              "is there twice",
	} {
		_, err := load(t, text)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", text, err, want)
		}
	}
}
