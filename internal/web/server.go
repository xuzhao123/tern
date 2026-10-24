package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/xuzhao123/tern/internal/appclient"
	"github.com/xuzhao123/tern/internal/appserver"
)

// 页面在 page/：外壳 index.html、样式 app.css 和按功能拆开的原生 ES 模块（入口 main.js），不打包，打进同一个程序。
//
//go:embed page
var webFS embed.FS

// 网页端是 app-server 的客户端：登录、页面、把每个 HTTP 接口翻成对应的协议方法（spec 第 5 节）。
// 对话、设置、文件、评测这些数据都在 app-server 里；网页端只读写自己的两份设置：账号（.data/users.json）
// 与记住的主机（.data/web-hosts.json）。
//
// 网页端可以同时连几台主机（spec 第二期第 4 节），每台一条 appclient 连接，见下面的主机表。
// 远端主机的地址带 /h/<别名>/ 前缀，不带前缀的就是本机；页面一次只操作当前主机，换主机是整页跳转。
type server struct {
	auth *auth

	mu         sync.Mutex
	hosts      map[string]*host           // 已连上的主机：别名（本机是空串）→ 连接
	order      []string                   // 连上的顺序：/connection 按它推，页面按它排组
	remembered []string                   // 页面上连过的主机：写进 .data/web-hosts.json，下次启动在后台连
	failed     map[string]string          // 记住了但这次没连上的：别名 → 那次的报错
	watch      map[chan struct{}]struct{} // 正在等 /connection 的连接
}

// host 是一台主机的连接。state 在 server.mu 下读写；runs 用原子量，因为它从连接的读循环里加。
type host struct {
	alias  string
	client *appclient.Client
	state  appclient.State
	runs   atomic.Int64 // 收到 thread/started、thread/status/changed 就加一：这台的列表变了，页面重新加载
}

// remoteFlag 收 -remote：可以写多次，每台主机一条连接；不写就是本机。
type remoteFlag []string

func (r *remoteFlag) String() string { return strings.Join(*r, "、") }

func (r *remoteFlag) Set(value string) error {
	*r = append(*r, value)
	return nil
}

// Run 启动网页端：go run . web [-addr …] [-remote 别名 …] [-add-user 名字]。main 已经把工作目录换到 TERN_HOME，
// `.env`、`.data/` 都相对它，所以不在 tern 源码根目录也能跑。
// 不带 -remote 时连本机 app-server：先保证它在运行，再连上；带 -remote 时经 ssh 连远端（见 internal/appclient），
// 写了几台就连这几台，不连本机。命令行定的主机启动时连不上就报错退出；页面上连过的主机在后台一台台连，
// 连不上只记成 failed，不挡启动（ssh 要等到连接超时才会报错）。
// 启动 agent 的是 app-server，要从源码跑 agent 就 `tern app-server -dev` 或 `tern app-server start -dev`。
func Run(args []string, mcpHandler http.Handler) error {
	flags := flag.NewFlagSet("web", flag.ContinueOnError)
	addr := flags.String("addr", "127.0.0.1:8090", "网页端地址")
	var remotes remoteFlag
	flags.Var(&remotes, "remote", "经 SSH 连远端 app-server：ssh 别名（~/.ssh/config 里的 Host），可以写多次")
	addUserName := flags.String("add-user", "", "添加或重设一个登录账号（密码从标准输入读）后退出")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("web 只接受 -addr、-remote 或 -add-user")
	}
	if *addUserName != "" {
		return addUser(*addUserName)
	}
	authState, err := loadAuth()
	if err != nil {
		return err
	}
	s := &server{auth: authState, hosts: map[string]*host{}, failed: map[string]string{}, watch: map[chan struct{}]struct{}{}}
	// 命令行定的主机先连上：不带 -remote 是本机，带了几台就只连这几台（远端的 app-server 由 proxy 自己 start）。
	targets := []string{""}
	if len(remotes) > 0 {
		targets = remotes
	}
	var infos []string
	for _, alias := range targets {
		if s.connected(alias) != nil {
			continue // 同一台写了两遍
		}
		client, info, err := dialHost(alias)
		if err != nil {
			return err
		}
		s.addHost(alias, client)
		infos = append(infos, describeHost(alias, info))
	}
	if s.remembered, err = loadWebHosts(); err != nil {
		return err
	}
	go s.dialRemembered()
	defer s.closeHosts()
	mux := http.NewServeMux()
	web, _ := fs.Sub(webFS, "page")
	page, err := fs.ReadFile(web, "index.html")
	if err != nil {
		return err
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, req *http.Request) {
		// 只带了 -remote 时本机没连：页面跳到第一台远端，之后地址都带着那台的前缀。
		if h, prefixed := hostIn(req); h == nil && !prefixed {
			if first := s.firstHost(); first != nil {
				http.Redirect(w, req, "/h/"+first.alias+"/", http.StatusSeeOther)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(page)
	})
	// 样式与模块：embed 的文件没有修改时间，浏览器拿不到 Last-Modified；no-cache 让它每次都来问，换了程序就拿到新文件。
	files := http.StripPrefix("/ui/", http.FileServerFS(web))
	mux.HandleFunc("GET /ui/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, req)
	})
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPage)
	mux.HandleFunc("POST /logout", s.logoutUser)
	mux.HandleFunc("GET /me", s.me)
	mux.HandleFunc("GET /connection", s.connectionEvents)
	mux.HandleFunc("GET /hosts", s.listHosts)
	mux.HandleFunc("POST /hosts", s.connectHost)
	mux.HandleFunc("DELETE /hosts/{alias}", s.disconnectHost)
	mux.HandleFunc("GET /models", s.listModels)
	mux.HandleFunc("POST /attachments", s.uploadAttachment)
	mux.HandleFunc("GET /attachments/{name}", s.serveAttachment)
	mux.HandleFunc("GET /runs", s.listRuns)
	mux.HandleFunc("POST /runs", s.startRun)
	mux.HandleFunc("POST /runs/import", s.importRun)
	mux.HandleFunc("POST /runs/{id}/messages", s.continueRun)
	mux.HandleFunc("GET /runs/{id}/events", s.streamEvents)
	mux.HandleFunc("POST /runs/{id}/stop", s.stopRun)
	mux.HandleFunc("POST /runs/{id}/network", s.decideNetwork)
	mux.HandleFunc("POST /runs/{id}/approval", s.decideTool)
	mux.HandleFunc("POST /runs/{id}/answer", s.answerQuestion)
	mux.HandleFunc("POST /runs/{id}/steer", s.steerRun)
	mux.HandleFunc("POST /runs/{id}/rewind", s.rewindRun)
	mux.HandleFunc("POST /runs/{id}/fork", s.forkRun)
	mux.HandleFunc("GET /runs/{id}/files", s.listFiles)
	mux.HandleFunc("GET /runs/{id}/files/{path...}", s.downloadFile)
	mux.HandleFunc("DELETE /runs/{id}/files/{path...}", s.deleteFile)
	mux.HandleFunc("POST /runs/{id}/files", s.uploadFile)
	mux.HandleFunc("GET /audit", s.listAudit)
	mux.HandleFunc("GET /browser/{task}/{call}", s.browserFrame)
	mux.HandleFunc("GET /traces/{id}", s.traceSpans)
	mux.HandleFunc("GET /trace-metrics", s.traceMetrics)
	mux.HandleFunc("GET /evals", s.listEvals)
	mux.HandleFunc("GET /evals/cases", s.evalCases)
	mux.HandleFunc("GET /evals/{id}", s.getEval)
	mux.HandleFunc("POST /evals", s.startEval)
	mux.HandleFunc("POST /evals/{id}/{action}", s.evalAction)
	mux.HandleFunc("POST /runs/{id}/attribute", s.attributeRun)
	mux.HandleFunc("GET /memory", s.listMemory)
	mux.HandleFunc("POST /memory/{id}/forget", s.forgetMemory)
	mux.HandleFunc("GET /settings", s.getSettings)
	mux.HandleFunc("GET /commands", s.listCommands)
	mux.HandleFunc("PUT /settings", s.putSettings("user"))
	mux.HandleFunc("PUT /settings/platform", s.putSettings("platform"))
	mux.HandleFunc("POST /settings/workspaces", s.addWorkspace)
	mux.HandleFunc("DELETE /settings/workspaces/{name}", s.deleteWorkspace)
	mux.HandleFunc("GET /hub", s.listHub)
	mux.HandleFunc("GET /hub/enabled", s.enabledHub)
	mux.HandleFunc("POST /hub/skills", s.createSkill)
	mux.HandleFunc("DELETE /hub/skills/{name}", s.deleteSkill)
	mux.HandleFunc("POST /hub/skills/{name}/enabled", s.toggleSkill)
	mux.HandleFunc("POST /hub/mcp", s.addMCP)
	mux.HandleFunc("DELETE /hub/mcp/{name}", s.deleteMCP)
	mux.HandleFunc("POST /hub/mcp/{name}/enabled", s.toggleMCP)
	mux.HandleFunc("POST /hub/mcp/{name}/test", s.testMCP)
	// 内置远程 MCP：同一进程直接处理，不另开端口、不转发；同样只接受本机同源请求。
	mux.Handle("/mcp", sameOrigin(mcpHandler))
	fmt.Printf("网页端：http://%s\n远程 MCP：http://%s/mcp（内置 calculator，Streamable HTTP）\n", *addr, *addr)
	for _, line := range infos {
		fmt.Println(line)
	}
	fmt.Printf("agent：由 app-server 启动（要从源码跑 agent 用 tern app-server -dev 或 tern app-server start -dev）\n")
	fmt.Printf("登录账号：%s（%d 个）\n", usersPath, len(authState.accounts))
	return http.ListenAndServe(*addr, s.protect(s.withHosts(mux)))
}

// ---------- HTTP 到协议的翻译 ----------

// params 是这次请求的协议参数：请求体里的 JSON，加上路径与查询参数和登录账号。
// 路径、查询参数与登录账号最后放，请求体里写什么都盖不掉它们。
func (s *server) params(w http.ResponseWriter, req *http.Request, extra map[string]any) (map[string]any, bool) {
	p := map[string]any{}
	data, err := io.ReadAll(io.LimitReader(req.Body, maxBody))
	if err != nil {
		http.Error(w, "请求体读不出来", http.StatusBadRequest)
		return nil, false
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &p); err != nil {
			http.Error(w, "请求必须是JSON", http.StatusBadRequest)
			return nil, false
		}
	}
	maps.Copy(p, extra)
	p["user"] = current(req).Name
	return p, true
}

// webError 是网页端自己的错误（这台主机没连上）：带 HTTP 状态，fail 原样用它。
type webError struct {
	code int
	msg  string
}

func (e webError) Error() string { return e.msg }

// clientFor 是这次请求要用的连接：前缀指定的那台，或者本机。没连上就没有连接可用。
func (s *server) clientFor(req *http.Request) (*appclient.Client, error) {
	h, _ := hostIn(req)
	if h == nil {
		return nil, webError{http.StatusNotFound, "没有连上主机（本机）"}
	}
	return h.client, nil
}

// call 发一次协议请求，结果是回复的 JSON。
func (s *server) call(req *http.Request, method string, params any) (json.RawMessage, error) {
	var result json.RawMessage
	err := s.callInto(req, method, params, &result)
	return result, err
}

// callInto 发一次协议请求，把回复解进 result：用的是请求对应的那台主机。
func (s *server) callInto(req *http.Request, method string, params, result any) error {
	client, err := s.clientFor(req)
	if err != nil {
		return err
	}
	return client.Call(req.Context(), method, params, result)
}

// reply 把回复原样写回响应体：原来各接口的响应体就是这个结果。
func (s *server) reply(w http.ResponseWriter, req *http.Request, method string, extra map[string]any) bool {
	p, ok := s.params(w, req, extra)
	if !ok {
		return false
	}
	result, err := s.call(req, method, p)
	if err != nil {
		fail(w, err)
		return false
	}
	writeJSON(w, result)
	return true
}

// replyText 和 reply 一样，只是成功时回一句文字（原来回 ok 的接口）。
func (s *server) replyText(w http.ResponseWriter, req *http.Request, method string, extra map[string]any, text string) bool {
	p, ok := s.params(w, req, extra)
	if !ok {
		return false
	}
	if _, err := s.call(req, method, p); err != nil {
		fail(w, err)
		return false
	}
	io.WriteString(w, text)
	return true
}

// replyNone 成功时回 204（原来没有响应体的接口）。
func (s *server) replyNone(w http.ResponseWriter, req *http.Request, method string, extra map[string]any) bool {
	p, ok := s.params(w, req, extra)
	if !ok {
		return false
	}
	if _, err := s.call(req, method, p); err != nil {
		fail(w, err)
		return false
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

// statusOf 把错误还原成 HTTP 状态：网页端自己的错误（这台主机没连上）用它带的状态；
// 重连期间回 503（文字是“正在重连 <host>”）；方法出错时的 code
// 就是原来这个接口的状态码，照原样用；其余的（连接断了、协议本身的错误）回 502。
func statusOf(err error) int {
	var we webError
	if errors.As(err, &we) {
		return we.code
	}
	if errors.Is(err, appclient.ErrReconnecting) {
		return http.StatusServiceUnavailable
	}
	if code := appclient.Code(err); code >= 400 && code <= 599 {
		return code
	}
	return http.StatusBadGateway
}

func fail(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), statusOf(err))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// maxBody 是请求体里 JSON 的上限：设置项、提问这类都在几十 KB 以内；图片和文件走分块上传，不在这里读。
const maxBody = 1 << 20

// ---------- 主机表 ----------

// hostInfo 是 /connection 里的一台主机（spec 第二期第 4 节）。state 除了 connected、reconnecting，
// 还有 failed：记住了的主机这次没连上，error 是那次的报错。
type hostInfo struct {
	Alias  string `json:"alias"`  // ssh 别名；本机是空串
	Host   string `json:"host"`   // 显示名：本机用 initialize 报的主机名，远端用别名；没连上的本机没有名字
	Remote bool   `json:"remote"` // 是不是远端
	State  string `json:"state"`
	Runs   int64  `json:"runs"`
	Error  string `json:"error,omitempty"`
}

type connections struct {
	Hosts []hostInfo `json:"hosts"`
}

// failedState 是记住的主机这次没连上的状态：菜单里显示报错，点它就是再连一次。
const failedState = "failed"

// addHost 收下一条连上的主机：记进主机表，挂上回调——这台的对话列表变了、连接状态变了都推一次 /connection。
// 这台已经在表里（后台连记住的主机时，用户在菜单里点了同一台，两边同时拨号）就关掉这条新的、用原来那条：
// 那时它在候选里，既不算连上也不算连不上，谁也拦不住第二次拨号。
func (s *server) addHost(alias string, client *appclient.Client) *host {
	s.mu.Lock()
	if h := s.hosts[alias]; h != nil {
		s.mu.Unlock()
		client.Close()
		return h
	}
	h := &host{alias: alias, client: client, state: client.State()}
	s.hosts[alias] = h
	s.order = append(s.order, alias)
	s.mu.Unlock()
	client.OnStarted(func(json.RawMessage) { h.runs.Add(1); s.notify() })
	client.OnStatus(func(string, string) { h.runs.Add(1); s.notify() })
	client.OnState(func(st appclient.State) {
		s.mu.Lock()
		h.state = st
		s.mu.Unlock()
		s.notify()
	})
	return h
}

// connected 是这台主机现在的连接；没连上就是 nil。
func (s *server) connected(alias string) *host {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hosts[alias]
}

// firstHost 是连上的第一台：只带了 -remote 时，页面跳到它的地址。
func (s *server) firstHost() *host {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.order) == 0 {
		return nil
	}
	return s.hosts[s.order[0]]
}

// dropHost 把一台主机从表里拿掉、关掉连接，并从记住的主机里删掉。
// 关掉连接会让还开着的订阅结束，页面那边正在看的事件流随之写完 event: end。
func (s *server) dropHost(alias string) {
	s.mu.Lock()
	h := s.hosts[alias]
	delete(s.hosts, alias)
	s.order = slices.DeleteFunc(s.order, func(a string) bool { return a == alias })
	delete(s.failed, alias)
	had := slices.Contains(s.remembered, alias)
	s.remembered = slices.DeleteFunc(s.remembered, func(a string) bool { return a == alias })
	aliases := slices.Clone(s.remembered)
	s.mu.Unlock()
	if h != nil {
		h.client.Close()
	}
	if had {
		if err := saveWebHosts(aliases); err != nil {
			fmt.Fprintf(os.Stderr, "记住的主机写不进 %s：%v\n", webHostsPath, err)
		}
	}
	s.notify()
}

// closeHosts 关掉所有连接：跟 defer 在 Run 里，进程退出时收掉 ssh 进程。
func (s *server) closeHosts() {
	s.mu.Lock()
	hosts := make([]*host, 0, len(s.hosts))
	for _, h := range s.hosts {
		hosts = append(hosts, h)
	}
	s.mu.Unlock()
	for _, h := range hosts {
		h.client.Close()
	}
}

// reqHostKey 是请求 context 里的主机：/h/<别名>/ 前缀指定的那一台，或者本机。
type reqHostKey struct{}

type reqHost struct {
	host   *host // nil：本机没连（只带了 -remote）
	prefix bool  // 地址带了 /h/<别名>/ 前缀
}

func hostIn(req *http.Request) (*host, bool) {
	r, _ := req.Context().Value(reqHostKey{}).(*reqHost)
	if r == nil {
		return nil, false
	}
	return r.host, r.prefix
}

// withHosts 处理地址上的主机前缀：/h/<别名>/ 剥掉前缀交给同一套路由，主机放进请求的 context；
// 不带前缀的就是本机。没连上的主机回 404，记住了但这次没连上的回 502（文字是那次的报错）。
func (s *server) withHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rest, prefixed := strings.CutPrefix(req.URL.Path, "/h/")
		if !prefixed {
			ctx := context.WithValue(req.Context(), reqHostKey{}, &reqHost{host: s.connected("")})
			next.ServeHTTP(w, req.WithContext(ctx))
			return
		}
		alias, tail, _ := strings.Cut(rest, "/")
		s.mu.Lock()
		h, failed := s.hosts[alias], s.failed[alias]
		s.mu.Unlock()
		if h == nil {
			if failed != "" {
				http.Error(w, failed, http.StatusBadGateway)
			} else {
				http.Error(w, "没有连上主机 "+alias, http.StatusNotFound)
			}
			return
		}
		// 换一份请求再改路径：WithContext 出来的和原来共用 URL，改了就改了原来那份。
		ctx := context.WithValue(req.Context(), reqHostKey{}, &reqHost{host: h, prefix: true})
		r := req.Clone(ctx)
		r.URL.Path, r.URL.RawPath = "/"+tail, ""
		next.ServeHTTP(w, r)
	})
}

// ---------- 顶栏的主机与连接状态 ----------

// connectionEvents 是顶栏用的 SSE：连上先推一次当前状态，之后任何一台的状态或 runs 变了再推。
// 断线重连也是这样让页面知道：那一台的 state 变成 reconnecting，连上后变回 connected。
func (s *server) connectionEvents(w http.ResponseWriter, req *http.Request) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.watch[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.watch, ch)
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	push := func() bool {
		data, err := json.Marshal(s.connection())
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	if !push() {
		return
	}
	for {
		select {
		case <-ch:
			if !push() {
				return
			}
		case <-req.Context().Done():
			return
		}
	}
}

// connection 是现在要推给页面的状态：连上的各台（按连上的顺序），再加上记住了但这次没连上的。
func (s *server) connection() connections {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := connections{Hosts: make([]hostInfo, 0, len(s.order)+len(s.failed))}
	for _, alias := range s.order {
		h := s.hosts[alias]
		out.Hosts = append(out.Hosts, hostInfo{Alias: alias, Host: h.client.Host(), Remote: alias != "", State: string(h.state), Runs: h.runs.Load()})
	}
	for _, alias := range s.remembered {
		if msg, ok := s.failed[alias]; ok {
			// 记住的本机也没连上时，别名是空串：它不是远端，页面照旧写“本机”。
			out.Hosts = append(out.Hosts, hostInfo{Alias: alias, Host: alias, Remote: alias != "", State: failedState, Error: msg})
		}
	}
	return out
}

// notify 叫醒所有等着的 /connection：它们各自推一次最新状态。
func (s *server) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.watch {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// ---------- /hosts：页面上连别的主机 ----------

// listHosts 是主机菜单里还能连的候选：~/.ssh/config 里具体的 Host 别名，加上本机（还没连本机时）。
// 已经连上的、以及记住了但这次没连上的（菜单上半部分已经有了）不算候选。
func (s *server) listHosts(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	connected := make(map[string]bool, len(s.hosts))
	for alias := range s.hosts {
		connected[alias] = true
	}
	failed := maps.Clone(s.failed)
	s.mu.Unlock()
	candidates := []string{}
	if _, down := failed[""]; !connected[""] && !down {
		candidates = append(candidates, "")
	}
	for _, alias := range appclient.SSHHosts() {
		if _, down := failed[alias]; !connected[alias] && !down {
			candidates = append(candidates, alias)
		}
	}
	writeJSON(w, map[string]any{"candidates": candidates})
}

// connectHost 连一台上：空别名是本机（先保证 app-server 在运行），远端经 ssh。已经连上的直接回成功；
// 连不上回 502，文字是 ssh 的报错。页面上连过的主机记进 .data/web-hosts.json，下次启动自动连。
func (s *server) connectHost(w http.ResponseWriter, req *http.Request) {
	var input struct {
		Alias string `json:"alias"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&input); err != nil {
		http.Error(w, "请求必须是JSON", http.StatusBadRequest)
		return
	}
	alias := strings.TrimSpace(input.Alias)
	if alias != "" {
		if err := appclient.ValidAlias(alias); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	// 已经连上的直接回成功，也照样记下来：这一台是页面上点过的。
	if h := s.connected(alias); h != nil {
		s.rememberHost(alias)
		writeJSON(w, hostInfo{Alias: alias, Host: h.client.Host(), Remote: alias != "", State: string(h.state), Runs: h.runs.Load()})
		return
	}
	client, _, err := dialHost(alias)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// 拨号这会儿后台可能已经把这台连上了：addHost 会关掉这条新的、回原来那条。
	h := s.addHost(alias, client)
	s.mu.Lock()
	delete(s.failed, alias)
	s.mu.Unlock()
	s.rememberHost(alias)
	s.notify()
	writeJSON(w, hostInfo{Alias: alias, Host: h.client.Host(), Remote: alias != "", State: string(h.state), Runs: h.runs.Load()})
}

// disconnectHost 断开一台并从记住的主机里删掉（命令行连的那几台也能断开）。本机的别名用 - 表示，路径里不能是空的。
// 断开的是一台连上的主机、而且它是最后一台时不给断：断开之后页面没有主机可看了。
func (s *server) disconnectHost(w http.ResponseWriter, req *http.Request) {
	alias := req.PathValue("alias")
	if alias == "-" {
		alias = ""
	}
	s.mu.Lock()
	_, connected := s.hosts[alias]
	_, failed := s.failed[alias]
	last := connected && len(s.order) <= 1
	s.mu.Unlock()
	if !connected && !failed {
		http.Error(w, "没有连上主机 "+alias, http.StatusNotFound)
		return
	}
	if last {
		http.Error(w, "只剩这一台，不能断开", http.StatusConflict)
		return
	}
	s.dropHost(alias)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- 记住的主机 ----------

// 页面上连接过的主机记在这里，下次启动在后台自动连。这是网页端自己的设置，所有账号共用，不分个人。
const webHostsPath = ".data/web-hosts.json"

// hostsFile 是 .data/web-hosts.json 的内容：空别名表示本机。
type hostsFile struct {
	Hosts []string `json:"hosts"`
}

// loadWebHosts 读出记住的主机：文件不存在就是空（还没在页面上连过别的主机）。
func loadWebHosts() ([]string, error) {
	data, err := os.ReadFile(webHostsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file hostsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s 不是有效 JSON：%w", webHostsPath, err)
	}
	return file.Hosts, nil
}

// saveWebHosts 写回记住的主机：先写 .tmp 再 rename（同 users.json），别的进程读不到半个文件；权限 0600。
func saveWebHosts(aliases []string) error {
	if err := os.MkdirAll(".data", 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(hostsFile{Hosts: aliases}, "", "  ")
	if err := os.WriteFile(webHostsPath+".tmp", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(webHostsPath+".tmp", webHostsPath)
}

// rememberHost 把页面上连过的主机加进记住的列表。
func (s *server) rememberHost(alias string) {
	s.mu.Lock()
	if slices.Contains(s.remembered, alias) {
		s.mu.Unlock()
		return
	}
	s.remembered = append(s.remembered, alias)
	aliases := slices.Clone(s.remembered)
	s.mu.Unlock()
	if err := saveWebHosts(aliases); err != nil {
		fmt.Fprintf(os.Stderr, "记住的主机写不进 %s：%v\n", webHostsPath, err)
	}
}

// dialRemembered 在后台一台台连记住的主机：连不上只记成 failed（菜单里显示报错、点它重试），不挡启动。
// 放在后台是因为一台关了机的主机要等到 ssh 连接超时才报错。
func (s *server) dialRemembered() {
	s.mu.Lock()
	aliases := slices.Clone(s.remembered)
	s.mu.Unlock()
	for _, alias := range aliases {
		if s.connected(alias) != nil {
			continue
		}
		client, _, err := dialHost(alias)
		if err != nil {
			s.mu.Lock()
			s.failed[alias] = err.Error()
			s.mu.Unlock()
			s.notify()
			continue
		}
		s.addHost(alias, client)
		s.notify()
	}
}

// dialHost 连上一台主机：本机先保证 app-server 在运行，远端经 ssh（远端的 app-server 由 proxy 自己 start）。
func dialHost(alias string) (*appclient.Client, appserver.ServerInfo, error) {
	if alias != "" {
		return appclient.DialRemote("tern web", alias)
	}
	if _, err := appclient.Start(false); err != nil {
		return nil, appserver.ServerInfo{}, err
	}
	return appclient.Dial("tern web")
}

// describeHost 是启动时打印的一行：连上的是哪台、它的 TERN_HOME 与版本。
func describeHost(alias string, info appserver.ServerInfo) string {
	if alias == "" {
		return fmt.Sprintf("app-server：%s · %s · 版本 %s", info.Host, info.Home, info.Version)
	}
	return fmt.Sprintf("app-server：经 ssh 连到 %s（远端主机 %s · %s · 版本 %s）", alias, info.Host, info.Home, info.Version)
}

// ---------- 对话 ----------

func (s *server) listRuns(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "thread/list", nil)
}

func (s *server) startRun(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "thread/start", nil)
}

func (s *server) continueRun(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "turn/start", map[string]any{"threadId": req.PathValue("id")})
}

func (s *server) stopRun(w http.ResponseWriter, req *http.Request) {
	s.replyNone(w, req, "turn/interrupt", map[string]any{"threadId": req.PathValue("id")})
}

func (s *server) steerRun(w http.ResponseWriter, req *http.Request) {
	s.replyNone(w, req, "turn/steer", map[string]any{"threadId": req.PathValue("id")})
}

func (s *server) decideNetwork(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "approval/respond", map[string]any{"kind": "network", "threadId": req.PathValue("id")})
}

func (s *server) decideTool(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "approval/respond", map[string]any{"kind": "tool", "threadId": req.PathValue("id")})
}

func (s *server) answerQuestion(w http.ResponseWriter, req *http.Request) {
	s.replyNone(w, req, "approval/respond", map[string]any{"kind": "question", "threadId": req.PathValue("id")})
}

// 撤销：dry_run 只预览。失败时页面读响应体里的 error（协议的错误只有 message，没有原来那份 result）。
func (s *server) rewindRun(w http.ResponseWriter, req *http.Request) {
	p, ok := s.params(w, req, map[string]any{"threadId": req.PathValue("id")})
	if !ok {
		return
	}
	result, err := s.call(req, "thread/rewind", p)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusOf(err))
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, result)
}

// 分叉：请求体是 {turn}，回复新对话的摘要；不自动订阅，页面照 thread/start 之后那样自己订阅。
func (s *server) forkRun(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "thread/fork", map[string]any{"threadId": req.PathValue("id")})
}

// 事件流：先订阅（thread/resume），再把每条 thread/event 写成一条 data:；对话结束时写 event: end。
// 浏览器断开时 req.Context() 结束，退订随之发出；这台主机被断开时订阅跟着结束，也写一条 event: end 收住这条流。
func (s *server) streamEvents(w http.ResponseWriter, req *http.Request) {
	client, err := s.clientFor(req)
	if err != nil {
		fail(w, err)
		return
	}
	sub, err := client.Subscribe(req.Context(), req.PathValue("id"), 0)
	if err != nil {
		fail(w, err)
		return
	}
	defer sub.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	end := func() {
		// 只写 event: 不写 data: 的话浏览器不会派发这个事件（data 为空的事件被丢掉），所以照旧带一个空对象。
		fmt.Fprint(w, "event: end\ndata: {}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
	for {
		e, ok := sub.Next(req.Context())
		if !ok {
			end()
			return
		}
		if e.End {
			end()
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", e.Data)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (s *server) importRun(w http.ResponseWriter, req *http.Request) {
	s.upload(w, req, "thread/import", nil)
}

func (s *server) attributeRun(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "thread/attribute", map[string]any{"threadId": req.PathValue("id")})
}

// ---------- 附件与沙箱文件 ----------

func (s *server) uploadAttachment(w http.ResponseWriter, req *http.Request) {
	s.upload(w, req, "attachment/upload", map[string]any{"name": req.URL.Query().Get("name")})
}

// 读取附图：Content-Type 只取按文件头认出的那一种并禁止嗅探。
func (s *server) serveAttachment(w http.ResponseWriter, req *http.Request) {
	s.download(w, req, "attachment/read", map[string]any{"name": req.PathValue("name")}, func(head chunkHead) {
		w.Header().Set("Content-Type", head.Mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=86400")
	})
}

func (s *server) listFiles(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "files/list", map[string]any{"threadId": req.PathValue("id")})
}

// 下载沙箱文件：内容由模型写入，一律当附件下载并禁止嗅探，模型写的 HTML 不会在页面源里执行。
func (s *server) downloadFile(w http.ResponseWriter, req *http.Request) {
	path := req.PathValue("path")
	s.download(w, req, "files/read", map[string]any{"threadId": req.PathValue("id"), "path": path}, func(head chunkHead) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+urlPathEscape(head.Name))
		w.Header().Set("Cache-Control", "no-store")
	})
}

func (s *server) uploadFile(w http.ResponseWriter, req *http.Request) {
	s.upload(w, req, "files/write", map[string]any{"threadId": req.PathValue("id"), "name": req.URL.Query().Get("name")})
}

func (s *server) deleteFile(w http.ResponseWriter, req *http.Request) {
	s.replyText(w, req, "files/delete", map[string]any{"threadId": req.PathValue("id"), "path": req.PathValue("path")}, "ok")
}

// 浏览器画面：agent 运行中反复来取，就是实时画面；调用结束后它就是这一步的快照。
func (s *server) browserFrame(w http.ResponseWriter, req *http.Request) {
	s.download(w, req, "browser/frame", map[string]any{"task": req.PathValue("task"), "call": req.PathValue("call")}, func(head chunkHead) {
		w.Header().Set("Content-Type", head.Mime)
		w.Header().Set("Cache-Control", "no-store")
	})
}

// ---------- 模型、调用链、记忆、审计 ----------

func (s *server) listModels(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "model/list", nil)
}

func (s *server) traceSpans(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "trace/spans", map[string]any{"id": req.PathValue("id")})
}

func (s *server) traceMetrics(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "trace/metrics", map[string]any{"conversation": req.URL.Query().Get("conversation")})
}

func (s *server) listMemory(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "memory/list", nil)
}

// 删除记忆：结果是 agent 的输出原文。
func (s *server) forgetMemory(w http.ResponseWriter, req *http.Request) {
	p, ok := s.params(w, req, map[string]any{"id": req.PathValue("id")})
	if !ok {
		return
	}
	var output string
	if err := s.callInto(req, "memory/forget", p, &output); err != nil {
		fail(w, err)
		return
	}
	io.WriteString(w, output)
}

func (s *server) listAudit(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "audit/list", map[string]any{"task": req.URL.Query().Get("task")})
}

// ---------- 设置、工作区、命令 ----------

func (s *server) getSettings(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "settings/read", nil)
}

func (s *server) putSettings(layer string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		s.replyText(w, req, "settings/write", map[string]any{"layer": layer}, "ok")
	}
}

func (s *server) addWorkspace(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "workspace/add", nil)
}

func (s *server) deleteWorkspace(w http.ResponseWriter, req *http.Request) {
	s.replyText(w, req, "workspace/remove", map[string]any{"name": req.PathValue("name")}, "ok")
}

func (s *server) listCommands(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "command/list", map[string]any{"workspace": req.URL.Query().Get("workspace")})
}

// ---------- Skills / MCP 中心 ----------

func (s *server) listHub(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "hub/list", nil)
}

func (s *server) enabledHub(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "hub/enabled", nil)
}

func (s *server) createSkill(w http.ResponseWriter, req *http.Request) {
	s.replyText(w, req, "hub/skill/create", nil, "ok")
}

func (s *server) deleteSkill(w http.ResponseWriter, req *http.Request) {
	s.replyText(w, req, "hub/skill/delete", map[string]any{"name": req.PathValue("name")}, "ok")
}

func (s *server) toggleSkill(w http.ResponseWriter, req *http.Request) {
	s.replyText(w, req, "hub/skill/toggle", map[string]any{"name": req.PathValue("name")}, "ok")
}

func (s *server) addMCP(w http.ResponseWriter, req *http.Request) {
	s.replyText(w, req, "hub/mcp/add", nil, "ok")
}

func (s *server) deleteMCP(w http.ResponseWriter, req *http.Request) {
	s.replyText(w, req, "hub/mcp/delete", map[string]any{"name": req.PathValue("name")}, "ok")
}

func (s *server) toggleMCP(w http.ResponseWriter, req *http.Request) {
	s.replyText(w, req, "hub/mcp/toggle", map[string]any{"name": req.PathValue("name")}, "ok")
}

func (s *server) testMCP(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "hub/mcp/test", map[string]any{"name": req.PathValue("name")})
}

// ---------- 评测 ----------

func (s *server) listEvals(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "eval/list", nil)
}

func (s *server) evalCases(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "eval/cases", nil)
}

func (s *server) getEval(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "eval/get", map[string]any{"id": req.PathValue("id")})
}

func (s *server) startEval(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "eval/start", nil)
}

func (s *server) evalAction(w http.ResponseWriter, req *http.Request) {
	s.reply(w, req, "eval/action", map[string]any{"id": req.PathValue("id"), "action": req.PathValue("action")})
}

// 中心能写配置、能让 agent 启动任意命令，所以拒绝其他网站发来的跨站请求（浏览器会带上 Origin），
// 也拒绝非本机 Host，防止 DNS 重绑定把外部域名指到 127.0.0.1。
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host := req.Host
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h
		}
		origin, err := url.Parse(req.Header.Get("Origin"))
		if (host != "127.0.0.1" && host != "localhost") || (req.Header.Get("Origin") != "" && (err != nil || origin.Host != req.Host)) {
			http.Error(w, "只接受网页端页面发来的请求", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, req)
	})
}
