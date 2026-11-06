package main

import (
	"crypto/sha256"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var diffOpts = cmp.Options{
	cmp.AllowUnexported(DiffAction{}, FileNodeChange{}),
	cmpopts.EquateEmpty(),
}

func file(name string, tag byte) *FileNode {
	h := sha256.New()
	h.Write([]byte{byte(RegularNode)})
	h.Write([]byte{tag})

	var hash [32]byte
	copy(hash[:], h.Sum(nil))

	return &FileNode{Name: name, Mode: RegularNode, Hash: hash}
}

func symlink(name string, target string) *FileNode {
	h := sha256.New()
	h.Write([]byte{byte(SymlinkNode)})
	h.Write([]byte(target))

	var hash [32]byte
	copy(hash[:], h.Sum(nil))

	return &FileNode{Name: name, Mode: SymlinkNode, Hash: hash}
}

func dir(name string, children ...*FileNode) *FileNode {
	h := sha256.New()
	h.Write([]byte{byte(DirectoryNode)})

	for _, c := range children {
		h.Write([]byte{byte(c.Mode)})
		h.Write([]byte(c.Name))
		h.Write([]byte{0})
		h.Write(c.Hash[:])
	}

	var hash [32]byte
	copy(hash[:], h.Sum(nil))

	return &FileNode{Name: name, Mode: DirectoryNode, Hash: hash, Children: children}
}

func createAction(path string, mode FileNodeType, hash [32]byte) DiffAction {
	return DiffAction{Kind: ActionKindCreate, Change: FileNodeChange{Path: path, Mode: mode, Hash: hash}}
}

func replaceAction(path string, hash [32]byte) DiffAction {
	return DiffAction{Kind: ActionKindReplace, Change: FileNodeChange{Path: path, Mode: RegularNode, Hash: hash}}
}

func deleteAction(path string) DiffAction {
	return DiffAction{Kind: ActionKindDelete, Change: FileNodeChange{Path: path}}
}

func renameAction(from, path string, mode FileNodeType, hash [32]byte) DiffAction {
	return DiffAction{
		Kind:   ActionKindRename,
		From:   from,
		Change: FileNodeChange{Path: path, Mode: mode, Hash: hash},
	}
}

func TestDiff(t *testing.T) {
	same := file("same", 1)
	changedLocal := file("changed", 1)
	changedRemote := file("changed", 2)
	gone := file("gone", 1)
	kept := file("x", 1)
	added := file("new", 3)
	nested := file("e", 1)
	otherNested := file("f", 2)

	equalHashLocal := dir("root", file("a", 1))
	equalHashRemote := dir("root", file("b", 2))
	equalHashRemote.Hash = equalHashLocal.Hash

	cases := []struct {
		name   string
		local  *FileNode
		remote *FileNode
		want   []DiffAction
	}{
		{
			name:   "equal",
			local:  dir("root", file("b", 1)),
			remote: dir("root", file("b", 1)),
		},
		{
			name:   "equal hash ignores children",
			local:  equalHashLocal,
			remote: equalHashRemote,
		},
		{
			name:   "replace file",
			local:  dir("root", file("b", 1)),
			remote: dir("root", file("b", 2)),
			want:   []DiffAction{replaceAction("b", file("b", 2).Hash)},
		},
		{
			name:   "add file",
			local:  dir("root"),
			remote: dir("root", file("b", 1)),
			want:   []DiffAction{createAction("b", RegularNode, file("b", 1).Hash)},
		},
		{
			name:   "remove file",
			local:  dir("root", file("b", 1)),
			remote: dir("root"),
			want:   []DiffAction{deleteAction("b")},
		},
		{
			name:   "add directory",
			local:  dir("root"),
			remote: dir("root", dir("d", nested)),
			want: []DiffAction{
				createAction("d", DirectoryNode, dir("d", nested).Hash),
				createAction(filepath.Join("d", "e"), RegularNode, nested.Hash),
			},
		},
		{
			name:   "remove directory",
			local:  dir("root", dir("d", nested, otherNested)),
			remote: dir("root"),
			want: []DiffAction{
				deleteAction(filepath.Join("d", "e")),
				deleteAction(filepath.Join("d", "f")),
				deleteAction("d"),
			},
		},
		{
			name:   "rename file",
			local:  dir("root", file("old", 1)),
			remote: dir("root", file("new", 1)),
			want: []DiffAction{
				renameAction("old", "new", RegularNode, file("old", 1).Hash),
			},
		},
		{
			name:   "rename directory",
			local:  dir("root", dir("old", nested)),
			remote: dir("root", dir("new", nested)),
			want: []DiffAction{
				renameAction("old", "new", DirectoryNode, dir("old", nested).Hash),
			},
		},
		{
			name:   "copy",
			local:  dir("root", file("old", 1)),
			remote: dir("root", file("old", 1), file("new", 1)),
			want: []DiffAction{
				createAction("new", RegularNode, file("new", 1).Hash),
			},
		},
		{
			name:   "moved file with new content",
			local:  dir("root", file("old", 1)),
			remote: dir("root", file("new", 2)),
			want: []DiffAction{
				createAction("new", RegularNode, file("new", 2).Hash),
				deleteAction("old"),
			},
		},
		{
			name:   "file becomes directory",
			local:  dir("root", file("x", 1)),
			remote: dir("root", dir("x", file("y", 2))),
			want: []DiffAction{
				deleteAction("x"),
				createAction("x", DirectoryNode, dir("x", file("y", 2)).Hash),
				createAction(filepath.Join("x", "y"), RegularNode, file("y", 2).Hash),
			},
		},
		{
			name:   "directory becomes file",
			local:  dir("root", dir("x", file("y", 1))),
			remote: dir("root", file("x", 2)),
			want: []DiffAction{
				deleteAction(filepath.Join("x", "y")),
				deleteAction("x"),
				createAction("x", RegularNode, file("x", 2).Hash),
			},
		},
		{
			name: "several edits",
			local: dir("root",
				same,
				changedLocal,
				gone,
				dir("keep", kept),
			),
			remote: dir("root",
				same,
				changedRemote,
				dir("keep", kept),
				added,
			),
			want: []DiffAction{
				replaceAction("changed", changedRemote.Hash),
				createAction("new", RegularNode, added.Hash),
				deleteAction("gone"),
			},
		},
		{
			name:   "retarget symlink",
			local:  dir("root", symlink("l", "a")),
			remote: dir("root", symlink("l", "b")),
			want: []DiffAction{{
				Kind:   ActionKindReplace,
				Change: FileNodeChange{Path: "l", Mode: SymlinkNode, Hash: symlink("l", "b").Hash},
			}},
		},
		{
			name:   "untouched sibling",
			local:  dir("root", file("a", 1), dir("b", file("c", 1))),
			remote: dir("root", file("a", 2), dir("b", file("c", 1))),
			want:   []DiffAction{replaceAction("a", file("a", 2).Hash)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.local.Diff(tc.remote)
			if diff := cmp.Diff(tc.want, got, diffOpts...); diff != "" {
				t.Errorf("Diff() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

type actionKey struct {
	kind ActionKind
	path string
	mode FileNodeType
	hash [32]byte
}

func TestDiff_SetArithmetic(t *testing.T) {
	cases := []struct {
		name   string
		local  *FileNode
		remote *FileNode
	}{
		{name: "add nested", local: dir("root"), remote: dir("root", dir("d", file("e", 1)))},
		{name: "remove nested", local: dir("root", dir("d", file("e", 1), file("f", 2))), remote: dir("root")},
		{name: "replace and keep", local: dir("root", file("a", 1), file("b", 1)), remote: dir("root", file("a", 2), file("b", 1))},
		{name: "file to directory", local: dir("root", file("a", 1)), remote: dir("root", dir("a", file("b", 2)))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summarize(tc.local.Diff(tc.remote))
			want := diffOracle(tc.local, tc.remote)
			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty(), cmpopts.EquateComparable(actionKey{})); diff != "" {
				t.Errorf("Diff() set mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func summarize(actions []DiffAction) []actionKey {
	keys := make([]actionKey, len(actions))
	for i, action := range actions {
		keys[i] = actionKey{
			kind: action.Kind,
			path: action.Change.Path,
			mode: action.Change.Mode,
			hash: action.Change.Hash,
		}
	}
	sortActionKeys(keys)
	return keys
}

func diffOracle(local *FileNode, remote *FileNode) []actionKey {
	localNodes := collect(local)
	remoteNodes := collect(remote)

	var keys []actionKey
	seen := make(map[string]bool, len(remoteNodes))

	for path, remoteNode := range remoteNodes {
		seen[path] = true
		localNode, found := localNodes[path]

		switch {
		case !found:
			keys = append(keys, actionKey{kind: ActionKindCreate, path: path, mode: remoteNode.Mode, hash: remoteNode.Hash})
		case localNode.Mode != remoteNode.Mode:
			keys = append(keys, actionKey{kind: ActionKindDelete, path: path})
			keys = append(keys, actionKey{kind: ActionKindCreate, path: path, mode: remoteNode.Mode, hash: remoteNode.Hash})
		case remoteNode.Mode == RegularNode && localNode.Hash != remoteNode.Hash:
			keys = append(keys, actionKey{kind: ActionKindReplace, path: path, mode: RegularNode, hash: remoteNode.Hash})
		}
	}

	for path := range localNodes {
		if seen[path] {
			continue
		}
		keys = append(keys, actionKey{kind: ActionKindDelete, path: path})
	}

	sortActionKeys(keys)
	return keys
}

func collect(root *FileNode) map[string]*FileNode {
	nodes := make(map[string]*FileNode)
	var walk func(*FileNode, string)
	walk = func(node *FileNode, prefix string) {
		for _, child := range node.Children {
			path := filepath.Join(prefix, child.Name)
			nodes[path] = child
			walk(child, path)
		}
	}
	walk(root, "")
	return nodes
}

func sortActionKeys(keys []actionKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].path != keys[j].path {
			return keys[i].path < keys[j].path
		}
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].mode < keys[j].mode
	})
}
