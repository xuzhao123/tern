package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/protocol"
)

type projectAction struct {
	Name       string   `json:"name"`
	Command    string   `json:"command"`
	Platforms  []string `json:"platforms"`
	Background bool     `json:"background"`
	Ports      []int    `json:"ports"`
	Initialize bool     `json:"initialize"` // 已不再支持，只为读到时明确拒绝（见 operationConfig）
}
type operationRecord struct {
	ID            string    `json:"id"`
	ThreadID      string    `json:"thread_id"`
	User          string    `json:"user"`
	Name          string    `json:"name"`
	Command       string    `json:"command"`
	Directory     string    `json:"directory"`
	ConfigVersion string    `json:"config_version"`
	RequestID     string    `json:"request_id"`
	Ports         []int     `json:"ports"`
	State         string    `json:"state"`
	Reason        string    `json:"reason,omitempty"`
	PID           int       `json:"pid"`
	Identity      string    `json:"identity"`
	Started       time.Time `json:"started"`
	Ended         time.Time `json:"ended,omitempty"`
	ExitCode      *int      `json:"exit_code,omitempty"`
	LogError      string    `json:"log_error,omitempty"`
}
type operation struct {
	mu      sync.Mutex
	record  operationRecord
	dir     string
	log     []byte
	logSize int64 // output.log 当前的字节数
	cmd     *exec.Cmd
	done    chan struct{}
}

func privateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func processIdentity(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	at := strings.LastIndex(string(data), ")")
	if at < 0 {
		return ""
	}
	fields := strings.Fields(string(data[at+1:]))
	if len(fields) < 20 || fields[0] == "Z" {
		return ""
	}
	return fields[19]
}
func ServiceWorkerStarted(dir string) error {
	if filepath.Dir(dir) != filepath.Join(home(), ".data", "services") {
		return fmt.Errorf("服务运行目录无效")
	}
	if _, err := uuid.Parse(filepath.Base(dir)); err != nil {
		return err
	}
	return privateJSON(filepath.Join(dir, "worker.json"), map[string]any{"pid": os.Getpid(), "identity": processIdentity(os.Getpid())})
}
func (op *operation) snapshot() operationRecord { op.mu.Lock(); defer op.mu.Unlock(); return op.record }
func (op *operation) save() error               { return privateJSON(filepath.Join(op.dir, "state.json"), op.record) }
func (op *operation) Write(data []byte) (int, error) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.log = append(op.log, data...)
	if len(op.log) > 256<<10 {
		op.log = slices.Clone(op.log[len(op.log)-(256<<10):])
	}
	// 平时只追加这一段；文件超过 1MiB 时整份换成内存里最后的 256KiB，长期服务的日志不会无限增长，也不必每段都重写。
	path := filepath.Join(op.dir, "output.log")
	var err error
	if op.logSize+int64(len(data)) > 1<<20 {
		err = os.WriteFile(path, op.log, 0600)
		op.logSize = int64(len(op.log))
	} else {
		var f *os.File
		if f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600); err == nil {
			_, err = f.Write(data)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
		}
		op.logSize += int64(len(data))
	}
	if err != nil {
		op.record.LogError = "日志未能持久保存"
	}
	return len(data), nil
}
func (s *Server) loadOperationsLocked() {
	if s.operations != nil {
		return
	}
	s.operations = map[string]*operation{}
	paths, _ := filepath.Glob(filepath.Join(s.dir, ".data", "services", "*", "state.json"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec operationRecord
		if json.Unmarshal(data, &rec) != nil || rec.ID != filepath.Base(filepath.Dir(path)) {
			continue
		}
		op := &operation{record: rec, dir: filepath.Dir(path)}
		op.log, _ = os.ReadFile(filepath.Join(op.dir, "output.log"))
		if len(op.log) > 256<<10 {
			op.log = op.log[len(op.log)-(256<<10):]
		}
		if slices.Contains([]string{"starting", "running", "stopping"}, rec.State) {
			var worker struct {
				PID      int    `json:"pid"`
				Identity string `json:"identity"`
			}
			if raw, err := os.ReadFile(filepath.Join(op.dir, "worker.json")); err == nil && json.Unmarshal(raw, &worker) == nil {
				op.record.PID, op.record.Identity = worker.PID, worker.Identity
			}
			op.record.State, op.record.Reason = "unavailable", "服务进程未被本次服务端接管；核对退出后可明确重新运行"
			op.save()
		}
		s.operations[rec.ID] = op
	}
}
func (c *conn) operationConfig(id string) (*run, string, string, []projectAction, *protocol.Error) {
	r, ferr := c.s.threadOf(id)
	if ferr != nil {
		return nil, "", "", nil, ferr
	}
	r.mu.Lock()
	dir := r.workDir()
	r.mu.Unlock()
	if dir == "" {
		return nil, "", "", nil, bad(400, "当前任务没有项目目录")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", "", nil, bad(404, "项目目录不可用")
	}
	defer root.Close()
	f, err := root.OpenFile(".agents/actions.json", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return r, dir, "", []projectAction{}, nil
	}
	if err != nil {
		return nil, "", "", nil, bad(400, "项目动作配置无法读取")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil, "", "", nil, bad(400, "项目动作配置须为不超过64KiB的普通文件")
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(data) > 65536 {
		return nil, "", "", nil, bad(400, "项目动作配置读取失败或过大")
	}
	var config struct {
		Version int             `json:"version"`
		Actions []projectAction `json:"actions"`
	}
	if json.Unmarshal(data, &config) != nil || config.Version != 1 || len(config.Actions) > 30 {
		return nil, "", "", nil, bad(400, "actions.json需要version=1和最多30项actions")
	}
	seen := map[string]bool{}
	for _, a := range config.Actions {
		if strings.TrimSpace(a.Name) == "" || len(a.Name) > 80 || seen[a.Name] || strings.TrimSpace(a.Command) == "" || len(a.Command) > 8000 || len(a.Ports) > 8 {
			return nil, "", "", nil, bad(400, "项目动作名称、命令或端口数量无效")
		}
		// 初始化动作原来只能在副本对话里跑；副本去掉以后不再支持，配置里还写着就整份拒绝，不把它当普通命令在原目录执行。
		if a.Initialize {
			return nil, "", "", nil, bad(400, "项目动作 "+a.Name+" 标了 initialize：初始化动作已不再支持，请去掉这一项或改成普通动作")
		}
		seen[a.Name] = true
		ports := map[int]bool{}
		for _, port := range a.Ports {
			if port < 1024 || port > 65535 || port == 3128 || ports[port] {
				return nil, "", "", nil, bad(400, "服务端口无效或重复（3128为保留端口）")
			}
			ports[port] = true
		}
	}
	return r, dir, fmt.Sprintf("%x", sha256.Sum256(data)), config.Actions, nil
}
func (c *conn) operationsList(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	r, dir, version, actions, ferr := c.operationConfig(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	c.s.operationMu.Lock()
	c.s.loadOperationsLocked()
	list := []operationRecord{}
	for _, op := range c.s.operations {
		rec := op.snapshot()
		if (rec.ThreadID == r.ID || rec.Directory == dir) && rec.User == userName(m) {
			list = append(list, rec)
		}
	}
	c.s.operationMu.Unlock()
	slices.SortFunc(list, func(a, b operationRecord) int { return b.Started.Compare(a.Started) })
	return map[string]any{"directory": dir, "version": version, "actions": actions, "operations": list, "supported": runtime.GOOS == "linux"}, nil
}
func (c *conn) operationStart(m protocol.Message) (any, *protocol.Error) {
	return c.requestOnce(m, (*conn).operationStartOnce)
}
func (c *conn) operationStartOnce(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID  string `json:"threadId"`
		Name      string `json:"name"`
		Version   string `json:"version"`
		Confirmed bool   `json:"confirmed"`
		RequestID string `json:"request_id"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	if runtime.GOOS != "linux" {
		return nil, bad(400, "项目操作目前需要Linux沙箱执行主机")
	}
	if !p.Confirmed || !requestIDPattern.MatchString(p.RequestID) {
		return nil, bad(400, "需要预览命令后明确确认和request_id")
	}
	r, dir, version, actions, ferr := c.operationConfig(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	if version != p.Version {
		return nil, bad(409, "项目操作配置已改变，请重新预览")
	}
	at := slices.IndexFunc(actions, func(a projectAction) bool { return a.Name == p.Name })
	if at < 0 {
		return nil, bad(404, "没有这个项目操作")
	}
	action := actions[at]
	if len(action.Platforms) > 0 && !slices.Contains(action.Platforms, runtime.GOOS) {
		return nil, bad(400, "当前平台不支持这个操作")
	}
	r.mu.Lock()
	enabled := r.bashEnabled
	var snap event
	for _, e := range r.events {
		if e.Kind == "start" || e.Kind == "continue" {
			snap = e
		}
	}
	r.mu.Unlock()
	if !snap.Bash && !enabled {
		return nil, bad(403, "此任务未启用命令工具")
	}
	s := c.s
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	s.loadOperationsLocked()
	running := 0
	for _, op := range s.operations {
		rec := op.snapshot()
		live := slices.Contains([]string{"starting", "running", "stopping"}, rec.State) || rec.Identity != "" && processIdentity(rec.PID) == rec.Identity
		if live {
			running++
			if rec.Directory == dir && rec.Name == action.Name {
				return nil, bad(409, "这个项目动作已有实例，请先查看或停止原实例")
			}
		}
	}
	if running >= 16 {
		return nil, bad(409, "最多同时运行16个项目操作")
	}
	record := audit.Record{Actor: "user", User: userName(m), Event: "user_project_operation", Conversation: r.ID, Target: action.Name, Detail: dir}
	record.Args(action.Command)
	if err := audit.Write(c.ctx, record); err != nil {
		return nil, bad(500, "审计写入失败，没有启动命令")
	}
	id := uuid.NewString()
	runDir := filepath.Join(s.dir, ".data", "services", id)
	if err := os.MkdirAll(filepath.Join(runDir, "scratch"), 0700); err != nil {
		return nil, bad(500, "无法建立私有服务目录")
	}
	os.Chmod(filepath.Dir(runDir), 0700)
	os.Chmod(runDir, 0700)
	op := &operation{record: operationRecord{ID: id, ThreadID: r.ID, User: userName(m), Name: action.Name, Command: action.Command, Directory: dir, ConfigVersion: version, RequestID: p.RequestID, Ports: slices.Clone(action.Ports), State: "starting", Started: time.Now().UTC()}, dir: runDir, done: make(chan struct{})}
	if err := op.save(); err != nil {
		return nil, bad(500, "启动记录未保存，没有执行命令")
	}
	ports := []string{}
	for _, port := range action.Ports {
		ports = append(ports, strconv.Itoa(port))
	}
	limit := "5m"
	if action.Background {
		limit = "24h"
	}
	args := []string{"-shell", action.Command, "-bash", "-workspace", dir, "-permission-mode", snap.Permission, "-bash-timeout", limit, "-service-log"}
	if len(ports) > 0 {
		args = append(args, "-service-ports", strings.Join(ports, ","))
	}
	for _, domain := range r.netAllow() {
		args = append(args, "-net-allow", domain)
	}
	cmd := s.agentCommand(context.Background(), args...)
	cmd.Env = append(os.Environ(), "AGENT_SHELL_DIR="+filepath.Join(runDir, "scratch"), "AGENT_SERVICE_DIR="+runDir, "AGENT_USER="+userName(m))
	cmd.Stdout, cmd.Stderr = op, op
	cmd.Stdin = nil
	cmd.SysProcAttr = serviceProcessAttr()
	op.cmd = cmd
	s.operations[id] = op
	if err := cmd.Start(); err != nil {
		op.record.State, op.record.Reason = "failed", err.Error()
		op.save()
		close(op.done)
		return nil, bad(500, "服务启动失败："+err.Error())
	}
	op.mu.Lock()
	op.record.PID, op.record.Identity, op.record.State = cmd.Process.Pid, processIdentity(cmd.Process.Pid), "running"
	err := op.save()
	op.mu.Unlock()
	if err != nil {
		cmd.Process.Signal(os.Interrupt)
	}
	go func() {
		defer close(op.done)
		runErr := cmd.Wait()
		op.mu.Lock()
		defer op.mu.Unlock()
		op.record.Ended = time.Now().UTC()
		code := cmd.ProcessState.ExitCode()
		lines := strings.Split(strings.TrimSpace(string(op.log)), "\n")
		var result struct {
			ExitCode *int `json:"exit_code"`
		}
		if len(lines) > 0 && json.Unmarshal([]byte(lines[len(lines)-1]), &result) == nil && result.ExitCode != nil {
			code = *result.ExitCode
		}
		op.record.ExitCode = &code
		if op.record.State == "stopping" {
			op.record.State = "stopped"
		} else if runErr != nil || code != 0 {
			op.record.State = "failed"
		} else {
			op.record.State = "exited"
		}
		if runErr != nil {
			op.record.Reason = runErr.Error()
		}
		op.save()
	}()
	if err != nil {
		return nil, bad(500, "进程已请求停止：启动状态未能保存，请检查原实例")
	}
	return op.snapshot(), nil
}
func (c *conn) operationRead(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
		ID       string `json:"id"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	c.s.operationMu.Lock()
	c.s.loadOperationsLocked()
	op := c.s.operations[p.ID]
	c.s.operationMu.Unlock()
	if op == nil {
		return nil, bad(404, "实例不存在")
	}
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.record.ThreadID != p.ThreadID || op.record.User != userName(m) {
		return nil, bad(403, "实例不属于当前账号和任务")
	}
	return map[string]any{"operation": op.record, "output": string(op.log), "tail_bytes": len(op.log)}, nil
}
func (c *conn) operationStop(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
		ID       string `json:"id"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	s := c.s
	s.operationMu.Lock()
	s.loadOperationsLocked()
	op := s.operations[p.ID]
	s.operationMu.Unlock()
	if op == nil {
		return nil, bad(404, "实例不存在")
	}
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.record.ThreadID != p.ThreadID || op.record.User != userName(m) {
		return nil, bad(403, "实例不属于当前账号和任务")
	}
	if op.record.Identity == "" || processIdentity(op.record.PID) != op.record.Identity {
		return nil, bad(409, "原进程已经退出或身份无法确认，没有向其他进程发信号")
	}
	if op.record.State == "stopping" {
		return op.record, nil
	}
	if err := audit.Write(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_operation_stop", Conversation: p.ThreadID, Target: p.ID}); err != nil {
		return nil, bad(500, "审计失败，没有停止")
	}
	op.record.State = "stopping"
	if err := op.save(); err != nil {
		return nil, bad(500, "停止意图未保存")
	}
	process, _ := os.FindProcess(op.record.PID)
	if err := process.Signal(os.Interrupt); err != nil {
		return nil, bad(409, "停止结果待核对："+err.Error())
	}
	return op.record, nil
}
func (s *Server) closeOperations() {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	for _, op := range s.operations {
		rec := op.snapshot()
		if rec.Identity != "" && processIdentity(rec.PID) == rec.Identity {
			p, _ := os.FindProcess(rec.PID)
			p.Signal(os.Interrupt)
		}
	}
}

// recordTTL 之后，发送回执、网页 Git 操作记录与已结束的项目操作不再需要回查，启动时删掉，目录不会一直增长。
// 还可能在跑的操作（starting、running、stopping、unavailable）不删。
const recordTTL = 30 * 24 * time.Hour

func (s *Server) pruneRecords() {
	old := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && time.Since(info.ModTime()) > recordTTL
	}
	for _, dir := range []string{"requests", "git-actions"} {
		paths, _ := filepath.Glob(filepath.Join(s.dir, ".data", dir, "*.json"))
		for _, path := range paths {
			if old(path) {
				os.Remove(path)
			}
		}
	}
	paths, _ := filepath.Glob(filepath.Join(s.dir, ".data", "services", "*", "state.json"))
	for _, path := range paths {
		var rec operationRecord
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &rec) != nil || !slices.Contains([]string{"exited", "failed", "stopped"}, rec.State) || !old(path) {
			continue
		}
		if _, err := uuid.Parse(filepath.Base(filepath.Dir(path))); err == nil {
			os.RemoveAll(filepath.Dir(path))
		}
	}
}
