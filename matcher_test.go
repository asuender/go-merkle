package main

import (
	"testing"
	"time"
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
