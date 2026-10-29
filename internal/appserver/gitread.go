package appserver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// status/diff也会运行clean/process。用私有git目录读取同一对象、引用和index，
// 仅复制内置格式设置；不让仓库配置或并发修改的配置变成宿主命令。
func (c *conn) safeGitReadEnv(dir, index string) ([]string, func(), error) {
	read := func(args ...string) (string, error) {
		cmd := exec.CommandContext(c.ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = gitEnv("")
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	gitdir, err := read("rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, nil, err
	}
	common, err := read("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, nil, err
	}
	raw, err := read("config", "--null", "--list")
	if err != nil {
		return nil, nil, err
	}
	allowed := map[string]bool{"core.repositoryformatversion": true, "core.filemode": true, "core.ignorecase": true, "core.symlinks": true, "core.autocrlf": true, "core.eol": true, "core.safecrlf": true, "core.precomposeunicode": true, "core.quotepath": true, "diff.algorithm": true, "diff.indentheuristic": true, "extensions.objectformat": true, "extensions.compatobjectformat": true}
	values := map[string]string{}
	for _, entry := range strings.Split(raw, "\x00") {
		key, value, ok := strings.Cut(entry, "\n")
		if !ok {
			continue
		}
		key = strings.ToLower(key)
		if key == "extensions.refstorage" && value != "files" {
			return nil, nil, fmt.Errorf("网页Git读取尚不支持引用存储格式 %s", value)
		}

		if allowed[key] {
			values[key] = value
		}
	}
	temp, err := os.MkdirTemp("", "tern-git-read-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(temp) }
	head, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(temp, "HEAD"), head, 0600); err != nil {
		cleanup()
		return nil, nil, err
	}
	for name, target := range map[string]string{"objects": filepath.Join(common, "objects"), "refs": filepath.Join(common, "refs"), "packed-refs": filepath.Join(common, "packed-refs"), "shallow": filepath.Join(common, "shallow"), "info": filepath.Join(common, "info")} {
		if err := os.Symlink(target, filepath.Join(temp, name)); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	var config strings.Builder
	for key, value := range values {
		section, name, _ := strings.Cut(key, ".")
		fmt.Fprintf(&config, "[%s]\n%s = %s\n", section, name, strconv.Quote(value))
	}
	config.WriteString("[core]\nbare = false\nfsmonitor = false\nhooksPath = /dev/null\n[gc]\nauto = 0\n[maintenance]\nauto = false\n")
	if err := os.WriteFile(filepath.Join(temp, "config"), []byte(config.String()), 0600); err != nil {
		cleanup()
		return nil, nil, err
	}
	if index == "" {
		index = filepath.Join(gitdir, "index")
	}
	env := append(gitEnv(index), "GIT_DIR="+temp, "GIT_COMMON_DIR="+temp, "GIT_WORK_TREE="+dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	return env, cleanup, nil
}
