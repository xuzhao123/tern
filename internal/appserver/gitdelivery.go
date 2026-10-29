package appserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/protocol"
)

type gitPathState struct {
	Path     string `json:"path"`
	Index    string `json:"index"`
	Worktree string `json:"worktree"`
	Hash     string `json:"hash"`
	Notice   string `json:"notice,omitempty"`
}
type gitDeliveryState struct {
	Directory     string         `json:"directory"`
	Head          string         `json:"head"`
	Branch        string         `json:"branch"`
	IndexHash     string         `json:"index_hash"`
	Version       string         `json:"version"`
	Author        string         `json:"author"`
	Files         []gitPathState `json:"files"`
	CommitBlocked string         `json:"commit_blocked,omitempty"`
	Conflicts     bool           `json:"conflicts"`
	indexPath     string
	indexData     []byte
	status        string
}
type gitActionRecord struct {
	ID          string `json:"id"`
	User        string `json:"user"`
	ThreadID    string `json:"thread_id"`
	Directory   string `json:"directory"`
	Action      string `json:"action"`
	State       string `json:"state"`
	BeforeIndex string `json:"before_index"`
	AfterIndex  string `json:"after_index,omitempty"`
	Head        string `json:"head"`
	Branch      string `json:"branch"`
	Commit      string `json:"commit,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func gitEnv(index string) []string {
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
func (c *conn) deliveryGit(dir, index, input string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	argv := append([]string{"--literal-pathspecs", "-c", "core.fsmonitor=false", "-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)
	if !slices.Contains(args, "core.hooksPath") {
		argv = append([]string{"-c", "core.hooksPath=/dev/null"}, argv...)
	}
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Dir = dir
	cmd.Env = gitEnv(index)
	if slices.Contains(args, "status") {
		env, cleanup, err := c.safeGitReadEnv(dir, index)
		if err != nil {
			return "", err
		}
		defer cleanup()
		cmd.Env = env
	}

	cmd.Stdin = strings.NewReader(input)
	out := &diffOutput{limit: 16 << 20}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), err)
	}
	if out.truncated {
		return "", fmt.Errorf("Git输出超过16MiB，无法完整核对")
	}
	return out.data.String(), nil
}
func hashBytes(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func readIndex(path string) ([]byte, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return nil, fmt.Errorf("index不是普通文件或超过32MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if len(data) > 32<<20 {
		return nil, fmt.Errorf("index过大")
	}
	return data, err
}
func (c *conn) gitDeliverySnapshot(dir string) (*gitDeliveryState, error) {
	top, err := c.deliveryGit(dir, "", "", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	actual, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	top = strings.TrimSpace(top)
	if top != actual {
		return nil, fmt.Errorf("网页Git写操作要求登记完整仓库根目录")
	}
	state := &gitDeliveryState{Directory: dir, Files: []gitPathState{}}
	branch, _ := c.deliveryGit(dir, "", "", "symbolic-ref", "-q", "HEAD")
	state.Branch = strings.TrimSpace(branch)
	head, _ := c.deliveryGit(dir, "", "", "rev-parse", "--verify", "HEAD^{commit}")
	state.Head = strings.TrimSpace(head)
	index, err := c.deliveryGit(dir, "", "", "rev-parse", "--git-path", "index")
	if err != nil {
		return nil, err
	}
	state.indexPath = strings.TrimSpace(index)
	if !filepath.IsAbs(state.indexPath) {
		state.indexPath = filepath.Join(dir, state.indexPath)
	}
	state.indexData, err = readIndex(state.indexPath)
	if err != nil {
		return nil, err
	}
	state.IndexHash = hashBytes(state.indexData)
	status, err := c.deliveryGit(dir, "", "", "-c", "status.renames=false", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=dirty")
	if err != nil {
		return nil, err
	}
	state.status = status
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	positions := map[string]int{}
	var totalBytes int64
	for _, entry := range strings.Split(status, "\x00") {
		if len(entry) < 4 {
			continue
		}
		name := entry[3:]
		if !fs.ValidPath(name) {
			return nil, fmt.Errorf("Git返回了不可访问路径")
		}
		if at, ok := positions[name]; ok {
			if entry[:2] == "??" {
				state.Files[at].Worktree = "?"
			}
			continue
		}
		positions[name] = len(state.Files)
		f := gitPathState{Path: name, Index: entry[:1], Worktree: entry[1:2]}
		if strings.Contains(entry[:2], "U") || entry[:2] == "AA" || entry[:2] == "DD" {
			state.Conflicts = true
		}
		if len(state.Files) >= 500 {
			return nil, fmt.Errorf("变更文件超过500项，网页写操作不支持")
		}
		info, e := root.Lstat(name)
		if os.IsNotExist(e) {
			f.Hash = "deleted"
		} else if e != nil {
			return nil, fmt.Errorf("无法核对文件 %s", name)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, e := root.Readlink(name)
			if e != nil {
				return nil, e
			}
			f.Hash = "link:" + hashBytes([]byte(target))
		} else if !info.Mode().IsRegular() || info.Size() > 64<<20 {
			f.Notice = "目录、特殊文件或超过64MiB，不能在网页暂存"
			f.Hash = fmt.Sprintf("metadata:%d:%d", info.Size(), info.ModTime().UnixNano())
		} else {
			totalBytes += info.Size()
			if totalBytes > 128<<20 {
				return nil, fmt.Errorf("变更文件合计超过128MiB，请使用原Git流程交付")
			}
			file, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if e != nil {
				return nil, e
			}
			sum := sha256.New()
			_, e = io.Copy(sum, io.LimitReader(file, (64<<20)+1))
			after, statErr := file.Stat()
			file.Close()
			if e != nil || statErr != nil || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
				return nil, fmt.Errorf("核对期间文件变化：%s", name)
			}
			f.Hash = fmt.Sprintf("%x:%o", sum.Sum(nil), info.Mode().Perm())
		}
		state.Files = append(state.Files, f)
	}
	if state.Conflicts {
		state.CommitBlocked = "仓库存在未解决的冲突"
	}
	if !strings.HasPrefix(state.Branch, "refs/heads/") {
		state.CommitBlocked = "当前不是命名分支，请先在Git中选择分支"
	}
	author, e := c.deliveryGit(dir, "", "", "var", "GIT_AUTHOR_IDENT")
	if e != nil {
		state.CommitBlocked = "Git作者身份未配置"
	}
	// git var includes the current timestamp; identity, not the clock, belongs in the review version.
	state.Author = strings.TrimSpace(author)
	if end := strings.LastIndex(state.Author, ">"); end >= 0 {
		state.Author = state.Author[:end+1]
	}
	signed, _ := c.deliveryGit(dir, "", "", "config", "--bool", "--get", "commit.gpgSign")
	if strings.TrimSpace(signed) == "true" {
		state.CommitBlocked = "仓库要求签名，请通过原Git流程提交；网页不会跳过签名"
	}
	hooks, _ := c.deliveryGit(dir, "", "", "config", "--path", "--get", "core.hooksPath")
	if strings.TrimSpace(hooks) == "" {
		hooks, _ = c.deliveryGit(dir, "", "", "rev-parse", "--git-path", "hooks")
	}
	hooks = strings.TrimSpace(hooks)
	if !filepath.IsAbs(hooks) {
		hooks = filepath.Join(dir, hooks)
	}
	entries, _ := os.ReadDir(hooks)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sample") {
			continue
		}
		info, err := os.Stat(filepath.Join(hooks, entry.Name()))
		if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			state.CommitBlocked = "仓库配置了可执行Git钩子，请通过原Git流程提交；网页不会跳过钩子"
		}
	}
	// 标记会执行自定义转换的文件；只读地查询属性，不运行clean/process脚本。
	if len(state.Files) > 0 {
		args := []string{"check-attr", "-z", "filter", "text", "eol", "working-tree-encoding", "ident", "--"}
		for _, f := range state.Files {
			args = append(args, f.Path)
		}
		attrs, err := c.deliveryGit(dir, "", "", args...)
		if err != nil {
			return nil, err
		}
		parts := strings.Split(attrs, "\x00")
		autocrlf, _ := c.deliveryGit(dir, "", "", "config", "--get", "core.autocrlf")
		if strings.TrimSpace(autocrlf) == "true" || strings.TrimSpace(autocrlf) == "input" {
			for i := range state.Files {
				state.Files[i].Notice = "core.autocrlf行尾转换需要通过原Git流程暂存"
			}
		}
		for i := 0; i+2 < len(parts); i += 3 {
			if parts[i+2] != "unspecified" && parts[i+2] != "unset" {
				if at, ok := positions[parts[i]]; ok {
					state.Files[at].Notice = "Git属性" + parts[i+1] + "转换需要通过原Git流程暂存"
				}
			}
		}
	}
	endIndex, err := readIndex(state.indexPath)
	if err != nil || hashBytes(endIndex) != state.IndexHash {
		return nil, fmt.Errorf("读取期间index改变，请刷新")
	}
	endHead, _ := c.deliveryGit(dir, "", "", "rev-parse", "--verify", "HEAD^{commit}")
	endBranch, _ := c.deliveryGit(dir, "", "", "symbolic-ref", "-q", "HEAD")
	if strings.TrimSpace(endHead) != state.Head || strings.TrimSpace(endBranch) != state.Branch {
		return nil, fmt.Errorf("读取期间分支改变，请刷新")
	}
	encoded, _ := json.Marshal(state)
	state.Version = hashBytes(encoded)
	return state, nil
}
func (c *conn) deliveryTarget(id string) (*run, string, *protocol.Error) {
	r, ferr := c.s.threadOf(id)
	if ferr != nil {
		return nil, "", ferr
	}
	r.mu.Lock()
	dir := r.workDir()
	r.mu.Unlock()
	if dir == "" {
		return nil, "", bad(400, "任务没有项目目录")
	}
	return r, dir, nil
}

// 不调用add：即使仓库在预览后新增filter配置，暂存也只写入读取到的文件字节。
func (c *conn) stageRawPaths(dir, index string, paths []string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	filemode, _ := c.deliveryGit(dir, "", "", "config", "--bool", "--get", "core.filemode")
	for _, name := range paths {
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			if _, err := c.deliveryGit(dir, index, "", "update-index", "--force-remove", "--", name); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		mode, data := "100644", []byte(nil)
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := root.Readlink(name)
			if err != nil {
				return err
			}
			mode, data = "120000", []byte(target)
		} else {
			f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			stat, err := f.Stat()
			if err != nil || !stat.Mode().IsRegular() || stat.Size() > 64<<20 {
				f.Close()
				return fmt.Errorf("文件已变为不支持的类型或大小：%s", name)
			}
			data, err = io.ReadAll(io.LimitReader(f, (64<<20)+1))
			f.Close()
			if err != nil || len(data) > 64<<20 {
				return fmt.Errorf("无法完整读取文件：%s", name)
			}
			if strings.TrimSpace(filemode) != "false" && stat.Mode()&0100 != 0 {
				mode = "100755"
			} else if strings.TrimSpace(filemode) == "false" {
				old, err := c.deliveryGit(dir, index, "", "ls-files", "--stage", "--", name)
				if err != nil {
					return err
				}
				if strings.HasPrefix(old, "100755 ") {
					mode = "100755"
				}
			}
		}
		oid, err := c.deliveryGit(dir, index, string(data), "hash-object", "-w", "--no-filters", "--stdin")
		if err != nil {
			return err
		}
		if _, err := c.deliveryGit(dir, index, "", "update-index", "--add", "--cacheinfo", mode, strings.TrimSpace(oid), name); err != nil {
			return err
		}
	}
	return nil
}
func (c *conn) gitState(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	_, dir, ferr := c.deliveryTarget(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	state, err := c.gitDeliverySnapshot(dir)
	if err != nil {
		return nil, bad(409, err.Error())
	}
	return state, nil
}
func (s *Server) gitActionPath(user, id string) string {
	return filepath.Join(s.dir, ".data", "git-actions", hashBytes([]byte(user+"\x00"+id))+".json")
}
func (s *Server) saveGitAction(record gitActionRecord) error {
	path := s.gitActionPath(record.User, record.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return privateJSON(path, record)
}
func (c *conn) gitAct(m protocol.Message) (any, *protocol.Error) {
	return c.requestOnce(m, (*conn).gitActOnce)
}
func (c *conn) gitActOnce(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID  string   `json:"threadId"`
		Action    string   `json:"action"`
		Version   string   `json:"version"`
		Paths     []string `json:"paths"`
		Message   string   `json:"message"`
		Confirmed bool     `json:"confirmed"`
		RequestID string   `json:"request_id"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	if !requestIDPattern.MatchString(p.RequestID) || !p.Confirmed || !slices.Contains([]string{"stage", "unstage", "commit"}, p.Action) || len(p.Paths) > 500 {
		return nil, bad(400, "需要明确确认、有效操作和request_id")
	}
	r, dir, ferr := c.deliveryTarget(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.mu.Lock()
	busy := !r.Done || r.shellBusy
	r.mu.Unlock()
	if busy {
		return nil, bad(409, "当前任务正在执行，请结束当前轮后操作Git")
	}
	c.s.gitMu.Lock()
	defer c.s.gitMu.Unlock()
	state, err := c.gitDeliverySnapshot(dir)
	if err != nil {
		return nil, bad(409, err.Error())
	}
	if state.Version != p.Version {
		return nil, bad(409, "Git状态或文件已变化，请重新检查范围")
	}
	if state.Conflicts {
		return nil, bad(409, "存在冲突，请先解决")
	}
	if p.Action == "commit" && (strings.TrimSpace(p.Message) == "" || len(p.Message) > 10000 || state.CommitBlocked != "") {
		return nil, bad(409, "不能提交："+state.CommitBlocked+"（需要非空且不超过10000字节的说明）")
	}
	selected := map[string]bool{}
	for _, name := range p.Paths {
		if selected[name] {
			return nil, bad(400, "文件路径重复")
		}
		selected[name] = true
		at := slices.IndexFunc(state.Files, func(f gitPathState) bool { return f.Path == name })
		if at < 0 || state.Files[at].Notice != "" {
			return nil, bad(409, "文件不在可操作范围："+name)
		}
	}
	if p.Action != "commit" && len(selected) == 0 {
		return nil, bad(400, "请选择文件")
	}
	lock, err := os.OpenFile(state.indexPath+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, bad(409, "Git index已被其他操作锁定，未删除任何锁")
	}
	ownLock := true
	defer func() {
		lock.Close()
		if ownLock {
			os.Remove(state.indexPath + ".lock")
		}
	}()
	temp := state.indexPath + ".tern-" + uuid.NewString()
	defer os.Remove(temp)
	defer os.Remove(temp + ".lock")
	if len(state.indexData) > 0 {
		if err := os.WriteFile(temp, state.indexData, 0600); err != nil {
			return nil, bad(500, err.Error())
		}
	}
	auditRecord := audit.Record{Actor: "user", User: userName(m), Event: "user_git_" + p.Action, Conversation: r.ID, Target: dir}
	auditArgs, _ := json.Marshal(p)
	auditRecord.Args(string(auditArgs))
	if err := audit.Write(c.ctx, auditRecord); err != nil {
		return nil, bad(500, "审计失败，没有改Git状态")
	}
	record := gitActionRecord{ID: p.RequestID, User: userName(m), ThreadID: r.ID, Directory: dir, Action: p.Action, State: "preparing", BeforeIndex: state.IndexHash, Head: state.Head, Branch: state.Branch}
	if err := c.s.saveGitAction(record); err != nil {
		return nil, bad(500, "操作日志未保存，没有修改Git")
	}
	fail := func(err error) (any, *protocol.Error) {
		record.State, record.Reason = "rejected", err.Error()
		c.s.saveGitAction(record)
		return nil, bad(409, err.Error())
	}
	if p.Action == "stage" {
		if err := c.stageRawPaths(dir, temp, p.Paths); err != nil {
			return fail(err)
		}
	} else if p.Action == "unstage" {
		args := []string{"reset", "-q", "HEAD", "--"}
		if state.Head == "" {
			args = []string{"update-index", "--force-remove", "--"}
		}
		args = append(args, p.Paths...)
		if _, err := c.deliveryGit(dir, temp, "", args...); err != nil {
			return fail(err)
		}
	}
	// temp index准备完后再核对真实index及工作文件；此时原生index.lock仍由本操作持有。
	latest, err := c.gitDeliverySnapshot(dir)
	if err != nil {
		return fail(err)
	}
	if latest.Version != state.Version {
		return fail(fmt.Errorf("准备期间仓库发生变化，真实index未替换"))
	}
	if p.Action == "commit" {
		hasStaged := false
		for _, f := range state.Files {
			if f.Index != " " && f.Index != "?" {
				hasStaged = true
				if f.Notice != "" {
					return fail(fmt.Errorf("已暂存文件不能完整核对：%s（%s）", f.Path, f.Notice))
				}
			}
		}
		if !hasStaged {
			return fail(fmt.Errorf("没有已暂存的变化"))
		}
		tree, err := c.deliveryGit(dir, temp, "", "write-tree")
		if err != nil {
			return fail(err)
		}
		tree = strings.TrimSpace(tree)
		oldTree := ""
		if state.Head != "" {
			oldTree, _ = c.deliveryGit(dir, "", "", "rev-parse", state.Head+"^{tree}")
		}
		if tree == strings.TrimSpace(oldTree) || len(state.Files) == 0 {
			return fail(fmt.Errorf("没有已暂存的变化"))
		}
		args := []string{"-c", "commit.gpgSign=false", "commit-tree", tree}
		if state.Head != "" {
			args = append(args, "-p", state.Head)
		}
		args = append(args, "-F", "-")
		oid, err := c.deliveryGit(dir, temp, p.Message+"\n", args...)
		if err != nil {
			return fail(err)
		}
		record.Commit = strings.TrimSpace(oid)
		record.State = "prepared"
		if err := c.s.saveGitAction(record); err != nil {
			return fail(err)
		}
		old := state.Head
		if old == "" {
			old = strings.Repeat("0", len(record.Commit))
		}
		if _, err := c.deliveryGit(dir, "", "", "-c", "core.hooksPath=/dev/null", "update-ref", "--no-deref", "-m", "tern web: "+strings.Split(p.Message, "\n")[0], state.Branch, record.Commit, old); err != nil {
			record.State, record.Reason = "unknown", err.Error()
			c.s.saveGitAction(record)
			return record, nil
		}
	} else {
		after, err := readIndex(temp)
		if err != nil {
			return fail(err)
		}
		record.AfterIndex, record.State = hashBytes(after), "prepared"
		if err := c.s.saveGitAction(record); err != nil {
			return fail(err)
		}
		if _, err := lock.Write(after); err != nil {
			return fail(err)
		}
		if err := lock.Sync(); err != nil {
			return fail(err)
		}
		if err := lock.Close(); err != nil {
			return fail(err)
		}
		if err := os.Rename(state.indexPath+".lock", state.indexPath); err != nil {
			return fail(err)
		}
		ownLock = false
	}
	record.State = "completed"
	if err := c.s.saveGitAction(record); err != nil {
		record.State, record.Reason = "unknown", "Git动作已执行，结果日志保存失败，请查询原操作"
	}
	return record, nil
}
func (c *conn) gitOperation(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID  string `json:"threadId"`
		RequestID string `json:"request_id"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	if !requestIDPattern.MatchString(p.RequestID) {
		return nil, bad(400, "请求ID无效")
	}
	raw, err := os.ReadFile(c.s.gitActionPath(userName(m), p.RequestID))
	if os.IsNotExist(err) {
		return map[string]string{"state": "not_found"}, nil
	}
	var record gitActionRecord
	if err != nil || json.Unmarshal(raw, &record) != nil {
		return nil, bad(500, "操作日志无法读取")
	}
	if record.ThreadID != p.ThreadID {
		return nil, bad(403, "操作不属于当前任务")
	}
	if record.State == "completed" || record.State == "rejected" {
		return record, nil
	}
	if record.Commit != "" {
		tip, e := c.deliveryGit(record.Directory, "", "", "rev-parse", "--verify", record.Branch)
		if e == nil {
			if strings.TrimSpace(tip) == record.Commit {
				record.State = "completed"
			} else if _, e := c.deliveryGit(record.Directory, "", "", "merge-base", "--is-ancestor", record.Commit, strings.TrimSpace(tip)); e == nil {
				record.State = "completed"
			}
		}
	} else if record.AfterIndex != "" {
		state, e := c.gitDeliverySnapshot(record.Directory)
		if e == nil && state.IndexHash == record.AfterIndex {
			record.State = "completed"
		}
	}
	if record.State != "completed" {
		record.State = "unknown"
		record.Reason = "当前仓库不足以证明原动作结果，不会自动重做"
	}
	return record, nil
}
