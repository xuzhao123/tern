package appclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/xuzhao123/tern/internal/appserver"
	"github.com/xuzhao123/tern/internal/protocol"
)

// 界面这一侧的一条连接：所有请求共用一个连接，一条事件流是这条连接上的一个订阅。
//
// 连接断了（ssh 断了、app-server 重启了）界面不用管：这里按 1、2、4…秒退避重连，最多 30 秒；
// 本机模式每次先 start，远程模式重新起 ssh。重连期间请求立刻失败（ErrReconnecting），
// 正在看的订阅不结束——连上后按每个订阅最后收到的 seq 重新 thread/resume 补齐。
type Client struct {
	name  string                             // 界面名字，写进 initialize：app-server 记在审计记录的 Detail 里
	alias string                             // -remote 的 ssh 别名；空表示本机
	dial  func() (io.ReadWriteCloser, error) // 建立一条物理连接：本机是控制 socket，远程是一条 ssh

	mu        sync.Mutex
	conn      *protocol.Conn
	srv       appserver.ServerInfo
	subs      map[string]*Subscription
	state     State
	onStarted func(json.RawMessage)
	onStatus  func(threadID, status string)
	onState   func(State)

	closing   chan struct{} // Close 之后关闭：重连循环据此退出
	closeOnce sync.Once
}

// State 是连接状态：界面据此在顶栏显示已连接或重连中。
type State string

const (
	StateConnected    State = "connected"
	StateReconnecting State = "reconnecting"
)

// ErrReconnecting：连接断了、正在重连，请求立刻失败。网页端据此回 503“正在重连 <host>”。
var ErrReconnecting = errors.New("正在重连")

const (
	reconnectMin  = time.Second      // 第一次重试之前等多久，之后每次翻倍
	reconnectMax  = 30 * time.Second // 退避的上限
	resumeTimeout = 10 * time.Second // 重连后重新 thread/resume 的时限
)

// Event 是订阅收到的一条：对话里的一个事件，或者对话已结束（End）。
type Event struct {
	ThreadID string
	Data     json.RawMessage // 事件的 JSON，界面原样转给页面
	End      bool

	seq int64 // 事件的序号：重连后按它接着补齐，界面用不着
}

// Dial 连本机 app-server 并完成 initialize：name 是这个界面的名字（网页端 "tern web"，chat "tern chat"）。
// 连不上时返回的错误带 ErrNotRunning；要先保证 app-server 在运行的调用方先调 Start。
// 连上之后断线由这里的重连循环接手，请求方什么都不用做。
func Dial(name string) (*Client, appserver.ServerInfo, error) {
	return dial(name, "", dialLocal)
}

// DialRemote 经 ssh 连远端 app-server：ssh 的标准输入输出就是协议通道，initialize 同本机。
// 远端由 proxy 保证 app-server 在运行；连不上时立即返回错误（带上 ssh 的报错），不进入重连。
func DialRemote(name, alias string) (*Client, appserver.ServerInfo, error) {
	if err := ValidAlias(alias); err != nil {
		return nil, appserver.ServerInfo{}, err
	}
	return dial(name, alias, func() (io.ReadWriteCloser, error) { return dialSSH(alias) })
}

func dial(name, alias string, d func() (io.ReadWriteCloser, error)) (*Client, appserver.ServerInfo, error) {
	c := &Client{name: name, alias: alias, dial: d, subs: map[string]*Subscription{}, state: StateConnected, closing: make(chan struct{})}
	if err := c.attach(); err != nil {
		return nil, appserver.ServerInfo{}, err
	}
	go c.watch()
	return c, c.Info(), nil
}

// Info 是连上的 app-server 报的版本、协议版本、主机与 TERN_HOME，启动时打印给人看。
func (c *Client) Info() appserver.ServerInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.srv
}

// Host 是顶栏显示的主机：远程模式是 ssh 别名，本机模式是 app-server 报的主机名。
func (c *Client) Host() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.alias != "" {
		return c.alias
	}
	return c.srv.Host
}

// State 是现在的连接状态。
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// OnState 注册连接状态的回调：断线进入重连、重连成功后各来一次，界面据此提示（chat 的灰字、网页端的顶栏）。
func (c *Client) OnState(f func(State)) {
	c.mu.Lock()
	c.onState = f
	c.mu.Unlock()
}

// OnStarted 注册 thread/started 的回调：有新的对话（界面开的、评测或导入产生的）时调用。
func (c *Client) OnStarted(f func(json.RawMessage)) {
	c.mu.Lock()
	c.onStarted = f
	c.mu.Unlock()
}

// OnStatus 注册 thread/status/changed 的回调：一轮开始、结束时各来一次，status 是 active 或 idle。
func (c *Client) OnStatus(f func(threadID, status string)) {
	c.mu.Lock()
	c.onStatus = f
	c.mu.Unlock()
}

// Call 发一次请求。params 为 nil 时不带参数；result 为 nil 时忽略回复。
// 协议错误原样返回，界面按 Code 还原成 HTTP 状态；重连期间立即失败，错误是 ErrReconnecting。
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	conn, err := c.connection()
	if err != nil {
		return err
	}
	return conn.Call(ctx, method, params, result)
}

// Subscribe 订阅一条对话：把 after 之后的事件全部推来，再推新事件；对话结束时最后一条 End 为 true。
// 收件放在这个订阅自己的队列里，界面读得慢不会卡住同一连接上的其他调用。
func (c *Client) Subscribe(ctx context.Context, threadID string, after int64) (*Subscription, error) {
	conn, err := c.connection()
	if err != nil {
		return nil, err
	}
	// 编号由界面取：推送可能比 thread/resume 的回复先到，得在发请求之前就登记好。
	s := &Subscription{client: c, id: newID(), threadID: threadID, last: after, ready: make(chan struct{}, 1)}
	c.mu.Lock()
	c.subs[s.id] = s
	c.mu.Unlock()
	var res struct {
		Seq int64 `json:"seq"`
	}
	params := map[string]any{"threadId": threadID, "after": after, "subscription": s.id}
	if err := conn.Call(ctx, "thread/resume", params, &res); err != nil {
		c.mu.Lock()
		delete(c.subs, s.id)
		c.mu.Unlock()
		return nil, err
	}
	s.mu.Lock()
	s.seq = res.Seq
	s.mu.Unlock()
	return s, nil
}

// Close 断开与 app-server 的连接并停掉重连（网页端、chat 退出时；网页端在菜单里断开一台主机也走这里）。
// 还开着的订阅一起结束：界面那边正在看的事件流随之收住（网页端写一条 event: end），不会一直等下去。
func (c *Client) Close() {
	c.closeOnce.Do(func() { close(c.closing) })
	c.mu.Lock()
	conn := c.conn
	subs := make([]*Subscription, 0, len(c.subs))
	for _, s := range c.subs {
		subs = append(subs, s)
	}
	c.subs = map[string]*Subscription{}
	c.mu.Unlock()
	for _, s := range subs {
		s.finish()
	}
	if conn != nil {
		conn.Close()
	}
}

// connection 是这次请求用的连接。重连期间立刻失败：请求不排队、不重发，断在半路的那一次照实报错。
func (c *Client) connection() (*protocol.Conn, error) {
	c.mu.Lock()
	conn, state := c.conn, c.state
	c.mu.Unlock()
	if state != StateConnected || conn == nil {
		return nil, c.reconnecting()
	}
	select {
	case <-conn.Closed():
		// 刚断、重连循环还没来得及改状态：照样当作正在重连。
		return nil, c.reconnecting()
	default:
	}
	return conn, nil
}

// reconnecting 是重连期间请求失败的错误：带上主机，界面能认出来是在等重连。
func (c *Client) reconnecting() error {
	return fmt.Errorf("%w %s", ErrReconnecting, c.Host())
}

// attach 连上并登记这条连接；旧的连接收掉（重连时换成新的那条）。
func (c *Client) attach() error {
	rwc, err := c.dial()
	if err != nil {
		return err
	}
	conn, srv, err := initialize(rwc, c.name, c.handle)
	if err != nil {
		if errors.Is(err, ErrHandshakeTimeout) {
			// 连一台开着机却连不通的主机时，ssh 自己要等到 TCP 超时才退出；这里说清是哪一台、等的是哪一步。
			name := c.alias
			if name == "" {
				name = "本机"
			}
			err = fmt.Errorf("连 %s 超时：%d 秒内没有完成握手", name, int(handshakeTimeout.Seconds()))
		}
		return sshError(rwc, err)
	}
	c.mu.Lock()
	old := c.conn
	c.conn, c.srv = conn, srv
	c.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

// watch 看着当前连接：断了就进入重连，连上后改回已连接。界面退出（Close）时跟着停。
func (c *Client) watch() {
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn == nil {
			return
		}
		select {
		case <-conn.Closed():
		case <-c.closing:
			return
		}
		select {
		case <-c.closing:
			return
		default:
		}
		c.setState(StateReconnecting)
		if !c.retry() {
			return
		}
		c.setState(StateConnected)
	}
}

// retry 一直试到连上或界面退出：失败后等 1、2、4…秒再试，最多 30 秒。
func (c *Client) retry() bool {
	delay := reconnectMin
	for {
		if err := c.reattach(); err == nil {
			return true
		}
		select {
		case <-time.After(delay):
		case <-c.closing:
			return false
		}
		if delay *= 2; delay > reconnectMax {
			delay = reconnectMax
		}
	}
}

// reattach 换一条新连接：本机模式先保证 app-server 在运行，远程模式重新起 ssh（远端的 proxy 自己 start）。
// 连上后把每个订阅按最后收到的 seq 重新 thread/resume，中间的事件由 app-server 补推。
func (c *Client) reattach() error {
	if c.alias == "" {
		if _, err := Start(false); err != nil {
			return err
		}
	}
	if err := c.attach(); err != nil {
		return err
	}
	c.mu.Lock()
	conn := c.conn
	subs := make([]*Subscription, 0, len(c.subs))
	for _, s := range c.subs {
		subs = append(subs, s)
	}
	c.mu.Unlock()
	for _, s := range subs {
		ctx, cancel := context.WithTimeout(context.Background(), resumeTimeout)
		err := s.resumeOn(ctx, conn)
		cancel()
		if err == nil {
			continue
		}
		// 只有对话已经不在了（404）才结束这个订阅；别的失败多半是连接又断了（网络抖动），
		// 留着它，下一次重连再按最后收到的 seq 补齐。
		if Code(err) != 404 {
			continue
		}
		c.mu.Lock()
		delete(c.subs, s.id)
		c.mu.Unlock()
		s.finish()
	}
	return nil
}

// setState 记下连接状态并通知界面。
func (c *Client) setState(st State) {
	c.mu.Lock()
	c.state = st
	f := c.onState
	c.mu.Unlock()
	if f != nil {
		f(st)
	}
}

// handle 是协议连接上的通知：订阅的事件进各自的队列，另外两种交给界面注册的回调。
// 它在连接的读循环里跑，不能阻塞，所以这里只加锁放进队列。
func (c *Client) handle(m protocol.Message) {
	switch m.Method {
	case "thread/event":
		var p struct {
			Subscription string          `json:"subscription"`
			ThreadID     string          `json:"threadId"`
			Event        json.RawMessage `json:"event"`
			End          bool            `json:"end"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		c.mu.Lock()
		s := c.subs[p.Subscription]
		c.mu.Unlock()
		if s == nil {
			return
		}
		// 再取一次序号：重连后要按最后收到的那条接着补齐。事件里本来就有 seq，这里只解出这一个字段。
		var seq struct {
			Seq int64 `json:"seq"`
		}
		if len(p.Event) > 0 && json.Unmarshal(p.Event, &seq) != nil {
			return
		}
		s.push(Event{ThreadID: p.ThreadID, Data: p.Event, End: p.End, seq: seq.Seq})
	case "thread/started":
		c.mu.Lock()
		f := c.onStarted
		c.mu.Unlock()
		if f != nil {
			f(m.Params)
		}
	case "thread/status/changed":
		var p struct {
			ThreadID string `json:"threadId"`
			Status   string `json:"status"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		c.mu.Lock()
		f := c.onStatus
		c.mu.Unlock()
		if f != nil {
			f(p.ThreadID, p.Status)
		}
	}
}

// Subscription 是一次订阅：界面从它依次取事件，直到 end；Close 就退订。
// 连接断了它不结束，重连后 appclient 自己在新连接上补齐。
type Subscription struct {
	client   *Client
	id       string
	threadID string

	mu       sync.Mutex
	seq      int64 // thread/resume 回复里的 seq，见 Seq
	last     int64 // 最后收到的事件的序号：重连后从这里接着补齐
	queue    []Event
	ready    chan struct{} // 有新事件或结束时通知 Next
	finished bool
}

// Seq 是订阅那一刻对话最后一个事件的序号（thread/resume 的回复）：序号不超过它的是已有事件。
// 接一条正在跑的对话时，界面用它分清哪些要显示、哪些只用来补齐轮次和还没人答的审批。
func (s *Subscription) Seq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// Next 取下一条事件，没有就等到有为止。订阅结束（对话结束、界面退订、重连后补不齐）时返回 false；
// 连接断开、正在重连时只是等着，界面那边的事件流一直开着。
func (s *Subscription) Next(ctx context.Context) (Event, bool) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			e := s.queue[0]
			s.queue = s.queue[1:]
			// 队列里还有：给自己留一个信号，取完这条回来不用等下一次推送。
			if len(s.queue) > 0 {
				select {
				case s.ready <- struct{}{}:
				default:
				}
			}
			s.mu.Unlock()
			return e, true
		}
		finished := s.finished
		s.mu.Unlock()
		if finished {
			return Event{}, false
		}
		select {
		case <-s.ready:
		case <-ctx.Done():
			return Event{}, false
		}
	}
}

// Close 退订：先结束自己这边的取件，再告诉 app-server 不用再推。连接已经断了就不用发了。
func (s *Subscription) Close() {
	s.finish()
	c := s.client
	c.mu.Lock()
	delete(c.subs, s.id)
	conn, state := c.conn, c.state
	c.mu.Unlock()
	if conn == nil || state != StateConnected {
		return
	}
	select {
	case <-conn.Closed():
		return
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), unsubscribeTimeout)
	defer cancel()
	conn.Call(ctx, "thread/unsubscribe", map[string]string{"subscription": s.id}, nil)
}

// resumeOn 在重连后的新连接上重新 thread/resume：after 是这个订阅最后收到的 seq，
// 中间的事件由 app-server 补推，界面那边接着显示，既不重复也不缺。
func (s *Subscription) resumeOn(ctx context.Context, conn *protocol.Conn) error {
	s.mu.Lock()
	after, finished := s.last, s.finished
	s.mu.Unlock()
	if finished {
		return nil
	}
	return conn.Call(ctx, "thread/resume", map[string]any{"threadId": s.threadID, "after": after, "subscription": s.id}, nil)
}

// push 收下一条：只放进队列，读得慢的界面不会堵住连接（每块内容也不大，队列不限长）。
func (s *Subscription) push(e Event) {
	s.mu.Lock()
	if e.seq > s.last {
		s.last = e.seq
	}
	s.queue = append(s.queue, e)
	if e.End {
		s.finished = true
	}
	s.mu.Unlock()
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *Subscription) finish() {
	s.mu.Lock()
	s.finished = true
	s.mu.Unlock()
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

// Code 是协议错误里的 code：方法出错时是原来网页端的 HTTP 状态码。连接断了、协议本身的错误没有 code，返回 0。
func Code(err error) int {
	var e *protocol.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// newID 给一次订阅取个编号：随机，同一个连接里不重。
func newID() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}
