# Ignore pattern format

Checklist of the rules from the "Pattern format" section of [gitignore(5)](https://git-scm.com/docs/gitignore#_pattern_format), copied verbatim.
Check off a rule once the matcher implements it, or strike it through if it doesn't apply to go-merkle.

## General rules

- [ ] ~~A blank line matches no files, so it can serve as a separator for readability.~~
- [ ] ~~A line starting with # serves as a comment. Put a backslash ("`\`") in front of the first hash for patterns that begin with a hash.~~
- [ ] ~~Trailing spaces are ignored unless they are quoted with backslash ("`\`").~~
- [x] An optional prefix "`!`" which negates the pattern; any matching file excluded by a previous pattern will become included again. It is not possible to re-include a file if a parent directory of that file is excluded. Git doesn't list excluded directories for performance reasons, so any patterns on contained files have no effect, no matter where they are defined. Put a backslash ("`\`") in front of the first "`!`" for patterns that begin with a literal "`!`", for example, "`\!important!.txt`".
- [x] The slash "`/`" is used as the directory separator. Separators may occur at the beginning, middle or end of the `.gitignore` search pattern.
- [x] If there is a separator at the beginning or middle (or both) of the pattern, then the pattern is relative to the directory level of the particular `.gitignore` file itself. Otherwise the pattern may also match at any level below the `.gitignore` level.
- [ ] ~~Patterns read from exclude sources that are outside the working tree, such as $GIT_COMMON_DIR/info/exclude and core.excludesFile, are treated as if they are specified at the root of the working tree, i.e. a leading "/" in such patterns anchors the match at the root of the repository.~~
- [x] If there is a separator at the end of the pattern then the pattern will only match directories, otherwise the pattern can match both files and directories.
- [x] For example, a pattern `doc/frotz/` matches `doc/frotz` directory, but not `a/doc/frotz` directory; however `frotz/` matches `frotz` and `a/frotz` that is a directory (all paths are relative from the `.gitignore` file).
- [x] An asterisk "`*`" matches anything except a slash. The character "`?`" matches any one character except "`/`". The range notation, e.g. `[a-zA-Z]`, can be used to match one of the characters in a range. See fnmatch(3) and the FNM_PATHNAME flag for a more detailed description.
- [x] A backslash ("`\`") can be used to escape any character. E.g., "`\*`" matches a literal asterisk (and "`\a`" matches "`a`", even though there is no need for escaping there). As with fnmatch(3), a backslash at the end of a pattern is an invalid pattern that never matches.

## Two consecutive asterisks

Two consecutive asterisks ("`**`") in patterns matched against full pathname may have special meaning:

- [x] A leading "`**`" followed by a slash means match in all directories. For example, "`**/foo`" matches file or directory "`foo`" anywhere, the same as pattern "`foo`". "`**/foo/bar`" matches file or directory "`bar`" anywhere that is directly under directory "`foo`".
- [x] A trailing "`/**`" matches everything inside. For example, "`abc/**`" matches all files inside directory "`abc`", relative to the location of the `.gitignore` file, with infinite depth.
- [x] A slash followed by two consecutive asterisks then a slash matches zero or more directories. For example, "`a/**/b`" matches "`a/b`", "`a/x/b`", "`a/x/y/b`" and so on.
- [x] Other consecutive asterisks are considered regular asterisks and will match according to the previous rules.
