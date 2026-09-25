package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type FileNodeType int

const (
	UnknownNode FileNodeType = iota
	DirectoryNode
	RegularNode
)

// A Snapshot represents one specific Merkle tree state
// by holding the root node and all file contents of the
// underlying tree.
type Snapshot struct {
	root  *FileNode
	blobs map[[32]byte][]byte
}

// A FileNode represents an individual node inside a Merkle tree.
type FileNode struct {
	name     string
	mode     FileNodeType
	hash     [32]byte
	children []*FileNode
}

// A FileNodeChange holds the necessary information needed by the followers
// to perform the necessary I/O operations after the diff.
type FileNodeChange struct {
	path string
	mode FileNodeType
	hash [32]byte
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
// For a rename, from is the existing path and change.path is the destination.
type DiffAction struct {
	kind   ActionKind
	from   string
	change FileNodeChange
}

// Diff compares remote to n and returns a slice of DiffAction
// comprising all the information needed for followers to replicate
// the exact Merkle tree from the leader in their local context.
func (n *FileNode) Diff(remote *FileNode) []DiffAction {
	return diffDir(n, remote, "")
}

func diffDir(local *FileNode, remote *FileNode, prefix string) []DiffAction {
	if local.hash == remote.hash {
		return nil
	}

	localByName := make(map[string]*FileNode)
	remoteByName := make(map[string]*FileNode)

	for _, c := range local.children {
		localByName[c.name] = c
	}
	for _, c := range remote.children {
		remoteByName[c.name] = c
	}

	var actions []DiffAction
	used := make(map[string]bool)

	for _, r := range remote.children {
		path := filepath.Join(prefix, r.name)
		l, found := localByName[r.name]

		if found {
			switch {
			case l.mode != r.mode:
				actions = append(actions, deleteSubtree(l, path)...)
				actions = append(actions, createSubtree(r, path)...)

			case l.mode == DirectoryNode && r.mode == DirectoryNode:
				actions = append(actions, diffDir(l, r, path)...)

			case l.hash != r.hash:
				actions = append(actions, DiffAction{kind: ActionKindReplace, change: FileNodeChange{path: path, mode: RegularNode, hash: r.hash}})
			}
		} else {
			wasRename := false

			for _, lc := range local.children {
				if lc.hash != r.hash || lc.mode != r.mode {
					continue
				}

				// both hash and mode are equal: avoid accidentally
				// flagging to rename a different file that also
				// exists on the remote
				// this will be important to enable copying a file
				// without removing it, hence keeping the same hash
				if _, found := remoteByName[lc.name]; !found {
					actions = append(actions, DiffAction{kind: ActionKindRename, from: filepath.Join(prefix, lc.name), change: FileNodeChange{path: path, mode: r.mode, hash: r.hash}})
					used[lc.name] = true
					wasRename = true
					break
				}
			}

			if !wasRename {
				actions = append(actions, createSubtree(r, path)...)
			}
		}
	}

	for _, l := range local.children {
		_, found := remoteByName[l.name]
		// if l.name was not flagged before, `used[l.name]` will return the zero value
		// (false), making this faster than a traditional array lookup
		if used[l.name] || found {
			continue
		}

		actions = append(actions, deleteSubtree(l, filepath.Join(prefix, l.name))...)
	}

	return actions
}

func createSubtree(node *FileNode, path string) []DiffAction {
	actions := []DiffAction{{
		kind:   ActionKindCreate,
		change: FileNodeChange{path: path, mode: node.mode, hash: node.hash},
	}}

	if node.mode != DirectoryNode {
		return actions
	}

	for _, c := range node.children {
		actions = append(actions, createSubtree(c, filepath.Join(path, c.name))...)
	}

	return actions
}

func deleteSubtree(node *FileNode, path string) []DiffAction {
	var actions []DiffAction

	if node.mode == DirectoryNode {
		for _, c := range node.children {
			actions = append(actions, deleteSubtree(c, filepath.Join(path, c.name))...)
		}
	}

	actions = append(actions, DiffAction{kind: ActionKindDelete, change: FileNodeChange{path: path}})
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

		for _, c := range node.children {
			stack = append(stack, c)
		}

		result = append(result, node)
	}

	return result
}

// String returns a string representation of n,
// currently listing all the nodes in a Preorder
// fashion, separated by newlines.
func (n *FileNode) String() string {
	names := []string{}

	for _, node := range n.Flatten() {
		names = append(names, node.name)
	}

	return strings.Join(names, "\n")
}

// BuildDirectorySnapshot recursively traverses fsys from its root and grows a
// new snapshot holding a Merkle tree representing the filesystem hierarchy,
// while excluding those paths that match the patterns in exclude.
// The root node is named ".".
func BuildDirectorySnapshot(fsys fs.FS, exclude []*regexp.Regexp) (*Snapshot, error) {
	return buildDir(fsys, ".", exclude)
}

func buildDir(fsys fs.FS, dir string, exclude []*regexp.Regexp) (*Snapshot, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	var children []*FileNode
	blobs := make(map[[32]byte][]byte)

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

			var hash [32]byte
			copy(hash[:], h.Sum(nil))

			children = append(children, &FileNode{name: filename, mode: RegularNode, hash: hash})
			blobs[hash] = content

		default:
			return nil, fmt.Errorf("path %s is not regular file or directory", childPath)
		}
	}

	h := sha256.New()
	h.Write([]byte{byte(DirectoryNode)})

	for _, c := range children {
		h.Write([]byte{byte(c.mode)})
		h.Write([]byte(c.name))
		h.Write([]byte{0})
		h.Write(c.hash[:])
	}

	var hash [32]byte
	copy(hash[:], h.Sum(nil))

	root := &FileNode{name: path.Base(dir), mode: DirectoryNode, hash: hash, children: children}
	snapshot := &Snapshot{root: root, blobs: blobs}

	return snapshot, nil
}
