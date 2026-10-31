// Package preview 在独立的无头 Chrome 里打开用户显式授权的本机开发服务，只把画面和一组受限动作交给网页端。
//
// 隔离分两层：Chrome 不套 bwrap、保留自己的沙箱；每个会话的流量都经一个只放行授权 origin 的代理（见 proxy.go）。
// 代理管不到非网络的地址（CDP 直接导航到 file:// 能读到本地文件），所以 CDP 只在 tern 进程里用，
// 对外只有下面这几种动作，导航前在这里查 scheme 与 origin。
package preview

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const (
	// DefaultWidth 与 DefaultHeight 是页面没给尺寸时的视口。
	DefaultWidth, DefaultHeight = 1280, 800

	minWidth, maxWidth   = 320, 2560
	minHeight, maxHeight = 240, 1600

	// maxFrame 是协议的二进制块上限（appserver.MaxChunk）：更大的帧直接丢掉，不改 protocol 的上限。
	maxFrame = 4 << 20
	maxText  = 4096

	// maxQueue 是一个会话里排队等执行的动作数：满了回 429，不悄悄丢。
	maxQueue = 64
	// maxWait 是取画面最多等多久。
	maxWait = 10 * time.Second

	// maxClients 与 maxClientLen 是页面客户端的上限：一个会话最多记 32 个客户端的序号，
	// 键最长 64 字节；多了就丢最久没动的那个，免得这张表无界。
	maxClients   = 32
	maxClientLen = 64

	// 30 分钟没有取画面、也没有动作就自动关。
	idleTimeout = 30 * time.Minute
	// 一个动作在 Chrome 里最多等这么久。
	actionTimeout = 15 * time.Second
	// 开一个会话（起 Chrome、第一次导航）最多等这么久。
	openTimeout = 30 * time.Second
	// 每隔这么久看一眼主框架的地址。
	originTick = 500 * time.Millisecond
	// 换页后每 100 毫秒读一次地址，连着两次一样就算稳了（最多 2 秒）。
	settleTick  = 100 * time.Millisecond
	settleTries = 20
)

// Error 是给界面的错误：Code 沿用网页端的 HTTP 状态码（403 不在授权范围、404 没有会话、409 顺序或视口不对、429 队列满、500 起不来）。
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func fail(code int, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Status 是会话描述：界面只靠它显示状态，拿不到 CDP 地址、代理端口与 profile 路径。
type Status struct {
	State        string `json:"state"`
	Origin       string `json:"origin"`
	URL          string `json:"url"`
	Viewport     int64  `json:"viewport"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Frame        int64  `json:"frame"`
	Denied       int    `json:"denied"`
	LeftOrigin   bool   `json:"left_origin"`
	PopupBlocked int    `json:"popup_blocked"`
	Reason       string `json:"reason,omitempty"`
}

// Frame 是取画面的一次回复：没有比 after 新的帧时只带 Frame（等于 after），不带数据。
type Frame struct {
	Frame  int64  `json:"frame"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Data   []byte `json:"data,omitempty"`
}

// Action 是界面能发的一个动作：Kind 加它自己的参数，坐标是视口 CSS 像素。
// Client 是页面这次建视图时随机生成的字符串，Seq 在这个 Client 内递增（页面刷新、第二个标签页、重建视图各算各的）。
type Action struct {
	Kind      string
	Client    string
	Seq       int64
	Viewport  int64
	URL       string
	Width     int
	Height    int
	Type      string
	X, Y      float64
	Button    string
	Clicks    int
	DX, DY    float64
	Modifiers []string
	Key       string
	Code      string
	Text      string
}

// clientTrack 是一个页面客户端最后收下的动作序号：同一个 client 的 seq 必须比它大，
// 超过 maxClients 时按 used 丢最久没动的那个。
type clientTrack struct {
	seq  int64
	used time.Time
}

// grant 是一次授权：只认一个 origin（scheme://host:port），同 origin 的任意路径都可以。
// P1 只接受本机开发服务：主机是字面的 localhost、127.0.0.1 或 [::1]，端口必填。
type grant struct {
	scheme string
	host   string // localhost、127.0.0.1 或 [::1]：原样写进 origin，也原样跟代理收到的 host 比
	port   string
	origin string // scheme://host:port
	dial   string // 放行的连接一律连到这里，不按请求里的名字解析
}

// parseGrant 把一个地址变成授权：不接受 userinfo、别的主机名、别的 scheme。
func parseGrant(raw string) (grant, *Error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return grant{}, fail(403, "地址看不懂：%s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return grant{}, fail(403, "预览只接受 http:// 或 https:// 地址")
	}
	if u.User != nil {
		return grant{}, fail(403, "地址里不能带用户名和密码")
	}
	host := u.Hostname()
	switch host {
	case "localhost", "127.0.0.1":
	case "::1":
		host = "[::1]"
	default:
		return grant{}, fail(403, "预览只接受本机开发服务：主机只能是 localhost、127.0.0.1 或 [::1]")
	}
	port := u.Port()
	if port == "" {
		return grant{}, fail(403, "地址里要写端口，例如 http://localhost:3000")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return grant{}, fail(403, "端口要在 1 到 65535 之间")
	}
	g := grant{scheme: u.Scheme, host: host, port: port, origin: u.Scheme + "://" + host + ":" + port}
	if host == "[::1]" {
		g.dial = "[::1]:" + port
	} else {
		g.dial = "127.0.0.1:" + port
	}
	return g, nil
}

// Manager 管一个 app-server 里的全部预览会话：一个对话同时最多一个会话。
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session

	reason string // 这台机器上用不了预览的原因；空表示能用
	chrome string // Chrome 可执行文件的路径
}

// NewManager 判断这台机器能不能用预览：找得到 Chrome 就行，不真的起浏览器（起不起得来要等第一次 open）。
func NewManager() *Manager {
	m := &Manager{sessions: map[string]*Session{}}
	m.chrome, m.reason = findChrome()
	return m
}

// Reason 是不能用预览的原因：空表示能用，界面据此决定还显不显示地址栏。
func (m *Manager) Reason() string { return m.reason }

// Open 让这条对话授权一个地址并开一个会话：同一个 origin 已经有会话就复用，换 origin 先关旧的。
// 起不来时不回错误，回的会话描述里 state 是 failed、reason 是原因，界面照实显示。
func (m *Manager) Open(threadID, raw string, width, height int) (Status, *Error) {
	if threadID == "" {
		return Status{}, fail(400, "没有对话ID")
	}
	if m.reason != "" {
		return Status{}, fail(409, "%s", m.reason)
	}
	g, ferr := parseGrant(raw)
	if ferr != nil {
		return Status{}, ferr
	}
	width, height = clampSize(width, height)

	m.mu.Lock()
	old := m.sessions[threadID]
	if old != nil && old.grant.origin == g.origin && old.usable() {
		m.mu.Unlock()
		old.touch()
		// 复用的会话可能停在 about:blank（离开过授权范围）：按这次给的地址再导航一遍，地址栏回车才回得来。
		// 这次导航是 app-server 自己发起的，不看也不推进 seq（页面不知道，也不该替它编号）。
		if old.ready() {
			if ferr := old.reopen(raw); ferr != nil {
				return Status{}, ferr
			}
		}
		return old.status(), nil
	}
	s := newSession(m, threadID, g)
	m.sessions[threadID] = s // 先登记再起 Chrome：同一个对话同时来两次 open 也只会留下一个
	m.mu.Unlock()
	if old != nil {
		old.close("换了授权目标")
	}
	if ferr := s.start(width, height, raw); ferr != nil {
		s.fail(ferr.Msg)
	}
	return s.status(), nil
}

// Get 是这条对话的会话描述；没有会话时 ok 为 false（界面回 404）。
func (m *Manager) Get(threadID string) (Status, bool) {
	s := m.session(threadID)
	if s == nil {
		return Status{}, false
	}
	return s.status(), true
}

// Frame 取比 after 新的那一帧：有就立刻回，没有就等到 wait 超时（最多 10 秒）。
func (m *Manager) Frame(ctx context.Context, threadID string, after int64, wait time.Duration) (Frame, *Error) {
	s := m.session(threadID)
	if s == nil {
		return Frame{}, fail(404, "这条对话没有预览会话")
	}
	return s.frameAt(ctx, after, wait)
}

// Action 执行一个受限动作：排队前就核验 seq 与视口版本，执行在会话的队列里串行。
func (m *Manager) Action(threadID string, a Action) *Error {
	s := m.session(threadID)
	if s == nil {
		return fail(404, "这条对话没有预览会话")
	}
	return s.act(a)
}

// Close 关掉一条对话的会话，回它的 origin（审计要写）；本来就没有会话时 ok 为 false。
func (m *Manager) Close(threadID string) (string, bool) {
	s := m.session(threadID)
	if s == nil {
		return "", false
	}
	origin := s.grant.origin
	s.close("网页端关掉了")
	return origin, true
}

// CloseAll 关掉所有会话：app-server 退出时用，Chrome 进程与私有 profile 都收掉。
func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = map[string]*Session{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.close("app-server 退出了")
	}
}

func (m *Manager) session(threadID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[threadID]
}

// forget 把会话从表里摘掉：会话自己关掉时调，摘的必须是同一个会话（换 origin 时新的可能已经登记上了）。
func (m *Manager) forget(s *Session) {
	m.mu.Lock()
	if m.sessions[s.threadID] == s {
		delete(m.sessions, s.threadID)
	}
	m.mu.Unlock()
}

// Session 是一个对话里的一个授权 origin 和一个独立的 Chrome 进程。
type Session struct {
	mgr      *Manager
	threadID string
	grant    grant
	proxy    *proxy

	ctx    context.Context // 关会话时取消：等画面的人、看地址与弹窗的两个循环都跟着停
	cancel context.CancelFunc
	tab    context.Context // chromedp 的会话：动作与取地址都用它
	stop   func()          // 关掉 Chrome
	dir    string          // Chrome 的 profile：0700 的临时目录，会话结束删除

	mu         sync.Mutex
	state      string // opening、ready、failed、closed
	reason     string
	url        string
	width      int
	height     int
	viewport   int64
	frame      int64
	data       []byte
	frameReady chan struct{} // 每来一帧关掉再换一个新的：等新帧的人靠它被叫醒
	leftOrigin bool
	popups     int
	busy       bool // 动作正在执行：看地址的循环这时不插手，由动作自己核对
	down       bool
	idle       *time.Timer
	queue      chan job

	// 每个页面客户端最后收下的动作序号：页面每次建视图换一个 client，各记各的号（见 clientTrack）。
	clients map[string]*clientTrack
}

// job 是一次排进队列的动作：done 回执行结果，发动作的人等它。
type job struct {
	run  func(context.Context) *Error
	done chan *Error
}

func newSession(m *Manager, threadID string, g grant) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{mgr: m, threadID: threadID, grant: g, ctx: ctx, cancel: cancel,
		state: "opening", viewport: 1, frameReady: make(chan struct{}), queue: make(chan job, maxQueue),
		clients: map[string]*clientTrack{}}
}

// start 起代理与 Chrome，按视口大小打开授权地址，开始推画面，再把动作队列与两个看守循环跑起来。
func (s *Session) start(width, height int, raw string) *Error {
	s.touch()

	p, err := newProxy(s.grant)
	if err != nil {
		return fail(500, "起预览代理失败：%s", err.Error())
	}
	s.proxy = p
	dir, err := os.MkdirTemp("", "tern-preview-")
	if err != nil {
		return fail(500, "建预览的临时目录失败：%s", err.Error())
	}
	os.Chmod(dir, 0o700)
	s.dir = dir

	// 不加 --no-sandbox：Chrome 用自己的沙箱，网络边界由代理负责（见 spec 的原型结论）。
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(s.mgr.chrome),
		chromedp.UserDataDir(dir),
		chromedp.Flag("proxy-server", "http://"+p.address()),
		chromedp.Flag("proxy-bypass-list", "<-loopback>"),                           // 本机地址也走代理，否则 localhost 会绕过它
		chromedp.Flag("force-webrtc-ip-handling-policy", "disable_non_proxied_udp"), // WebRTC 不许绕开代理走 UDP
		chromedp.Flag("dns-prefetch-disable", true),
		// 后台联网不是预览需要的，照 internal/sandbox 里 chrome 包装的意思一起关掉，免得代理日志里全是噪音。
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("metrics-recording-only", true),
		chromedp.Flag("disable-domain-reliability", true),
	)
	alloc, stopAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	tab, stopTab := chromedp.NewContext(alloc)
	s.tab, s.stop = tab, func() { stopTab(); stopAlloc() }
	if err := chromedp.Do(tab); err != nil {
		return fail(500, "起不来无头 Chrome（预览要求 Chrome 自己的沙箱）：%s", clip(err.Error()))
	}
	// 下载一律拒绝：预览不是下载器，落到私有目录以外就管不住了。
	if _, err := chromedp.Call(tab, browser.SetDownloadBehavior, browser.SetDownloadBehaviorParams{Behavior: browser.SetDownloadBehaviorBehaviorDeny}); err != nil {
		return fail(500, "关不掉下载：%s", clip(err.Error()))
	}
	// 无头 Chrome 里这个页面默认没有焦点：页面里点输入框、调 input.focus() 都不生效，按键与文字动作会全被丢掉。
	if _, err := chromedp.Call(tab, emulation.SetFocusEmulationEnabled, emulation.SetFocusEmulationEnabledParams{Enabled: true}); err != nil {
		return fail(500, "打不开焦点模拟：%s", clip(err.Error()))
	}
	// 开一个会话（起 Chrome、第一次导航）最多等这么久；ctx 从 chromedp 的会话上派生，动作才找得到它的连接。
	ctx, cancel := context.WithTimeout(tab, openTimeout)
	defer cancel()
	if err := s.viewportSize(tab, width, height); err != nil {
		return fail(500, "设不了视口大小：%s", clip(err.Error()))
	}
	s.mu.Lock()
	s.width, s.height = width, height
	s.mu.Unlock()

	// 先订阅再导航：订阅会缓存事件，不会漏掉第一帧；循环在标签页关闭时结束。
	frames := chromedp.Events(tab, page.ScreencastFrame)
	go func() {
		for frame, err := range frames {
			if err != nil {
				return
			}
			// 每帧都要 ack：不回的话 Chrome 最多再发三帧就停下等（screencast 的流量控制）。
			chromedp.Call(s.tab, page.ScreencastFrameAck, page.ScreencastFrameAckParams{SessionID: frame.SessionID})
			if len(frame.Data) > maxFrame {
				continue // 超过协议的块上限就丢掉这一帧
			}
			s.putFrame(frame.Data)
		}
	}()
	if ferr := s.navigate(ctx, raw); ferr != nil {
		return ferr
	}
	if err := s.startScreencast(width, height); err != nil {
		return fail(500, "开不了实时画面：%s", clip(err.Error()))
	}

	s.mu.Lock()
	s.state = "ready"
	s.mu.Unlock()
	go s.loop()
	go s.watchOrigin()
	go s.watchPopups()
	return nil
}

// fail 把会话记成失败并收掉 Chrome、代理与 profile；条目留着，界面才看得到原因，关掉或超时再清。
func (s *Session) fail(msg string) {
	s.mu.Lock()
	s.state, s.reason = "failed", msg
	s.mu.Unlock()
	s.teardown()
}

// usable 是这个会话还能用：打开中或已经就绪。失败过的会话重开一次，接着复用没意义。
func (s *Session) usable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == "opening" || s.state == "ready"
}

// ready 是会话已经就绪（动作队列在跑）：只有这时才能往里排动作。
func (s *Session) ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == "ready"
}

// reopen 在复用的会话里按这次给的地址导航一遍：照常排进动作队列、照常查 origin，
// 成功了 navigate 会把 left_origin 清掉。这是 app-server 自己发起的动作，不走 seq。
func (s *Session) reopen(raw string) *Error {
	return s.enqueue(func(ctx context.Context) *Error { return s.navigate(ctx, raw) })
}

// close 关掉会话：状态记 closed，Chrome、代理与私有 profile 都收掉，之后这条对话没有会话。
func (s *Session) close(reason string) {
	s.mu.Lock()
	if s.down {
		s.mu.Unlock()
		return
	}
	s.down = true
	s.state, s.reason = "closed", reason
	s.data = nil
	if s.idle != nil {
		s.idle.Stop()
	}
	s.mu.Unlock()
	s.teardown()
	s.mgr.forget(s)
}

// teardown 收掉会话占的资源；重复调用无副作用。
func (s *Session) teardown() {
	s.cancel()
	if s.stop != nil {
		s.stop()
	}
	if s.proxy != nil {
		s.proxy.close()
	}
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
}

// status 是会话描述。
func (s *Session) status() Status {
	s.mu.Lock()
	st := Status{State: s.state, Origin: s.grant.origin, URL: s.url, Viewport: s.viewport,
		Width: s.width, Height: s.height, Frame: s.frame, LeftOrigin: s.leftOrigin, PopupBlocked: s.popups, Reason: s.reason}
	s.mu.Unlock()
	if s.proxy != nil {
		st.Denied = s.proxy.denied()
	}
	return st
}

// touch 重新开始算空转：取画面与动作都算在用。
func (s *Session) touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle == nil {
		s.idle = time.AfterFunc(idleTimeout, func() { s.close("30 分钟没人用，自动关闭") })
		return
	}
	s.idle.Reset(idleTimeout)
}

// putFrame 换掉内存里的最新一帧并叫醒等画面的人：只留一帧，不写盘、不进对话存档。
func (s *Session) putFrame(data []byte) {
	s.mu.Lock()
	s.frame++
	s.data = data
	old := s.frameReady
	s.frameReady = make(chan struct{})
	s.mu.Unlock()
	close(old)
}

func (s *Session) setURL(raw string) {
	s.mu.Lock()
	s.url = raw
	s.mu.Unlock()
}

// frameAt 等一帧比 after 新的画面：有就立刻回，没有就等到 wait 超时（回带 after 的空帧）。
func (s *Session) frameAt(ctx context.Context, after int64, wait time.Duration) (Frame, *Error) {
	if wait > maxWait {
		wait = maxWait
	}
	var timeout <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		timeout = timer.C
	}
	for {
		s.mu.Lock()
		frame, data, ready, width, height := s.frame, s.data, s.frameReady, s.width, s.height
		s.mu.Unlock()
		if frame > after && data != nil {
			s.touch()
			return Frame{Frame: frame, Width: width, Height: height, Data: data}, nil
		}
		if timeout == nil {
			return Frame{Frame: after}, nil
		}
		select {
		case <-ready:
		case <-timeout:
			return Frame{Frame: after}, nil
		case <-s.ctx.Done():
			return Frame{}, fail(409, "预览已经关闭")
		case <-ctx.Done():
			return Frame{Frame: after}, nil // 界面自己不等了：当这次没有新帧
		}
	}
}

// act 收下一个页面发来的动作：先按 client 核验 seq、核对视口版本，再排进队列等执行。
func (s *Session) act(a Action) *Error {
	s.mu.Lock()
	if s.state != "ready" {
		state, reason := s.state, s.reason
		s.mu.Unlock()
		if reason != "" {
			return fail(409, "预览现在用不了（%s）：%s", state, reason)
		}
		return fail(409, "预览现在是 %s，不能发动作", state)
	}
	// seq 按 client 认领：页面每次建视图换一个随机 client、seq 在 client 里从 1 递增，
	// 所以刷新页面、第二个标签页、重建视图都不会跟会话里已有的号撞上；新的 client 从任意 seq 开始。
	// 键有界：client 只留前 maxClientLen 字节，超过 maxClients 个时丢最久没动的那个。
	client := a.Client
	if len(client) > maxClientLen {
		client = client[:maxClientLen]
	}
	track := s.clients[client]
	if track != nil && a.Seq <= track.seq {
		s.mu.Unlock()
		return fail(409, "这个动作已经过期（seq %d），不再执行", a.Seq)
	}
	// 改过视口以后旧坐标不能再按新尺寸解释：只有鼠标动作带坐标。
	if a.Kind == "mouse" && a.Viewport != s.viewport {
		s.mu.Unlock()
		return fail(409, "画面尺寸变了，旧坐标不能用")
	}
	if len(s.queue) >= cap(s.queue) {
		s.mu.Unlock()
		return fail(429, "上一步动作还在排队，稍后再试")
	}
	if track == nil {
		track = s.claimClient(client)
	}
	track.seq, track.used = a.Seq, time.Now()
	s.mu.Unlock()
	s.touch()

	var run func(context.Context) *Error
	switch a.Kind {
	case "navigate":
		run = func(ctx context.Context) *Error { return s.navigate(ctx, a.URL) }
	case "reload":
		run = s.reload
	case "back":
		run = func(ctx context.Context) *Error { return s.history(ctx, false) }
	case "forward":
		run = func(ctx context.Context) *Error { return s.history(ctx, true) }
	case "resize":
		run = func(ctx context.Context) *Error { return s.resize(ctx, a.Width, a.Height) }
	case "mouse":
		run = func(ctx context.Context) *Error {
			// 排队期间可能改过尺寸：执行前再核对一次，旧坐标不按新尺寸解释。
			if ferr := s.staleViewport(a.Viewport); ferr != nil {
				return ferr
			}
			return s.mouse(ctx, a)
		}
	case "key":
		run = func(ctx context.Context) *Error { return s.key(ctx, a) }
	case "text":
		run = func(ctx context.Context) *Error { return s.text(ctx, a) }
	default:
		return fail(400, "没有这个动作：%s", a.Kind)
	}
	// seq 只在上面按页面发来的动作认领一次；排队执行不看它。
	return s.enqueue(run)
}

// claimClient 给一个新的页面客户端开一条序号记录：已经记满 maxClients 个时先丢最久没动的那个。
// 调用方持有 s.mu。
func (s *Session) claimClient(client string) *clientTrack {
	if len(s.clients) >= maxClients {
		var oldest string
		var oldestAt time.Time
		first := true
		for name, track := range s.clients {
			if first || track.used.Before(oldestAt) {
				oldest, oldestAt, first = name, track.used, false
			}
		}
		delete(s.clients, oldest)
	}
	track := &clientTrack{}
	s.clients[client] = track
	return track
}

// enqueue 把一次动作排进队列并等它执行完：队列满了回 429（不悄悄丢动作）。
// 它不看也不推进任何 client 的 seq——seq 只给页面发来的动作用（由 act 认领），app-server 自己发起的动作
// （比如 Open 复用会话时的导航）直接走这里，免得推进一个页面不知道的序号，让页面的第一个动作被当成过期。
func (s *Session) enqueue(run func(context.Context) *Error) *Error {
	j := job{run: run, done: make(chan *Error, 1)}
	select {
	case s.queue <- j:
	default:
		return fail(429, "上一步动作还在排队，稍后再试")
	}
	select {
	case err := <-j.done:
		return err
	case <-s.ctx.Done():
		return fail(409, "预览已经关闭")
	}
}

// loop 是一个会话里唯一执行动作的地方：按收到的顺序一个一个执行，执行期间告诉看地址的循环别插手。
func (s *Session) loop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.queue:
			ctx, cancel := context.WithTimeout(s.tab, actionTimeout)
			s.mu.Lock()
			s.busy = true
			s.mu.Unlock()
			err := j.run(ctx)
			s.mu.Lock()
			s.busy = false
			s.mu.Unlock()
			cancel()
			j.done <- err
		}
	}
}

// navigate 先查 scheme 与 origin 再导航：CDP 能直接打开 file:// 读到本地文件，这里必须挡住。
func (s *Session) navigate(ctx context.Context, raw string) *Error {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return fail(403, "地址看不懂：%s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fail(403, "预览只打开 http:// 或 https:// 地址")
	}
	if originOf(u) != s.grant.origin {
		return fail(403, "地址不在授权范围里（只授权了 %s）", s.grant.origin)
	}
	err = chromedp.Do(ctx, chromedp.Navigate(raw))
	// 主框架最后停在哪里要再看一次：服务端可能把请求重定向到了别处，那边被代理 403 时
	// Chrome 报的是加载失败（net::ERR_HTTP_RESPONSE_CODE_FAILURE），先看它停在哪儿再决定算不算失败。
	cur := s.settle(ctx)
	if cur != "" && cur != "about:blank" && !s.sameOrigin(cur) {
		s.park(ctx) // 离开了授权 origin：停在空白页、left_origin 置上，这是按规则挡住了，不是失败
		return nil
	}
	if err != nil {
		return fail(500, "导航失败：%s", clip(err.Error()))
	}
	s.mu.Lock()
	s.leftOrigin = false // 一次成功的导航就把“离开了授权范围”清掉
	if cur != "" {
		s.url = cur
	}
	s.mu.Unlock()
	return nil
}

// reload 刷新当前页：换页以后照样核对 origin。
func (s *Session) reload(ctx context.Context) *Error {
	if err := chromedp.Do(ctx, chromedp.Reload()); err != nil {
		return fail(500, "刷新失败：%s", clip(err.Error()))
	}
	return s.backToRoot(ctx)
}

// history 后退或前进：之后核对 origin，不在授权范围里就再导航回授权 origin 的根路径。
func (s *Session) history(ctx context.Context, forward bool) *Error {
	script := "(history.back(), true)"
	if forward {
		script = "(history.forward(), true)"
	}
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[bool](script, chromedp.EvalAwaitPromise)); err != nil {
		return fail(500, "换页没发出去：%s", clip(err.Error()))
	}
	return s.backToRoot(ctx)
}

// backToRoot 换页以后核对 origin：不等就再导航回授权 origin 的根路径，不跟过去。
func (s *Session) backToRoot(ctx context.Context) *Error {
	cur := s.settle(ctx)
	if cur != "" && s.sameOrigin(cur) {
		s.setURL(cur)
		return nil
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(s.grant.origin+"/")); err != nil {
		return fail(500, "回到授权范围失败：%s", clip(err.Error()))
	}
	s.setURL(s.grant.origin + "/")
	return nil
}

// resize 改视口：设备度量跟着改，视口版本加一，之后的鼠标动作要带新版本；画面的最大宽高跟着视口走。
func (s *Session) resize(ctx context.Context, width, height int) *Error {
	width, height = clampSize(width, height)
	if err := s.viewportSize(ctx, width, height); err != nil {
		return fail(500, "改不了视口大小：%s", clip(err.Error()))
	}
	if _, err := chromedp.Call(ctx, page.StopScreencast, cdp.Empty{}); err != nil {
		return fail(500, "停不掉旧的画面：%s", clip(err.Error()))
	}
	if err := s.startScreencast(width, height); err != nil {
		return fail(500, "改不了画面大小：%s", clip(err.Error()))
	}
	s.mu.Lock()
	s.width, s.height = width, height
	s.viewport++
	s.mu.Unlock()
	return nil
}

// staleViewport 是执行鼠标动作前再核对一次视口版本：排队期间改过尺寸的旧坐标不能用。
func (s *Session) staleViewport(viewport int64) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if viewport != s.viewport {
		return fail(409, "画面尺寸变了，旧坐标不能用")
	}
	return nil
}

// wheelParams 是滚轮事件：cdproto 的 DeltaX、DeltaY 带 omitzero，值是 0 时会被整条省掉，
// Chrome 收到没有 delta 的 mouseWheel 会报 "'deltaX' and 'deltaY' are expected"，
// 所以自己写一份参数、按 CDP 的字段名发（0 也照样带上）。
type wheelParams struct {
	Type      input.DispatchMouseEventType `json:"type"`
	X         float64                      `json:"x"`
	Y         float64                      `json:"y"`
	DeltaX    float64                      `json:"deltaX"`
	DeltaY    float64                      `json:"deltaY"`
	Modifiers int64                        `json:"modifiers,omitempty"`
}

func (s *Session) mouse(ctx context.Context, a Action) *Error {
	if a.Type == "wheel" {
		cmd := cdp.Command[wheelParams, cdp.Empty]{Method: "Input.dispatchMouseEvent"}
		params := wheelParams{Type: input.DispatchMouseEventTypeMouseWheel, X: a.X, Y: a.Y,
			DeltaX: a.DX, DeltaY: a.DY, Modifiers: modifiers(a.Modifiers)}
		if _, err := chromedp.Call(ctx, cmd, params); err != nil {
			return fail(500, "滚轮没发出去：%s", clip(err.Error()))
		}
		return nil
	}
	p := input.DispatchMouseEventParams{X: a.X, Y: a.Y, Modifiers: modifiers(a.Modifiers)}
	switch a.Type {
	case "move":
		p.Type = input.DispatchMouseEventTypeMouseMoved
	case "down":
		p.Type = input.DispatchMouseEventTypeMousePressed
	case "up":
		p.Type = input.DispatchMouseEventTypeMouseReleased
	default:
		return fail(400, "鼠标动作只有 move、down、up、wheel")
	}
	if a.Type == "down" || a.Type == "up" {
		p.Button = mouseButton(a.Button)
		p.ClickCount = int64(max(a.Clicks, 1))
	}
	if _, err := chromedp.Call(ctx, input.DispatchMouseEvent, p); err != nil {
		return fail(500, "鼠标动作没发出去：%s", clip(err.Error()))
	}
	return nil
}

func (s *Session) key(ctx context.Context, a Action) *Error {
	p := input.DispatchKeyEventParams{Key: a.Key, Code: a.Code, Text: a.Text, Modifiers: modifiers(a.Modifiers)}
	switch a.Type {
	case "down":
		p.Type = input.DispatchKeyEventTypeKeyDown
	case "up":
		p.Type = input.DispatchKeyEventTypeKeyUp
	default:
		return fail(400, "按键动作只有 down、up")
	}
	if _, err := chromedp.Call(ctx, input.DispatchKeyEvent, p); err != nil {
		return fail(500, "按键没发出去：%s", clip(err.Error()))
	}
	return nil
}

// text 是输入法提交的那段文字（中文输入走这里），不再另发按键，免得输入两次。
func (s *Session) text(ctx context.Context, a Action) *Error {
	if a.Text == "" {
		return nil
	}
	if utf8.RuneCountInString(a.Text) > maxText {
		return fail(400, "一次最多输入 %d 字", maxText)
	}
	if _, err := chromedp.Call(ctx, input.InsertText, input.InsertTextParams{Text: a.Text}); err != nil {
		return fail(500, "文字没输入进去：%s", clip(err.Error()))
	}
	return nil
}

// watchOrigin 每 500 毫秒看一眼主框架的地址：服务端重定向与页面脚本自己的跳转都在这里收住，不跟过去。
func (s *Session) watchOrigin() {
	t := time.NewTicker(originTick)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.checkOrigin()
		}
	}
}

func (s *Session) checkOrigin() {
	s.mu.Lock()
	busy, state := s.busy, s.state
	s.mu.Unlock()
	if busy || state != "ready" {
		return // 动作正在执行：它自己会在换页之后核对
	}
	ctx, cancel := context.WithTimeout(s.tab, actionTimeout)
	defer cancel()
	cur := s.location(ctx)
	if cur == "" || cur == "about:blank" {
		return
	}
	if s.sameOrigin(cur) {
		s.setURL(cur)
		return
	}
	s.park(ctx)
}

// park 把主框架停在空白页：不在授权范围里的地址不跟过去，状态里记 left_origin。
func (s *Session) park(ctx context.Context) {
	chromedp.Do(ctx, chromedp.Navigate("about:blank"))
	s.mu.Lock()
	s.leftOrigin = true
	s.url = "about:blank"
	s.mu.Unlock()
}

// watchPopups 每秒看一眼浏览器里的页面：不是这个会话那一页的（弹出页、window.open 出来的）一律关掉。
func (s *Session) watchPopups() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.closePopups()
		}
	}
}

func (s *Session) closePopups() {
	c := chromedp.FromContext(s.tab)
	if c == nil || c.Browser == nil || c.Target == nil {
		return
	}
	// 用会话自己的 tab ctx：会话关掉时这次列 target 跟着取消，不用 context.Background()。
	ctx, cancel := context.WithTimeout(s.tab, actionTimeout)
	defer cancel()
	targets, err := chromedp.Targets(ctx)
	if err != nil {
		return
	}
	mine := target.ID(c.Target.TargetID)
	for _, info := range targets {
		if info.TargetID == mine || info.Type != "page" {
			continue
		}
		if _, err := cdp.Call(ctx, c.Browser, target.CloseTarget, target.CloseTargetParams{TargetID: info.TargetID}); err != nil {
			continue
		}
		s.mu.Lock()
		s.popups++
		s.mu.Unlock()
	}
}

// location 读主框架现在的地址；读不到时回空串（页面正在换、或者已经关了）。
func (s *Session) location(ctx context.Context) string {
	href, err := chromedp.Run(ctx, chromedp.Evaluate[string]("location.href", chromedp.EvalAwaitPromise))
	if err != nil {
		return ""
	}
	return href
}

// settle 等换页之后地址稳定下来：连着读到两次一样就回它，最多等 2 秒。
func (s *Session) settle(ctx context.Context) string {
	var last string
	for i := 0; i < settleTries; i++ {
		select {
		case <-ctx.Done():
			return last
		case <-time.After(settleTick):
		}
		cur := s.location(ctx)
		if cur != "" && cur == last {
			return cur
		}
		last = cur
	}
	return last
}

// sameOrigin 核对一个地址是不是就是授权的那个 origin。
func (s *Session) sameOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return originOf(u) == s.grant.origin
}

// viewportSize 按视口尺寸改设备度量：鼠标坐标就是这时的 CSS 像素。
func (s *Session) viewportSize(ctx context.Context, width, height int) error {
	_, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
		Width: int64(width), Height: int64(height), DeviceScaleFactor: 1,
	})
	return err
}

// startScreencast 开始推画面：JPEG、质量 70、最大宽高等于视口。
func (s *Session) startScreencast(width, height int) error {
	quality := int64(70)
	_, err := chromedp.Call(s.tab, page.StartScreencast, page.StartScreencastParams{
		Format: page.StartScreencastFormatJpeg, Quality: &quality, MaxWidth: int64(width), MaxHeight: int64(height),
	})
	return err
}

// findChrome 找 Chrome 可执行文件，并给出这台机器上用不了预览的原因；CHROME_PATH 的规则同 internal/browser。
func findChrome() (path, reason string) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "", "网页预览只支持 Linux 与 macOS"
	}
	if os.Getenv("CHROME_NO_SANDBOX") == "1" {
		return "", "预览要求 Chrome 自己的沙箱：改了 CHROME_NO_SANDBOX 才能用"
	}
	if p := os.Getenv("CHROME_PATH"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, ""
		}
		return "", "CHROME_PATH 指的浏览器不存在：" + p
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "headless_shell", "headless-shell"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, ""
		}
	}
	return "", "找不到 Chrome（用 CHROME_PATH 指定可执行文件）"
}

// clampSize 把视口尺寸收在能用的范围里；没给时用默认大小。
func clampSize(width, height int) (int, int) {
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}
	return min(max(width, minWidth), maxWidth), min(max(height, minHeight), maxHeight)
}

// originOf 把一个地址写成 scheme://host:port；没写端口时按 scheme 的默认端口算。
func originOf(u *url.URL) string {
	if u.Scheme == "" {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	if host == "::1" {
		host = "[::1]"
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return u.Scheme + "://" + host + ":" + port
}

// modifiers 把动作里的修饰键换成 CDP 的位：Alt 1、Ctrl 2、Meta 4、Shift 8（Modifiers 字段是 int64）。
func modifiers(names []string) int64 {
	var m int64
	for _, name := range names {
		switch strings.ToLower(name) {
		case "alt":
			m |= 1
		case "ctrl", "control":
			m |= 2
		case "meta", "cmd", "command":
			m |= 4
		case "shift":
			m |= 8
		}
	}
	return m
}

func mouseButton(name string) input.MouseButton {
	switch strings.ToLower(name) {
	case "right":
		return input.MouseButtonRight
	case "middle":
		return input.MouseButtonMiddle
	}
	return input.MouseButtonLeft
}

// clip 把 Chrome 的报错截短：它有时会把整段栈带回来。
func clip(s string) string {
	r := []rune(s)
	if len(r) > 300 {
		return string(r[:300]) + "…"
	}
	return s
}
