package appserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/xuzhao123/tern/internal/protocol"
)

const treePageSize, maxTreeEntries = 200, 10000

type treeEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Symlink     bool   `json:"symlink,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

type treeCursor struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Offset  int    `json:"offset"`
}

// 只列任务快照目录的一层；分页引用目录版本，不能把变化前后的两页拼在一起。
func (c *conn) workspaceTreeList(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Path     string `json:"path"`
		Cursor   string `json:"cursor"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if len(input.Path) > 4096 || len(input.Cursor) > 8192 {
		return nil, bad(400, "目录路径或分页游标过长")
	}
	r, ferr := c.s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.mu.Lock()
	dir := r.workDir()
	r.mu.Unlock()
	if dir == "" {
		return nil, bad(400, "这条任务没有项目工作目录")
	}
	name := input.Path
	if filepath.IsAbs(name) {
		var err error
		name, err = filepath.Rel(dir, name)
		if err != nil {
			return nil, bad(403, "目录不属于当前任务的工作目录")
		}
	}
	name = filepath.ToSlash(filepath.Clean(name))
	if !fs.ValidPath(name) {
		return nil, bad(403, "只能浏览当前任务工作目录内的目录")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, bad(404, "任务的工作目录不存在或无法访问")
	}
	defer root.Close()
	folder, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, bad(404, "目录不存在、无法读取，或链接指向工作目录之外")
	}
	defer folder.Close()
	before, err := folder.Stat()
	if err != nil || !before.IsDir() {
		return nil, bad(400, "这个路径不是目录")
	}
	list, err := folder.ReadDir(maxTreeEntries + 1)
	if err != nil && err != io.EOF {
		return nil, bad(500, "目录读取失败")
	}
	if len(list) > maxTreeEntries {
		return nil, bad(422, "单目录超过10000项，无法完整列举；请使用项目文件筛选或输入完整文件路径")
	}
	entries := make([]treeEntry, 0, len(list))
	for _, item := range list {
		if c.ctx.Err() != nil {
			return nil, bad(408, "目录读取已取消")
		}
		if item.Name() == ".git" {
			continue
		}
		entry := treeEntry{Name: item.Name(), Path: filepath.ToSlash(filepath.Join(name, item.Name())), Kind: "file"}
		mode := item.Type()
		if mode&os.ModeSymlink != 0 {
			entry.Symlink = true
			info, statErr := root.Stat(entry.Path)
			if statErr != nil {
				entry.Kind, entry.Unavailable = "link", "链接不存在、无法读取或指向项目目录之外"
			} else {
				mode = info.Mode()
			}
		}
		if entry.Unavailable == "" {
			if mode.IsDir() {
				entry.Kind = "directory"
			} else if !mode.IsRegular() {
				entry.Kind, entry.Unavailable = "special", "不能预览设备、管道或其他特殊文件"
			}
		}
		if !utf8.ValidString(entry.Name) || strings.IndexFunc(entry.Name, unicode.IsControl) >= 0 || len(entry.Path) > 4096 {
			entry.Unavailable = "路径含不支持的字符或过长，不能在网页打开"
		}
		entries = append(entries, entry)
	}
	after, err := folder.Stat()
	current, currentErr := root.Stat(name)
	if err != nil || currentErr != nil || !os.SameFile(before, current) || !before.ModTime().Equal(after.ModTime()) {
		return nil, bad(409, "读取期间目录已变化，请刷新后重新查看")
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Kind == "directory") != (entries[j].Kind == "directory") {
			return entries[i].Kind == "directory"
		}
		return entries[i].Name < entries[j].Name
	})
	encoded, _ := json.Marshal(entries)
	version := fmt.Sprintf("%x", sha256.Sum256(encoded))
	offset := 0
	if input.Cursor != "" {
		data, decodeErr := base64.RawURLEncoding.DecodeString(input.Cursor)
		var cursor treeCursor
		if decodeErr != nil || json.Unmarshal(data, &cursor) != nil || cursor.Path != name || cursor.Offset <= 0 {
			return nil, bad(400, "目录分页游标无效，请重新读取目录")
		}
		if cursor.Version != version || cursor.Offset > len(entries) {
			return nil, bad(409, "目录列表已变化，请刷新后重新分页")
		}
		offset = cursor.Offset
	}
	end := min(offset+treePageSize, len(entries))
	next := ""
	if end < len(entries) {
		data, _ := json.Marshal(treeCursor{name, version, end})
		next = base64.RawURLEncoding.EncodeToString(data)
	}
	return map[string]any{"directory": dir, "path": name, "entries": entries[offset:end], "version": version, "next_cursor": next, "total": len(entries), "page_size": treePageSize}, nil
}
