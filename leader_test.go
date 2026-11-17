package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
)

var errFlaky = errors.New("flaky filesystem failure")

// flakyFS is an empty filesystem whose snapshot builds can be switched to fail.
// Being empty, a build opens nothing but ".", so flipping failing can never
// break a build halfway through.
type flakyFS struct {
	fs.FS
	failing atomic.Bool
	// opened is closed after the first successful open, which signals that
	// the leader's initial build has passed.
	opened chan struct{}
	once   sync.Once
}

func newFlakyFS() *flakyFS {
	return &flakyFS{FS: fstest.MapFS{}, opened: make(chan struct{})}
}

func (f *flakyFS) Open(name string) (fs.File, error) {
	if f.failing.Load() {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errFlaky}
	}

	file, err := f.FS.Open(name)
	f.once.Do(func() { close(f.opened) })
	return file, err
}

// startLeader runs the leader on a random port in the background and returns
// a function stopping it as well as a channel receiving its result.
func startLeader(t *testing.T, fsys fs.FS, interval, maxStaleness time.Duration) (context.CancelFunc, <-chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	// the leader may still log after the test has finished,
	// which rules out t.Output()
	logger := log.New(io.Discard, "", 0)

	errc := make(chan error, 1)
	go func() {
		errc <- RunLeader(ctx, fsys, logger, LeaderConfig{Interval: interval, MaxStaleness: maxStaleness})
	}()

	return cancel, errc
}

func awaitExit(t *testing.T, errc <-chan error) error {
	t.Helper()

	select {
	case err := <-errc:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("leader did not exit in time")
		return nil
	}
}

func assertRunning(t *testing.T, errc <-chan error) {
	t.Helper()

	select {
	case err := <-errc:
		t.Fatalf("leader exited unexpectedly: %v", err)
	default:
	}
}

func TestRunLeaderExitsWhenRebuildsFailPastMaxStaleness(t *testing.T) {
	const interval, maxStaleness = 10 * time.Millisecond, 100 * time.Millisecond

	fsys := newFlakyFS()
	// taken before the initial build, so it bounds the last success from below
	start := time.Now()
	_, errc := startLeader(t, fsys, interval, maxStaleness)
	<-fsys.opened
	fsys.failing.Store(true)

	err := awaitExit(t, errc)
	if !errors.Is(err, errFlaky) {
		t.Fatalf("got error %v, want it to wrap %v", err, errFlaky)
	}
	if elapsed := time.Since(start); elapsed < maxStaleness {
		t.Fatalf("exited after %v, before max staleness of %v", elapsed, maxStaleness)
	}
}

func TestRunLeaderToleratesFailureStreaksShorterThanMaxStaleness(t *testing.T) {
	const interval, maxStaleness = 10 * time.Millisecond, 200 * time.Millisecond

	fsys := newFlakyFS()
	cancel, errc := startLeader(t, fsys, interval, maxStaleness)
	<-fsys.opened

	// failures add up to more than maxStaleness, but each streak alone stays
	// below it, so only a leader resetting on success survives
	for range 2 {
		fsys.failing.Store(true)
		time.Sleep(120 * time.Millisecond)
		fsys.failing.Store(false)
		time.Sleep(50 * time.Millisecond)
	}

	assertRunning(t, errc)
	cancel()
	if err := awaitExit(t, errc); err != nil {
		t.Fatalf("got error %v after cancellation, want nil", err)
	}
}

func TestRunLeaderIgnoresFailuresWhenMaxStalenessIsZero(t *testing.T) {
	const interval = 10 * time.Millisecond

	fsys := newFlakyFS()
	cancel, errc := startLeader(t, fsys, interval, 0)
	<-fsys.opened
	fsys.failing.Store(true)

	time.Sleep(200 * time.Millisecond)

	assertRunning(t, errc)
	cancel()
	if err := awaitExit(t, errc); err != nil {
		t.Fatalf("got error %v after cancellation, want nil", err)
	}
}

func newTestLeader(t *testing.T, fsys fs.FS) *leader {
	t.Helper()
	l := &leader{}
	l.snapshot.Store(mustSnapshot(t, fsys, nil))
	return l
}

func getTree(l *leader, ifNoneMatch string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/tree", nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	rec := httptest.NewRecorder()
	l.handleTree(rec, req)
	return rec
}

func getBlob(l *leader, hash Hash) *httptest.ResponseRecorder {
	return getRawBlob(l, fmt.Sprintf("%x", hash))
}

func getRawBlob(l *leader, raw string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/blobs/"+raw, nil)
	// handlers are called directly rather than through the leader's mux,
	// so the path value has to be set by hand
	req.SetPathValue("hash", raw)
	rec := httptest.NewRecorder()
	l.handleBlob(rec, req)
	return rec
}

// hasDirective reports whether the Cache-Control header of rec contains
// directive, ignoring order, case and surrounding whitespace.
func hasDirective(rec *httptest.ResponseRecorder, directive string) bool {
	for d := range strings.SplitSeq(rec.Header().Get("Cache-Control"), ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(d), "=")
		if strings.EqualFold(name, directive) {
			return true
		}
	}
	return false
}

func TestHandleTreeRevalidatesWithETag(t *testing.T) {
	l := newTestLeader(t, fstest.MapFS{"a.txt": &fstest.MapFile{Data: []byte("one")}})

	first := getTree(l, "")
	if first.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", first.Code, http.StatusOK)
	}
	if !hasDirective(first, "no-cache") {
		t.Fatalf("got Cache-Control %q, want it to contain no-cache", first.Header().Get("Cache-Control"))
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("got no ETag")
	}

	second := getTree(l, etag)
	if second.Code != http.StatusNotModified {
		t.Fatalf("got status %d, want %d", second.Code, http.StatusNotModified)
	}
	if second.Body.Len() != 0 {
		t.Fatalf("got %d body bytes on 304, want none", second.Body.Len())
	}
	if got := second.Header().Get("ETag"); got != etag {
		t.Fatalf("got ETag %q on 304, want %q", got, etag)
	}
}

func TestHandleTreeServesNewTreeForStaleETag(t *testing.T) {
	fsys := fstest.MapFS{"a.txt": &fstest.MapFile{Data: []byte("one")}}
	l := newTestLeader(t, fsys)
	stale := getTree(l, "").Header().Get("ETag")

	fsys["a.txt"].Data = []byte("two")
	l.snapshot.Store(mustSnapshot(t, fsys, nil))

	rec := getTree(l, stale)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d for stale ETag, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("ETag"); got == stale {
		t.Fatalf("got unchanged ETag %q after content change", got)
	}
	var root FileNode
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatalf("decoding tree: %v", err)
	}
	if want := l.snapshot.Load().root.Hash; root.Hash != want {
		t.Fatalf("got root hash %x, want %x", root.Hash, want)
	}
}

func TestHandleBlobMarksOnlyFoundBlobsImmutable(t *testing.T) {
	l := newTestLeader(t, fstest.MapFS{"a.txt": &fstest.MapFile{Data: []byte("one")}})

	found := getBlob(l, child(t, l.snapshot.Load().root, "a.txt").Hash)
	if found.Code != http.StatusOK {
		t.Fatalf("got status %d for existing blob, want %d", found.Code, http.StatusOK)
	}
	if !hasDirective(found, "immutable") || !hasDirective(found, "max-age") {
		t.Fatalf("got Cache-Control %q for existing blob, want immutable with max-age", found.Header().Get("Cache-Control"))
	}

	// a blob missing now may be added by a later rebuild,
	// so caching the 404 for long would hide it
	missing := getBlob(l, sha256.Sum256([]byte("not in snapshot")))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("got status %d for missing blob, want %d", missing.Code, http.StatusNotFound)
	}
	if hasDirective(missing, "immutable") || hasDirective(missing, "max-age") {
		t.Fatalf("got Cache-Control %q for missing blob, want no long-lived caching", missing.Header().Get("Cache-Control"))
	}
}

func TestHandleTreeRoundTripsJSON(t *testing.T) {
	l := newTestLeader(t, fstest.MapFS{
		"a.txt":      &fstest.MapFile{Data: []byte("one")},
		"sub/b.txt":  &fstest.MapFile{Data: []byte("two")},
		"sub/link":   &fstest.MapFile{Data: []byte("b.txt"), Mode: fs.ModeSymlink},
		"empty":      &fstest.MapFile{Mode: fs.ModeDir},
		"sub/deep/c": &fstest.MapFile{Data: []byte("three")},
	})

	rec := getTree(l, "")
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("got Content-Type %q, want application/json", got)
	}

	// the follower rebuilds its view of the leader from this alone,
	// so anything lost in encoding would surface as a bogus diff
	var got FileNode
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding tree: %v", err)
	}
	if diff := cmp.Diff(l.snapshot.Load().root, &got); diff != "" {
		t.Fatalf("decoded tree mismatch (-want +got):\n%s", diff)
	}
}

func TestHandleBlobServesContent(t *testing.T) {
	l := newTestLeader(t, fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("one")},
		"link":  &fstest.MapFile{Data: []byte("a.txt"), Mode: fs.ModeSymlink},
	})
	root := l.snapshot.Load().root

	tests := []struct {
		name string
		want string
	}{
		{name: "a.txt", want: "one"},
		{name: "link", want: "a.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := getBlob(l, child(t, root, tt.name).Hash)
			if rec.Code != http.StatusOK {
				t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
				t.Fatalf("got Content-Type %q, want application/octet-stream", got)
			}
			if got := rec.Body.String(); got != tt.want {
				t.Fatalf("got body %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleBlobRejectsMalformedHash(t *testing.T) {
	l := newTestLeader(t, fstest.MapFS{"a.txt": &fstest.MapFile{Data: []byte("one")}})
	valid := fmt.Sprintf("%x", child(t, l.snapshot.Load().root, "a.txt").Hash)

	tests := []struct {
		name string
		raw  string
	}{
		{name: "not hex", raw: strings.Repeat("z", len(valid))},
		{name: "too short", raw: valid[:len(valid)-2]},
		{name: "too long", raw: valid + "00"},
		{name: "odd length", raw: valid[:len(valid)-1]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := getRawBlob(l, tt.raw); rec.Code != http.StatusBadRequest {
				t.Fatalf("got status %d for %q, want %d", rec.Code, tt.raw, http.StatusBadRequest)
			}
		})
	}
}

// A cache refreshing its stored response from a 304 replaces the stored
// headers with the ones sent, so leaving out no-cache would drop it.
func TestHandleTreeKeepsCacheControlOnNotModified(t *testing.T) {
	l := newTestLeader(t, fstest.MapFS{"a.txt": &fstest.MapFile{Data: []byte("one")}})
	etag := getTree(l, "").Header().Get("ETag")

	rec := getTree(l, etag)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusNotModified)
	}
	if !hasDirective(rec, "no-cache") {
		t.Fatalf("got Cache-Control %q on 304, want it to contain no-cache", rec.Header().Get("Cache-Control"))
	}
}

func TestLeaderConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     LeaderConfig
		wantErr bool
	}{
		{name: "defaults", cfg: LeaderConfig{Interval: DefaultInterval, MaxStaleness: DefaultMaxStaleness}},
		{name: "staleness disabled", cfg: LeaderConfig{Interval: time.Second}},
		{name: "zero interval", cfg: LeaderConfig{}, wantErr: true},
		{name: "negative interval", cfg: LeaderConfig{Interval: -time.Second}, wantErr: true},
		{name: "negative staleness", cfg: LeaderConfig{Interval: time.Second, MaxStaleness: -time.Second}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.validate(); (err != nil) != tt.wantErr {
				t.Fatalf("got error %v, want error: %v", err, tt.wantErr)
			}
		})
	}
}
