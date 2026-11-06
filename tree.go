package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type FileNodeType int

// Note: add new node types only at the end here. Otherwise,
// compatability could be broken when reading older file modes.
const (
	UnknownNode FileNodeType = iota
	DirectoryNode
	RegularNode
	SymlinkNode
)

// A Hash is a SHA-256 digest. It implements [encoding.TextMarshaler] and
// [encoding.TextUnmarshaler], so JSON represents it as a hex string.
// This is due to the fact that the [json] package treats fixed-sized
// arrays and slices differently.
type Hash [32]byte

func (h Hash) MarshalText() ([]byte, error) {
	return []byte(hex.EncodeToString(h[:])), nil
}

func (h *Hash) UnmarshalText(b []byte) error {
	if len(b) != hex.EncodedLen(len(h)) {
		return fmt.Errorf("invalid hash length: %d", len(b))
	}

	_, err := hex.Decode(h[:], b)
	return err
}

// A Snapshot represents one specific Merkle tree state
// by holding the root node and all file contents of the
// underlying tree.
type Snapshot struct {
	root  *FileNode
	blobs map[Hash][]byte
}

// A FileNode represents an individual node inside a Merkle tree.
type FileNode struct {
	Name     string       `json:"name"`
	Mode     FileNodeType `json:"mode"`
	Hash     Hash         `json:"hash"`
	Children []*FileNode  `json:"children,omitempty"`
}

// A FileNodeChange holds the necessary information needed by the followers
// to perform the necessary I/O operations after the diff.
type FileNodeChange struct {
	Path string       `json:"path"`
	Mode FileNodeType `json:"mode"`
	Hash Hash         `json:"hash"`
}

type ActionKind int

const (
	ActionKindUnchanged ActionKind = iota
	ActionKindCreate
	ActionKindReplace
	ActionKindRename
	ActionKindDelete
)

// A DiffAction represents one single I/O action (creation, modification, deletion, or rename)
// as a result of making a diff between two Merkle trees.
// For a rename, from is the existing path and change.Path is the destination.
type DiffAction struct {
	Kind   ActionKind
	From   string
	Change FileNodeChange
}

// Diff compares remote to n and returns a slice of DiffAction
// comprising all the information needed for followers to replicate
// the exact Merkle tree from the leader in their local context.
func (n *FileNode) Diff(remote *FileNode) []DiffAction {
	return diffDir(n, remote, "")
}

func diffDir(local *FileNode, remote *FileNode, prefix string) []DiffAction {
	if local.Hash == remote.Hash {
		return nil
	}

	localByName := make(map[string]*FileNode)
	remoteByName := make(map[string]*FileNode)

	for _, c := range local.Children {
		localByName[c.Name] = c
	}
	for _, c := range remote.Children {
		remoteByName[c.Name] = c
	}

	var actions []DiffAction
	used := make(map[string]bool)

	for _, r := range remote.Children {
		path := filepath.Join(prefix, r.Name)
		l, found := localByName[r.Name]

		if found {
			switch {
			case l.Mode != r.Mode:
				actions = append(actions, deleteSubtree(l, path)...)
				actions = append(actions, createSubtree(r, path)...)

			case l.Mode == DirectoryNode && r.Mode == DirectoryNode:
				actions = append(actions, diffDir(l, r, path)...)

			case l.Hash != r.Hash:
				actions = append(actions, DiffAction{Kind: ActionKindReplace, Change: FileNodeChange{Path: path, Mode: r.Mode, Hash: r.Hash}})
			}
		} else {
			wasRename := false

			for _, lc := range local.Children {
				if lc.Hash != r.Hash || lc.Mode != r.Mode {
					continue
				}

				// both hash and mode are equal: avoid accidentally
				// flagging to rename a different file that also
				// exists on the remote
				// this will be important to enable copying a file
				// without removing it, hence keeping the same hash
				if _, found := remoteByName[lc.Name]; !found {
					actions = append(actions, DiffAction{Kind: ActionKindRename, From: filepath.Join(prefix, lc.Name), Change: FileNodeChange{Path: path, Mode: r.Mode, Hash: r.Hash}})
					used[lc.Name] = true
					wasRename = true
					break
				}
			}

			if !wasRename {
				actions = append(actions, createSubtree(r, path)...)
			}
		}
	}

	for _, l := range local.Children {
		_, found := remoteByName[l.Name]
		// if l.Name was not flagged before, `used[l.Name]` will return the zero value
		// (false), making this faster than a traditional array lookup
		if used[l.Name] || found {
			continue
		}

		actions = append(actions, deleteSubtree(l, filepath.Join(prefix, l.Name))...)
	}

	return actions
}

func createSubtree(node *FileNode, path string) []DiffAction {
	actions := []DiffAction{{
		Kind:   ActionKindCreate,
		Change: FileNodeChange{Path: path, Mode: node.Mode, Hash: node.Hash},
	}}

	if node.Mode != DirectoryNode {
		return actions
	}

	for _, c := range node.Children {
		actions = append(actions, createSubtree(c, filepath.Join(path, c.Name))...)
	}

	return actions
}

func deleteSubtree(node *FileNode, path string) []DiffAction {
	var actions []DiffAction

	if node.Mode == DirectoryNode {
		for _, c := range node.Children {
			actions = append(actions, deleteSubtree(c, filepath.Join(path, c.Name))...)
		}
	}

	actions = append(actions, DiffAction{Kind: ActionKindDelete, Change: FileNodeChange{Path: path}})
	return actions
}

// Flatten flattens n by recursively traversing through
// the tree and returns a one-level *FileNode slice
// containing all the nodes of the tree.
func (n *FileNode) Flatten() []*FileNode {
	stack := []*FileNode{n}
	result := []*FileNode{}

	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]

		for _, c := range node.Children {
			stack = append(stack, c)
		}

		result = append(result, node)
	}

	return result
}

// String returns a string representation of n,
// currently listing all the nodes in a postorder (i.e. reversed)
// fashion, separated by newlines.
func (n *FileNode) String() string {
	names := []string{}

	for _, node := range n.Flatten() {
		names = append(names, node.Name)
	}

	return strings.Join(names, "\n")
}

// BuildDirectorySnapshot recursively traverses fsys from its root and grows a
// new snapshot holding a Merkle tree representing the filesystem hierarchy,
// while excluding files that match the patterns in exclude.
// The root node is named ".".
//
// Symlinks are recorded as links and never followed, so the follower gets the
// same link rather than a copy of its target. Their blob is the raw target.
// Links with an absolute target or one resolving outside the root are skipped,
// since the follower's root lives elsewhere and they would point at something
// unrelated there.
//
// fsys must implement [fs.ReadLinkFS] if it contains symlinks.
func BuildDirectorySnapshot(fsys fs.FS, exclude []*regexp.Regexp) (*Snapshot, error) {
	return buildDir(fsys, ".", exclude)
}

func buildDir(fsys fs.FS, dir string, exclude []*regexp.Regexp) (*Snapshot, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	var children []*FileNode
	blobs := make(map[Hash][]byte)

	for _, entry := range entries {
		filename := entry.Name()
		filetype := entry.Type()

		ignore := false
		for _, pattern := range exclude {
			if pattern.MatchString(filename) {
				ignore = true
				break
			}
		}

		if ignore {
			continue
		}

		childPath := path.Join(dir, filename)

		switch {
		case filetype.IsDir():
			nested, err := buildDir(fsys, childPath, exclude)
			if err != nil {
				return nil, err
			}

			children = append(children, nested.root)
			maps.Copy(blobs, nested.blobs)

		case filetype.IsRegular():
			content, err := fs.ReadFile(fsys, childPath)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", childPath, err)
			}

			h := sha256.New()
			h.Write([]byte{byte(RegularNode)})
			h.Write(content)

			var hash Hash
			copy(hash[:], h.Sum(nil))

			children = append(children, &FileNode{Name: filename, Mode: RegularNode, Hash: hash})
			blobs[hash] = content

		// TODO: it could happen that exclude hides paths that are
		// referenced by valid symlinks, so resolve this situation.
		case filetype&fs.ModeSymlink != 0:
			target, err := fs.ReadLink(fsys, childPath)
			if err != nil {
				return nil, fmt.Errorf("read symlink %s: %w", childPath, err)
			}

			// [fs.ValidPath] rejects ".." and leading slashes, so that way,
			// we can be sure that we neither escape the current directory
			// nor follow an absolute path.
			// Joining with dir would hide an absolute target, so we additionally
			// guard that with [path.IsAbs].
			cleanedTarget := path.Join(dir, target)
			if path.IsAbs(target) || !fs.ValidPath(cleanedTarget) {
				continue
			}

			h := sha256.New()
			h.Write([]byte{byte(SymlinkNode)})
			h.Write([]byte(target))

			var hash Hash
			copy(hash[:], h.Sum(nil))

			children = append(children, &FileNode{Name: filename, Mode: SymlinkNode, Hash: hash})
			blobs[hash] = []byte(target)

		default:
			return nil, fmt.Errorf("path %s is not a supported file type", childPath)
		}
	}

	h := sha256.New()
	h.Write([]byte{byte(DirectoryNode)})

	for _, c := range children {
		h.Write([]byte{byte(c.Mode)})
		h.Write([]byte(c.Name))
		h.Write([]byte{0})
		h.Write(c.Hash[:])
	}

	var hash Hash
	copy(hash[:], h.Sum(nil))

	root := &FileNode{Name: path.Base(dir), Mode: DirectoryNode, Hash: hash, Children: children}
	snapshot := &Snapshot{root: root, blobs: blobs}

	return snapshot, nil
}
