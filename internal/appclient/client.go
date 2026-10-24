package appclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xuzhao123/tern/internal/appserver"
	"github.com/xuzhao123/tern/internal/protocol"
)

// 界面这一侧：连本机 app-server 的控制 socket（或经 ssh 连远端，见 ssh.go），并保证本机的在运行。
// 一条连接怎么用、断了怎么重连见 rpc.go。
// proxy 也在这里：经 SSH 时把 SSH 的标准输入输出接到控制 socket，协议原样转发。
// 协议版本常量在 internal/appserver，这里只引用它。

// ErrNotRunning：控制 socket 连不上，本机没有在运行的 app-server。
var ErrNotRunning = errors.New("app-server 没有在运行")

// SelfName 是 app-server 自己那几条命令（start、stop、proxy）连上来时报的界面名字。
// 界面的名字由界面自己给：网页端 "tern web"，chat "tern chat"；app-server 把收到的那一个记进审计记录的 Detail。
const SelfName = "tern app-server"

const (
	handshakeTimeout   = 10 * time.Second
	startTimeout       = 30 * time.Second
	stopTimeout        = 10 * time.Second
	unsubscribeTimeout = 5 * time.Second
	logTailLines       = 5 // 子进程提前退出时，错误里带 app-server.log 的最后几行
)

// Info 是 start 的输出：控制 socket（相对 TERN_HOME）、进程号、版本。
type Info struct {
	Socket  string `json:"socket"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// connect 连本机控制 socket 并完成 initialize，返回连接与服务端信息；name 是这个界面的名字（见 SelfName），
// handle 收 app-server 发来的通知（start、stop 这类探测连上就关，交 nil）。
// 控制 socket 连不上时返回的错误带 ErrNotRunning；握手失败（比如协议版本不一致）原样返回。
func connect(name string, handle func(protocol.Message)) (*protocol.Conn, appserver.ServerInfo, error) {
	rwc, err := dialLocal()
	if err != nil {
		return nil, appserver.ServerInfo{}, err
	}
	return initialize(rwc, name, handle)
}

// dialLocal 是本机模式的连接：控制 socket。连不上时错误带 ErrNotRunning，调用方据此知道要先 start。
func dialLocal() (io.ReadWriteCloser, error) {
	nc, err := net.Dial("unix", appserver.Socket)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrNotRunning, err)
	}
	return nc, nil
}

// initialize 在一条新连接（本机 socket 或 ssh 的标准输入输出）上发 initialize，返回连接与服务端信息。
// 协议版本对不上、对端先断开都在这里报错；本机与远程的握手完全一样。
func initialize(rwc io.ReadWriteCloser, name string, handle func(protocol.Message)) (*protocol.Conn, appserver.ServerInfo, error) {
	if handle == nil {
		handle = func(protocol.Message) {}
	}
	c := protocol.NewConn(rwc, rwc, handle)
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	var res struct {
		Server appserver.ServerInfo `json:"server"`
	}
	err := c.Call(ctx, "initialize", map[string]any{
		"client":   map[string]string{"name": name, "version": appserver.Version()},
		"protocol": appserver.Protocol,
	}, &res)
	if err != nil {
		c.Close()
		return nil, appserver.ServerInfo{}, err
	}
	return c, res.Server, nil
}

// Start 保证本机 app-server 在运行：控制 socket 能答 initialize 就直接返回（已在运行的不会因为 dev 换模式），
// 否则以新会话（setsid）在后台起一个，等它能答 initialize 再返回；子进程提前退出就不再等，报错并带上日志尾巴。
// dev 是 -dev：每轮用 go run . 启动 agent。-dev 由 app-server 自己判断，这里只把它传给后台进程。
// 这里不往 stdout 打印：proxy 的 stdout 是协议通道，要报告结果的调用方自己打印 Info。
func Start(dev bool) (Info, error) {
	if c, srv, err := connect(SelfName, nil); err == nil {
		c.Close()
		return info(srv), nil
	} else if !errors.Is(err, ErrNotRunning) {
		return Info{}, err // 有在跑的，但握手没成（协议版本不一致等），不另起一个
	}
	exited, err := spawn(dev)
	if err != nil {
		return Info{}, err
	}
	for deadline := time.Now().Add(startTimeout); time.Now().Before(deadline); {
		c, srv, err := connect(SelfName, nil)
		if err == nil {
			c.Close()
			return info(srv), nil
		}
		if !errors.Is(err, ErrNotRunning) {
			return Info{}, err
		}
		select {
		case <-exited:
			// 锁被占住、监听失败这类：等下去也不会有 socket，把日志尾巴带出去。
			return Info{}, fmt.Errorf("app-server 提前退出（见 %s）：\n%s", appserver.LogFile, logTail())
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	return Info{}, fmt.Errorf("启动 app-server 后 %s 内没能连上 %s，见 %s", startTimeout, appserver.Socket, appserver.LogFile)
}

// Stop 发 server/shutdown 并等它退出；没有在运行的 app-server 时提示一句就正常返回。
// 按 pid 文件等进程真的不在了，而不是等 socket 消失：删掉 socket 之后进程还要做收尾，
// 文件锁这时还没放开，紧接着的 start 会起不来。
func Stop() error {
	c, _, err := connect(SelfName, nil)
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			fmt.Println("没有在运行的 app-server")
			return nil
		}
		return err
	}
	defer c.Close()
	pid := readPID()
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	if err := c.Call(ctx, "server/shutdown", nil, nil); err != nil {
		return fmt.Errorf("server/shutdown：%w", err)
	}
	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		if pid > 0 {
			if gone(pid) {
				return nil
			}
		} else if _, err := os.Stat(appserver.Socket); errors.Is(err, os.ErrNotExist) {
			return nil // 读不到 pid 时退回到等 socket 被删掉
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("app-server 没有在 %s 内退出", stopTimeout)
}

// gone 判断进程是不是已经退出了。kill(pid, 0) 返回 ESRCH 说明进程不在了；
// 已经退出、但父进程还没回收的僵尸进程，kill 照样成功，所以再看一眼 /proc 里的状态：
// app-server 由 setsid 起在后台，起它的进程先退出，没有 init 回收的容器里它会一直留在僵尸状态。
func gone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// 第二个字段是状态；进程名（第一个字段）可能带空格和括号，从最后一个 ')' 往后看。
	i := strings.LastIndexByte(string(data), ')')
	return i >= 0 && i+2 < len(data) && data[i+2] == 'Z'
}

// Proxy 先保证本机 app-server 在运行，再把标准输入输出与控制 socket 原样转发，任何一边断开就退出。
// stdout 就是协议通道，所以那次 start 不能往 stdout 写别的。经 SSH 时不知道该用哪种模式，已在运行的照旧；没在运行的不带 -dev 起。
func Proxy() error {
	if _, err := Start(false); err != nil {
		return err
	}
	nc, err := net.Dial("unix", appserver.Socket)
	if err != nil {
		return fmt.Errorf("连不上 %s：%w", appserver.Socket, err)
	}
	defer nc.Close()
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(nc, os.Stdin)
		nc.Close() // 标准输入断开（SSH 断了）：让另一个方向也收住
		done <- struct{}{}
	}()
	go func() {
		io.Copy(os.Stdout, nc)
		done <- struct{}{}
	}()
	<-done
	return nil
}

// spawn 以新会话（setsid）在后台起一个 app-server，stdout、stderr 写进 .data/app-server/app-server.log：
// 起它的进程退出、SSH 断开都不影响它。TERN_HOME 等环境变量照原样继承。
// 返回的通道在子进程退出（Wait 回收完）后关闭，start 据此不再空等。
func spawn(dev bool) (<-chan struct{}, error) {
	if err := os.MkdirAll(appserver.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("建 %s：%w", appserver.Dir, err)
	}
	logFile, err := os.OpenFile(appserver.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开 %s：%w", appserver.LogFile, err)
	}
	defer logFile.Close()
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("取自己的可执行文件：%w", err)
	}
	args := []string{"app-server"}
	if dev {
		args = append(args, "-dev")
	}
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 app-server：%w", err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait() // 收回子进程，免得留下僵尸；这里也不等它退出
		close(exited)
	}()
	return exited, nil
}

// logTail 是 app-server.log 的最后几行，子进程提前退出时带进错误里。
func logTail() string {
	data, err := os.ReadFile(appserver.LogFile)
	if err != nil {
		return "日志读不到"
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > logTailLines {
		lines = lines[len(lines)-logTailLines:]
	}
	return strings.Join(lines, "\n")
}

// readPID 是 pid 文件里记的进程号，读不到时返回 0。
func readPID() int {
	data, err := os.ReadFile(appserver.PIDFile)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// info 从 pid 文件和握手结果拼出 start 的输出。
func info(srv appserver.ServerInfo) Info {
	return Info{Socket: appserver.Socket, PID: readPID(), Version: srv.Version}
}
