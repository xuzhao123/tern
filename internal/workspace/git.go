package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// GitEnv 是在宿主机上跑 git 的环境：去掉继承的 GIT_*，不抢锁、不弹提示。
func GitEnv(index string) []string {
	env := []string{}
	for _, pair := range os.Environ() {
		if !strings.HasPrefix(pair, "GIT_") {
			env = append(env, pair)
		}
	}
	env = append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	if index != "" {
		env = append(env, "GIT_INDEX_FILE="+index)
	}
	return env
}

// SafeGitEnv 给只读的 git 命令一份环境：status/diff也会运行clean/process，ls-files会运行fsmonitor。
// 用私有git目录读取同一对象、引用和index，仅复制内置格式设置；不让仓库配置或并发修改的配置变成宿主命令。
// 全局配置也不读，所以全局的 core.excludesFile 不生效。workTree 是仓库的工作树根目录。
func SafeGitEnv(ctx context.Context, dir, workTree, index string) ([]string, func(), error) {
	read := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = GitEnv("")
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
	env := append(GitEnv(index), "GIT_DIR="+temp, "GIT_COMMON_DIR="+temp, "GIT_WORK_TREE="+workTree, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	return env, cleanup, nil
}

// Git 在工作区里跑一条只读的 git 命令。工作区的内容（连同 .git）模型能影响，而这里在沙箱外执行，
// 所以一律走 SafeGitEnv；工作区不是 git 仓库时返回错误。
func Git(ctx context.Context, args ...string) (string, error) {
	top := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	top.Dir, top.Env = Dir, GitEnv("")
	out, err := top.Output()
	if err != nil {
		return "", err
	}
	env, cleanup, err := SafeGitEnv(ctx, Dir, strings.TrimSpace(string(out)), "")
	if err != nil {
		return "", err
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env = Dir, env
	out, err = cmd.Output()
	return string(out), err
}

// inGitDir 判断写这个路径会不会落进 .git（含经符号链接绕进去的）。.git 里的配置和 hook 是宿主机上会执行的东西，
// 沙箱里的 bash 只读，编辑工具也不能写。悬空的符号链接解析不了，写入时会跟着它创建目标，同样拒绝。
func inGitDir(path string) bool {
	has := func(rel string) bool {
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.EqualFold(part, ".git") {
				return true
			}
		}
		return false
	}
	if has(path) {
		return true
	}
	base, err := filepath.EvalSymlinks(Dir)
	if err != nil {
		return false
	}
	for p := filepath.Join(Dir, path); len(p) > len(Dir); p = filepath.Dir(p) {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			rel, err := filepath.Rel(base, real)
			return err == nil && has(rel)
		}
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}
