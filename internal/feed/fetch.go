package feed

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

// Fetched is a fetch that worked.
type Fetched struct {
	Feed     *Feed  // nil if the feed hasn't changed since the last fetch
	ETag     string // for the next fetch, to ask only for a change
	Modified string
}

// Error is a fetch that failed. Note is what the pages show, a few plain
// words; Err is the error in full, for the log.
type Error struct {
	Note string
	Err  error
}

func (e *Error) Error() string {
	if e.Note == e.Err.Error() {
		return e.Note
	}
	return e.Note + ": " + e.Err.Error()
}
func (e *Error) Unwrap() error { return e.Err }

var (
	Timeout   = 30 * time.Second
	MaxSize   = int64(10 << 20)
	userAgent = "thicket (+https://github.com/obk/thicket)"
	client    = &http.Client{}
)

// Fetch gets a feed and reads it. etag and modified are from the last
// fetch, so that a feed that hasn't changed isn't sent again.
func Fetch(ctx context.Context, url, etag, modified string) (*Fetched, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &Error{"not a URL thicket can fetch", err}
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/rdf+xml, application/xml;q=0.9, text/xml;q=0.9, */*;q=0.1")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if modified != "" {
		req.Header.Set("If-Modified-Since", modified)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &Error{reason(err), err}
	}
	defer resp.Body.Close()
	f := &Fetched{ETag: resp.Header.Get("ETag"), Modified: resp.Header.Get("Last-Modified")}
	if resp.StatusCode == http.StatusNotModified {
		f.ETag, f.Modified = etag, modified
		return f, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The standard text, not the server's own, which could say anything.
		status := fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		return nil, &Error{status, errors.New(status)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxSize+1))
	if err != nil {
		return nil, &Error{reason(err), err}
	}
	if int64(len(data)) > MaxSize {
		return nil, &Error{fmt.Sprintf("larger than %d MB", MaxSize>>20), errors.New("too large")}
	}
	// Links are relative to where the feed ended up, after redirects.
	if f.Feed, err = Parse(data, resp.Request.URL.String()); err != nil {
		note := "not a feed thicket can read"
		if errors.Is(err, ErrHTML) || errors.Is(err, ErrNotFeed) {
			note = err.Error()
		}
		return nil, &Error{note, err}
	}
	return f, nil
}

// reason says why a fetch failed without the host names and addresses
// that Go's errors include.
func reason(err error) string {
	var (
		dns      *net.DNSError
		invalid  x509.CertificateInvalidError
		hostname x509.HostnameError
		unknown  x509.UnknownAuthorityError
		record   tls.RecordHeaderError
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return fmt.Sprintf("no answer within %s", Timeout)
	case errors.As(err, &dns):
		if dns.IsNotFound {
			return "the name doesn't exist in DNS"
		}
		return "the DNS lookup failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "the connection was closed"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "the host is unreachable"
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return "the certificate has expired"
		}
		return "the certificate isn't valid"
	case errors.As(err, &hostname):
		return "the certificate is for another name"
	case errors.As(err, &unknown):
		return "the certificate isn't from a trusted authority"
	case errors.As(err, &record):
		return "the server didn't answer with TLS"
	}
	return "no answer"
}
