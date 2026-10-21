# thicket

A small feed reader. thicket fetches your RSS and Atom feeds every half
hour or so, keeps their items, and serves a few pages: the newest items
of every feed, a day at a time, each feed's own, and each item to read
without leaving.

It is one Go binary with its pages and CSS built in, and an SQLite file
for the items. The pages need no JavaScript, and nothing on them is
tracked or saved: it is a list to read, not an inbox to clear.

    go build
    ./thicket -config example/thicket.toml       # http://127.0.0.1:8080/

## The pages

- **Latest** (`/`): the newest items of every feed, grouped by day, with
  the feed, the author, the time and the start of the text. 50 to a page,
  then "Older".
- **Feeds** (`/feeds/`): each feed, its website, how many items it has
  and when it was last fetched, or, in red, why it couldn't be: "404 Not
  Found", "an HTML page, not a feed", "no answer within 30s".
- **A feed** (`/feeds/1/`): its items alone.
- **An item** (`/items/42/`): the whole post, as its feed gives it, and a
  link to the original. An item with neither text nor pictures, only a
  title and a link, goes straight to the original instead.

An item's HTML is cleaned before it is shown: links, images, lists,
tables and code stay; scripts, styles, forms and frames go, and so would
anything that tried to run. Images load from their own sites, which
aren't told where their readers came from.

## Feeds

    title = "My feeds"
    timezone = "Europe/London"

    [[feed]]
    url = "https://go.dev/blog/feed.atom"

    [[feed]]
    url = "https://github.com/obk/canopy/releases.atom"
    name = "canopy releases"

A feed is the address of an RSS 2.0, RSS 1.0 or Atom feed, not of the
website: look for "RSS", "Atom" or "feed" on the site, or a
`<link rel="alternate">` in its HTML. `name` is shown instead of the
feed's own title, if it has one you would rather not read.

Every setting, with its default:

| Setting | Default | |
|---|---|---|
| `title` | `"Feeds"` | the pages' title |
| `listen` | `"127.0.0.1:8080"` | where to serve; `-listen` overrides it |
| `interval` | `"30m"` | between fetches of each feed, at least `5m` |
| `keep_days` | `90` | how long items are kept after they leave their feed |
| `database` | `"thicket.db"` | the items, in SQLite, beside the config |
| `timezone` | the system's | for the times on the pages, such as `"Europe/London"` |

## Fetching

Each feed is fetched `interval` after it was last tried, four at a time,
so a restart doesn't fetch everything again. thicket asks only for a
change (`If-None-Match`, `If-Modified-Since`), and a feed that hasn't
changed sends nothing back. A fetch gives up after 30 seconds, and a feed
larger than 10 MB isn't read.

A new item is dated when its feed says it was published, or when thicket
first saw it if the feed gives no date, or a date still to come. An item
that changes in its feed changes in thicket too, but keeps its date, so a
corrected typo doesn't bring it back to the top. At most 200 items are
read from a feed, and none older than `keep_days`.

An item is deleted once it is both older than `keep_days` and gone from
its feed: a feed that keeps its last ten posts keeps them in thicket too,
however old. A feed taken out of the config disappears from the pages;
its items are deleted as they age.

thicket's log says when a feed starts failing, with the full error, and
when it works again.

## Running it on a server

thicket is for one reader, or a few who read the same feeds: there is
nothing to sign in to. It listens only on 127.0.0.1 by default. To read
it from elsewhere, serve it behind a web server for HTTPS, such as
Caddy, with a password if it shouldn't be public:

    feeds.example.com {
        basic_auth {
            you $2a$14$...      # caddy hash-password
        }
        reverse_proxy 127.0.0.1:8080
    }

and keep it running with systemd, in `/etc/systemd/system/thicket.service`:

    [Unit]
    Description=thicket feed reader
    After=network-online.target
    Wants=network-online.target

    [Service]
    ExecStart=/usr/local/bin/thicket -config /etc/thicket/thicket.toml
    DynamicUser=yes
    StateDirectory=thicket
    Restart=always

    [Install]
    WantedBy=multi-user.target

with `database = "/var/lib/thicket/thicket.db"` in the config: systemd runs
thicket as a user of its own, which can write only there.

To look at the items yourself, with the `sqlite3` program:

    sqlite3 thicket.db "SELECT datetime(date / 1000, 'unixepoch'), title
                     FROM items ORDER BY date DESC LIMIT 20"

## Development

    go test ./...
    go vet ./...
    gofmt -l .

## License

[GNU GPL v3](LICENSE), or any later version.

    thicket, a small feed reader.
    Copyright (C) 2026 The spinney authors

    This program is free software: you can redistribute it and/or modify it
    under the terms of the GNU General Public License as published by the
    Free Software Foundation, either version 3 of the License, or (at your
    option) any later version.

    This program is distributed in the hope that it will be useful, but
    WITHOUT ANY WARRANTY; without even the implied warranty of
    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU General
    Public License for more details.
