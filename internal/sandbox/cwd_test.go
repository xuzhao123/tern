package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 宿主机模式（不需要 bwrap）下真实跑 bash：cd 带到下一条命令；离开工作区重置；
// Cwd/RestoreCwd 模拟“这一轮存进检查点、下一轮新进程读回”；换了模式从根目录开始。
func TestCwdAcrossCallsAndTurns(t *testing.T) {
	base := t.TempDir()
	project, scratch := filepath.Join(base, "proj"), filepath.Join(base, "scratch")
	os.MkdirAll(filepath.Join(project, "sub", "deep"), 0o755)
	os.MkdirAll(scratch, 0o755)
	saved := []any{Enabled, Dir, Project, ProjectWrite, Timeout, Host}
	defer func() {
		Enabled, Dir, Project, ProjectWrite, Timeout, Host = saved[0].(bool), saved[1].(string), saved[2].(string), saved[3].(bool), saved[4].(time.Duration), saved[5].(bool)
		RestoreCwd("", false)
	}()
	Enabled, Dir, Project, ProjectWrite, Timeout, Host = true, scratch, project, true, 30*time.Second, true
	RestoreCwd("", false)
	run := func(command string) map[string]any {
		t.Helper()
		r, err := Run(context.Background(), "t", "bash", `{"command":"`+command+`"}`)
		if err != nil {
			t.Fatal(err)
		}
		return r.(map[string]any)
	}
	root := realDir(project)
	if out := run("cd sub && pwd")["output"]; strings.TrimSpace(out.(string)) != root+"/sub" {
		t.Fatalf("cd sub：%v", out)
	}
	if m := run("pwd; exit 3"); strings.TrimSpace(m["output"].(string)) != root+"/sub" || m["exit_code"] != 3 || m["cwd"] != root+"/sub" {
		t.Fatalf("下一条命令应还在 sub，退出码不变：%v", m)
	}
	// 这一轮结束：存进检查点；下一轮是新进程，读回来接着用。
	dir, host := Cwd()
	RestoreCwd("", false)
	RestoreCwd(dir, host)
	if CurrentDir() != root+"/sub" || strings.TrimSpace(run("pwd")["output"].(string)) != root+"/sub" {
		t.Fatalf("下一轮应接着在 sub：%s", CurrentDir())
	}
	if m := run("cd /"); m["cwd_note"] == nil || CurrentDir() != root {
		t.Fatalf("离开工作区应重置回根目录：%v %s", m, CurrentDir())
	}
	// 上一轮在宿主机上记的目录，换到沙箱模式就不用了。
	RestoreCwd(root+"/sub", true)
	Host = false
	if got := CurrentDir(); got == root+"/sub" {
		t.Fatalf("换了模式应从根目录开始：%s", got)
	}
}
