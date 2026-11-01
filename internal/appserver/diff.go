package appserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/xuzhao123/tern/internal/protocol"
)

// workspace/diff：这条对话的工作区相对 HEAD 的改动，网页端的“改动”面板据此显示整份 diff。
// 已跟踪的文件取 git diff HEAD，未跟踪的文件按“新增”给出内容；命令只读，带 GIT_OPTIONAL_LOCKS=0 不碰仓库里的锁。
// 文件内容由模型或 git 提供，页面只用 textContent 显示。
const (
	maxDiffFile  = 256 << 10 // 未跟踪的文件超过它就只写一行说明
	maxDiffTotal = 1 << 20   // 所有 diff 加起来的预算，超过后后面的文件只给路径、状态、行数
	maxDiffFiles = 500
)

type diffFile struct {
	Path       string `json:"path"`
	Status     string `json:"status"` // porcelain 的两位状态去掉空格：M、A、D、R、??
	Added      int    `json:"added"`
	Deleted    int    `json:"deleted"`
	Diff       string `json:"diff"` // 从 "diff --git" 那行之后开始；未跟踪文件每行前加 +；被截断时为空
	OldPath    string `json:"old_path"`
	Binary     bool   `json:"binary"`
	Truncated  bool   `json:"truncated"`
	Notice     string `json:"notice"`
	Version    string `json:"version"`
	Conflicted bool   `json:"conflicted"`
	recreated  bool
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
	cmd := exec.CommandContext(c.ctx, "git", append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "log.showSignature=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnv("")
	if slices.Contains(args, "diff") {
		env, cleanup, err := c.safeGitReadEnv(dir, "")
		if err != nil {
			return "", err
		}
		defer cleanup()
		cmd.Env = env
	}
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

// 空树可由 Git 按仓库的对象格式计算，无需写入对象或拼接两份 patch。
func (c *conn) diffBase(dir string) (string, string, error) {
	if oid, err := c.git(dir, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"); err == nil {
		return "HEAD", strings.TrimSpace(oid), nil
	}
	ref, err := c.git(dir, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return "", "", errors.New("无法读取 HEAD，且不是尚无提交的分支")
	}
	refs, err := c.git(dir, "for-each-ref", "--format=%(refname)", strings.TrimSpace(ref))
	if err != nil {
		return "", "", err
	}
	for _, name := range strings.Fields(refs) {
		if name == strings.TrimSpace(ref) {
			return "", "", errors.New("HEAD 引用存在，但无法解析为提交")
		}
	}
	oid, err := c.git(dir, "hash-object", "-t", "tree", "--stdin")
	return "EMPTY_TREE", strings.TrimSpace(oid), err
}

// 只读预览有明确输出上限；即使文件很大也不将全部 patch 堆入内存。
type diffOutput struct {
	data      bytes.Buffer
	limit     int
	truncated bool
}

func (w *diffOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := w.limit - w.data.Len(); len(p) > remaining {
		p = p[:remaining]
		w.truncated = true
	}
	_, _ = w.data.Write(p)
	return n, nil
}
func diffArgs(compare []string, format []string, paths ...string) []string {
	args := []string{"--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--find-renames", "--src-prefix=a/", "--dst-prefix=b/"}
	args = append(args, format...)
	args = append(args, compare...)
	args = append(args, "--")
	return append(args, paths...)
}
func splitDiff(text string) []string {
	segments := []string{}
	var cur strings.Builder
	started := false
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "diff --cc ") || strings.HasPrefix(line, "diff --combined ") {
			if started {
				segments = append(segments, cur.String())
			}
			cur.Reset()
			started = true
			continue
		}
		if started {
			cur.WriteString(line)
		}
	}
	if started {
		segments = append(segments, cur.String())
	}
	return segments
}
func (c *conn) diffPatch(dir string, compare []string, file diffFile, limit int) (diffFile, error) {
	paths := []string{file.Path}
	if file.OldPath != "" {
		paths = append(paths, file.OldPath)
	}
	cmd := exec.CommandContext(c.ctx, "git", diffArgs(compare, []string{"--numstat", "--patch", "-z"}, paths...)...)
	cmd.Dir = dir
	env, cleanup, err := c.safeGitReadEnv(dir, "")
	if err != nil {
		return file, err
	}
	defer cleanup()
	cmd.Env = env
	out := &diffOutput{limit: limit}
	var stderr strings.Builder
	cmd.Stdout = out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return file, fmt.Errorf("git diff 失败：%s (%w)", strings.TrimSpace(stderr.String()), err)
	}
	if out.truncated {
		file.Truncated = true
		return file, nil
	}
	text := out.data.String()
	at := strings.Index(text, "\x00\x00")
	if file.Conflicted {
		patch := text
		if at >= 0 {
			patch = text[at+2:]
		}
		file.Diff = strings.Join(splitDiff(patch), "\n")
		file.Added, file.Deleted = 0, 0
		return file, nil
	}
	if at < 0 {
		return file, errors.New("比较期间文件状态变化，请刷新改动")
	}
	stats, patches := parseNumstat(text[:at+1]), splitDiff(text[at+2:])
	if len(stats) != len(patches) {
		return file, errors.New("文件行统计与差异不一致，请刷新改动")
	}
	for i, stat := range stats {
		if stat.Path == file.Path && stat.OldPath == file.OldPath {
			file.Diff, file.Added, file.Deleted, file.Binary = patches[i], stat.Added, stat.Deleted, stat.Binary
			return file, nil
		}
	}
	return file, errors.New("比较期间文件路径或重命名状态发生变化，请刷新改动")
}

func (c *conn) workspaceDiff(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Scope    string `json:"scope"`
		Base     string `json:"base"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if input.Scope == "" {
		input.Scope = "workspace"
	}
	if input.Scope != "workspace" && input.Scope != "unstaged" && input.Scope != "staged" && input.Scope != "branch" {
		return nil, bad(400, "不支持的比较范围")
	}
	if input.Scope != "branch" && input.Base != "" {
		return nil, bad(400, "只有分支比较可以指定基准")
	}
	if input.Scope == "branch" && (strings.TrimSpace(input.Base) == "" || len(input.Base) > 255 || strings.ContainsAny(input.Base, "\x00\n\r")) {
		return nil, bad(400, "请选择有效的基准分支或提交")
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
	headOID, _ := c.git(dir, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	headOID = strings.TrimSpace(headOID)
	head, _ := c.git(dir, "log", "-1", "--format=%h %s")
	branch, _ := c.git(dir, "symbolic-ref", "--short", "-q", "HEAD")
	baseRef, baseOID, baseTip, target, targetOID := "INDEX", "", "", "worktree", ""
	var compare []string
	if input.Scope == "workspace" || input.Scope == "staged" {
		var err error
		baseRef, baseOID, err = c.diffBase(dir)
		if err != nil {
			return nil, bad(500, "比较基准不可用："+err.Error())
		}
		compare = []string{baseOID}
		if input.Scope == "staged" {
			compare = []string{"--cached", baseOID}
			target = "index"
		}
	} else if input.Scope == "branch" {
		if headOID == "" {
			return nil, bad(400, "当前分支尚无可比较的 HEAD 提交")
		}
		oid, err := c.git(dir, "rev-parse", "--verify", "--end-of-options", input.Base+"^{commit}")
		if err != nil {
			return nil, bad(400, "基准不是有效提交："+input.Base)
		}
		baseTip = strings.TrimSpace(oid)
		ancestor, err := c.git(dir, "merge-base", baseTip, headOID)
		if err != nil {
			return nil, bad(400, "基准与当前分支没有可用的共同祖先")
		}
		baseRef, baseOID, target, targetOID = input.Base, strings.TrimSpace(ancestor), "HEAD", headOID
		compare = []string{baseOID, headOID}
	}
	status, err := c.git(dir, diffArgs(compare, []string{"--name-status", "-z"})...)
	if err != nil {
		return nil, bad(500, "读取比较文件失败："+err.Error())
	}
	numstat, err := c.git(dir, diffArgs(compare, []string{"--numstat", "-z"})...)
	if err != nil {
		return nil, bad(500, "读取改动统计失败："+err.Error())
	}
	stats := map[string]numstatEntry{}
	for _, e := range parseNumstat(numstat) {
		stats[e.Path] = e
	}
	files := parseDiffNames(status)
	deleted := map[string]int{}
	for i, f := range files {
		if f.Status == "D" {
			deleted[f.Path] = i
		}
	}
	conflicts := map[string]bool{}
	if input.Scope != "branch" {
		unmerged, err := c.git(dir, "ls-files", "--unmerged", "-z")
		if err != nil {
			return nil, bad(500, "读取合并状态失败："+err.Error())
		}
		for _, record := range strings.Split(unmerged, "\x00") {
			if parts := strings.SplitN(record, "\t", 2); len(parts) == 2 {
				conflicts[parts[1]] = true
			}
		}
	}
	if input.Scope == "workspace" || input.Scope == "unstaged" {
		untracked, err := c.git(dir, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, bad(500, "读取未跟踪文件失败："+err.Error())
		}
		for _, name := range strings.Split(untracked, "\x00") {
			if name != "" {
				if i, ok := deleted[name]; ok && input.Scope == "workspace" {
					files[i].Status = "M"
					files[i].recreated = true
					continue
				}
				files = append(files, diffFile{Path: name, Status: "??"})
			}
		}
	}
	totalFiles := len(files)
	truncated := totalFiles > maxDiffFiles
	if truncated {
		files = files[:maxDiffFiles]
	}
	filtered := map[string]bool{}
	filterNotice := ""
	if input.Scope == "workspace" || input.Scope == "unstaged" {
		configured, _ := c.git(dir, "config", "--name-only", "--get-regexp", "^filter\\.")
		if strings.TrimSpace(configured) != "" {
			filterNotice = "自定义Git过滤器未执行；这类文件的转换结果未核对，需通过原Git流程检查。"
		}
		if len(files) > 0 {
			args := []string{"check-attr", "-z", "filter", "--"}
			for _, f := range files {
				args = append(args, f.Path)
			}
			attrs, e := c.git(dir, args...)
			if e != nil {
				return nil, bad(500, "无法核对Git文件属性")
			}
			parts := strings.Split(attrs, "\x00")
			for i := 0; i+2 < len(parts); i += 3 {
				if parts[i+2] != "unspecified" && parts[i+2] != "unset" {
					filtered[parts[i]] = true
					filterNotice = "自定义Git过滤器未执行；这类文件的转换结果未核对，需通过原Git流程检查。"
				}
			}
		}
	}
	used := 0
	for i := range files {
		f := &files[i]
		f.Conflicted = conflicts[f.Path]
		if st, ok := stats[f.Path]; ok && f.Status != "??" {
			f.Added, f.Deleted, f.Binary = st.Added, st.Deleted, st.Binary
		}
		if filtered[f.Path] && f.Status != "??" {
			f.Added, f.Deleted, f.Diff = 0, 0, ""
			f.Notice = "自定义Git过滤器未执行，此文件的转换差异不能在网页核对"
		} else if used >= maxDiffTotal {
			f.Truncated = true
			f.Notice = "超过整份预览大小上限，未读取此文件内容"
		} else if f.recreated {
			*f, err = c.recreatedDiff(dir, baseOID, *f, min(maxDiffFile, maxDiffTotal-used))
			if err != nil {
				return nil, bad(500, err.Error())
			}
			if f.Status == "" {
				totalFiles--
				continue
			}
		} else if f.Status == "??" {
			f.Diff, f.Added, f.Binary, f.Truncated, f.Notice = untrackedDiff(dir, f.Path)
			if used+len(f.Diff) > maxDiffTotal {
				f.Diff = ""
				f.Truncated = true
				f.Notice = "超过整份预览大小上限"
			}
		} else {
			limit := min(maxDiffFile, maxDiffTotal-used)
			*f, err = c.diffPatch(dir, compare, *f, limit)
			if err != nil {
				return nil, bad(500, err.Error())
			}
			if f.Truncated {
				f.Notice = "改动超过预览大小上限，未返回部分 patch，避免产生不完整行号"
			}
		}
		if f.Conflicted {
			f.Notice = strings.TrimSpace(f.Notice + " 文件存在合并冲突，不提供合并行的定位反馈")
		}
		used += len(f.Diff)
		truncated = truncated || f.Truncated
		view, _ := json.Marshal(f)
		f.Version = fmt.Sprintf("%x", sha256.Sum256(view))
	}
	visible := files[:0]
	for _, f := range files {
		if f.Status != "" {
			visible = append(visible, f)
		}
	}
	files = visible
	result := map[string]any{"dir": dir, "head": strings.TrimSpace(head), "head_oid": headOID, "branch": strings.TrimSpace(branch), "scope": input.Scope, "base_ref": baseRef, "base_oid": baseOID, "base_tip_oid": baseTip, "target": target, "target_oid": targetOID, "files": files, "truncated": truncated, "file_limit": maxDiffFiles, "total_files": totalFiles}
	if filterNotice != "" {
		result["filter_notice"] = filterNotice
	}
	view, _ := json.Marshal(result)
	result["view_version"] = fmt.Sprintf("%x", sha256.Sum256(view))
	return result, nil
}

// --name-status -z 对重命名显式给出两侧路径，不从带引号的 patch 头猜路径。
func parseDiffNames(text string) []diffFile {
	parts := strings.Split(text, "\x00")
	files := []diffFile{}
	seen := map[string]bool{}
	for i := 0; i+1 < len(parts); {
		status, name := parts[i], parts[i+1]
		i += 2
		if status == "" || name == "" {
			continue
		}
		f := diffFile{Path: name, Status: status[:1]}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if i >= len(parts) {
				break
			}
			f.OldPath, f.Path = name, parts[i]
			i++
		}
		if !seen[f.Path] {
			files = append(files, f)
			seen[f.Path] = true
		}
	}
	return files
}

type numstatEntry struct {
	OldPath        string
	Path           string
	Added, Deleted int
	Binary         bool
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
		e := numstatEntry{Added: atoi(fields[0]), Deleted: atoi(fields[1]), Path: fields[2], Binary: fields[0] == "-" || fields[1] == "-"}
		if e.Path == "" && i+2 < len(parts) {
			e.OldPath = parts[i+1]
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
func readDiffFile(dir, name string) ([]byte, os.FileMode, bool, string) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, 0, false, "工作目录无法读取"
	}
	defer root.Close()
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, false, "文件无法读取，或是符号链接/非普通文件"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, false, "不是普通文件，不显示内容"
	}
	if info.Size() > maxDiffFile {
		return nil, info.Mode(), true, "文件超过 256 KiB，不显示内容"
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDiffFile+1))
	if err != nil {
		return nil, info.Mode(), false, "读取失败：" + err.Error()
	}
	if len(data) > maxDiffFile {
		return nil, info.Mode(), true, "文件超过 256 KiB，不显示内容"
	}
	if after, err := file.Stat(); err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, info.Mode(), false, "文件在读取期间发生变化，请刷新后再查看"
	}
	return data, info.Mode(), false, ""
}
func untrackedDiff(dir, name string) (string, int, bool, bool, string) {
	data, _, large, notice := readDiffFile(dir, name)
	if notice != "" {
		return "", 0, false, large, notice
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", 0, true, false, "二进制文件，不显示内容"
	}
	var b strings.Builder
	lines := 0
	if len(data) > 0 {
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			b.WriteString("+")
			b.WriteString(line)
			b.WriteByte('\n')
			lines++
		}
		if data[len(data)-1] != '\n' {
			b.WriteString("\\ No newline at end of file\n")
		}
	}
	return b.String(), lines, false, false, ""
}

// 暂存删除后同名重建在 Git 索引里是 D + ??，整份工作目录比较需合并成基准→磁盘的一份差异。
// no-index 只比较本次限量读取的两个私有临时文件，不更新项目索引或对象库。
func (c *conn) recreatedDiff(dir, base string, f diffFile, limit int) (diffFile, error) {
	f.Added, f.Deleted = 0, 0
	data, mode, large, notice := readDiffFile(dir, f.Path)
	if notice != "" {
		f.Truncated, f.Notice = large, notice
		return f, nil
	}
	blob := &diffOutput{limit: maxDiffFile}
	cmd := exec.CommandContext(c.ctx, "git", "cat-file", "blob", base+":"+f.Path)
	cmd.Dir = dir
	cmd.Stdout = blob
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return f, fmt.Errorf("读取基准文件失败：%s (%w)", stderr.String(), err)
	}
	if blob.truncated {
		f.Truncated = true
		f.Notice = "基准文件超过 256 KiB，不返回部分内容"
		return f, nil
	}
	entry, err := c.git(dir, "--literal-pathspecs", "ls-tree", "-z", base, "--", f.Path)
	if err != nil {
		return f, err
	}
	fields := strings.Fields(strings.SplitN(entry, "\t", 2)[0])
	if len(fields) < 1 || (fields[0] != "100644" && fields[0] != "100755") {
		f.Status = "T"
		f.Notice = "基准文件类型不是普通文件，请核对类型变化"
		return f, nil
	}
	attrs, err := c.git(dir, "check-attr", "-z", "filter", "--", f.Path)
	if err != nil {
		return f, err
	}
	parts := strings.Split(attrs, "\x00")
	if len(parts) >= 3 && parts[2] != "unspecified" && parts[2] != "unset" {
		f.Notice = "重建文件使用自定义 Git 过滤器，网页未执行该转换，内容尚未核对"
		return f, nil
	}
	temp, err := os.MkdirTemp("", "tern-diff-")
	if err != nil {
		return f, err
	}
	defer os.RemoveAll(temp)
	objects := filepath.Join(temp, "objects")
	if err := os.Mkdir(objects, 0700); err != nil {
		return f, err
	}
	original, err := c.git(dir, "rev-parse", "--git-path", "objects")
	if err != nil {
		return f, err
	}
	original = strings.TrimSpace(original)
	if !filepath.IsAbs(original) {
		original = filepath.Join(dir, original)
	}
	env, cleanup, err := c.safeGitReadEnv(dir, "")
	if err != nil {
		return f, err
	}
	defer cleanup()
	env = append(env, "GIT_OBJECT_DIRECTORY="+objects, "GIT_ALTERNATE_OBJECT_DIRECTORIES="+strconv.Quote(original))
	// Git 自己按原路径执行内置 EOL/编码归一化；新对象只落在私有临时目录。
	cmd = exec.CommandContext(c.ctx, "git", "hash-object", "-w", "--path="+f.Path, "--stdin")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(data)
	var hash strings.Builder
	stderr.Reset()
	cmd.Stdout = &hash
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return f, fmt.Errorf("Git 文本归一化失败：%s (%w)", stderr.String(), err)
	}
	normalized := &diffOutput{limit: maxDiffFile}
	stderr.Reset()
	cmd = exec.CommandContext(c.ctx, "git", "cat-file", "blob", strings.TrimSpace(hash.String()))
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = normalized
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return f, fmt.Errorf("读取归一化文本失败：%s (%w)", stderr.String(), err)
	}
	if normalized.truncated {
		f.Truncated = true
		f.Notice = "归一化文本超过预览上限"
		return f, nil
	}
	data = normalized.data.Bytes()
	oldMode, newMode := os.FileMode(0600), os.FileMode(0600)
	if fields[0] == "100755" {
		oldMode = 0700
	}
	if mode&0100 != 0 {
		newMode = 0700
	}
	if value, err := c.git(dir, "config", "--bool", "core.filemode"); err == nil && strings.TrimSpace(value) == "false" {
		newMode = oldMode
	}
	if bytes.Equal(blob.data.Bytes(), data) && oldMode == newMode {
		f.Status = ""
		return f, nil
	}
	if f.Binary || bytes.IndexByte(data, 0) >= 0 || bytes.IndexByte(blob.data.Bytes(), 0) >= 0 {
		f.Binary = true
		f.Notice = "二进制文件，不显示内容"
		return f, nil
	}
	left, right := filepath.Join(temp, "before"), filepath.Join(temp, "after")
	if err := os.WriteFile(left, blob.data.Bytes(), oldMode); err != nil {
		return f, err
	}
	if err := os.WriteFile(right, data, newMode); err != nil {
		return f, err
	}
	out := &diffOutput{limit: limit}
	stderr.Reset()
	cmd = exec.CommandContext(c.ctx, "git", "-c", "core.autocrlf=false", "-c", "core.attributesFile=/dev/null", "-c", "core.filemode=true", "diff", "--no-index", "--no-color", "--no-ext-diff", "--no-textconv", "--numstat", "--patch", "-z", "--", left, right)
	cmd.Dir = temp
	cmd.Stdout = out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return f, fmt.Errorf("比较重建文件失败：%s (%w)", stderr.String(), err)
		}
	}
	if out.truncated {
		f.Truncated = true
		f.Notice = "重建文件改动超过预览上限"
		return f, nil
	}
	text := out.data.String()
	at := strings.Index(text, "\x00\x00")
	if at < 0 {
		return f, errors.New("无法解析重建文件的比较结果")
	}
	stats := parseNumstat(text[:at+1])
	if len(stats) != 1 {
		return f, errors.New("重建文件的行统计不一致")
	}
	f.Added, f.Deleted, f.Binary = stats[0].Added, stats[0].Deleted, stats[0].Binary
	patch := text[at+2:]
	lines := strings.SplitAfter(patch, "\n")
	var result strings.Builder
	inHunk := false
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			continue
		}
		if strings.HasPrefix(line, "@@") {
			inHunk = true
		}
		if !inHunk && strings.HasPrefix(line, "--- ") {
			line = "--- " + strconv.Quote("a/"+f.Path) + "\n"
		}
		if !inHunk && strings.HasPrefix(line, "+++ ") {
			line = "+++ " + strconv.Quote("b/"+f.Path) + "\n"
		}
		result.WriteString(line)
	}
	f.Diff = result.String()
	return f, nil
}
