package appserver

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/xuzhao123/tern/internal/tenant"
)

// 常驻的 app-server：一个 TERN_HOME 一个，在 .data/app-server/control.sock 上等界面连进来。
// socket 用相对路径（工作目录就是 TERN_HOME），免得超过 Unix socket 路径 108 字节的上限。
// 谁能连进来由所在目录决定：.data/app-server/ 是 0700，经 SSH 时就是登录的那个用户，不再另外鉴权。
const (
	Dir       = ".data/app-server"
	Socket    = Dir + "/control.sock"
	LlmSocket = Dir + "/llm.sock"
	PIDFile   = Dir + "/app-server.pid"
	LogFile   = Dir + "/app-server.log"
	lockFile  = Dir + "/lock"
)

// Protocol 是控制 socket 上的协议版本：改了方法或参数的含义就加一。界面只比这个数，界面那一侧在 internal/appclient。
const Protocol = 1

type Server struct {
	ln   net.Listener
	llm  *http.Server // 模型代理，跑在 llm.sock 上，路由同原来的 POST /llm/<对话ID>
	lock *os.File     // 文件锁一直开着，进程退出（包括崩溃）时由系统释放

	dir string // TERN_HOME：对话存档、沙箱、附件都相对它
	dev bool   // 每次启动 agent 用 go run .（要求 TERN_HOME 是 tern 源码根目录）

	mu    sync.Mutex
	conns map[*conn]struct{}

	runs  []*run
	hubMu sync.Mutex // 保护 .data/hub.json 与 .data/settings.json 的读改写
	evals evals      // 评测记录（见 eval.go）

	once sync.Once
	done chan struct{}
}

// Run 前台运行 app-server：拿文件锁、写 pid、监听控制 socket 与模型代理 socket，直到收到 server/shutdown、SIGINT 或 SIGTERM。
// 给人看的日志写 stderr；后台启动时（appclient）stderr 接到 .data/app-server/app-server.log。
func Run(args []string) error {
	flags := flag.NewFlagSet("app-server", flag.ContinueOnError)
	dev := flags.Bool("dev", false, "每次启动 agent 用 go run .：改完 agent 代码不必重启 app-server，代价是每次多一次编译")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不认识的参数：%s", flags.Arg(0))
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	// -dev 每轮用 go run . 启动 agent，所以 TERN_HOME 得是 tern 源码根目录。
	if *dev && !tenant.SourceRoot(dir) {
		return fmt.Errorf("-dev 要用 go run . 启动 agent，TERN_HOME 须是 tern 源码根目录：%s", dir)
	}
	// hooks 配置里的相对路径按 TERN_HOME 解析：agent 与 app-server 的工作目录都是它。
	hooksBase = dir
	s, err := open(dir, *dev)
	if err != nil {
		return err
	}
	defer s.Shutdown()
	s.loadRuns()
	fmt.Fprintf(os.Stderr, "app-server 在 %s 监听（pid %d）\n", Socket, os.Getpid())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigs:
			s.Shutdown() // 和 server/shutdown 一样收尾：删掉 socket 与 pid 文件再退出
		case <-s.done:
		}
	}()
	return s.accept()
}

// open 建好目录，拿文件锁并写 pid，删掉上次留下的 socket，再开始监听控制 socket 与模型代理 socket。
func open(dir string, dev bool) (*Server, error) {
	if err := os.MkdirAll(Dir, 0o700); err != nil {
		return nil, fmt.Errorf("建 %s：%w", Dir, err)
	}
	lock, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("这个 TERN_HOME 已经有 app-server 在运行（pid %s）", pidText())
		}
		return nil, err
	}
	if err := os.WriteFile(PIDFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		lock.Close()
		return nil, fmt.Errorf("写 %s：%w", PIDFile, err)
	}
	s := &Server{lock: lock, dir: dir, dev: dev, conns: map[*conn]struct{}{}, done: make(chan struct{}),
		evals: evals{jobs: map[string]*evalJob{}}}
	os.Remove(Socket)
	if s.ln, err = net.Listen("unix", Socket); err != nil {
		lock.Close()
		return nil, fmt.Errorf("监听 %s：%w", Socket, err)
	}
	// 模型代理同样用一个固定的 Unix socket：app-server 没有端口，同一台机器上还可以有好几个 TERN_HOME。
	os.Remove(LlmSocket)
	llm, err := net.Listen("unix", LlmSocket)
	if err != nil {
		s.ln.Close()
		lock.Close()
		return nil, fmt.Errorf("监听 %s：%w", LlmSocket, err)
	}
	s.llm = &http.Server{Handler: s.llmHandler()}
	go s.llm.Serve(llm)
	return s, nil
}

// pidText 是 pid 文件里记的进程号，读不到时写“?”。
func pidText() string {
	data, err := os.ReadFile(PIDFile)
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(data))
}

// Shutdown 关监听、断开所有连接，再删掉 socket 与 pid 文件；重复调用无副作用。
// 正在跑的 agent 不动：新的 app-server 起来后由它接管。
func (s *Server) Shutdown() {
	s.once.Do(func() {
		s.ln.Close()
		s.llm.Close()
		s.mu.Lock()
		for c := range s.conns {
			c.nc.Close()
		}
		s.mu.Unlock()
		os.Remove(Socket)
		os.Remove(LlmSocket)
		os.Remove(PIDFile)
		close(s.done)
	})
}

// accept 挨个接受连接，直到 Shutdown 关掉监听。
func (s *Server) accept() error {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			// Shutdown 关掉监听之后 Accept 立刻失败，这不是错误。
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.serve(nc)
	}
}

// serve 服务一个连接，直到它断开或 Shutdown 把它关掉；断开即退订这个连接上的全部订阅。
func (s *Server) serve(nc net.Conn) {
	c := newConn(s, nc)
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	<-c.pc.Closed()
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	c.cancel() // 推送与在处理的请求都跟着退出；收到一半的上传随连接一起丢掉
	nc.Close()
}
