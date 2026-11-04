package appserver

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/protocol"
)

// workspace/files：输入框里敲 @ 时按子序列搜工作区的文件（Codex 的 fuzzyFileSearch）。
// 候选是工作区目录里 git ls-files 的结果（已跟踪的加没被忽略的新文件），在内存里按目录缓存 10 秒：
// 敲字时一个字一个请求，不缓存就要每次都跑一遍 git。只读，带 GIT_OPTIONAL_LOCKS=0（见 git）。
const (
	maxWorkspaceFiles = 20
	workspaceFileTTL  = 10 * time.Second
)

// workspaceFileEntry 是一个工作区目录最近一次的候选文件。
type workspaceFileEntry struct {
	files []string
	at    time.Time
}

// workspace/files：参数给 workspace（新对话选的工作区名字）或 threadId（已有对话，用它快照里工作区目录）之一。
func (c *conn) workspaceFiles(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Workspace string `json:"workspace"`
		ThreadID  string `json:"threadId"`
		Query     string `json:"query"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	dir := ""
	if input.ThreadID != "" {
		r, ferr := c.s.threadOf(input.ThreadID)
		if ferr != nil {
			return nil, ferr
		}
		r.mu.Lock()
		dir = r.workDir()
		r.mu.Unlock()
	} else if input.Workspace != "" {
		s := c.s
		s.hubMu.Lock()
		f, err := s.readSettings()
		s.hubMu.Unlock()
		if err != nil {
			return nil, bad(500, err.Error())
		}
		for _, w := range f.Workspaces {
			if w.Name == input.Workspace {
				dir = w.Path
				break
			}
		}
	}
	if dir == "" {
		return nil, bad(400, "没有工作区")
	}
	if !isGitRepo(dir) {
		return nil, bad(400, "不是 git 仓库，搜不了文件")
	}
	files, ferr := c.workspaceFileCandidates(dir)
	if ferr != nil {
		return nil, ferr
	}
	return map[string]any{"files": matchWorkspaceFiles(files, input.Query)}, nil
}

// workspaceFileCandidates 是工作区目录里的候选文件：git ls-files 的已跟踪文件加没被忽略的新文件，10 秒内复用上一次的结果。
func (c *conn) workspaceFileCandidates(dir string) ([]string, *protocol.Error) {
	s := c.s
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if e, ok := s.fileCache[dir]; ok && time.Since(e.at) < workspaceFileTTL {
		return e.files, nil
	}
	out, err := c.git(dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, bad(500, "git ls-files 失败："+err.Error())
	}
	files := []string{}
	for _, name := range strings.Split(out, "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	if s.fileCache == nil {
		s.fileCache = map[string]workspaceFileEntry{}
	}
	s.fileCache[dir] = workspaceFileEntry{files: files, at: time.Now()}
	return files, nil
}

// matchWorkspaceFiles 按四档打分：文件名以 query 开头、文件名里有连续的 query、路径里有连续的 query、只是子序列（前三档都没有时才用）；
// 同一档里路径短的在前，再按字母序。query 为空时按路径排序给前面的。最多给 maxWorkspaceFiles 个。
func matchWorkspaceFiles(files []string, query string) []string {
	q := strings.ToLower(strings.Join(strings.Fields(query), ""))
	if q == "" {
		sorted := append([]string{}, files...)
		sort.Strings(sorted)
		return sorted[:min(len(sorted), maxWorkspaceFiles)]
	}
	type hit struct {
		path string
		rank int
	}
	hits := []hit{}
	for _, name := range files {
		lower := strings.ToLower(name)
		base := lower
		if i := strings.LastIndex(lower, "/"); i >= 0 {
			base = lower[i+1:]
		}
		rank := -1
		switch {
		case strings.HasPrefix(base, q):
			rank = 0
		case strings.Contains(base, q):
			rank = 1
		case strings.Contains(lower, q):
			rank = 2
		case subsequence(lower, q):
			rank = 3
		}
		if rank >= 0 {
			hits = append(hits, hit{name, rank})
		}
	}
	// 有名字或路径里连续命中的，就不要只是凑巧能拼出来的子序列（搜 perm 不该冒出 spec/README.md）。
	if slices.ContainsFunc(hits, func(h hit) bool { return h.rank < 3 }) {
		hits = slices.DeleteFunc(hits, func(h hit) bool { return h.rank == 3 })
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		if len(hits[i].path) != len(hits[j].path) {
			return len(hits[i].path) < len(hits[j].path)
		}
		return hits[i].path < hits[j].path
	})
	out := make([]string, 0, min(len(hits), maxWorkspaceFiles))
	for _, h := range hits[:min(len(hits), maxWorkspaceFiles)] {
		out = append(out, h.path)
	}
	return out
}

// subsequence 判断 q 的字符能不能在 s 里按顺序找齐（两边都已经是小写）。@ 后面敲的字不必连续。
func subsequence(s, q string) bool {
	qs := []rune(q)
	i := 0
	for _, r := range s {
		if i < len(qs) && r == qs[i] {
			i++
		}
	}
	return i == len(qs)
}
