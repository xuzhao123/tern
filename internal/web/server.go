package web

import (
	"bytes"
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
// 对话、设置、文件、评测这些数据都在 app-server 里；网页端只读 .data/users.json，不读别的数据。
// 带 -remote 时连的是远端 app-server（经 ssh），其余完全一样；断线重连由 appclient 负责。
type server struct {
	auth   *auth             // 账号与会话（见 auth.go）
	client *appclient.Client // 与 app-server 的连接，所有请求共用这一条
	remote string            // -remote 的 ssh 别名；空表示本机

	runs atomic.Int64 // 收到 thread/started、thread/status/changed 就加一：顶栏的 runs 变了，页面刷新列表

	mu    sync.Mutex
	state appclient.State            // 连接状态，随 appclient 的回调更新
	watch map[chan struct{}]struct{} // 正在等 /connection 的连接
}

// Run 启动网页端：go run . web [-addr …] [-remote 别名] [-add-user 名字]。main 已经把工作目录换到 TERN_HOME，
// `.env`、`.data/` 都相对它，所以不在 tern 源码根目录也能跑。
// 不带 -remote 时连本机 app-server：先保证它在运行，再连上；带 -remote 时经 ssh 连远端（见 internal/appclient），
// 远端的 app-server 由 proxy 自己保证在运行。启动 agent 的是 app-server，要从源码跑 agent 就
// `tern app-server -dev` 或 `tern app-server start -dev`（-dev 是本机的 app-server 的事）。
func Run(args []string, mcpHandler http.Handler) error {
	flags := flag.NewFlagSet("web", flag.ContinueOnError)
	addr := flags.String("addr", "127.0.0.1:8090", "网页端地址")
	remote := flags.String("remote", "", "经 SSH 连远端 app-server：ssh 别名（~/.ssh/config 里的 Host）")
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
	var client *appclient.Client
	var info appserver.ServerInfo
	if *remote != "" {
		client, info, err = appclient.DialRemote("tern web", *remote)
	} else {
		if _, err := appclient.Start(false); err != nil {
			return err
		}
		client, info, err = appclient.Dial("tern web")
	}
	if err != nil {
		return err
	}
	defer client.Close()
	s := &server{auth: authState, client: client, remote: *remote, state: client.State(), watch: map[chan struct{}]struct{}{}}
	// 列表变了（新建对话、一轮开始或结束）就推一下 /connection，页面据此刷新左侧列表。
	client.OnStarted(func(json.RawMessage) { s.changed() })
	client.OnStatus(func(string, string) { s.changed() })
	client.OnState(func(st appclient.State) {
		s.mu.Lock()
		s.state = st
		s.mu.Unlock()
		s.notify()
	})
	mux := http.NewServeMux()
	web, _ := fs.Sub(webFS, "page")
	page, err := fs.ReadFile(web, "index.html")
	if err != nil {
		return err
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
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
	if *remote != "" {
		fmt.Printf("app-server：经 ssh 连到 %s（远端主机 %s · %s · 版本 %s）\n", *remote, info.Host, info.Home, info.Version)
	} else {
		fmt.Printf("app-server：%s · %s · 版本 %s\n", info.Host, info.Home, info.Version)
	}
	fmt.Printf("agent：由 app-server 启动（要从源码跑 agent 用 tern app-server -dev 或 tern app-server start -dev）\n")
	fmt.Printf("登录账号：%s（%d 个）\n", usersPath, len(authState.accounts))
	return http.ListenAndServe(*addr, s.protect(mux))
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

// call 发一次协议请求，结果是回复的 JSON。
func (s *server) call(req *http.Request, method string, params any) (json.RawMessage, error) {
	var result json.RawMessage
	err := s.client.Call(req.Context(), method, params, &result)
	return result, err
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

// statusOf 把协议错误还原成 HTTP 状态：重连期间回 503（文字是“正在重连 <host>”）；方法出错时的 code
// 就是原来这个接口的状态码，照原样用；其余的（连接断了、协议本身的错误）回 502。
func statusOf(err error) int {
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

// ---------- 顶栏的主机与连接状态 ----------

// connectionInfo 是 GET /connection 推的内容（spec 第 7 节）。
type connectionInfo struct {
	Host   string `json:"host"`
	Remote bool   `json:"remote"`
	State  string `json:"state"`
	Runs   int64  `json:"runs"`
}

// connectionEvents 是顶栏用的 SSE：连上先推一次当前状态，之后状态或 runs 变了再推。
// 断线重连也是这样让页面知道：state 变成 reconnecting，连上后变回 connected。
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

// connection 是现在要推给页面的状态：主机、是不是远程、连接状态、runs。
func (s *server) connection() connectionInfo {
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	return connectionInfo{Host: s.client.Host(), Remote: s.remote != "", State: string(state), Runs: s.runs.Load()}
}

// changed 记下对话列表变了（新建对话、一轮开始或结束），页面凭 runs 的变化重新加载列表。
func (s *server) changed() {
	s.runs.Add(1)
	s.notify()
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

// 事件流：先订阅（thread/resume），再把每条 thread/event 写成一条 data:；对话结束时写 event: end。
// 浏览器断开时 req.Context() 结束，退订随之发出。
func (s *server) streamEvents(w http.ResponseWriter, req *http.Request) {
	sub, err := s.client.Subscribe(req.Context(), req.PathValue("id"), 0)
	if err != nil {
		fail(w, err)
		return
	}
	defer sub.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for {
		e, ok := sub.Next(req.Context())
		if !ok {
			return
		}
		if e.End {
			// 只写 event: 不写 data: 的话浏览器不会派发这个事件（data 为空的事件被丢掉），所以照旧带一个空对象。
			fmt.Fprint(w, "event: end\ndata: {}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
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
	if err := s.client.Call(req.Context(), "memory/forget", p, &output); err != nil {
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
