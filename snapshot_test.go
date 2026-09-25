package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSnapshotDiff(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	for _, dir := range []string{localDir, remoteDir} {
		writeFile(t, filepath.Join(dir, "a", "b.txt"), "one")
		writeFile(t, filepath.Join(dir, "c.txt"), "same")
	}

	local := mustSnapshot(t, localDir, nil)
	remote := mustSnapshot(t, remoteDir, nil)
	if diff := cmp.Diff([]DiffAction{}, local.root.Diff(remote.root), diffOpts...); diff != "" {
		t.Fatalf("identical trees mismatch (-want +got):\n%s", diff)
	}

	writeFile(t, filepath.Join(remoteDir, "a", "b.txt"), "two")
	remote = mustSnapshot(t, remoteDir, nil)
	replaced := child(t, remote.root, "a", "b.txt")
	want := []DiffAction{replaceAction(filepath.Join("a", "b.txt"), replaced.hash)}
	if diff := cmp.Diff(want, local.root.Diff(remote.root), diffOpts...); diff != "" {
		t.Fatalf("content edit mismatch (-want +got):\n%s", diff)
	}

	if err := os.Remove(filepath.Join(remoteDir, "c.txt")); err != nil {
		t.Fatal(err)
	}
	remote = mustSnapshot(t, remoteDir, nil)
	replaced = child(t, remote.root, "a", "b.txt")
	want = []DiffAction{
		replaceAction(filepath.Join("a", "b.txt"), replaced.hash),
		deleteAction("c.txt"),
	}
	if diff := cmp.Diff(want, local.root.Diff(remote.root), diffOpts...); diff != "" {
		t.Fatalf("delete mismatch (-want +got):\n%s", diff)
	}

	writeFile(t, filepath.Join(remoteDir, "skip.tmp"), "ignored")
	writeFile(t, filepath.Join(remoteDir, "extra.txt"), "added")
	exclude := []*regexp.Regexp{regexp.MustCompile(`\.tmp$`)}
	local = mustSnapshot(t, localDir, exclude)
	remote = mustSnapshot(t, remoteDir, exclude)
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

func mustSnapshot(t *testing.T, path string, exclude []*regexp.Regexp) *Snapshot {
	t.Helper()
	snapshot, err := BuildDirectorySnapshot(path, exclude)
	if err != nil {
		t.Fatalf("BuildDirectorySnapshot(%q): %v", path, err)
	}
	return snapshot
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
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
