package workspace

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v：%v\n%s", args, err, out)
	}
}

func tool(name string, args map[string]any) (any, error) {
	data, _ := json.Marshal(args)
	return Run(context.Background(), name, string(data))
}

// 仓库配置里能变成宿主命令的（fsmonitor、clean filter）在沙箱外的 git 读取里都不能运行，读取的结果照旧。
func TestGitIgnoresRepoConfig(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "user.email", "t@example.com")
	gitIn(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("dist/\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dist"), 0o755)
	os.WriteFile(filepath.Join(dir, "dist", "b.js"), []byte("needle\n"), 0o644)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "init")
	gitIn(t, dir, "config", "core.fsmonitor", "touch "+marker+"; :")
	gitIn(t, dir, "config", "filter.x.clean", "touch "+marker+"; cat")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("2\n"), 0o644)
	if err := Open(dir); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status", "--porcelain"}, {"diff", "--stat", "HEAD"}, {"ls-files", "--others", "--ignored", "--exclude-standard", "--directory"}} {
		if _, err := Git(context.Background(), args...); err != nil {
			t.Fatalf("git %v：%v", args, err)
		}
	}
	if out, _ := Git(context.Background(), "status", "--porcelain"); !strings.Contains(out, "a.txt") {
		t.Fatalf("status 没看到改动：%q", out)
	}
	if !ignored(context.Background())["dist/"] {
		t.Fatal(".gitignore 应该生效")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("仓库配置里的命令被执行了")
	}
}

// 工作区是仓库的子目录时路径仍相对工作区，.gitignore 照常生效。
func TestGitInSubdirectory(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	os.MkdirAll(filepath.Join(dir, "sub", "out"), 0o755)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("out/\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "out", "x"), nil, 0o644)
	if err := Open(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	if got := ignored(context.Background()); !got["out/"] {
		t.Fatalf("子目录工作区：%v", got)
	}
}

// 编辑工具不能写 .git：直接写、大小写变体、经符号链接（含悬空的）、补丁里的路径都拒绝；.gitignore 不受影响。
func TestEditToolsRefuseGitDir(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	if err := Open(dir); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(dir, ".git", "config"))
	os.Symlink(".git", filepath.Join(dir, "link"))
	os.Symlink(".git/hooks/new", filepath.Join(dir, "dangling"))
	for _, path := range []string{".git/config", ".GIT/config", "sub/.git/config", "link/config", "dangling"} {
		if _, err := tool("write_file", map[string]any{"path": path, "content": "x"}); err == nil || !strings.Contains(err.Error(), ".git") {
			t.Errorf("write_file %s 应被拒绝：%v", path, err)
		}
		patch := "*** Begin Patch\n*** Add File: " + path + "\n+x\n*** End Patch"
		if _, err := tool("apply_patch", map[string]any{"input": patch}); err == nil || !strings.Contains(err.Error(), ".git") {
			t.Errorf("apply_patch %s 应被拒绝：%v", path, err)
		}
	}
	if _, err := tool("edit_file", map[string]any{"path": ".git/config", "old_string": "a", "new_string": "b"}); err == nil || !strings.Contains(err.Error(), ".git") {
		t.Errorf("edit_file 应被拒绝：%v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".git", "config")); string(after) != string(config) {
		t.Error(".git/config 被改了")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "new")); err == nil {
		t.Error("悬空链接的目标被创建了")
	}
	if _, err := tool("write_file", map[string]any{"path": ".gitignore", "content": "x\n"}); err != nil {
		t.Errorf(".gitignore 应该能写：%v", err)
	}
}
