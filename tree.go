package main

import (
	"crypto/sha256"
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

type FileNode struct {
	name     string
	mode     FileNodeType
	content  []byte
	hash     []byte
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

func BuildNodeHierarchy(path string, exclude [](*regexp.Regexp)) (*FileNode, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	children := [](*FileNode){}

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
			node, err := BuildNodeHierarchy(relPath, exclude)
			if err != nil {
				return nil, err
			}

			children = append(children, node)

		case filetype.IsRegular():
			content, err := os.ReadFile(relPath)
			if err != nil {
				return nil, err
			}

			hash := sha256.New()
			hash.Write([]byte{byte(RegularNode)})
			hash.Write(content)

			children = append(children, &FileNode{name: filename, mode: RegularNode, content: content, hash: hash.Sum(nil)})
		}
	}

	hash := sha256.New()
	hash.Write([]byte{byte(DirectoryNode)})

	for _, c := range children {
		hash.Write([]byte{byte(c.mode)})
		hash.Write([]byte(c.name))
		hash.Write([]byte{0})
		hash.Write(c.hash)
	}

	root := &FileNode{name: filepath.Base(path), mode: DirectoryNode, hash: hash.Sum(nil), children: children}

	return root, nil
}
