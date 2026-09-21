package main

import (
	"crypto/sha256"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type FileNodeType int

const (
	DirectoryNode FileNodeType = iota
	RegularNode
)

type Snapshot struct {
	root  *FileNode
	blobs map[[32]byte][]byte
}

type FileNode struct {
	name     string
	mode     FileNodeType
	hash     [32]byte
	children [](*FileNode)
}

func (n *FileNode) Flatten() [](*FileNode) {
	stack := [](*FileNode){n}
	result := [](*FileNode){}

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

func (n *FileNode) String() string {
	names := []string{}

	for _, node := range n.Flatten() {
		names = append(names, node.name)
	}

	return strings.Join(names, "\n")
}

func BuildDirectorySnapshot(path string, exclude [](*regexp.Regexp)) (*Snapshot, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	children := [](*FileNode){}
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

		relPath := filepath.Join(path, filename)

		switch {
		case filetype.IsDir():
			nested, err := BuildDirectorySnapshot(relPath, exclude)
			if err != nil {
				return nil, err
			}

			children = append(children, nested.root)
			maps.Copy(blobs, nested.blobs)

		case filetype.IsRegular():
			content, err := os.ReadFile(relPath)
			if err != nil {
				return nil, err
			}

			h := sha256.New()
			h.Write([]byte{byte(RegularNode)})
			h.Write(content)

			var hash [32]byte
			copy(hash[:], h.Sum(nil))

			children = append(children, &FileNode{name: filename, mode: RegularNode, hash: hash})
			blobs[hash] = content
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

	root := &FileNode{name: filepath.Base(path), mode: DirectoryNode, hash: hash, children: children}
	snapshot := &Snapshot{root: root, blobs: blobs}

	return snapshot, nil
}
