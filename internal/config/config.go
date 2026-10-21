// Package config loads thicket's TOML configuration file.
package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"time"
	_ "time/tzdata" // so timezone works on systems without a zoneinfo, such as Windows

	"github.com/BurntSushi/toml"
)

type Config struct {
	Title    string         `toml:"title"`
	Listen   string         `toml:"listen"`
	Interval time.Duration  `toml:"interval"`  // between fetches of each feed
	KeepDays int            `toml:"keep_days"` // how long items are kept once they leave their feed
	Database string         `toml:"database"`  // SQLite file, relative to the config's folder
	Timezone string         `toml:"timezone"`  // for the times on the pages; the system's if empty
	Location *time.Location `toml:"-"`
	Feeds    []Feed         `toml:"feed"`
}

type Feed struct {
	URL  string `toml:"url"`
	Name string `toml:"name"` // shown instead of the feed's own title
}

// Keep is how long items are kept, as a duration.
func (c *Config) Keep() time.Duration {
	return time.Duration(c.KeepDays) * 24 * time.Hour
}

// Load reads the config and fills in the defaults. Relative paths in it
// become relative to the current folder.
func Load(name string) (*Config, error) {
	c := &Config{
		Title:    "Feeds",
		Listen:   "127.0.0.1:8080",
		Interval: 30 * time.Minute,
		KeepDays: 90,
		Database: "thicket.db",
	}
	md, err := toml.DecodeFile(name, c)
	if err != nil {
		return nil, err
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, fmt.Errorf("%s: unknown setting %s", name, u[0])
	}
	if !filepath.IsAbs(c.Database) {
		c.Database = filepath.Join(filepath.Dir(name), c.Database)
	}

	// Fetching more often than this gains little: feeds rarely change
	// faster, and their owners notice readers that ask too often.
	if c.Interval < 5*time.Minute {
		return nil, fmt.Errorf("%s: interval %s: at least 5m", name, c.Interval)
	}
	if c.KeepDays < 1 {
		return nil, fmt.Errorf("%s: keep_days %d: at least 1", name, c.KeepDays)
	}
	c.Location = time.Local
	if c.Timezone != "" {
		if c.Location, err = time.LoadLocation(c.Timezone); err != nil {
			return nil, fmt.Errorf("%s: timezone %q: not a known zone, such as \"Europe/London\"", name, c.Timezone)
		}
	}

	if len(c.Feeds) == 0 {
		return nil, fmt.Errorf("%s: no feeds: add a [[feed]] with a url", name)
	}
	seen := map[string]bool{}
	for i, f := range c.Feeds {
		u, err := url.Parse(f.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("%s: feed %d: url %q is not http:// or https://", name, i+1, f.URL)
		}
		if seen[f.URL] {
			return nil, fmt.Errorf("%s: feed %q is there twice", name, f.URL)
		}
		seen[f.URL] = true
	}
	return c, nil
}
