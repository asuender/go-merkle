package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"regexp"
	"strings"
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
// ignore (regex) patterns from s
func parseIgnorePatterns(s string) ([](*regexp.Regexp), error) {
	patterns := [](*regexp.Regexp){}

	for s := range strings.SplitSeq(s, ",") {
		p, err := regexp.Compile(s)
		if err != nil {
			return nil, err
		}

		patterns = append(patterns, p)
	}

	return [](*regexp.Regexp){}, nil
}

// RunLeader runs the tool in leader mode in the root directory
// of fs, ignoring the files specified by ignorePatterns and serving
// the Merkle trees via port.
func RunLeader(fsys fs.FS, logger *log.Logger, port int, ignorePatterns [](*regexp.Regexp)) error {
	_, err := BuildDirectorySnapshot(os.DirFS("."), ignorePatterns)
	if err != nil {
		return err
	}

	// TODO: implement core leader logic

	return nil
}

// RunFollower runs the tool in follower mode by connecting to the leader
// specified by host and port.
func RunFollower(fsys fs.FS, logger *log.Logger, host string, port int) error {
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

	switch os.Args[1] {
	case "lead":
		leaderCmd.Parse(os.Args[2:])
		logger := log.New(os.Stderr, "leader: ", 0)

		ignorePatterns, err := parseIgnorePatterns(*leaderIgnorePatterns)
		if err != nil {
			logger.Fatalf("%v", err)
		}

		if err := RunLeader(os.DirFS("."), logger, *leaderPort, ignorePatterns); err != nil {
			logger.Fatalf("%v", err)
		}

	case "follow":
		followerCmd.Parse(os.Args[2:])

		logger := log.New(os.Stderr, "follower: ", 0)
		if err := RunFollower(os.DirFS("."), logger, *followerHost, *followerPort); err != nil {
			logger.Fatalf("%v", err)
		}

	default:
		fmt.Println("expected `lead` or `follow` subcommand.")
		fmt.Println(usage)
		os.Exit(1)
	}
}
