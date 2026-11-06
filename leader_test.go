package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
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
