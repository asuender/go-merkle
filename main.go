package main

import (
	"fmt"
	"os"
	"regexp"
)

var ignorePatternStrings = []string{}

func main() {
	var ignorePatterns [](*regexp.Regexp)

	for _, s := range ignorePatternStrings {
		ignorePatterns = append(ignorePatterns, regexp.MustCompile(s))
	}

	snapshot, err := BuildDirectorySnapshot(os.DirFS("."), ignorePatterns)
	if err != nil {
		fmt.Println("Error: ", err)
		return
	}

	fmt.Println(snapshot.root.String())
}
