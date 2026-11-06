# go-merkle

>[!note]
> This project is a **work in progress**. Expect capabilities explained below to either be not implemented or behave incorrectly.

Single-leader file replication based on Merkle trees, implemented in Go.
One leader serves the files in its working directory, and followers copy them into their own empty directories.

This is a learning project, inspired by Ben Dicken's [video on Merkle trees](https://www.youtube.com/watch?v=86Elcm_6X_Y).

## Build

Requires Go 1.27.1 or later.

```sh
go build
go test ./...
```

## Usage

```sh
# In the directory to replicate:
go-merkle lead --port 8000

# In an empty directory, on the same machine or another one:
go-merkle follow --host 127.0.0.1 --port 8000
```

| Command  | Flag              | Default     | Description                                  |
| -------- | ----------------- | ----------- | -------------------------------------------- |
| `lead`   | `--port`          | `8000`      | Port to serve updates on                     |
| `lead`   | `--ignore`        | none        | Comma-separated regexes of names to ignore   |
| `lead`   | `--interval`      | `10s`       | How often the snapshot is rebuilt            |
| `lead`   | `--max-staleness` | `1m`        | How long rebuilds may keep failing           |
| `follow` | `--host`          | `127.0.0.1` | Leader host                                  |
| `follow` | `--port`          | `8000`      | Leader port                                  |

`--ignore` patterns are matched against each file or directory name, not its full path. An ignored directory is skipped with everything inside it.

`--interval` takes a Go duration such as `500ms`, `30s` or `1m`, and must be positive.
The wait starts after each rebuild finishes, so rebuilds never overlap.

`--max-staleness` makes the leader exit once no rebuild has succeeded for that long, so a supervisor can restart it instead of followers silently receiving outdated files.
It is checked after each failed rebuild, so the actual exit happens up to one interval later. `0` disables it; negative values are rejected.

## How it works

The leader hashes its directory into a Merkle tree using SHA-256.
A file's hash covers its contents, a symlink's hash covers its target path, and a directory's hash covers the mode, name and hash of each child.
A change to any file therefore changes the hash of every directory above it, up to the root.

The planned replication flow is:

1. A follower connects to the leader and requests the tree, which contains no file contents.
2. The follower requests each file's contents by hash.
3. The follower polls the leader for the current tree.
4. If the root hash differs, the follower diffs the two trees and skips every subtree whose hash matches.
5. The follower requests the contents it doesn't have, then applies the creates, replacements, renames and deletes.

The tree will be sent as JSON with hex-encoded hashes, and file contents will be sent separately.

## Limitations

- Only regular files, directories and symlinks are replicated. Anything else, such as devices or sockets, is skipped.
- Files deleted while a snapshot is being built are left out of it. Any other read error, such as a permission error, fails the rebuild, because leaving the file out would delete it on the followers. While such an error persists, the leader keeps serving its last snapshot until `--max-staleness` is exceeded.
- Symlinks are replicated as links and never followed. Links with an absolute target or one pointing outside the replicated directory are skipped. A link to an ignored file is kept and will dangle on the follower.
- A snapshot holds the contents of every file in memory.
- Followers must start in an empty directory.

## License

[MIT](./LICENSE)
