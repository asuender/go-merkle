package main

import (
	"fmt"
	"path/filepath"
	"regexp"
)

var ignorePatternStrings = []string{}

func main() {
	var ignorePatterns [](*regexp.Regexp)

	for _, s := range ignorePatternStrings {
		ignorePatterns = append(ignorePatterns, regexp.MustCompile(s))
	}

	root, err := BuildNodeHierarchy(filepath.Dir("."), ignorePatterns)
	if err != nil {
		fmt.Println("Error: ", err)
		return
	}

	fmt.Println(root.String())
}
