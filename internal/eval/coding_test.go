package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 试次目录的仓库配置被模型改过时，saveDiff 不执行里面的命令，diff 照常含改动和新文件。
func TestSaveDiffIgnoresRepoConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	marker := filepath.Join(dir, "..", "marker-"+filepath.Base(dir))
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v：%v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n"), 0o644)
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "init")
	run("config", "core.fsmonitor", "touch "+marker+"; :")
	run("config", "filter.x.clean", "touch "+marker+"; cat")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("n\n"), 0o644)
	path := saveDiff("rec", "task", dir)
	if path == "" {
		t.Fatal("saveDiff 失败")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "+2") || !strings.Contains(string(data), "new.txt") {
		t.Fatalf("diff 不对：%s", data)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("仓库配置里的命令被执行了")
	}
}
