package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestDistIndexAssetRefsAreEmbedded(t *testing.T) {
	index, err := Dist.ReadFile("dist/index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}

	refs := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(string(index), -1)
	for _, match := range refs {
		path := "dist/" + strings.TrimPrefix(match[1], "/")
		if _, err := fs.Stat(Dist, path); err != nil {
			t.Fatalf("embedded index.html references %s, but %s is missing: %v", match[1], path, err)
		}
	}
}
