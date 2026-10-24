package appserver

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuzhao123/tern/internal/protocol"
)

// workspace/diff：这条对话的工作区相对 HEAD 的改动，网页端的“改动”面板据此显示整份 diff。
// 已跟踪的文件取 git diff HEAD，未跟踪的文件按“新增”给出内容；命令只读，带 GIT_OPTIONAL_LOCKS=0 不碰仓库里的锁。
// 文件内容由模型或 git 提供，页面只用 textContent 显示。
const (
	maxDiffFile  = 256 << 10 // 未跟踪的文件超过它就只写一行说明
	maxDiffTotal = 1 << 20   // 所有 diff 加起来的预算，超过后后面的文件只给路径、状态、行数
)

type diffFile struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // porcelain 的两位状态去掉空格：M、A、D、R、??
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Diff    string `json:"diff"` // 从 "diff --git" 那行之后开始；未跟踪文件每行前加 +；被截断时为空
}

// workDir 是这条对话快照里的工作区目录（最近一次 start/continue 上的 workspace_dir，续聊沿用开始时的）。调用方持有 r.mu。
func (r *run) workDir() string {
	for i := len(r.events) - 1; i >= 0; i-- {
		if e := r.events[i]; e.Kind == "start" || e.Kind == "continue" {
			if e.WorkDir != "" {
				return e.WorkDir
			}
		}
	}
	return ""
}

// git 在 dir 里跑一条只读的 git 命令。stderr 一并收进错误信息：看不懂时页面能看到 git 的原话。
func (c *conn) git(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(c.ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	return string(out), nil
}

func (c *conn) workspaceDiff(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.mu.Lock()
	dir := r.workDir()
	r.mu.Unlock()
	if dir == "" {
		return nil, bad(400, "这条对话没有工作区")
	}
	if !isGitRepo(dir) {
		return nil, bad(400, "不是 git 仓库，看不了 diff")
	}
	status, err := c.git(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, bad(500, "git status 失败："+err.Error())
	}
	head := ""
	if out, err := c.git(dir, "log", "-1", "--format=%h %s"); err == nil {
		head = strings.TrimSpace(out)
	}
	// 仓库还没有提交时 HEAD 不存在：改看暂存区加工作区，两部分接起来（顺序与 numstat 一致）。
	type gitCall struct {
		out  *strings.Builder
		args []string
	}
	var patch, numstat strings.Builder
	calls := []gitCall{{&patch, []string{"diff", "HEAD", "--no-color", "--no-ext-diff"}}, {&numstat, []string{"diff", "HEAD", "--numstat", "-z"}}}
	if _, err := c.git(dir, "rev-parse", "--verify", "HEAD"); err != nil {
		calls = []gitCall{{&patch, []string{"diff", "--cached", "--no-color", "--no-ext-diff"}}, {&patch, []string{"diff", "--no-color", "--no-ext-diff"}},
			{&numstat, []string{"diff", "--cached", "--numstat", "-z"}}, {&numstat, []string{"diff", "--numstat", "-z"}}}
	}
	for _, call := range calls {
		out, err := c.git(dir, call.args...)
		if err != nil {
			return nil, bad(500, "git "+call.args[0]+" 失败："+err.Error())
		}
		call.out.WriteString(out)
	}
	// git diff 与 git diff --numstat 的顺序一致：逐段对上，按新路径（重命名后的名字）索引。
	tracked := map[string]diffFile{}
	segments, stats := splitDiff(patch.String()), parseNumstat(numstat.String())
	for i, seg := range segments {
		if i >= len(stats) {
			break
		}
		tracked[stats[i].Path] = diffFile{Added: stats[i].Added, Deleted: stats[i].Deleted, Diff: seg}
	}
	files, used, truncated := []diffFile{}, 0, false
	keep := func(diff string) bool {
		if truncated || used+len(diff) > maxDiffTotal {
			truncated = true
			return false
		}
		used += len(diff)
		return true
	}
	for _, e := range parseStatus(status) {
		f := diffFile{Path: e.path, Status: e.status}
		if e.untracked {
			diff, added := untrackedDiff(dir, e.path)
			f.Added = added
			if keep(diff) {
				f.Diff = diff
			}
		} else if t, ok := tracked[e.path]; ok {
			f.Added, f.Deleted = t.Added, t.Deleted
			if keep(t.Diff) {
				f.Diff = t.Diff
			}
		}
		files = append(files, f)
	}
	return map[string]any{"dir": dir, "head": head, "files": files, "truncated": truncated}, nil
}

type statusEntry struct {
	path      string
	status    string
	untracked bool
}

// parseStatus 解 porcelain=v1 -z 的输出：每条是 "XY 路径" 后跟 NUL；重命名、复制再多跟一个原路径。
func parseStatus(out string) []statusEntry {
	parts := strings.Split(out, "\x00")
	entries := []statusEntry{}
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if len(rec) < 4 {
			continue
		}
		xy := rec[:2]
		entries = append(entries, statusEntry{path: rec[3:], status: statusCode(xy), untracked: xy == "??"})
		if strings.ContainsAny(xy, "RC") {
			i++ // 原路径：面板上只显示改名后的名字
		}
	}
	return entries
}

// statusCode 是 porcelain 的两位状态去掉空格：取第一个非空的位（暂存区在前），RD 这类按 R 显示。
func statusCode(xy string) string {
	if xy == "??" {
		return "??"
	}
	for _, ch := range xy {
		if ch != ' ' {
			return string(ch)
		}
	}
	return "?"
}

// splitDiff 把 git diff 的输出按文件切开，每段不含 "diff --git" 那一行（页面自己据此画出路径）。
func splitDiff(out string) []string {
	segments := []string{}
	var cur strings.Builder
	in := false
	for _, line := range strings.SplitAfter(out, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if in {
				segments = append(segments, cur.String())
			}
			cur.Reset()
			in = true
			continue
		}
		if in {
			cur.WriteString(line)
		}
	}
	if in {
		segments = append(segments, cur.String())
	}
	return segments
}

type numstatEntry struct {
	Path           string
	Added, Deleted int
}

// parseNumstat 解 numstat -z：每条是 "增\t删\t路径" 后跟 NUL；重命名的那条路径为空，后面跟原路径、新路径。
func parseNumstat(out string) []numstatEntry {
	parts := strings.Split(out, "\x00")
	entries := []numstatEntry{}
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if rec == "" {
			continue
		}
		fields := strings.SplitN(rec, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		e := numstatEntry{Added: atoi(fields[0]), Deleted: atoi(fields[1]), Path: fields[2]}
		if e.Path == "" && i+2 < len(parts) {
			i += 2 // 原路径、新路径：按新路径索引
			e.Path = parts[i]
		}
		entries = append(entries, e)
	}
	return entries
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s) // 二进制文件是 "-"
	return n
}

// untrackedDiff 给未跟踪的文件造一段“新增”的 diff：每行前加 +。二进制、超过 256KB、不是普通文件
// （符号链接可能指向仓库外，不跟出去）只写一行说明。
func untrackedDiff(dir, name string) (string, int) {
	info, err := os.Lstat(filepath.Join(dir, name))
	if err != nil || !info.Mode().IsRegular() {
		return "（不是普通文件，不显示内容）\n", 0
	}
	if info.Size() > maxDiffFile {
		return "（文件超过 256 KB，不显示内容）\n", 0
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "（读不出来：" + err.Error() + "）\n", 0
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "（二进制文件，不显示内容）\n", 0
	}
	text := strings.TrimSuffix(string(data), "\n")
	var b strings.Builder
	lines := 0
	if text != "" {
		for _, line := range strings.Split(text, "\n") {
			b.WriteString("+")
			b.WriteString(line)
			b.WriteString("\n")
			lines++
		}
	}
	return b.String(), lines
}
