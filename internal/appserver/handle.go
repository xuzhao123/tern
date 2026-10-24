package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/xuzhao123/tern/internal/protocol"
)

// ServerInfo 是 initialize 的回复：版本只用来显示，协议版本给界面核对，host 与 home 说明连的是哪台机器、哪个数据目录。
type ServerInfo struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	Host     string `json:"host"`
	Home     string `json:"home"`
}

// 一个连接一份状态：第一条必须是 initialize，之后的方法才认。
type conn struct {
	s      *Server
	nc     net.Conn
	pc     *protocol.Conn
	ready  chan struct{} // pc 赋值之后关闭：handle 里要往同一个连接回话
	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	initialized bool
	client      string                        // initialize 里的 client.name：审计记录的 Detail 写它，看得出是哪个界面点的
	subs        map[string]context.CancelFunc // 这条连接上的订阅：界面取的编号 → 退订
	uploads     map[string][]byte             // 收到一半的分块上传，归这个连接所有
	out         []protocol.Message            // 发给所有连接的通知排队写，不卡 run.add
	outReady    chan struct{}
}

// newConn 先建出 protocol.Conn，再把状态装上：NewConn 要求建的时候就给 handle，而 handle 里要往这个连接回话。
func newConn(s *Server, nc net.Conn) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{s: s, nc: nc, ready: make(chan struct{}), ctx: ctx, cancel: cancel,
		subs: map[string]context.CancelFunc{}, uploads: map[string][]byte{}, outReady: make(chan struct{}, 1)}
	c.pc = protocol.NewConn(nc, nc, func(m protocol.Message) {
		<-c.ready
		// initialize 不会阻塞，就在读循环里直接处理：一次写进来的 initialize 和紧跟的请求，
		// 后者一定排在它后面，不会先被回“先发 initialize”。
		if m.Method == "initialize" {
			c.handle(m)
			return
		}
		// 别的请求各起一个 goroutine：Conn 的 handle 不能阻塞，写同一个连接由 Conn 自己排队。
		go c.handle(m)
	})
	close(c.ready)
	go c.flush()
	return c
}

func (c *conn) handle(m protocol.Message) {
	if m.ID == nil {
		return // 这一期没有界面发给 app-server 的通知
	}
	if m.Method != "initialize" && !c.isInitialized() {
		c.fail(*m.ID, -32600, "先发 initialize")
		return
	}
	if m.Method == "initialize" {
		c.initialize(m)
		return
	}
	if m.Method == "server/shutdown" {
		c.pc.Reply(*m.ID, nil, nil)
		c.s.Shutdown() // 先回复，再关监听和所有连接、删掉 socket 与 pid 文件
		return
	}
	call := methods[m.Method]
	if call == nil {
		c.fail(*m.ID, -32601, "没有这个方法："+m.Method)
		return
	}
	result, err := call(c, m)
	c.pc.Reply(*m.ID, result, err)
}

// methods 是这一期的全部方法，见设计第 5 节。每个方法返回的结果原样成为 JSON-RPC 的 result，出错时 code 是原来网页端的 HTTP 状态码。
var methods = map[string]func(*conn, protocol.Message) (any, *protocol.Error){
	"thread/list":        (*conn).threadList,
	"thread/start":       (*conn).threadStart,
	"thread/resume":      (*conn).threadResume,
	"thread/unsubscribe": (*conn).threadUnsubscribe,
	"thread/rewind":      (*conn).threadRewind,
	"thread/import":      (*conn).threadImport,
	"thread/attribute":   (*conn).threadAttribute,
	"turn/start":         (*conn).turnStart,
	"turn/steer":         (*conn).turnSteer,
	"turn/interrupt":     (*conn).turnInterrupt,
	"approval/respond":   (*conn).approvalRespond,
	"settings/read":      (*conn).settingsRead,
	"settings/write":     (*conn).settingsWrite,
	"workspace/add":      (*conn).workspaceAdd,
	"workspace/remove":   (*conn).workspaceRemove,
	"model/list":         (*conn).modelList,
	"command/list":       (*conn).commandList,
	"attachment/upload":  (*conn).attachmentUpload,
	"attachment/read":    (*conn).attachmentRead,
	"files/list":         (*conn).filesList,
	"files/read":         (*conn).filesRead,
	"files/write":        (*conn).filesWrite,
	"files/delete":       (*conn).filesDelete,
	"browser/frame":      (*conn).browserFrame,
	"trace/spans":        (*conn).traceSpans,
	"trace/metrics":      (*conn).traceMetrics,
	"memory/list":        (*conn).memoryList,
	"memory/forget":      (*conn).memoryForget,
	"audit/list":         (*conn).auditList,
	"hub/list":           (*conn).hubList,
	"hub/enabled":        (*conn).hubEnabled,
	"hub/skill/create":   (*conn).skillCreate,
	"hub/skill/delete":   (*conn).skillDelete,
	"hub/skill/toggle":   (*conn).skillToggle,
	"hub/mcp/add":        (*conn).mcpAdd,
	"hub/mcp/delete":     (*conn).mcpDelete,
	"hub/mcp/toggle":     (*conn).mcpToggle,
	"hub/mcp/test":       (*conn).mcpTest,
	"eval/list":          (*conn).evalList,
	"eval/cases":         (*conn).evalCases,
	"eval/get":           (*conn).evalGet,
	"eval/start":         (*conn).evalStart,
	"eval/action":        (*conn).evalAction,
}

// initialize 核对协议版本：版本对不上就拒绝，两边的方法与参数可能读不懂。
func (c *conn) initialize(m protocol.Message) {
	var p struct {
		Client struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"client"`
		Protocol int `json:"protocol"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		c.fail(*m.ID, -32602, "initialize 参数不对："+err.Error())
		return
	}
	if p.Protocol != Protocol {
		c.fail(*m.ID, -32600, fmt.Sprintf("协议版本不一致：客户端 %d，app-server %d", p.Protocol, Protocol))
		return
	}
	fmt.Fprintf(os.Stderr, "界面已连接：%s %s\n", p.Client.Name, p.Client.Version)
	name := strings.TrimSpace(p.Client.Name)
	if name == "" {
		name = "未知界面" // 界面没报名字（不是这三种界面自己连的），审计里也要看得出
	}
	c.mu.Lock()
	c.initialized, c.client = true, name
	c.mu.Unlock()
	c.pc.Reply(*m.ID, map[string]any{"server": info()}, nil)
}

// clientName 是这个连接的界面名字，写进审计记录的 Detail。
func (c *conn) clientName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

func (c *conn) isInitialized() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.initialized
}

func (c *conn) fail(id int64, code int, message string) {
	c.pc.Reply(id, nil, &protocol.Error{Code: code, Message: message})
}

// bad 是方法出错时的错误：code 填现在网页端返回的 HTTP 状态码，界面照 code 原样还原成 HTTP 状态。
func bad(code int, message string) *protocol.Error {
	return &protocol.Error{Code: code, Message: message}
}

// decode 解出参数；请求体不是 JSON 时与原来一样回 400。
func decode(m protocol.Message, v any) *protocol.Error {
	if err := json.Unmarshal(m.Params, v); err != nil {
		return bad(400, "请求必须是JSON")
	}
	return nil
}

// userName 是请求里带的登录账号：网页端填登录名，用于设置的个人层和 agent 审计里的发起人；chat 不带。
func userName(m protocol.Message) string {
	var p struct {
		User string `json:"user"`
	}
	json.Unmarshal(m.Params, &p)
	return p.User
}

func info() ServerInfo {
	host, _ := os.Hostname()
	return ServerInfo{Version: Version(), Protocol: Protocol, Host: host, Home: home()}
}

// home 是 TERN_HOME 的绝对路径：main 已经 chdir 过去，环境变量也写回了。
func home() string {
	if dir := os.Getenv("TERN_HOME"); dir != "" {
		return dir
	}
	dir, _ := os.Getwd()
	return dir
}

// Version 取构建信息里的提交号前 12 位，改过工作区时加 +dirty；取不到（如 go run）写 devel。只用来显示。
func Version() string {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	var rev string
	dirty := false
	for _, s := range build.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "devel"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}

// broadcast 把通知发给所有连接。各连接各发各的队列，写不出去只堵住它自己，不堵住会话（run.add）。
func (s *Server) broadcast(method string, params any) {
	data, err := json.Marshal(params)
	if err != nil {
		return
	}
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.enqueue(method, data)
	}
}

func (c *conn) enqueue(method string, params json.RawMessage) {
	c.mu.Lock()
	c.out = append(c.out, protocol.Message{Method: method, Params: params})
	c.mu.Unlock()
	select {
	case c.outReady <- struct{}{}:
	default:
	}
}

// flush 按排队顺序写这个连接上的广播通知；排队而不是直接在 run.add 里写，慢连接（比如 SSH 卡住）不会卡住对话。
func (c *conn) flush() {
	for {
		select {
		case <-c.outReady:
			for {
				c.mu.Lock()
				if len(c.out) == 0 {
					c.mu.Unlock()
					break
				}
				m := c.out[0]
				c.out = c.out[1:]
				c.mu.Unlock()
				c.pc.Notify(m.Method, m.Params)
			}
		case <-c.pc.Closed():
			return
		}
	}
}
