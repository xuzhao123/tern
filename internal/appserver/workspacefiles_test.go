package appserver

import (
	"slices"
	"testing"
)

func TestMatchWorkspaceFiles(t *testing.T) {
	files := []string{"spec/README.md", "internal/permission/AGENTS.md", "internal/permission/permission.go", "spec/check-permission.mjs", "permission_test.go"}
	got := matchWorkspaceFiles(files, "perm")
	want := []string{"permission_test.go", "internal/permission/permission.go", "spec/check-permission.mjs", "internal/permission/AGENTS.md"}
	if !slices.Equal(got, want) {
		t.Errorf("连续命中时不要子序列：%v", got)
	}
	// 没有连续命中时，子序列还是有用的。
	if got := matchWorkspaceFiles(files, "ipa"); !slices.Contains(got, "internal/permission/AGENTS.md") {
		t.Errorf("子序列：%v", got)
	}
}
