package main

import (
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
	want := []DiffAction{replaceAction(filepath.Join("a", "b.txt"), replaced.hash)}
	if diff := cmp.Diff(want, local.root.Diff(remote.root), diffOpts...); diff != "" {
		t.Fatalf("content edit mismatch (-want +got):\n%s", diff)
	}

	delete(remoteFS, "c.txt")
	remote = mustSnapshot(t, remoteFS, nil)
	replaced = child(t, remote.root, "a", "b.txt")
	want = []DiffAction{
		replaceAction(filepath.Join("a", "b.txt"), replaced.hash),
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
		replaceAction(filepath.Join("a", "b.txt"), replaced.hash),
		createAction("extra.txt", RegularNode, added.hash),
		deleteAction("c.txt"),
	}
	if diff := cmp.Diff(want, local.root.Diff(remote.root), diffOpts...); diff != "" {
		t.Fatalf("exclude mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildDirectorySnapshotRejectsSymlink(t *testing.T) {
	fsys := fstest.MapFS{
		"link": &fstest.MapFile{Data: []byte("target"), Mode: fs.ModeSymlink},
	}
	if _, err := BuildDirectorySnapshot(fsys, nil); err == nil {
		t.Fatal("expected error for symlink")
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
		for _, c := range node.children {
			if c.name == name {
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
