package main

// wildmatch reports whether text matches the glob pattern, following git's
// wildmatch with WM_PATHNAME: "*" and "?" never match "/", only "**" does.
func wildmatch(pattern, text string) bool {
	// TODO(human): implement glob matching to satisfy TestWildmatch.
	return false
}
