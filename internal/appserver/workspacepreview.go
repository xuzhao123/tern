package appserver

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/xuzhao123/tern/internal/protocol"
)

const maxProjectPreview = 2 << 20

// workspace/file/read 的根只取任务快照；预览不登记模型的 seen 状态，也不授予写权限。
func (c *conn) workspaceFileRead(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Path     string `json:"path"`
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
		return nil, bad(400, "这条任务没有项目工作目录")
	}
	name := input.Path
	if filepath.IsAbs(name) {
		var err error
		name, err = filepath.Rel(dir, name)
		if err != nil {
			return nil, bad(403, "路径不属于当前任务的工作目录")
		}
	}
	name = filepath.ToSlash(filepath.Clean(name))
	if name == "." || !fs.ValidPath(name) {
		return nil, bad(403, "请指定当前任务工作目录内的文件")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, bad(404, "任务的工作目录不存在或无法访问")
	}
	defer root.Close()
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, bad(404, "文件不存在、无法读取，或链接指向工作目录之外")
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return nil, bad(400, "只能预览普通文件，不能读取目录、管道或设备")
	}
	result := map[string]any{"path": name, "directory": dir, "size": before.Size(), "modified": before.ModTime().UnixMilli()}
	if before.Size() > maxProjectPreview {
		result["too_large"] = true
		return result, nil
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProjectPreview+1))
	if err != nil {
		return nil, bad(500, "文件读取失败")
	}
	after, err := file.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(data)) != before.Size() {
		return nil, bad(409, "读取期间文件已变化，请刷新后重新查看")
	}
	result["version"] = fmt.Sprintf("%x", sha256.Sum256(data))
	result["data"] = data
	return result, nil
}
