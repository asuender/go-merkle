package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"regexp"
)

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = 8000
)

var ignorePatternStrings = []string{}

// parseIgnorePatterns extracts multiple comma-separated
// ignore (regex) patterns from s
func parseIgnorePatterns(s string) ([](*regexp.Regexp), error) {
	// TODO: implement parseIngorePatterns logic

	return [](*regexp.Regexp){}, nil
}

func RunLeader(fsys fs.FS, logger *log.Logger, port int, ignorePatterns [](*regexp.Regexp)) error {
	for _, s := range ignorePatternStrings {
		ignorePatterns = append(ignorePatterns, regexp.MustCompile(s))
	}

	_, err := BuildDirectorySnapshot(os.DirFS("."), ignorePatterns)
	if err != nil {
		return err
	}

	// TODO: implement core leader logic

	return nil
}

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
		os.Exit(1)
	}
}
