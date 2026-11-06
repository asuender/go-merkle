package main

import (
	"encoding/hex"
	"io/fs"
	"path/filepath"
	"regexp"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

func TestSnapshotDiff(t *testing.T) {
	localFS := fstest.MapFS{
		"a/b.txt": &fstest.MapFile{Data: []byte("one")},
		"c.txt":   &fstest.MapFile{Data: []byte("same")},
	}
	remoteFS := fstest.MapFS{
		"a/b.txt": &fstest.MapFile{Data: []byte("one")},
		"c.txt":   &fstest.MapFile{Data: []byte("same")},
	}

	local := mustSnapshot(t, localFS, nil)
	remote := mustSnapshot(t, remoteFS, nil)
	if diff := cmp.Diff([]DiffAction{}, local.root.Diff(remote.root), diffOpts...); diff != "" {
		t.Fatalf("identical trees mismatch (-want +got):\n%s", diff)
	}

	remoteFS["a/b.txt"].Data = []byte("two")
	remote = mustSnapshot(t, remoteFS, nil)
	replaced := child(t, remote.root, "a", "b.txt")
	want := []DiffAction{replaceAction(filepath.Join("a", "b.txt"), replaced.Hash)}
	if diff := cmp.Diff(want, local.root.Diff(remote.root), diffOpts...); diff != "" {
		t.Fatalf("content edit mismatch (-want +got):\n%s", diff)
	}

	delete(remoteFS, "c.txt")
	remote = mustSnapshot(t, remoteFS, nil)
	replaced = child(t, remote.root, "a", "b.txt")
	want = []DiffAction{
		replaceAction(filepath.Join("a", "b.txt"), replaced.Hash),
		deleteAction("c.txt"),
	}
	if diff := cmp.Diff(want, local.root.Diff(remote.root), diffOpts...); diff != "" {
		t.Fatalf("delete mismatch (-want +got):\n%s", diff)
	}

	remoteFS["skip.tmp"] = &fstest.MapFile{Data: []byte("ignored")}
	remoteFS["extra.txt"] = &fstest.MapFile{Data: []byte("added")}
	exclude := []*regexp.Regexp{regexp.MustCompile(`\.tmp$`)}
	local = mustSnapshot(t, localFS, exclude)
	remote = mustSnapshot(t, remoteFS, exclude)
	added := child(t, remote.root, "extra.txt")
	replaced = child(t, remote.root, "a", "b.txt")
	want = []DiffAction{
		replaceAction(filepath.Join("a", "b.txt"), replaced.Hash),
		createAction("extra.txt", RegularNode, added.Hash),
		deleteAction("c.txt"),
	}
	if diff := cmp.Diff(want, local.root.Diff(remote.root), diffOpts...); diff != "" {
		t.Fatalf("exclude mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildDirectorySnapshotKeepsSymlinkInsideRoot(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("content")},
		"link":  &fstest.MapFile{Data: []byte("a.txt"), Mode: fs.ModeSymlink},
	}

	snapshot := mustSnapshot(t, fsys, nil)

	// sha256(byte(SymlinkNode) || "a.txt"), computed independently with shasum
	var wantHash Hash
	hex.Decode(wantHash[:], []byte("df8c7059061541560a9bf5320546d5937432ad53293bdcb45f47917529f04eb1"))

	link := child(t, snapshot.root, "link")
	want := &FileNode{Name: "link", Mode: SymlinkNode, Hash: wantHash}
	if diff := cmp.Diff(want, link); diff != "" {
		t.Fatalf("symlink node mismatch (-want +got):\n%s", diff)
	}
	if got := string(snapshot.blobs[wantHash]); got != "a.txt" {
		t.Fatalf("symlink blob = %q, want %q", got, "a.txt")
	}
}

func TestBuildDirectorySnapshotSkipsSymlinkEscapingRoot(t *testing.T) {
	withoutLink := fstest.MapFS{
		"sub/a.txt": &fstest.MapFile{Data: []byte("content")},
	}
	withLink := fstest.MapFS{
		"sub/a.txt": &fstest.MapFile{Data: []byte("content")},
		"sub/link":  &fstest.MapFile{Data: []byte("../../outside.txt"), Mode: fs.ModeSymlink},
	}

	want := mustSnapshot(t, withoutLink, nil)
	got := mustSnapshot(t, withLink, nil)

	if got.root.Hash != want.root.Hash {
		t.Fatalf("root hash changed by escaping symlink:\n%s", got.root)
	}
}

func TestBuildDirectorySnapshotSkipsSymlinkWithAbsoluteTarget(t *testing.T) {
	withoutLink := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("content")},
	}
	withLink := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("content")},
		"link":  &fstest.MapFile{Data: []byte("/etc/hosts"), Mode: fs.ModeSymlink},
	}

	want := mustSnapshot(t, withoutLink, nil)
	got := mustSnapshot(t, withLink, nil)

	if got.root.Hash != want.root.Hash {
		t.Fatalf("root hash changed by absolute symlink:\n%s", got.root)
	}
}

func TestBuildDirectorySnapshotKeepsNestedSymlinkPointingUpInsideRoot(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt":    &fstest.MapFile{Data: []byte("content")},
		"sub/link": &fstest.MapFile{Data: []byte("../a.txt"), Mode: fs.ModeSymlink},
	}

	snapshot := mustSnapshot(t, fsys, nil)

	link := child(t, snapshot.root, "sub", "link")
	if link.Mode != SymlinkNode {
		t.Fatalf("sub/link mode = %v, want SymlinkNode", link.Mode)
	}
	if got := string(snapshot.blobs[link.Hash]); got != "../a.txt" {
		t.Fatalf("symlink blob = %q, want %q", got, "../a.txt")
	}
}

func mustSnapshot(t *testing.T, fsys fs.FS, exclude []*regexp.Regexp) *Snapshot {
	t.Helper()
	snapshot, err := BuildDirectorySnapshot(fsys, exclude)
	if err != nil {
		t.Fatalf("BuildDirectorySnapshot: %v", err)
	}
	return snapshot
}

func child(t *testing.T, node *FileNode, names ...string) *FileNode {
	t.Helper()
	for _, name := range names {
		var next *FileNode
		for _, c := range node.Children {
			if c.Name == name {
				next = c
				break
			}
		}
		if next == nil {
			t.Fatalf("missing child %q", name)
		}
		node = next
	}
	return node
}
