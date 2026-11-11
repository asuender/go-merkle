package main

import (
	"io/fs"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
)

// TestWildmatch ports the table from git's t/t3070-wildmatch.sh at commit
// fb255012acbc. Only the first column is used: test-tool's case-sensitive
// "wildmatch" mode, which matches with WM_PATHNAME like gitignore does.
// Rows marked E (error) in git only apply to ls-files, never to this column.
func TestWildmatch(t *testing.T) {
	tests := []struct {
		text    string
		pattern string
		want    bool
	}{
		// Basic wildmatch features
		{`foo`, `foo`, true},
		{`foo`, `bar`, false},
		{``, ``, true},
		{`foo`, `???`, true},
		{`foo`, `??`, false},
		{`foo`, `*`, true},
		{`foo`, `f*`, true},
		{`foo`, `*f`, false},
		{`foo`, `*foo*`, true},
		{`foobar`, `*ob*a*r*`, true},
		{`aaaaaaabababab`, `*ab`, true},
		{`foo*`, `foo\*`, true},
		{`foobar`, `foo\*bar`, false},
		{`f\oo`, `f\\oo`, true},
		{`foo\`, `foo\`, false},
		{`ball`, `*[al]?`, true},
		{`ten`, `[ten]`, false},
		{`ten`, `**[!te]`, true},
		{`ten`, `**[!ten]`, false},
		{`ten`, `t[a-g]n`, true},
		{`ten`, `t[!a-g]n`, false},
		{`ton`, `t[!a-g]n`, true},
		{`ton`, `t[^a-g]n`, true},
		{`a]b`, `a[]]b`, true},
		{`a-b`, `a[]-]b`, true},
		{`a]b`, `a[]-]b`, true},
		{`aab`, `a[]-]b`, false},
		{`aab`, `a[]a-]b`, true},
		{`]`, `]`, true},

		// Extended slash-matching features
		{`foo/baz/bar`, `foo*bar`, false},
		{`foo/baz/bar`, `foo**bar`, false},
		{`foobazbar`, `foo**bar`, true},
		{`foo/baz/bar`, `foo/**/bar`, true},
		{`foo/baz/bar`, `foo/**/**/bar`, true},
		{`foo/b/a/z/bar`, `foo/**/bar`, true},
		{`foo/b/a/z/bar`, `foo/**/**/bar`, true},
		{`foo/bar`, `foo/**/bar`, true},
		{`foo/bar`, `foo/**/**/bar`, true},
		{`foo/bar`, `foo?bar`, false},
		{`foo/bar`, `foo[/]bar`, false},
		{`foo/bar`, `foo[^a-z]bar`, false},
		{`foo/bar`, `f[^eiu][^eiu][^eiu][^eiu][^eiu]r`, false},
		{`foo-bar`, `f[^eiu][^eiu][^eiu][^eiu][^eiu]r`, true},
		{`foo`, `**/foo`, true},
		{`XXX/foo`, `**/foo`, true},
		{`bar/baz/foo`, `**/foo`, true},
		{`bar/baz/foo`, `*/foo`, false},
		{`foo/bar/baz`, `**/bar*`, false},
		{`deep/foo/bar/baz`, `**/bar/*`, true},
		{`deep/foo/bar/baz/`, `**/bar/*`, false},
		{`deep/foo/bar/baz/`, `**/bar/**`, true},
		{`deep/foo/bar`, `**/bar/*`, false},
		{`deep/foo/bar/`, `**/bar/**`, true},
		{`foo/bar/baz`, `**/bar**`, false},
		{`foo/bar/baz/x`, `*/bar/**`, true},
		{`deep/foo/bar/baz/x`, `*/bar/**`, false},
		{`deep/foo/bar/baz/x`, `**/bar/*/*`, true},

		// Various additional tests
		{`acrt`, `a[c-c]st`, false},
		{`acrt`, `a[c-c]rt`, true},
		{`]`, `[!]-]`, false},
		{`a`, `[!]-]`, true},
		{``, `\`, false},
		{`\`, `\`, false},
		{`XXX/\`, `*/\`, false},
		{`XXX/\`, `*/\\`, true},
		{`foo`, `foo`, true},
		{`@foo`, `@foo`, true},
		{`foo`, `@foo`, false},
		{`[ab]`, `\[ab]`, true},
		{`[ab]`, `[[]ab]`, true},
		{`[ab]`, `[[:]ab]`, true},
		{`[ab]`, `[[::]ab]`, false},
		{`[ab]`, `[[:digit]ab]`, true},
		{`[ab]`, `[\[:]ab]`, true},
		{`?a?b`, `\??\?b`, true},
		{`abc`, `\a\b\c`, true},
		{`foo`, ``, false},
		{`foo/bar/baz/to`, `**/t[o]`, true},

		// Character class tests
		{`a1B`, `[[:alpha:]][[:digit:]][[:upper:]]`, true},
		{`a`, `[[:digit:][:upper:][:space:]]`, false},
		{`A`, `[[:digit:][:upper:][:space:]]`, true},
		{`1`, `[[:digit:][:upper:][:space:]]`, true},
		{`1`, `[[:digit:][:upper:][:spaci:]]`, false},
		{` `, `[[:digit:][:upper:][:space:]]`, true},
		{`.`, `[[:digit:][:upper:][:space:]]`, false},
		{`.`, `[[:digit:][:punct:][:space:]]`, true},
		{`5`, `[[:xdigit:]]`, true},
		{`f`, `[[:xdigit:]]`, true},
		{`D`, `[[:xdigit:]]`, true},
		{`_`, `[[:alnum:][:alpha:][:blank:][:cntrl:][:digit:][:graph:][:lower:][:print:][:punct:][:space:][:upper:][:xdigit:]]`, true},
		{`.`, `[^[:alnum:][:alpha:][:blank:][:cntrl:][:digit:][:lower:][:space:][:upper:][:xdigit:]]`, true},
		{`5`, `[a-c[:digit:]x-z]`, true},
		{`b`, `[a-c[:digit:]x-z]`, true},
		{`y`, `[a-c[:digit:]x-z]`, true},
		{`q`, `[a-c[:digit:]x-z]`, false},

		// Additional tests, including some malformed wildmatch patterns
		{`]`, `[\\-^]`, true},
		{`[`, `[\\-^]`, false},
		{`-`, `[\-_]`, true},
		{`]`, `[\]]`, true},
		{`\]`, `[\]]`, false},
		{`\`, `[\]]`, false},
		{`ab`, `a[]b`, false},
		{`a[]b`, `a[]b`, false},
		{`ab[`, `ab[`, false},
		{`ab`, `[!`, false},
		{`ab`, `[-`, false},
		{`-`, `[-]`, true},
		{`-`, `[a-`, false},
		{`-`, `[!a-`, false},
		{`-`, `[--A]`, true},
		{`5`, `[--A]`, true},
		{` `, `[ --]`, true},
		{`$`, `[ --]`, true},
		{`-`, `[ --]`, true},
		{`0`, `[ --]`, false},
		{`-`, `[---]`, true},
		{`-`, `[------]`, true},
		{`j`, `[a-e-n]`, false},
		{`-`, `[a-e-n]`, true},
		{`a`, `[!------]`, true},
		{`[`, `[]-a]`, false},
		{`^`, `[]-a]`, true},
		{`^`, `[!]-a]`, false},
		{`[`, `[!]-a]`, true},
		{`^`, `[a^bc]`, true},
		{`-b]`, `[a-]b]`, true},
		{`\`, `[\]`, false},
		{`\`, `[\\]`, true},
		{`\`, `[!\\]`, false},
		{`G`, `[A-\\]`, true},
		{`aaabbb`, `b*a`, false},
		{`aabcaa`, `*ba*`, false},
		{`,`, `[,]`, true},
		{`,`, `[\\,]`, true},
		{`\`, `[\\,]`, true},
		{`-`, `[,-.]`, true},
		{`+`, `[,-.]`, false},
		{`-.]`, `[,-.]`, false},
		{`2`, `[\1-\3]`, true},
		{`3`, `[\1-\3]`, true},
		{`4`, `[\1-\3]`, false},
		{`\`, `[[-\]]`, true},
		{`[`, `[[-\]]`, true},
		{`]`, `[[-\]]`, true},
		{`-`, `[[-\]]`, false},

		// Test recursion
		{`-adobe-courier-bold-o-normal--12-120-75-75-m-70-iso8859-1`, `-*-*-*-*-*-*-12-*-*-*-m-*-*-*`, true},
		{`-adobe-courier-bold-o-normal--12-120-75-75-X-70-iso8859-1`, `-*-*-*-*-*-*-12-*-*-*-m-*-*-*`, false},
		{`-adobe-courier-bold-o-normal--12-120-75-75-/-70-iso8859-1`, `-*-*-*-*-*-*-12-*-*-*-m-*-*-*`, false},
		{`XXX/adobe/courier/bold/o/normal//12/120/75/75/m/70/iso8859/1`, `XXX/*/*/*/*/*/*/12/*/*/*/m/*/*/*`, true},
		{`XXX/adobe/courier/bold/o/normal//12/120/75/75/X/70/iso8859/1`, `XXX/*/*/*/*/*/*/12/*/*/*/m/*/*/*`, false},
		{`abcd/abcdefg/abcdefghijk/abcdefghijklmnop.txt`, `**/*a*b*g*n*t`, true},
		{`abcd/abcdefg/abcdefghijk/abcdefghijklmnop.txtz`, `**/*a*b*g*n*t`, false},
		{`foo`, `*/*/*`, false},
		{`foo/bar`, `*/*/*`, false},
		{`foo/bba/arr`, `*/*/*`, true},
		{`foo/bb/aa/rr`, `*/*/*`, false},
		{`foo/bb/aa/rr`, `**/**/**`, true},
		{`abcXdefXghi`, `*X*i`, true},
		{`ab/cXd/efXg/hi`, `*X*i`, false},
		{`ab/cXd/efXg/hi`, `*/*X*/*/*i`, true},
		{`ab/cXd/efXg/hi`, `**/*X*/**/*i`, true},

		// Extra pathmatch tests
		{`foo`, `fo`, false},
		{`foo/bar`, `foo/bar`, true},
		{`foo/bar`, `foo/*`, true},
		{`foo/bba/arr`, `foo/*`, false},
		{`foo/bba/arr`, `foo/**`, true},
		{`foo/bba/arr`, `foo*`, false},
		{`foo/bba/arr`, `foo**`, false},
		{`foo/bba/arr`, `foo/*arr`, false},
		{`foo/bba/arr`, `foo/**arr`, false},
		{`foo/bba/arr`, `foo/*z`, false},
		{`foo/bba/arr`, `foo/**z`, false},
		{`foo/bar`, `foo?bar`, false},
		{`foo/bar`, `foo[/]bar`, false},
		{`foo/bar`, `foo[^a-z]bar`, false},
		{`ab/cXd/efXg/hi`, `*Xg*i`, false},

		// Extra case-sensitivity tests
		{`a`, `[A-Z]`, false},
		{`A`, `[A-Z]`, true},
		{`A`, `[a-z]`, false},
		{`a`, `[a-z]`, true},
		{`a`, `[[:upper:]]`, false},
		{`A`, `[[:upper:]]`, true},
		{`A`, `[[:lower:]]`, false},
		{`a`, `[[:lower:]]`, true},
		{`A`, `[B-Za]`, false},
		{`a`, `[B-Za]`, true},
		{`A`, `[B-a]`, false},
		{`a`, `[B-a]`, true},
		{`z`, `[Z-y]`, false},
		{`Z`, `[Z-y]`, true},
	}

	for _, tt := range tests {
		if got := wildmatch(tt.pattern, tt.text); got != tt.want {
			t.Errorf("wildmatch(%q, %q) = %v, want %v", tt.pattern, tt.text, got, tt.want)
		}
	}
}

// A naive backtracking matcher retries every "*" at every position, which
// blows up on this input. git's test kills the matcher after two seconds.
func TestWildmatchIsNotExponential(t *testing.T) {
	done := make(chan bool, 1)
	go func() {
		done <- wildmatch("*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wildmatch did not finish within 2s")
	}
}

type excludeCase struct {
	path  string
	isDir bool
	want  bool
}

// excludeTests is adapted from git's t/t0008-ignores.sh. git scopes each
// .gitignore to its own directory, while go-merkle has one list applied at the
// root, so the patterns are flattened into one list per group and the expected
// results come from running `git check-ignore --no-index` on that list.
//
// Only paths whose parent directories aren't excluded are listed:
// check-ignore reports those as ignored too, but buildDir never descends far
// enough to ask, so they are covered by the snapshot tests instead.
var excludeTests = []struct {
	name     string
	patterns []string
	cases    []excludeCase
}{
	{
		name:     "standard ignores",
		patterns: []string{"one", "ignored-*", "top-level-dir/"},
		cases: []excludeCase{
			{"one", false, true},
			{"a/one", false, true},
			{"not-ignored", false, false},
			{"ignored-and-untracked", false, true},
			{"a/ignored-and-untracked", false, true},
			{"top-level-dir", true, true},
			{"a/top-level-dir", true, true},
			{"top-level-dir", false, false},
		},
	},
	{
		// The --stdin list, plus the patterns behind "nested include of
		// negated pattern". Flattening makes "!on*" apply everywhere, so
		// "one" is no longer excluded anywhere.
		name:     "negation and last match wins",
		patterns: []string{"one", "two*", "*three", "!on*", "!two"},
		cases: []excludeCase{
			{"one", false, false},
			{"a/b/one", false, false},
			{"a/b/on", false, false},
			{"a/b/one one", false, false},
			{"a/b/two", false, false},
			{"a/b/twooo", false, true},
			{"a/3-three", false, true},
			{"a/three-not-this-one", false, false},
			{"a/b/not-ignored", false, false},
		},
	},
	{
		name:     "ignored sub-directory",
		patterns: []string{"ignored-dir/"},
		cases: []excludeCase{
			{"a/b/ignored-dir", true, true},
			{"a/b/ignored-dir", false, false},
		},
	},
	{
		name:     "exact prefix matching with root",
		patterns: []string{"/git/"},
		cases: []excludeCase{
			{"git", true, true},
			{"git-foo", true, false},
			{"a/git", true, false},
		},
	},
	{
		name:     "exact prefix matching without root",
		patterns: []string{"git/"},
		cases: []excludeCase{
			{"git", true, true},
			{"a/git", true, true},
			{"git-foo", true, false},
			{"a/git-foo", true, false},
		},
	},
	{
		name:     "directories and ** matches",
		patterns: []string{"data/**", "!data/**/", "!data/**/*.txt"},
		cases: []excludeCase{
			{"file", false, false},
			{"data", true, false},
			{"data/file", false, true},
			{"data/data1", true, false},
			{"data/data1/file1", false, true},
			{"data/data1/file1.txt", false, false},
			{"data/data2/file2", false, true},
			{"data/data2/file2.txt", false, false},
		},
	},
	{
		name:     "** not confused by matching leading prefix",
		patterns: []string{"foo**/bar"},
		cases: []excludeCase{
			{"foobar", false, false},
			{"foo/bar", false, true},
		},
	},

	// The remaining groups cover rules from docs/patterns.md that t0008
	// doesn't test directly.
	{
		name:     "leading slash anchors to the root",
		patterns: []string{"/build"},
		cases: []excludeCase{
			{"build", false, true},
			{"build", true, true},
			{"a/build", false, false},
		},
	},
	{
		name:     "middle slash anchors to the root",
		patterns: []string{"a/b"},
		cases: []excludeCase{
			{"a/b", false, true},
			{"x/a/b", false, false},
		},
	},
	{
		name:     "trailing slash matches directories only",
		patterns: []string{"cache/"},
		cases: []excludeCase{
			{"cache", true, true},
			{"x/cache", true, true},
			{"cache", false, false},
		},
	},
	{
		// The example from the gitignore docs, word for word.
		name:     "doc/frotz/ example",
		patterns: []string{"doc/frotz/"},
		cases: []excludeCase{
			{"doc/frotz", true, true},
			{"a/doc/frotz", true, false},
			{"doc/frotz", false, false},
		},
	},
	{
		name:     "frotz/ example",
		patterns: []string{"frotz/"},
		cases: []excludeCase{
			{"frotz", true, true},
			{"a/frotz", true, true},
			{"a/frotz", false, false},
		},
	},
	{
		// buildDir passes symlinks as non-directories, as git does, so a
		// directory-only pattern never matches a link, whatever it points at.
		name:     "trailing slash ignores symlinks",
		patterns: []string{"link/"},
		cases: []excludeCase{
			{"link", false, false},
		},
	},
	{
		name:     "escaped leading bang is literal",
		patterns: []string{`\!x`},
		cases: []excludeCase{
			{"!x", false, true},
			{"x", false, false},
		},
	},
	{
		name:     "patterns empty after removing syntax never match",
		patterns: []string{"/", "!", "!/"},
		cases: []excludeCase{
			{"a", false, false},
			{"a", true, false},
		},
	},
	{
		name:     "negation after exclusion",
		patterns: []string{"*.log", "!keep.log"},
		cases: []excludeCase{
			{"x.log", false, true},
			{"keep.log", false, false},
			{"a/keep.log", false, false},
		},
	},
}

func TestMatcherExcludes(t *testing.T) {
	for _, group := range excludeTests {
		t.Run(group.name, func(t *testing.T) {
			m := mustMatcher(t, group.patterns...)
			for _, c := range group.cases {
				if got := m.Excludes(c.path, c.isDir); got != c.want {
					t.Errorf("Excludes(%q, isDir=%v) = %v, want %v", c.path, c.isDir, got, c.want)
				}
			}
		})
	}
}

func TestNilMatcherExcludesNothing(t *testing.T) {
	var m *Matcher
	if m.Excludes("a", false) {
		t.Error("nil Matcher excluded a path")
	}
}

// A malformed pattern would otherwise never match, so a typo on the command
// line would silently replicate files the user meant to exclude.
func TestNewMatcherRejectsMalformedPatterns(t *testing.T) {
	// "foo\/" only becomes malformed once the trailing slash is removed, so
	// validation has to look at the glob wildmatch sees, not the raw argument.
	//
	// The last four hide the problem right after an escape or a bracket, which
	// a scan that steps past a whole token and then one more byte never sees.
	for _, p := range []string{`foo\`, `foo\/`, "[a", "a[]b", "[[:nope:]]", `[a\`, `\a\`, `[a]\`, "[a][b", `\[[a`} {
		if _, err := NewMatcher([]string{p}); err == nil {
			t.Errorf("NewMatcher(%q) succeeded, want an error", p)
		}
	}
}

// Each of these contains "\" or "[" that only looks malformed if validation
// reads escaped characters or bracket contents as syntax.
func TestNewMatcherAcceptsEscapedAndBracketSyntax(t *testing.T) {
	for _, p := range []string{`\[a`, `foo\\`, `\!x`, "[]a]", `[\]]`, "[[:]ab]", "[[:alpha:]]", "a[!/]b"} {
		if _, err := NewMatcher([]string{p}); err != nil {
			t.Errorf("NewMatcher(%q) = %v, want no error", p, err)
		}
	}
}

func TestBuildDirectorySnapshotSkipsIgnoredSubDirectory(t *testing.T) {
	fsys := fstest.MapFS{
		"a/b/ignored-dir/foo":    &fstest.MapFile{Data: []byte("foo")},
		"a/b/ignored-dir/twoooo": &fstest.MapFile{Data: []byte("twoooo")},
		"a/b/ignored-dir/seven":  &fstest.MapFile{Data: []byte("seven")},
		"a/b/kept":               &fstest.MapFile{Data: []byte("kept")},
	}

	// git can't re-include a file whose parent directory is excluded, and
	// neither can buildDir, since it never reads an excluded directory.
	got := snapshotPaths(t, fsys, "ignored-dir/", "!ignored-dir/foo")
	want := []string{"a", "a/b", "a/b/kept"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("snapshot paths mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildDirectorySnapshotReincludesThroughDirectories(t *testing.T) {
	fsys := fstest.MapFS{
		"file":                 &fstest.MapFile{Data: []byte("file")},
		"data/file":            &fstest.MapFile{Data: []byte("file")},
		"data/data1/file1":     &fstest.MapFile{Data: []byte("file1")},
		"data/data1/file1.txt": &fstest.MapFile{Data: []byte("file1.txt")},
		"data/data2/file2":     &fstest.MapFile{Data: []byte("file2")},
		"data/data2/file2.txt": &fstest.MapFile{Data: []byte("file2.txt")},
	}

	got := snapshotPaths(t, fsys, "data/**", "!data/**/", "!data/**/*.txt")
	want := []string{
		"data",
		"data/data1",
		"data/data1/file1.txt",
		"data/data2",
		"data/data2/file2.txt",
		"file",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("snapshot paths mismatch (-want +got):\n%s", diff)
	}
}

func snapshotPaths(t *testing.T, fsys fs.FS, patterns ...string) []string {
	t.Helper()
	snapshot := mustSnapshot(t, fsys, mustMatcher(t, patterns...))

	var paths []string
	for p := range collect(snapshot.root) {
		paths = append(paths, filepath.ToSlash(p))
	}
	slices.Sort(paths)
	return paths
}
