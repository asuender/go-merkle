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

| Command  | Flag         | Default     | Description                                |
| -------- | ------------ | ----------- | ------------------------------------------ |
| `lead`   | `--port`     | `8000`      | Port to serve updates on                   |
| `lead`   | `--ignore`   | none        | Comma-separated regexes of names to ignore |
| `lead`   | `--interval` | `10s`       | How often the snapshot is rebuilt          |
| `follow` | `--host`     | `127.0.0.1` | Leader host                                |
| `follow` | `--port`     | `8000`      | Leader port                                |

`--ignore` patterns are matched against each file or directory name, not its full path. An ignored directory is skipped with everything inside it.

`--interval` takes a Go duration such as `500ms`, `30s` or `1m`, and must be positive.
The wait starts after each rebuild finishes, so rebuilds never overlap.

## How it works

The leader hashes its directory into a Merkle tree using SHA-256.
A file's hash covers its contents, and a directory's hash covers the mode, name and hash of each child.
A change to any file therefore changes the hash of every directory above it, up to the root.

The planned replication flow is:

1. A follower connects to the leader and requests the tree, which contains no file contents.
2. The follower requests each file's contents by hash.
3. The follower polls the leader for the current tree.
4. If the root hash differs, the follower diffs the two trees and skips every subtree whose hash matches.
5. The follower requests the contents it doesn't have, then applies the creates, replacements, renames and deletes.

The tree will be sent as JSON with hex-encoded hashes, and file contents will be sent separately.

## Limitations

- Only regular files and directories are supported. Building a snapshot fails on anything else, including symlinks.
- A snapshot holds the contents of every file in memory.
- Followers must start in an empty directory.

## License

[MIT](./LICENSE)
