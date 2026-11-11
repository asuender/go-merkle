package main

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// pattern is one parsed command line pattern. glob is what wildmatch sees once
// the leading "!", the trailing "/" and an anchoring leading "/" are removed.
type pattern struct {
	glob    string
	negate  bool
	dirOnly bool
	// basename is set when the pattern had no "/" apart from a trailing one.
	// As in git, such a pattern matches the last path segment at any depth
	// rather than the whole path from the root.
	basename bool
}

func (p *pattern) matches(name string, isDir bool) bool {
	if p.dirOnly && !isDir {
		return false
	}

	if p.basename {
		return wildmatch(p.glob, path.Base(name))
	}

	return wildmatch(p.glob, name)
}

// Matcher decides which entries are left out of a snapshot, following the
// gitignore pattern format minus the rules that only make sense in a file
// (see docs/patterns.md). The last matching pattern wins, so a later "!"
// pattern re-includes what an earlier one excluded.
type Matcher struct {
	patterns []*pattern
}

// NewMatcher parses patterns in the order given. Patterns that are empty, or
// become empty once their syntax is removed such as "!" or "/", are skipped,
// since git never matches them either.
//
// A malformed pattern, such as one with an unclosed bracket or a trailing
// backslash, is an error. git silently treats it as never matching, but for
// a command line flag that would quietly replicate files the user meant to
// exclude.
func NewMatcher(patterns []string) (*Matcher, error) {
	m := &Matcher{}
	m.patterns = make([]*pattern, 0, len(patterns))

	for _, ps := range patterns {
		if len(ps) == 0 {
			continue
		}

		glob := ps
		negate := false
		dirOnly := false
		basename := false

		if glob[0] == '!' {
			glob = glob[1:]
			negate = true
		}

		if len(glob) > 0 && glob[len(glob)-1] == '/' {
			glob = glob[:len(glob)-1]
			dirOnly = true
		}

		if strings.IndexByte(glob, '/') < 0 {
			basename = true
		} else if len(glob) > 0 && glob[0] == '/' {
			glob = glob[1:]
		}

		if len(glob) == 0 {
			continue
		}

		if err := validateGlob(glob); err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", ps, err)
		}

		m.patterns = append(m.patterns, &pattern{glob: glob, negate: negate, dirOnly: dirOnly, basename: basename})
	}

	return m, nil
}

// Excludes reports whether the entry at childPath is left out. childPath is
// relative to the replicated root, uses "/" and has no leading "./". isDir must
// be false for symlinks, even ones pointing at a directory, since links are
// replicated as links and never followed.
//
// Parent directories aren't consulted, so a path inside an excluded directory
// isn't excluded by itself. That is only correct because callers never descend
// into an excluded directory, which is also why, as in git, nothing inside one
// can be re-included.
//
// A nil Matcher excludes nothing.
func (m *Matcher) Excludes(childPath string, isDir bool) bool {
	if m == nil {
		return false
	}

	for _, p := range slices.Backward(m.patterns) {
		if p.matches(childPath, isDir) {
			return !p.negate
		}
	}

	return false
}

// validateGlob reports the syntax errors that make wildmatch never match glob:
// a trailing backslash, and a bracket expression that is unclosed or names an
// unknown class. It must see the glob after [NewMatcher] removes the pattern
// syntax, since "foo\/" only ends in a lone backslash once the "/" is gone.
func validateGlob(glob string) error {
	px := 0

	for px < len(glob) {
		c := glob[px]

		switch c {
		case '\\':
			if px+1 >= len(glob) {
				return fmt.Errorf("escaping backslash is not allowed at the end of a pattern")
			}

			px += 2

		case '[':
			next, _, ok := matchBracket(glob, px, 0)
			if !ok {
				return fmt.Errorf("malformed bracket at offset %d", px)
			}

			px = next

		default:
			px++
		}
	}

	return nil
}

// wildmatch reports whether text matches the glob pattern, following git's
// implementation with WM_PATHNAME: "*" and "?" never match "/", only "**" does.
// Implementation is borrowed from both git's wildmatch.c and
// https://research.swtch.com/glob.
func wildmatch(pattern, text string) bool {
	px := 0
	nx := 0
	nextPx, nextNx := -1, -1
	dirPx, dirNx := -1, -1

	for px < len(pattern) || nx < len(text) {
		if px < len(pattern) {
			c := pattern[px]
			switch c {
			case '\\':
				// A backslash at the end of a pattern is an invalid pattern that never matches.
				if px >= len(pattern)-1 {
					return false
				}

				px++
				c = pattern[px]

				// A 'backslash' consumes the next character literally, hence we need
				// to fall through to match it.
				fallthrough

			// Match an ordinary character.
			default:
				if nx < len(text) && text[nx] == c {
					px++
					nx++
					continue
				}

			// Match any one character except '/'.
			case '?':
				// A '?' matches anything except a slash, we advance and
				// otherwise don't abort early, as existing bookmarks
				// may recover this situation.
				if nx < len(text) && text[nx] != '/' {
					px++
					nx++
					continue
				}

			// Match one character from a set, which never includes '/'.
			case '[':
				if nx < len(text) {
					next, matched, ok := matchBracket(pattern, px, text[nx])
					// Every way of matching the pattern has to pass this
					// bracket, so no bookmark can rescue a malformed one.
					if !ok {
						return false
					}

					if matched && text[nx] != '/' {
						px = next
						nx++
						continue
					}
				}

			// Match anything except a slash.
			case '*':
				end := px
				for end < len(pattern) && pattern[end] == '*' {
					end++
				}

				atStart := px == 0 || pattern[px-1] == '/'
				atEnd := end == len(pattern) || pattern[end] == '/'

				// Detect a '**'.
				if end-px >= 2 && atStart && atEnd {
					// Above, atEnd may also be true if we encountered a trailing
					// slash, therefore, to detect '/**', we need to check
					// whether we are at the end of the pattern.
					if end == len(pattern) {
						return true
					}

					// We now safely detected '**/'
					dirPx = px
					px = end + 1
					dirNx = nx
					nextNx = -1
					continue
				}

				nextPx = px
				nextNx = nx + 1
				px++
				continue
			}
		}

		// We could not match anything, but there is a bookmark.
		// Maybe restart.
		if 0 < nextNx && nextNx <= len(text) && text[nextNx-1] != '/' {
			px = nextPx
			nx = nextNx
			continue
		} else if dirPx >= 0 {
			// In the '**/' case, we can directly advance to the next
			// directory, as this pattern matches an arbitrary number
			// of nested directories. If there isn't one, then there is no
			// way to recover, and we need to abort.
			sx := strings.IndexByte(text[dirNx:], '/')
			if sx < 0 {
				return false
			}

			px = dirPx
			nx = dirNx + sx + 1
			continue
		}

		return false
	}

	return true
}

// matchBracket reports whether c belongs to the bracket expression opening at
// pattern[start], and returns the index just past its closing "]".
// ok is false if the expression is malformed, which git treats as a pattern
// that never matches rather than as literal text.
func matchBracket(pattern string, start int, c byte) (next int, matched, ok bool) {
	i := start + 1
	negated := i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^')
	if negated {
		i++
	}

	// Only a plain character can be the lower bound of a range. Classes and
	// finished ranges clear hasPrev, which makes a "-" after them a literal.
	var prev byte
	hasPrev := false

	// A "]" in the first position is a member of the set, not the terminator.
	for first := true; ; first = false {
		if i >= len(pattern) {
			return 0, false, false
		}

		p := pattern[i]
		if p == ']' && !first {
			return i + 1, matched != negated, true
		}

		switch {
		case p == '\\':
			i++
			if i >= len(pattern) {
				return 0, false, false
			}
			prev, hasPrev = pattern[i], true
			matched = matched || c == prev

		case p == '-' && hasPrev && i+1 < len(pattern) && pattern[i+1] != ']':
			i++
			if pattern[i] == '\\' {
				i++
				if i >= len(pattern) {
					return 0, false, false
				}
			}
			matched = matched || (prev <= c && c <= pattern[i])
			hasPrev = false

		case p == '[' && i+1 < len(pattern) && pattern[i+1] == ':':
			rel := strings.IndexByte(pattern[i+2:], ']')
			if rel < 0 {
				return 0, false, false
			}

			end := i + 2 + rel
			// Without a closing ":]" this is just a literal "[" in the set.
			if rel == 0 || pattern[end-1] != ':' {
				prev, hasPrev = p, true
				matched = matched || c == p
				break
			}

			member, known := inClass(pattern[i+2:end-1], c)
			if !known {
				return 0, false, false
			}
			matched = matched || member
			hasPrev = false
			i = end

		default:
			prev, hasPrev = p, true
			matched = matched || c == p
		}

		i++
	}
}

// inClass reports whether c belongs to the named POSIX character class, and
// whether that class exists. Classes are ASCII-only because paths are matched
// byte by byte, so a byte inside a UTF-8 sequence must never count as a letter.
func inClass(name string, c byte) (member, known bool) {
	lower := 'a' <= c && c <= 'z'
	upper := 'A' <= c && c <= 'Z'
	digit := '0' <= c && c <= '9'
	graph := '!' <= c && c <= '~'

	switch name {
	case "alnum":
		return lower || upper || digit, true
	case "alpha":
		return lower || upper, true
	case "blank":
		return c == ' ' || c == '\t', true
	case "cntrl":
		return c < ' ' || c == 0x7f, true
	case "digit":
		return digit, true
	case "graph":
		return graph, true
	case "lower":
		return lower, true
	case "print":
		return c == ' ' || graph, true
	case "punct":
		return graph && !lower && !upper && !digit, true
	case "space":
		// git's own ctype table leaves out \v and \f, unlike POSIX.
		return c == ' ' || c == '\t' || c == '\n' || c == '\r', true
	case "upper":
		return upper, true
	case "xdigit":
		return digit || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F'), true
	}

	return false, false
}
