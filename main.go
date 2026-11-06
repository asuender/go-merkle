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
	DefaultHost         = "127.0.0.1"
	DefaultPort         = 8000
	DefaultInterval     = 10 * time.Second
	DefaultMaxStaleness = time.Minute
)

const usage = `go-merkle - single-leader file replication based on Merkle trees

Usage:
  go-merkle <command> [flags]

Commands:
  lead      Serve the current directory to followers
  follow    Replicate a leader's files into the current (empty) directory

Examples:
  go-merkle lead --port 8000 --interval 30s
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

// LeaderConfig configures [RunLeader].
type LeaderConfig struct {
	// Port 0 lets the OS pick a free port.
	Port int
	// IgnorePatterns are matched against file and directory names.
	IgnorePatterns []*regexp.Regexp
	// Interval must be positive.
	Interval time.Duration
	// MaxStaleness is how long rebuilds may keep failing before the leader
	// exits. 0 disables this, so the leader keeps serving its last snapshot.
	MaxStaleness time.Duration
}

func (cfg LeaderConfig) validate() error {
	if cfg.Interval <= 0 {
		return fmt.Errorf("expected positive interval, got %v", cfg.Interval)
	}
	if cfg.MaxStaleness < 0 {
		return fmt.Errorf("expected non-negative max staleness, got %v", cfg.MaxStaleness)
	}
	return nil
}

// FollowerConfig configures [RunFollower].
type FollowerConfig struct {
	Host string
	Port int
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
// of fsys and serves its Merkle trees until ctx is done.
func RunLeader(ctx context.Context, fsys fs.FS, logger *log.Logger, cfg LeaderConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	// we build the snapshot before starting the server
	// to avoid requests returning nil if the initial snapshot
	// hasn't been stored yet
	initialSnapshot, err := BuildDirectorySnapshot(fsys, cfg.IgnorePatterns)
	if err != nil {
		return err
	}
	lastSuccessfulRebuild := time.Now()

	l := &leader{}
	l.snapshot.Store(initialSnapshot)

	logger.Println("stored initial snapshot")
	logger.Printf("rebuilding snapshot every %v", cfg.Interval)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /tree", l.handleTree)
	mux.HandleFunc("GET /blobs/{hash}", l.handleBlob)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: mux,
	}

	// if any errors occur while inside [http.Serve], the main approaches
	// would have no chance of exiting due to the line `<-ctx.Done()`
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	go func() {
		for {
			snapshot, err := BuildDirectorySnapshot(fsys, cfg.IgnorePatterns)
			if err != nil {
				if cfg.MaxStaleness != 0 && time.Now().After(lastSuccessfulRebuild.Add(cfg.MaxStaleness)) {
					cancel(fmt.Errorf("no successful rebuild for %v: %w", cfg.MaxStaleness, err))
					return
				}

				logger.Printf("rebuild failed: %v", err)
			} else {
				l.snapshot.Store(snapshot)
				lastSuccessfulRebuild = time.Now()
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(cfg.Interval):
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
			cancel(fmt.Errorf("HTTP server: %w", err))
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("HTTP server forcefully shutdown: %w", err)
	}

	if err := context.Cause(ctx); !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}

// RunFollower runs the tool in follower mode by connecting to the leader
// specified by cfg.
func RunFollower(ctx context.Context, fsys fs.FS, logger *log.Logger, cfg FollowerConfig) error {
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
	var leaderCfg LeaderConfig
	leaderCmd := flag.NewFlagSet("lead", flag.ExitOnError)
	leaderCmd.IntVar(&leaderCfg.Port, "port", DefaultPort, "The port the leader serves its updates on.")
	leaderIgnorePatterns := leaderCmd.String("ignore", "", "The patterns of files that shall be ignored by the leader.")
	leaderCmd.DurationVar(&leaderCfg.Interval, "interval", DefaultInterval, "How often the leader rebuilds its snapshot (e.g. 500ms, 30s, 1m).")
	leaderCmd.DurationVar(&leaderCfg.MaxStaleness, "max-staleness", DefaultMaxStaleness, "How long rebuilds may fail before exiting (e.g. 500ms, 30s, 1m). 0 disables this.")

	var followerCfg FollowerConfig
	followerCmd := flag.NewFlagSet("follow", flag.ExitOnError)
	followerCmd.StringVar(&followerCfg.Host, "host", DefaultHost, "The host the follower receives updates from.")
	followerCmd.IntVar(&followerCfg.Port, "port", DefaultPort, "The port the follower receives updates from.")

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
		leaderCfg.IgnorePatterns = ignorePatterns

		if err := RunLeader(ctx, os.DirFS("."), logger, leaderCfg); err != nil {
			logger.Fatalf("%v", err)
		}

	case "follow":
		followerCmd.Parse(os.Args[2:])

		logger := log.New(os.Stderr, "follower: ", 0)
		if err := RunFollower(ctx, os.DirFS("."), logger, followerCfg); err != nil {
			logger.Fatalf("%v", err)
		}

	default:
		fmt.Println("expected `lead` or `follow` subcommand.")
		fmt.Println(usage)
		os.Exit(1)
	}
}
