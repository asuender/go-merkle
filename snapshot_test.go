package main

import (
	"encoding/hex"
	"errors"
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

// failingFS fails accessing the paths in errs, while still listing them in
// their parent directory. That way, it reproduces entries changing between
// being listed and being read.
//
// It embeds [fs.FS] rather than [fstest.MapFS], so that only Open is promoted
// and helpers like [fs.ReadFile] cannot bypass it. ReadLink and Lstat are
// overridden explicitly to satisfy [fs.ReadLinkFS].
type failingFS struct {
	fs.FS
	errs map[string]error
}

func (f failingFS) Open(name string) (fs.File, error) {
	if err, ok := f.errs[name]; ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return f.FS.Open(name)
}

func (f failingFS) ReadLink(name string) (string, error) {
	if err, ok := f.errs[name]; ok {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: err}
	}
	return fs.ReadLink(f.FS, name)
}

func (f failingFS) Lstat(name string) (fs.FileInfo, error) {
	if err, ok := f.errs[name]; ok {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: err}
	}
	return fs.Lstat(f.FS, name)
}

func TestBuildDirectorySnapshotSkipsVanishedEntries(t *testing.T) {
	tests := []struct {
		name     string
		fsys     fstest.MapFS
		vanished string
	}{
		{
			name: "file",
			fsys: fstest.MapFS{
				"a.txt":    &fstest.MapFile{Data: []byte("content")},
				"gone.txt": &fstest.MapFile{Data: []byte("deleted")},
			},
			vanished: "gone.txt",
		},
		{
			name: "directory",
			fsys: fstest.MapFS{
				"a.txt":      &fstest.MapFile{Data: []byte("content")},
				"gone/b.txt": &fstest.MapFile{Data: []byte("deleted")},
			},
			vanished: "gone",
		},
		{
			name: "symlink",
			fsys: fstest.MapFS{
				"a.txt": &fstest.MapFile{Data: []byte("content")},
				"link":  &fstest.MapFile{Data: []byte("a.txt"), Mode: fs.ModeSymlink},
			},
			vanished: "link",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := mustSnapshot(t, fstest.MapFS{"a.txt": tt.fsys["a.txt"]}, nil)

			fsys := failingFS{FS: tt.fsys, errs: map[string]error{tt.vanished: fs.ErrNotExist}}
			got := mustSnapshot(t, fsys, nil)

			if got.root.Hash != want.root.Hash {
				t.Fatalf("vanished %s %q still affects the snapshot:\n%s", tt.name, tt.vanished, got.root)
			}
		})
	}
}

func TestBuildDirectorySnapshotFailsOnPermissionDenied(t *testing.T) {
	tests := []struct {
		name   string
		secret *fstest.MapFile
	}{
		{name: "file", secret: &fstest.MapFile{Data: []byte("unreadable")}},
		{name: "symlink", secret: &fstest.MapFile{Data: []byte("a.txt"), Mode: fs.ModeSymlink}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := failingFS{
				FS: fstest.MapFS{
					"a.txt":  &fstest.MapFile{Data: []byte("content")},
					"secret": tt.secret,
				},
				errs: map[string]error{"secret": fs.ErrPermission},
			}

			_, err := BuildDirectorySnapshot(fsys, nil)
			if !errors.Is(err, fs.ErrPermission) {
				t.Fatalf("got error %v, want it to wrap %v", err, fs.ErrPermission)
			}
		})
	}
}

func TestBuildDirectorySnapshotSkipsUnsupportedFileTypes(t *testing.T) {
	withoutSocket := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("content")},
	}
	withSocket := fstest.MapFS{
		"a.txt":    &fstest.MapFile{Data: []byte("content")},
		"dev.sock": &fstest.MapFile{Mode: fs.ModeSocket},
	}

	want := mustSnapshot(t, withoutSocket, nil)
	got := mustSnapshot(t, withSocket, nil)

	if got.root.Hash != want.root.Hash {
		t.Fatalf("root hash changed by socket:\n%s", got.root)
	}
}

// A vanished root must not be mistaken for an empty directory, as that
// snapshot would delete everything on the followers.
func TestBuildDirectorySnapshotFailsOnVanishedRoot(t *testing.T) {
	fsys := failingFS{
		FS:   fstest.MapFS{"a.txt": &fstest.MapFile{Data: []byte("content")}},
		errs: map[string]error{".": fs.ErrNotExist},
	}

	_, err := BuildDirectorySnapshot(fsys, nil)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("got error %v, want it to wrap %v", err, fs.ErrNotExist)
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
