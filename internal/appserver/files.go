package appserver

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/protocol"
	"github.com/xuzhao123/tern/internal/tenant"
)

// 对话的沙箱文件：agent 在 turn/started 里报告沙箱目录名（对话第一轮的检查点ID），
// 文件在 .data/sandbox/<目录名>/，就是沙箱里的 /workspace。
//
// 这个目录里的东西是模型写的，所以 app-server 把它当不可信数据：
//   - 只经 os.Root 访问：模型在沙箱里建的符号链接若指向 /workspace 之外（比如宿主机的 .env），解析到根外就报错，不会跟出去；
//   - 只下载普通文件，打开时带 O_NONBLOCK：命名管道不会卡住 app-server；
//   - 文件内容按块传，界面当附件下载、nosniff：模型写的 HTML 不会在界面的页面源里执行（那里有登录 Cookie）。
const (
	maxUpload    = 10 << 20
	maxListFiles = 1000
)

// frameName 是任务ID、调用ID、沙箱目录名这类名字的形状：浏览器画面与沙箱目录都用它挡路径穿越。
var frameName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// 最近一次 turn/started 报告的沙箱目录名；没开 bash 或早期的存档没有。调用方持有 r.mu。
func (r *run) sandboxID() string {
	for i := len(r.events) - 1; i >= 0; i-- {
		e := r.events[i]
		if e.Kind == "notify" && e.Method == "turn/started" {
			var p struct{ Sandbox string }
			if json.Unmarshal(e.Body, &p) == nil && p.Sandbox != "" && frameName.MatchString(p.Sandbox) && !strings.HasPrefix(p.Sandbox, ".") {
				return p.Sandbox
			}
		}
	}
	return ""
}

// sandboxOf 打开对话的沙箱目录；目录还没建（bash 还没调用过）时由 create 决定是否新建。
func (s *Server) sandboxOf(threadID string, create bool) (*run, *os.Root, *protocol.Error) {
	r, ferr := s.threadOf(threadID)
	if ferr != nil {
		return nil, nil, ferr
	}
	r.mu.Lock()
	id := r.sandboxID()
	r.mu.Unlock()
	if id == "" {
		return nil, nil, bad(404, "这条对话没有启用 Bash 沙箱")
	}
	dir := filepath.Join(s.dir, tenant.Path("", "sandbox", id))
	if create {
		os.MkdirAll(dir, 0o755)
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil, nil // 还没有任何文件
	}
	if err != nil {
		return nil, nil, bad(500, "无法打开沙箱目录")
	}
	return r, root, nil
}

type sandboxFile struct {
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	Time  time.Time `json:"time"`
	Kind  string    `json:"kind"` // file、dir、link、other；只有 file 能下载
	Link  string    `json:"link,omitempty"`
	Error string    `json:"error,omitempty"`
}

// files/list：列出沙箱里的文件，界面据此画文件面板。
func (c *conn) filesList(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, root, ferr := c.s.sandboxOf(input.ThreadID, false)
	if ferr != nil {
		return nil, ferr
	}
	files, truncated := []sandboxFile{}, false
	if root != nil {
		defer root.Close()
		// WalkDir 不跟符号链接进目录；Lstat 看到的是链接本身。
		fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
			if p == "." {
				return err
			}
			if len(files) >= maxListFiles {
				truncated = true
				return fs.SkipAll
			}
			f := sandboxFile{Path: p, Kind: "other"}
			if err != nil {
				f.Error = err.Error()
			} else if info, err := root.Lstat(p); err == nil {
				f.Size, f.Time = info.Size(), info.ModTime()
				switch mode := info.Mode(); {
				case mode.IsRegular():
					f.Kind = "file"
				case mode.IsDir():
					f.Kind, f.Size = "dir", 0
				case mode&fs.ModeSymlink != 0:
					f.Kind, f.Link = "link", readlink(root, p)
				}
			}
			files = append(files, f)
			return nil
		})
	}
	r.mu.Lock()
	running := r.proc != nil
	r.mu.Unlock()
	return map[string]any{"files": files, "truncated": truncated, "running": running}, nil
}

func readlink(root *os.Root, p string) string {
	target, _ := root.Readlink(p)
	return target
}

// files/read：按块读沙箱里的一个普通文件；size 是文件总长度，界面按它接着取。
func (c *conn) filesRead(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Path     string `json:"path"`
		Offset   int64  `json:"offset"`
		Length   int64  `json:"length"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	_, root, ferr := c.s.sandboxOf(input.ThreadID, false)
	if ferr != nil {
		return nil, ferr
	}
	if root == nil {
		return nil, bad(404, "找不到这个文件")
	}
	defer root.Close()
	name := input.Path
	if name == "" || !fs.ValidPath(name) {
		return nil, bad(404, "找不到这个文件")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, bad(404, "打不开这个文件（不存在，或是指向沙箱外的链接）")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, bad(400, "只能下载普通文件")
	}
	// 只读要的这一块：沙箱里可能有很大的文件，不能整份读进来。
	size := info.Size()
	if input.Length <= 0 || input.Length > MaxChunk {
		return nil, bad(400, "length 取 1 到 4194304")
	}
	if input.Offset < 0 || input.Offset > size {
		return nil, bad(400, "offset 超出内容范围")
	}
	data := make([]byte, min(input.Length, size-input.Offset))
	if len(data) > 0 {
		n, err := file.ReadAt(data, input.Offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, bad(500, "读取失败："+err.Error())
		}
		data = data[:n]
	}
	return map[string]any{"data": data, "size": size, "name": path.Base(name), "mime": "application/octet-stream", "modified": info.ModTime().UnixMilli()}, nil
}

// files/write：上传一个文件到沙箱顶层，分块收，最后一块带 done 与文件名。
// 只放到 /workspace 顶层、不覆盖已有文件、对话运行中不接受（同一条对话一次只处理一个提问，上传也算一次改动）。
// 人的这次操作记进审计，内容只存哈希。
func (c *conn) filesWrite(m protocol.Message) (any, *protocol.Error) {
	data, id, done, ferr := c.receive(m, maxUpload)
	if ferr != nil {
		return nil, ferr
	}
	if !done {
		return map[string]string{"uploadId": id}, nil
	}
	var input struct {
		ThreadID string `json:"threadId"`
		Name     string `json:"name"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, root, ferr := c.s.sandboxOf(input.ThreadID, true)
	if ferr != nil {
		return nil, ferr
	}
	if root == nil {
		return nil, bad(500, "无法创建沙箱目录")
	}
	defer root.Close()
	r.mu.Lock()
	running, task := r.proc != nil, r.sandboxID()
	r.mu.Unlock()
	if running {
		return nil, bad(409, "对话正在运行，等这一轮结束再上传")
	}
	name := input.Name
	if !validUploadName(name) {
		return nil, bad(400, "文件名只能是一层、不以点开头、不含控制字符，最长 128 字节")
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil, bad(409, "沙箱里已有同名文件："+name)
	}
	if err != nil {
		return nil, bad(500, "无法写入沙箱目录")
	}
	record := audit.Record{Actor: "user", User: userName(m), Event: "user_upload", Task: task, Conversation: r.ID, Target: name, Detail: c.clientName()}
	record.Args(string(data))
	// 和审批一样，写不进审计就不做：先记再写文件，失败则删掉空文件。
	if err := audit.Write(c.ctx, record); err != nil {
		file.Close()
		root.Remove(name)
		return nil, bad(500, "审计日志写入失败，没有上传："+err.Error())
	}
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		root.Remove(name)
		return nil, bad(500, "写入失败："+err.Error())
	}
	return map[string]any{"path": name, "size": len(data)}, nil
}

// files/delete：和上传一样只在这一轮结束后、只经 os.Root（路径穿越和指向沙箱外的链接都出不去；删链接只删链接本身），
// 目录连同内容一起删。先写审计 user_delete，写不进去就不删。
func (c *conn) filesDelete(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Path     string `json:"path"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, root, ferr := c.s.sandboxOf(input.ThreadID, false)
	if ferr != nil {
		return nil, ferr
	}
	if root == nil {
		return nil, bad(404, "找不到这个文件")
	}
	defer root.Close()
	r.mu.Lock()
	running, task := r.proc != nil, r.sandboxID()
	r.mu.Unlock()
	if running {
		return nil, bad(409, "对话正在运行，等这一轮结束再删除")
	}
	name := input.Path
	if name == "" || !fs.ValidPath(name) {
		return nil, bad(404, "找不到这个文件")
	}
	if _, err := root.Lstat(name); err != nil {
		return nil, bad(404, "找不到这个文件（不存在，或路径经过指向沙箱外的链接）")
	}
	record := audit.Record{Actor: "user", User: userName(m), Event: "user_delete", Task: task, Conversation: r.ID, Target: name, Detail: c.clientName()}
	if err := audit.Write(c.ctx, record); err != nil {
		return nil, bad(500, "审计日志写入失败，没有删除："+err.Error())
	}
	if err := root.RemoveAll(name); err != nil {
		return nil, bad(500, "删除失败："+err.Error())
	}
	return nil, nil
}

func validUploadName(name string) bool {
	if name == "" || len(name) > 128 || !utf8.ValidString(name) || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// browser/frame：agent 浏览时把画面写到 .data/browser/<任务ID>/<调用ID>.jpg（screencast 帧不断覆盖，结束时是最终截图）。
// 界面运行中反复来取这张图，就是实时画面；按块传，size 是整张图的长度。
func (c *conn) browserFrame(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Task   string `json:"task"`
		Call   string `json:"call"`
		Offset int64  `json:"offset"`
		Length int64  `json:"length"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	task, call := input.Task, strings.TrimSuffix(input.Call, ".jpg")
	if !frameName.MatchString(task) || !frameName.MatchString(call) || strings.HasPrefix(task, ".") || strings.Contains(call, ".") {
		return nil, bad(404, "找不到画面")
	}
	data, err := os.ReadFile(filepath.Join(c.s.dir, tenant.Path("", "browser", task, call+".jpg")))
	if err != nil {
		return nil, bad(404, "找不到画面")
	}
	part, ferr := chunk(data, input.Offset, input.Length)
	if ferr != nil {
		return nil, ferr
	}
	return map[string]any{"data": part, "size": len(data), "mime": "image/jpeg"}, nil
}
