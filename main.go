// Go-merkle replicates files from a single leader to its followers
// using Merkle trees.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = 8000
)

const usage = `go-merkle - single-leader file replication based on Merkle trees

Usage:
  go-merkle <command> [flags]

Commands:
  lead      Serve the current directory to followers
  follow    Replicate a leader's files into the current (empty) directory

Examples:
  go-merkle lead --port 8000
  go-merkle follow --host 127.0.0.1 --port 8000`

// parseIgnorePatterns extracts multiple comma-separated
// ignore (regex) patterns from s.
func parseIgnorePatterns(s string) ([](*regexp.Regexp), error) {
	// empty strings compile to a regex that matches everything,
	// therefore we need to exit early here
	if len(strings.TrimSpace(s)) == 0 {
		return [](*regexp.Regexp){}, nil
	}

	patterns := [](*regexp.Regexp){}

	for s := range strings.SplitSeq(s, ",") {
		p, err := regexp.Compile(s)
		if err != nil {
			return nil, err
		}

		patterns = append(patterns, p)
	}

	return patterns, nil
}

type leader struct {
	// snapshot is replaced as a whole on every rebuild and never mutated
	// after Store, so handlers can read it without locking.
	// Other approaches like using [sync.RWMutex] are also valid
	snapshot atomic.Pointer[Snapshot]
}

func (l *leader) handleTree(w http.ResponseWriter, r *http.Request) {
	snapshot := l.snapshot.Load()

	etag := fmt.Sprintf(`"%x"`, snapshot.root.Hash)
	// technically, we do not need to send an ETag header on every response,
	// but standard HTTP clients use it (even when responding with 304)
	// to confirm that the stored copy is still current
	w.Header().Set("ETag", etag)

	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	js, err := json.Marshal(snapshot.root)
	if err != nil {
		http.Error(w, "error encoding root file node", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(js)
}

func (l *leader) handleBlob(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("hash")

	var hash Hash
	if err := hash.UnmarshalText([]byte(raw)); err != nil {
		http.Error(w, "error parsing hash value", http.StatusBadRequest)
		return
	}

	snapshot := l.snapshot.Load()

	blob, ok := snapshot.blobs[hash]
	if !ok {
		http.Error(w, fmt.Sprintf("blob for hash '%x' does not exist", hash), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(blob)
}

// RunLeader runs the tool in leader mode in the root directory
// of fsys, ignoring the files specified by ignorePatterns and serving
// the Merkle trees via port.
func RunLeader(ctx context.Context, fsys fs.FS, logger *log.Logger, port int, ignorePatterns []*regexp.Regexp) error {
	// we build the snapshot before starting the server
	// to avoid requests returning nil if the initial snapshot
	// hasn't been stored yet
	initialSnapshot, err := BuildDirectorySnapshot(fsys, ignorePatterns)
	if err != nil {
		return err
	}

	l := &leader{}
	l.snapshot.Store(initialSnapshot)

	logger.Println("stored initial snapshot")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /tree", l.handleTree)
	mux.HandleFunc("GET /blobs/{hash}", l.handleBlob)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	// if any errors occur while inside [http.Serve], the main approaches
	// would have no chance of exiting due to the line `<-ctx.Done()`
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	go func() {
		for {
			snapshot, err := BuildDirectorySnapshot(fsys, ignorePatterns)
			if err != nil {
				// TODO: it could happen that sequential rebuilds fail due to permission reasons etc.
				// however, this is unlikely. In the future, I would consider counting
				// consecutive errors from tree growth and stop the server completely
				// after a specified threshold.
				logger.Printf("rebuild failed: %v", err)
			} else {
				l.snapshot.Store(snapshot)
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second * 10):
			}
		}
	}()

	// Binding the port before actually serving HTTP endpoints
	// helps failing fast and avoids misleading logging
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	logger.Printf("HTTP server listening on %s", ln.Addr())

	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			cancel(err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("HTTP server forcefully shutdown: %w", err)
	}

	if err := context.Cause(ctx); !errors.Is(err, context.Canceled) {
		// currently, context can only be cancelled by the HTTP server,
		// as the other goroutine does not throw fatal errors that stop the execution.
		return fmt.Errorf("HTTP server: %w", err)
	}

	return nil
}

// RunFollower runs the tool in follower mode by connecting to the leader
// specified by host and port.
func RunFollower(ctx context.Context, fsys fs.FS, logger *log.Logger, host string, port int) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("error reading current directory: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("current directory must be empty")
	}

	// TODO: implement follower logic

	return nil
}

func main() {
	leaderCmd := flag.NewFlagSet("lead", flag.ExitOnError)
	leaderPort := leaderCmd.Int("port", DefaultPort, "The port the leader serves its updates on.")
	leaderIgnorePatterns := leaderCmd.String("ignore", "", "The patterns of files that shall be ignored by the leader.")

	followerCmd := flag.NewFlagSet("follow", flag.ExitOnError)
	followerHost := followerCmd.String("host", DefaultHost, "The host the follower receives updates from.")
	followerPort := followerCmd.Int("port", DefaultPort, "The port the follower receives updates from.")

	if len(os.Args) < 2 {
		fmt.Println("expected `lead` or `follow` subcommand.")
		fmt.Println(usage)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// we need to stop() immediately after cancelling ctx as any further interrupts
	// after the first SIGTERM/SIGINT will be swallowed.
	context.AfterFunc(ctx, stop)

	switch os.Args[1] {
	case "lead":
		leaderCmd.Parse(os.Args[2:])
		logger := log.New(os.Stderr, "leader: ", 0)

		ignorePatterns, err := parseIgnorePatterns(*leaderIgnorePatterns)
		if err != nil {
			logger.Fatalf("%v", err)
		}

		if err := RunLeader(ctx, os.DirFS("."), logger, *leaderPort, ignorePatterns); err != nil {
			logger.Fatalf("%v", err)
		}

	case "follow":
		followerCmd.Parse(os.Args[2:])

		logger := log.New(os.Stderr, "follower: ", 0)
		if err := RunFollower(ctx, os.DirFS("."), logger, *followerHost, *followerPort); err != nil {
			logger.Fatalf("%v", err)
		}

	default:
		fmt.Println("expected `lead` or `follow` subcommand.")
		fmt.Println(usage)
		os.Exit(1)
	}
}
