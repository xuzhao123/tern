package appserver

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xuzhao123/tern/internal/agent"
	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/protocol"
	"github.com/xuzhao123/tern/internal/telemetry"
	"github.com/xuzhao123/tern/internal/tenant"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// app-server 不读取 agent 源码：它代理模型请求、转发终端输出，从协议数据还原过程。
// 一条对话（thread）就是原来网页端的一个 run：ID、事件列表，存档 .data/runs/<ID>.jsonl 格式不变。
type event struct {
	Seq        int             `json:"seq"`
	Time       time.Time       `json:"time"`
	Kind       string          `json:"kind"` // request、response、stdout、stderr、exit、notify、delta、rewind 等
	Call       int             `json:"call,omitempty"`
	Status     int             `json:"status,omitempty"`
	Millis     int64           `json:"ms,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Text       string          `json:"text,omitempty"`
	Embedding  string          `json:"embedding,omitempty"`
	Purpose    string          `json:"purpose,omitempty"` // 请求用途：main、compact、memory_contextualize、memory_rerank
	Memory     bool            `json:"memory,omitempty"`
	MemoryTTL  string          `json:"memory_ttl,omitempty"`
	Skills     []string        `json:"skills,omitempty"` // 本对话启用的 skill 与 MCP server 快照，续聊沿用
	MCP        []mcpServer     `json:"mcp,omitempty"`
	Subagents  bool            `json:"subagents,omitempty"`        // 本对话允许 spawn_agent，续聊沿用
	AskUser    bool            `json:"ask_user,omitempty"`         // 本对话提供 ask_user，模型可以在运行中向人提问，续聊沿用
	Browser    bool            `json:"browser,omitempty"`          // 本对话启用浏览器工具，续聊沿用
	DSSearch   bool            `json:"ds_search,omitempty"`        // 本对话启用 DeepSeek 搜索工具，续聊沿用
	Disallowed []string        `json:"disallowed_tools,omitempty"` // 设置快照：关掉的单个工具（内置工具、web_search、open_page），续聊沿用；旧存档没有，表示都开
	Provider   string          `json:"provider,omitempty"`         // 本对话的模型供应商（-provider），续聊沿用
	Model      string          `json:"model,omitempty"`            // 本对话的具体模型（agent 的 -model）；空表示用这家供应商的默认值
	Bash       bool            `json:"bash,omitempty"`             // 本对话启用 bash 沙箱，续聊沿用
	RAG        bool            `json:"rag,omitempty"`              // 设置快照：本对话启用知识库检索，续聊沿用
	Workspace  string          `json:"workspace,omitempty"`        // 设置快照：本对话的工作区名字与开始时解析出的目录，续聊沿用
	WorkDir    string          `json:"workspace_dir,omitempty"`    //
	MaxSteps   int             `json:"max_steps,omitempty"`        // 设置快照：模型请求预算（-1 不限）、上下文窗口与工具输出上限；旧存档没有，按 agent 默认值
	Window     int             `json:"context_window,omitempty"`   //
	ToolOutput int             `json:"tool_output,omitempty"`      //
	Permission string          `json:"permission,omitempty"`       // 本轮的权限模式（续聊可以换）与权限规则快照
	Rules      []string        `json:"permission_rules,omitempty"`
	Hooks      string          `json:"hooks,omitempty"`          // 平台设置里的 hooks 配置原文快照，续聊沿用
	NetAllow   []string        `json:"net_allow,omitempty"`      // 本轮沙箱可访问的域名，记在 start/continue 上：start 上是设置里的白名单，continue 上再加上本对话批准过的
	Task       string          `json:"task,omitempty"`           // 发出这次请求的 agent 任务ID（X-Agent-Task）
	Sub        bool            `json:"sub,omitempty"`            // 请求来自子 agent：不进入父对话流、不参与续聊恢复
	Trace      string          `json:"trace,omitempty"`          // start/continue 上记这次交互的 trace_id
	Span       string          `json:"span,omitempty"`           // request 上记 agent 的 chat span ID（来自 traceparent 头）
	Method     string          `json:"method,omitempty"`         // notify / approval 事件的协议方法名，参数在 Body
	RPC        int64           `json:"rpc,omitempty"`            // agent 发来的请求ID（approval），回复时带回
	ASeq       int64           `json:"aseq,omitempty"`           // 接管：这条事件对应的协议消息在 agent 一侧的序号；重启后从最大的序号之后补齐
	Chunks     int             `json:"chunks,omitempty"`         // 流式响应的 chunk 数；Body 是按 chunk 拼回的完整响应
	FirstToken int64           `json:"first_token_ms,omitempty"` // 从转发请求到收到第一段正文或推理的毫秒数
	Protocol   bool            `json:"protocol,omitempty"`       // start/continue 上标记这一轮经协议运行；之前的存档没有，界面据此启用旧存档兼容
	Eval       string          `json:"eval,omitempty"`           // 由评测发起的试次（评测ID · 用例 #序号），对话列表里不显示
	Source     string          `json:"source,omitempty"`         // 导入的对话：codex 或 claude-code，记在 start 上；只读，不能续聊
}

type run struct {
	ID, Query string
	Eval      string // 评测发起的试次，见 event.Eval
	Source    string // 从 Codex / Claude Code 导入的对话，见 event.Source
	ForkOf    string // 分出来的对话是这个 ID 的原对话，见 fork 事件；其余为空
	Done      bool
	s         *Server // 新建、收尾时要广播通知，见 add

	mu         sync.Mutex
	chains     map[string]*chain // 每个任务最近一次主请求，代理据此把存档写成增量
	calls      int
	seq        int // 最后一个事件的序号：只增不减。delta 只在内存里也占序号，所以重启后要从存档里最大的序号接着编
	events     []event
	changed    chan struct{}
	file       *os.File
	proc       *os.Process // 正在运行的 agent 进程；强制结束时向它的进程组发 SIGKILL
	stopping   bool        // 已请求过一次停止；再按一次强制结束
	rootTask   string      // 本次启动的父 agent 任务ID：启动后第一个带任务ID的请求一定来自父 agent
	token      string      // 本次启动的 agent 进程访问模型代理用的令牌（AGENT_PROXY_TOKEN），每次启动重新生成
	after      int64       // 接管时已经存下的最后一条协议消息的序号：补发里不超过它的不再记一遍（见 live.go）
	exitStatus string      // agent 退出前用 process/exit 报的状态；接管的进程拿不到退出码，靠它
	// 与正在运行的 agent 之间的协议连接，以及它在等待答复的联网审批（域名 → 请求ID）。
	conn      *protocol.Conn
	approvals map[string][]int64
	toolWaits map[string]toolWait // 等待确认的高风险操作（调用ID → 请求）
	asks      map[string]int64    // 模型在等人回答的提问（ask_user 的调用ID → 请求ID）
}

type toolWait struct {
	rpc                                int64
	Task, Tool, Risk, Arguments, Scope string // Scope：外发类可以“本对话内都允许”的范围，由 agent 给出
}

// agent 子进程：默认执行正在运行的自己（/proc/self/exe），与 app-server 同一份代码、不需要编译、在哪台机器运行就是哪台机器的格式；
// -dev 时改用 go run .，读取最新源码。
// agent 浏览时把画面写到 .data/browser/<任务ID>/<调用ID>.jpg（screencast 帧不断覆盖，结束时是最终截图）。
// 界面运行中反复来取这张图，就是实时画面；调用结束后它就是这一步的快照。
var providerPattern = regexp.MustCompile(`^[a-z]{1,32}$`)

func (s *Server) agentCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, selfExe(), args...)
	if s.dev {
		cmd = exec.CommandContext(ctx, "go", append([]string{"run", "."}, args...)...)
	}
	cmd.Dir = s.dir
	return cmd
}

// selfExe 是正在运行的程序本身：go run 起的 app-server 跑的是临时编译文件，界面退出后它就被删掉，按路径起不了 agent；
// /proc/self/exe 指向正在运行的程序。没有 /proc 时退回 os.Executable。
func selfExe() string {
	if _, err := os.Stat("/proc/self/exe"); err == nil {
		return "/proc/self/exe"
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return os.Args[0]
}

// thread/start：每次运行都启动一个独立的 agent 子进程（见 agentCommand），各次运行的状态与输出互不影响。
func (c *conn) threadStart(m protocol.Message) (any, *protocol.Error) {
	s := c.s
	// 普通对话只选模型和工作区，工具来自设置（见 settings.go）。
	var input struct {
		Query      string      `json:"query"`
		Embedding  string      `json:"embedding"`
		Provider   string      `json:"provider"`
		Model      string      `json:"model"`      // 第二级下拉选的具体模型；空：用这家供应商的默认值
		Workspace  *string     `json:"workspace"`  // nil：用设置里的默认工作区；"": 本对话不用工作区
		Permission string      `json:"permission"` // 空：用设置里的默认权限模式
		Images     []attachRef `json:"images"`     // 附图：先经 attachment/upload 存好，这里只带引用
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	input.Query = strings.TrimSpace(input.Query)
	name := userName(m)
	settings, _, err := s.effectiveFor(name)
	if err != nil {
		return nil, bad(500, "无法读取设置："+err.Error())
	}
	if input.Embedding == "" {
		input.Embedding = settings.Values["embedding"]
	}
	if input.Embedding != "ark" && input.Embedding != "local" {
		return nil, bad(400, "embedding须为ark或local")
	}
	if input.Query == "" {
		return nil, bad(400, "普通问答需要query")
	}
	if input.Provider == "" {
		input.Provider = settings.Values["provider"]
	}
	// 具体模型同样有默认值：设置里的“默认具体模型”，空表示用这家供应商的默认值。
	// 界面传了就以界面为准；它跟其他启动参数一起写进检查点，续聊、续跑、回放与子 agent 沿用。
	// 设置里的模型不属于这次选的供应商时（界面换了供应商）不带过去，免得被下面的清单校验拒绝。
	if input.Model == "" {
		input.Model = modelFor(input.Provider, settings.Values["model"])
	}
	// 供应商名由 agent 校验（路由表在 llm 包）；这里只挡住不像名字的输入。
	if !providerPattern.MatchString(input.Provider) {
		return nil, bad(400, "provider 只能是小写字母")
	}
	// 具体模型只能是这家供应商清单里的一个：清单同样来自 llm.Providers，界面不做第二份。
	if input.Model != "" {
		p, ok := llm.FindProvider(input.Provider)
		if _, found := p.FindModel(input.Model); !ok || !found {
			return nil, bad(400, "模型不在 "+input.Provider+" 的可选清单里")
		}
	}
	// 附图：要模型能看图才收，
	// 与 agent 发请求前的处理一致（不能看图的模型会把图换成说明文字，那等于白附）。
	if len(input.Images) > 0 {
		if ferr := s.checkImages(input.Images); ferr != nil {
			return nil, ferr
		}
		if model, vision := visionModel(input.Provider, input.Model); !vision {
			return nil, bad(400, "模型 "+model+" 不能看图，换一个能看图的模型再附图")
		}
	}
	// 工作区只能从设置里登记的列表里挑；界面给的是名字，目录由服务端按登记解析。
	workspace := settings.Values["workspace"]
	if input.Workspace != nil {
		workspace = *input.Workspace
	}
	var workDir string
	if workspace != "" {
		i := slices.IndexFunc(settings.Workspaces, func(w workspaceDir) bool { return w.Name == workspace })
		if i < 0 || !settings.on("coding") {
			return nil, bad(400, "不能使用工作区 "+workspace+"：没有登记或编码工具没打开")
		}
		workDir = settings.Workspaces[i].Path
	} else {
		workspace = ""
	}
	// 设置快照：这一刻打开的工具，写进 start 事件，续聊沿用。
	snap := event{Kind: "start", RAG: settings.on("rag"), Memory: settings.on("memory"), MemoryTTL: settings.Values["memory_ttl"], Subagents: settings.on("subagents"), AskUser: settings.on("ask_user"),
		Browser: settings.on("web_search") || settings.on("open_page"), DSSearch: settings.on("ds_search"), Provider: input.Provider, Model: input.Model, Bash: settings.on("bash"), Workspace: workspace, WorkDir: workDir,
		Permission: settings.Values["permission_mode"], Rules: ruleLines(settings.Values["permission_rules"])}
	for _, tool := range singleTools {
		if !settings.on(tool) {
			snap.Disallowed = append(snap.Disallowed, tool)
		}
	}
	if input.Permission != "" {
		if valid("permission_mode", input.Permission, settingsFile{}) != nil {
			return nil, bad(400, "permission 取 plan、ask、edit 或 full")
		}
		snap.Permission = input.Permission
	}
	if snap.Hooks, err = hooksConfig(settings.Values["hooks"]); err != nil {
		return nil, bad(500, "平台设置里的 hooks 无法使用："+err.Error())
	}
	snap.MaxSteps, _ = strconv.Atoi(settings.Values["max_steps"])
	if snap.MaxSteps == 0 {
		snap.MaxSteps = -1 // 设置里 0 表示不限；快照里 0 留给“旧存档没有这项、按 agent 默认值”
	}
	snap.Window, _ = strconv.Atoi(settings.Values["context_window"])
	if snap.Window == 0 {
		// 设置里是“按模型”，取路由表里这个模型核对过的输入上限；不知道时用 262144（改成按模型之前的默认值）。
		snap.Window = cmp.Or(llm.WindowOf(input.Provider, input.Model), 262144)
	}
	snap.ToolOutput, _ = strconv.Atoi(settings.Values["tool_output"])
	if settings.on("skills") {
		snap.Skills = settings.Skills
	}
	if settings.on("mcp") {
		snap.MCP = settings.MCP
	}
	if !snap.RAG && !snap.Memory {
		input.Embedding = ""
	}
	if snap.Bash {
		snap.NetAllow = netDomains(settings.Values["net_allow"])
	}
	snap.Embedding = input.Embedding
	args := snapshotArgs(snap, snap.NetAllow)
	// 对话 ID：随机 UUID（v4），不含时间，与 app-server 在哪个时区启动无关。
	// 早期存档的 ID 是“20261006-213820.763”这样的本地时间，照常读回，不改名，旧链接仍能打开。
	id := uuid.NewString()
	file, err := s.createRunFile(id)
	if err != nil {
		return nil, bad(500, err.Error())
	}
	r := &run{ID: id, Query: strings.TrimSpace(input.Query), changed: make(chan struct{}), file: file}
	s.addRun(r)
	ctx, span := r.beginTurn(input.Query)
	snap.Text, snap.Trace, snap.Protocol = r.Query, span.SpanContext().TraceID().String(), true
	r.add(snap)
	turn := map[string]any{"question": input.Query}
	if len(input.Images) > 0 {
		turn["images"] = input.Images
	}
	s.launch(ctx, r, args, turn, name)
	return r.summary(), nil
}

// model/list：输入框第二级下拉的候选项。路由表只存在 llm 包里，这里按它读出来给界面，不做第二份。
// default 是不选具体模型时 agent 实际会用的那个：.env 或环境变量里的 LLM_MODEL / DEEPSEEK_MODEL 覆盖路由表默认值，
// agent 子进程继承 app-server 的环境、在同一目录读 .env，所以按同样的规则解析。
// 每个模型带上能不能看图：界面据此禁用附图按钮，服务端收图时同样按这条规则判断。
// 供应商的下拉（输入框与设置页）也从这里来：label 是显示名，error 是本机没配好的原因（缺密钥或地址），界面据此灰掉。
func (c *conn) modelList(protocol.Message) (any, *protocol.Error) {
	type providerModels struct {
		Name    string      `json:"name"`
		Label   string      `json:"label"`
		Default string      `json:"default"`
		Error   string      `json:"error,omitempty"`
		Models  []llm.Model `json:"models"`
	}
	out := []providerModels{}
	for _, p := range llm.Providers {
		def, problem := p.Model, ""
		config, err := llm.LoadConfig(p.Name)
		if config.Model != "" { // 没有密钥时仍会解析出模型
			def = config.Model
		}
		if err != nil {
			problem = err.Error()
		}
		out = append(out, providerModels{Name: p.Name, Label: p.Label, Default: def, Error: problem, Models: p.Models})
	}
	return out, nil
}

// 对话存档在 .data/runs/<对话ID>.jsonl。
func (s *Server) runsDir() string {
	return filepath.Join(s.dir, tenant.Path("", "runs"))
}

func (s *Server) createRunFile(id string) (*os.File, error) {
	if err := os.MkdirAll(s.runsDir(), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(s.runsDir(), id+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

// threadOf 取参数里的对话，找不到时当作不存在（404）。
func (s *Server) threadOf(id string) (*run, *protocol.Error) {
	r := s.find(id)
	if r == nil {
		return nil, bad(404, "找不到这条对话")
	}
	return r, nil
}

// turn/start：同一条对话一次只处理一个提问；恢复历史不会重新执行已经完成的工具。
func (c *conn) turnStart(m protocol.Message) (any, *protocol.Error) {
	s := c.s
	var input struct {
		ThreadID   string      `json:"threadId"`
		Query      string      `json:"query"`
		Permission string      `json:"permission"` // 权限模式可以每轮换（和 Codex 的 /approvals、Claude Code 的 Shift+Tab 一样）；工具与规则沿用快照
		Images     []attachRef `json:"images"`     // 附图：同新对话，只带 attachment/upload 得到的引用
		Compact    bool        `json:"compact"`    // 这一轮第一次请求模型之前先压缩上下文（chat 的 /compact），原样交给 agent
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	if strings.TrimSpace(input.Query) == "" {
		return nil, bad(400, "请填写续聊问题")
	}
	if input.Permission != "" && valid("permission_mode", input.Permission, settingsFile{}) != nil {
		return nil, bad(400, "permission 取 plan、ask、edit 或 full")
	}
	if len(input.Images) > 0 {
		if ferr := s.checkImages(input.Images); ferr != nil {
			return nil, ferr
		}
	}
	r.mu.Lock()
	if !r.canContinue() {
		r.mu.Unlock()
		return nil, bad(409, "对话正在运行或尚未正常完成，暂不能续聊")
	}
	history, err := r.resumeHistory()
	if err != nil {
		r.mu.Unlock()
		return nil, bad(409, err.Error())
	}
	// 分支还没跑过、原对话就撤销到了分叉点之前：接着的那一轮已经不在了。这里先挡下，
	// 否则 agent 起来以后才报错退出。分叉、撤销留下的提醒同样在这里算出来（见 notes）。
	if history.Continues != "" && agent.Rewound(history.Continues) {
		r.mu.Unlock()
		return nil, bad(409, "接着的那一轮已经撤销，不能接着聊")
	}
	notes := r.notes()
	file, err := os.OpenFile(filepath.Join(s.runsDir(), r.ID+".jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		r.mu.Unlock()
		return nil, bad(500, "无法追加对话记录")
	}
	// 续聊沿用这条对话开始时的设置快照：最近一次 start/continue 事件为准，之后改设置不影响这条对话。
	var snap event
	for _, e := range r.events {
		if e.Kind == "start" || e.Kind == "continue" {
			snap = e
		}
	}
	snap.Kind, snap.Text, snap.Embedding = "continue", strings.TrimSpace(input.Query), history.Embedding
	if input.Permission != "" {
		snap.Permission = input.Permission
	}
	snap.RAG = snap.RAG || history.RAG // 之前的存档没有 rag 字段，按上一次请求里有没有 search_docs 判断
	// 模型跟着检查点走：续聊用上一轮实际用的模型（agent 的 history.Model），换模型要新建对话。
	if history.Model != "" {
		snap.Model = history.Model
	}
	// 带图时按这条对话实际用的模型判断能不能看图（snap.Model 为空就是 snap.Provider 的默认值）。
	if len(input.Images) > 0 {
		if model, vision := visionModel(snap.Provider, snap.Model); !vision {
			file.Close()
			r.mu.Unlock()
			return nil, bad(400, "模型 "+model+" 不能看图，换一个能看图的模型再附图")
		}
	}
	snap.Trace, snap.Protocol, snap.Status, snap.Seq, snap.Time = "", true, 0, 0, time.Time{}
	r.file, r.Done = file, false
	r.mu.Unlock()
	snap.NetAllow = r.netAllow() // netAllow 自己加锁，要在放开 r.mu 之后调用
	ctx, span := r.beginTurn(snap.Text)
	snap.Trace = span.SpanContext().TraceID().String()
	r.add(snap)
	args := []string{}
	if history.Effort != "" {
		args = append(args, "-reasoning-effort", history.Effort)
	}
	args = append(args, snapshotArgs(snap, r.netAllow())...)
	// 问题、续聊上下文与续接关系放进 turn/start。每一轮都用新的检查点ID，旧检查点保持原样（“一个ID一个问题”），
	// 续接关系让模型可以按 task_id 续跑之前各轮的子任务。
	turn := map[string]any{"question": strings.TrimSpace(input.Query), "history": history, "continues": history.Continues}
	if len(notes) > 0 {
		// 分叉、只退一边的撤销之后，记录和磁盘上的文件对不上：作为提醒追加在问题后面（agent 的 notes）。
		turn["notes"] = notes
	}
	if len(input.Images) > 0 {
		turn["images"] = input.Images
	}
	if input.Compact {
		turn["compact"] = true
	}
	s.launch(ctx, r, args, turn, userName(m))
	return r.summary(), nil
}

// 每次交互（首问或续聊）一个 trace，根 span 是 app-server 的 interaction；agent 进程的 invoke_agent 挂在它下面。
// 同一个对话的多个 trace 用 gen_ai.conversation.id（对话ID）归到一起。
func (r *run) beginTurn(query string) (context.Context, trace.Span) {
	r.mu.Lock()
	turn := 1
	for _, e := range r.events {
		if e.Kind == "start" || e.Kind == "continue" {
			turn++
		}
	}
	r.mu.Unlock()
	ctx, span := telemetry.Begin(context.Background(), "interaction", trace.SpanKindServer, semconv.GenAIConversationID(r.ID), attribute.Int("agent.turn", turn))
	telemetry.Content(span, "agent.question", query)
	return ctx, span
}

// agent 以 -agent-server 启动，协议走 .data/live/ 下的 Unix socket（见 live.go），给人看的终端输出也经协议的 log 通知送来。
// app-server 退出时 agent 照常运行，app-server 重启后重新接管（adopt）。turn 为 nil 时由命令行参数决定要做什么，不发 turn/start。
//
// 每次启动生成一个代理令牌：模型代理只接受带着本对话当前令牌的请求，别的进程不能往这条对话里塞记录；重新接管时 agent 也要出示它。
func (s *Server) launch(ctx context.Context, r *run, args []string, turn map[string]any, userName string) {
	span := trace.SpanFromContext(ctx)
	token := newToken()
	cmd := s.agentCommand(context.Background(), append([]string{"-agent-server"}, args...)...)
	// 模型请求经 llm.sock 上的代理：socket 用相对路径（agent 的工作目录就是 TERN_HOME）；
	// agent 有 AGENT_PROXY_SOCKET 时按“经代理”处理，模型地址里的主机名只是为了让请求凑成一个 HTTP 地址。
	cmd.Env = append(os.Environ(), "AGENT_PROXY_SOCKET="+LlmSocket, "AGENT_PROXY_URL=http://tern/llm/"+r.ID, "AGENT_CONVERSATION_ID="+r.ID,
		"AGENT_USER="+userName, "AGENT_PROXY_TOKEN="+token, "AGENT_APP_SOCKET="+livePath(r, ".sock"))
	cmd.Env = append(cmd.Env, telemetry.Env(ctx)...)
	os.MkdirAll(s.abs(filepath.Dir(livePath(r, ".sock"))), 0o700)
	s.cleanLive(r)
	// 文件描述符 2 接到文件上：app-server 退出后 agent 往这里写也不会出错；正常日志走协议，这里只会有崩溃一类的输出。
	errFile, err := os.OpenFile(s.abs(livePath(r, ".err")), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		r.add(event{Kind: "exit", Text: err.Error()})
		telemetry.End(span, err, "launch_error")
		return
	}
	cmd.Stderr = errFile
	r.mu.Lock()
	r.approvals, r.toolWaits, r.asks, r.token, r.exitStatus = map[string][]int64{}, map[string]toolWait{}, map[string]int64{}, token, ""
	r.mu.Unlock()
	// 自己的进程组：停止时向整组发信号；app-server 所在终端的 Ctrl-C 也不会传到 agent。-dev 模式下组里是 go run 和它编译出的 agent。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	errFile.Close()
	if err != nil {
		r.add(event{Kind: "exit", Text: err.Error()})
		telemetry.End(span, err, "launch_error")
		return
	}
	if info, err := json.Marshal(liveInfo{PID: cmd.Process.Pid, Token: token}); err == nil {
		os.WriteFile(s.abs(livePath(r, ".json")), info, 0o600)
	}
	r.mu.Lock()
	r.proc, r.stopping, r.rootTask = cmd.Process, false, ""
	r.mu.Unlock()
	exited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(exited)
	}()
	go func() {
		conn := s.connect(r, token, 0, exited, "")
		if conn != nil && turn != nil {
			if err := conn.Call(context.Background(), "turn/start", turn, nil); err != nil {
				r.add(event{Kind: "stdout", Text: "turn/start 失败：" + err.Error()})
			}
		}
		if conn != nil {
			<-conn.Closed()
		}
		<-exited
		text := "exit 0"
		if waitErr != nil {
			text = waitErr.Error()
		}
		// 先结束根 span 再记 exit：界面看到 exit 时，interaction 已经写进 trace 文件。
		span.SetAttributes(attribute.String("process.exit", text))
		telemetry.End(span, waitErr, "agent_exit")
		s.finish(r, text)
	}()
}

// snapshotArgs 把设置快照还原成 agent 的启动参数；首问和续聊共用，保证同一条对话每一轮的工具一致。
func snapshotArgs(e event, netAllow []string) []string {
	args := []string{}
	if e.RAG {
		args = append(args, "-rag")
	}
	if (e.RAG || e.Memory) && e.Embedding != "" {
		args = append(args, "-embedding", e.Embedding)
	}
	args = appendMemoryArgs(args, e.Memory, e.MemoryTTL)
	args = appendCapabilityArgs(args, e.Skills, e.MCP)
	if e.Subagents {
		args = append(args, "-subagents")
	}
	if e.AskUser {
		args = append(args, "-ask-user")
	}
	if e.Browser {
		args = append(args, "-browser")
	}
	if e.DSSearch {
		args = append(args, "-ds-search")
	}
	if len(e.Disallowed) > 0 {
		args = append(args, "-disallowed-tools", strings.Join(e.Disallowed, ","))
	}
	if e.Provider != "" {
		args = append(args, "-provider", e.Provider)
	}
	// 具体模型作为启动参数传：它跟其他参数一起写进检查点，中断续跑、归因回放和子 agent 都用同一个模型。
	if e.Model != "" {
		args = append(args, "-model", e.Model)
	}
	if e.Bash {
		args = append(args, "-bash")
		for _, domain := range netAllow {
			args = append(args, "-net-allow", domain)
		}
	}
	if e.WorkDir != "" {
		args = append(args, "-workspace", e.WorkDir)
	}
	if e.Permission != "" {
		args = append(args, "-permission-mode", e.Permission)
	}
	for _, line := range e.Rules {
		effect, rule, _ := strings.Cut(line, ":")
		args = append(args, "-"+strings.TrimSpace(effect), strings.TrimSpace(rule))
	}
	if e.Hooks != "" {
		args = append(args, "-hooks", e.Hooks)
	}
	if e.MaxSteps < 0 {
		args = append(args, "-max-steps", "0")
	}
	for flag, n := range map[string]int{"-max-steps": e.MaxSteps, "-context-window": e.Window, "-tool-output-tokens": e.ToolOutput} {
		if n > 0 {
			args = append(args, flag, strconv.Itoa(n))
		}
	}
	return args
}

func appendMemoryArgs(args []string, memory bool, ttl string) []string {
	if memory {
		args = append(args, "-memory")
	}
	if memory && ttl != "" {
		args = append(args, "-memory-ttl", ttl)
	}
	return args
}

// memory/list：记忆文件由 agent 写入；app-server 只读展示。
// 派生索引（背景说明、块状态）只用来展示，缺失或损坏时显示为空，不影响原始记忆。
func (c *conn) memoryList(protocol.Message) (any, *protocol.Error) {
	data, err := os.ReadFile(filepath.Join(c.s.dir, tenant.Path("", "memory.json")))
	if errors.Is(err, os.ErrNotExist) {
		data, err = []byte(`{"next":0,"memories":[]}`), nil
	}
	if err != nil || !json.Valid(data) {
		return nil, bad(500, "无法读取 .data/memory.json")
	}
	index, err := os.ReadFile(filepath.Join(c.s.dir, tenant.Path("", "memory-index", "contexts.json")))
	if err != nil || !json.Valid(index) {
		index = []byte(`{"chunks":{}}`)
	}
	return map[string]any{"path": tenant.Path("", "memory.json"), "now": time.Now(), "file": json.RawMessage(data), "index": json.RawMessage(index)}, nil
}

var memoryID = regexp.MustCompile(`^M[0-9]{1,9}$`)
var purposePattern = regexp.MustCompile(`^[a-z_]{1,32}$`)
var taskPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// memory/forget：删除走 agent 的 -memory-forget，与 agent 共用文件锁和事务。
func (c *conn) memoryForget(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ID   string `json:"id"`
		User string `json:"user"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if !memoryID.MatchString(input.ID) {
		return nil, bad(400, "记忆编号格式为M加数字")
	}
	cmd := c.s.agentCommand(c.ctx, "-memory-forget", input.ID)
	cmd.Env = append(os.Environ(), "AGENT_USER="+input.User)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, bad(409, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

// thread/rewind：撤销某一轮及之后（mode 可以是 both、files、chat，见 agent 的 rewind）。
// 文件由 agent 的 rewind 命令恢复（和记忆删除一样，改动用户数据的事交给 agent，app-server 不自己写工作区）；
// 这里核对这一轮属于这条对话、对话没有在运行，再把结果记进对话记录。
// dry_run 只预览：界面先列出会恢复、删除的文件和冲突，人确认后再真正执行。
func (c *conn) threadRewind(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Turn     string `json:"turn"`
		DryRun   bool   `json:"dry_run"`
		Mode     string `json:"mode"`
		User     string `json:"user"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	s := c.s
	r, ferr := s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	if !taskPattern.MatchString(input.Turn) {
		return nil, bad(400, "需要这一轮的检查点ID")
	}
	if input.Mode == "" {
		input.Mode = "both"
	}
	if input.Mode != "both" && input.Mode != "files" && input.Mode != "chat" {
		return nil, bad(400, "mode 取 both、files 或 chat")
	}
	r.mu.Lock()
	turns := r.turnIDs()
	running := !r.Done
	inherited := r.inherited(input.Turn)
	r.mu.Unlock()
	if !slices.Contains(turns, input.Turn) {
		return nil, bad(404, "这一轮不属于这条对话")
	}
	if running {
		return nil, bad(409, "对话正在运行，停下之后再撤销")
	}
	// 分叉时带过来的轮次只能在原对话里撤销：agent 的撤销沿磁盘上的续接链往后找，分支还没跑过时
	// 这条链会走进原对话后面的轮次，结果是还原原对话改过的文件，并把原对话的轮次标成已撤销。
	if inherited {
		return nil, bad(409, "这一轮是分叉时从原对话带过来的，到原对话里撤销")
	}
	args := []string{"rewind"}
	if input.DryRun {
		args = append(args, "-dry-run")
	}
	cmd := s.agentCommand(c.ctx, append(append(args, "-mode", input.Mode), input.Turn)...)
	cmd.Env = append(os.Environ(), "AGENT_USER="+input.User, "AGENT_CONVERSATION_ID="+r.ID)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	result := raw(output)
	if err != nil {
		return nil, bad(409, strings.TrimPrefix(strings.TrimSpace(stderr.String()), "Error: "))
	}
	if !input.DryRun {
		// 对话已经结束，记录文件关了：临时以追加方式打开（同联网审批）。
		r.mu.Lock()
		file, err := os.OpenFile(filepath.Join(s.runsDir(), r.ID+".jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			r.mu.Unlock()
			return nil, bad(500, "文件已撤销，但无法写入对话记录")
		}
		r.file = file
		r.mu.Unlock()
		r.add(event{Kind: "rewind", Text: input.Turn, Body: result})
		file.Close()
	}
	return result, nil
}

// thread/fork：从第 n 轮之后另起一条对话。前面各轮的事件原样复制进新对话的存档（seq 不改），原对话不变、文件不动；
// 分支和原对话共用工作区与 bash 沙箱，所以原对话之后各轮改过的文件要列出来，下一轮由 turn/start 提醒模型。
func (c *conn) threadFork(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Turn     string `json:"turn"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	s := c.s
	r, ferr := s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	if !taskPattern.MatchString(input.Turn) {
		return nil, bad(400, "需要这一轮的检查点ID")
	}
	r.mu.Lock()
	turns := r.turnIDs()
	running, source, eval := !r.Done, r.Source, r.Eval
	r.mu.Unlock()
	// 导入的对话没有 turn/started：先核对轮次的话，它们总是回“这一轮不属于这条对话”，到不了这两条，所以排在前面。
	if source != "" {
		return nil, bad(409, "导入的对话不能分叉")
	}
	if eval != "" {
		return nil, bad(409, "评测的试次不能分叉")
	}
	n := slices.Index(turns, input.Turn)
	if n < 0 {
		return nil, bad(404, "这一轮不属于这条对话")
	}
	n++
	if running {
		// 下面要预览之后各轮改过的文件，预览要拿各轮检查点的锁：这一轮还在跑就不能预览。
		return nil, bad(409, "对话正在运行，停下之后再分叉")
	}
	if agent.Rewound(input.Turn) {
		return nil, bad(409, "这一轮已经撤销，不能从它分叉")
	}
	// 第 n 轮的终点是它的 turn/started 之后下一个 start 或 continue 之前；最后一轮就是全部。内存里的 delta 不存档，也不带。
	r.mu.Lock()
	end := r.turnEnd(input.Turn)
	copied := []event{}
	for _, e := range r.events[:end] {
		if e.Kind != "delta" {
			copied = append(copied, e)
		}
	}
	r.mu.Unlock()
	// 第 n 轮之后第一个没撤销的轮次：它和之后各轮改过的文件仍在磁盘上，分支接着改之前要先知道。
	after, preview := 0, ""
	for _, id := range turns[n:] {
		if agent.Rewound(id) {
			continue
		}
		after++
		if preview == "" {
			preview = id
		}
	}
	files, filesError := []string{}, ""
	if preview != "" {
		files, filesError = s.rewindPreview(c.ctx, r.ID, preview)
		if files == nil {
			files = []string{}
		}
	}
	body, err := json.Marshal(map[string]any{"from": r.ID, "turn": input.Turn, "n": n, "after": after, "files": files, "files_error": filesError})
	if err != nil {
		return nil, bad(500, err.Error())
	}
	// 新对话的存档：复制的各行 + 一条 fork 事件；它已经结束，接着聊时 turn/start 再打开记录文件。
	id := uuid.NewString()
	file, err := s.createRunFile(id)
	if err != nil {
		return nil, bad(500, err.Error())
	}
	nr := &run{ID: id, ForkOf: r.ID, Done: true, changed: make(chan struct{})}
	var buf bytes.Buffer
	for _, e := range copied {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		buf.Write(append(line, '\n'))
		nr.events = append(nr.events, e)
		// calls、seq、Query 按复制的事件算，同 loadRuns：重启后读回来的和这里建出来的一样。
		nr.calls, nr.seq = max(nr.calls, e.Call), max(nr.seq, e.Seq)
		if e.Kind == "start" {
			nr.Query = e.Text
		}
		if e.Kind == "request" && nr.Query == "" {
			nr.Query = firstUserMessage(e.Body)
		}
	}
	nr.seq++
	fork := event{Kind: "fork", Text: r.ID, Body: body, Seq: nr.seq, Time: time.Now().UTC()}
	nr.events = append(nr.events, fork)
	if line, err := json.Marshal(fork); err == nil {
		buf.Write(append(line, '\n'))
	}
	if _, err := file.Write(buf.Bytes()); err != nil {
		file.Close()
		os.Remove(filepath.Join(s.runsDir(), id+".jsonl"))
		return nil, bad(500, "分叉的对话记录写不进去："+err.Error())
	}
	file.Close()
	s.addRun(nr)
	return nr.summary(), nil
}

// rewindPreview 跑一次 agent 的 rewind -dry-run，把之后各轮改过的文件（恢复、删除、bash 改过没快照）合起来。
// 跑失败不挡分叉：之后已经分过叉时，agent 会报“之后有 2 个分支”，这时只把错误记进 fork 事件。
func (s *Server) rewindPreview(ctx context.Context, threadID, turn string) ([]string, string) {
	cmd := s.agentCommand(ctx, "rewind", "-dry-run", "-mode", "chat", turn)
	cmd.Env = append(os.Environ(), "AGENT_CONVERSATION_ID="+threadID)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, strings.TrimPrefix(strings.TrimSpace(stderr.String()), "Error: ")
	}
	var result struct {
		Restore       []string `json:"restore"`
		Delete        []string `json:"delete"`
		Unsnapshotted []string `json:"unsnapshotted"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, "预览后面各轮改过的文件失败：" + err.Error()
	}
	return slices.Concat(result.Restore, result.Delete, result.Unsnapshotted), ""
}

// 这条对话各轮 agent 的检查点ID（turn/started 通知；子 agent 的包在 subagent/message 里，不会混进来）。调用方持有 r.mu。
func (r *run) turnIDs() []string {
	ids := []string{}
	for _, e := range r.events {
		var p struct {
			TurnID string `json:"turn_id"`
		}
		if e.Kind == "notify" && e.Method == "turn/started" && json.Unmarshal(e.Body, &p) == nil && p.TurnID != "" && !slices.Contains(ids, p.TurnID) {
			ids = append(ids, p.TurnID)
		}
	}
	return ids
}

// turnEnd 是某一轮在事件里的终点：它的 turn/started 之后下一个 start 或 continue 之前；最后一轮就是全部。调用方持有 r.mu。
func (r *run) turnEnd(turn string) int {
	at := -1
	for i, e := range r.events {
		var p struct {
			TurnID string `json:"turn_id"`
		}
		if e.Kind == "notify" && e.Method == "turn/started" && json.Unmarshal(e.Body, &p) == nil && p.TurnID == turn {
			at = i
		}
	}
	if at < 0 {
		return len(r.events)
	}
	for i := at + 1; i < len(r.events); i++ {
		if kind := r.events[i].Kind; kind == "start" || kind == "continue" {
			return i
		}
	}
	return len(r.events)
}

// inherited：这一轮是不是分叉时从原对话带过来的（fork 事件之前的 turn/started）。调用方持有 r.mu。
func (r *run) inherited(turn string) bool {
	fork := -1
	for i, e := range r.events {
		if e.Kind == "fork" {
			fork = i
		}
	}
	for i := 0; i < fork; i++ {
		var p struct {
			TurnID string `json:"turn_id"`
		}
		if e := r.events[i]; e.Kind == "notify" && e.Method == "turn/started" && json.Unmarshal(e.Body, &p) == nil && p.TurnID == turn {
			return true
		}
	}
	return false
}

type resumeInput struct {
	Messages  []json.RawMessage `json:"messages"`
	Usage     json.RawMessage   `json:"usage,omitempty"`
	WithTools bool              `json:"with_tools"`
	Model     string            `json:"-"`
	Effort    string            `json:"-"`
	RAG       bool              `json:"-"`
	Embedding string            `json:"-"`
	Continues string            `json:"-"` // 上一轮的检查点ID：本轮用新ID并记下续接关系，子任务可以按 task_id 引用之前各轮的子任务
}

// 调用方持有r.mu。请求里的View已经包含摘要，直接延续它，不重新拼接所有旧请求。
// 上一轮中途停下（停止按钮、超时、出错）时，改从 agent 的检查点接着聊；
// 撤销之后同样走检查点：接着的是撤销点的上一轮，它的检查点就是那时模型看到的上下文，和恢复后的文件同一时刻。
func (r *run) resumeHistory() (*resumeInput, error) {
	if id := r.lastCheckpoint(); id != "" && r.events[len(r.events)-1].Text != "exit 0" {
		result, err := r.checkpointHistory(id)
		if err != nil {
			return nil, err
		}
		result.Continues = id
		return r.withEmbedding(result), nil
	}
	start, end := 0, len(r.events)
	for i, e := range r.events {
		if e.Kind == "continue" {
			start = i + 1
		}
	}
	var request struct {
		Messages []json.RawMessage `json:"messages"`
		Tools    []json.RawMessage `json:"tools"`
		Model    string            `json:"model"`
		Effort   string            `json:"reasoning_effort"`
	}
	call, compact := 0, false
	var result *resumeInput
	bodies := requestBodies(r.events) // 存档里的请求可能是增量，先还原成整份
	for i, e := range r.events[start:end] {
		// 记忆背景生成、重排与独立验证是辅助调用：它们的输入不是对话，输出也不是 assistant 的回答，续聊不能从它们恢复。
		// 子 agent 的请求属于另一个上下文，同样跳过。
		if auxPurpose(e.Purpose) || e.Sub {
			continue
		}
		if e.Kind == "request" {
			request.Messages, request.Tools = nil, nil
			if json.Unmarshal(bodies[start+i], &request) != nil || len(request.Messages) == 0 {
				return nil, errors.New("存档缺少有效的模型请求")
			}
			var last struct{ Content string }
			json.Unmarshal(request.Messages[len(request.Messages)-1], &last)
			call, compact = e.Call, e.Purpose == "compact" || strings.HasPrefix(last.Content, "[上下文压缩请求]")
			if !compact {
				result = nil
			}
		}
		if e.Kind != "response" || e.Call != call || compact || e.Status != http.StatusOK {
			continue
		}
		var response struct {
			Usage   json.RawMessage `json:"usage"`
			Choices []struct {
				Message      json.RawMessage `json:"message"`
				FinishReason string          `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal(e.Body, &response) != nil || len(response.Choices) == 0 || response.Choices[0].FinishReason != "stop" {
			continue
		}
		choice := response.Choices[0]
		var reply struct {
			Role, Content string
			ToolCalls     []json.RawMessage `json:"tool_calls"`
		}
		if json.Unmarshal(choice.Message, &reply) != nil || reply.Role != "assistant" || strings.TrimSpace(reply.Content) == "" || len(reply.ToolCalls) > 0 {
			continue
		}
		messages := append(append([]json.RawMessage(nil), request.Messages...), choice.Message)
		result = &resumeInput{Messages: messages, Usage: response.Usage, WithTools: len(request.Tools) > 0, Model: request.Model, Effort: request.Effort}
		for _, rawTool := range request.Tools {
			var tool struct{ Function struct{ Name string } }
			if json.Unmarshal(rawTool, &tool) == nil && tool.Function.Name == "search_docs" {
				result.RAG = true
			}
		}
	}
	if result == nil {
		return nil, errors.New("存档没有完整的回答，无法恢复续聊上下文")
	}
	result.Continues = r.lastCheckpoint()
	return r.withEmbedding(result), nil
}

// 中途停下的对话从 agent 的检查点接着聊：检查点里有被打断那一轮的完整记录，
// 包括写成“未执行”“结果未知”的工具结果；存档里只能找到最后一个完整回答之前的请求。
func (r *run) checkpointHistory(id string) (*resumeInput, error) {
	data, err := os.ReadFile(filepath.Join(tenant.Path("", "checkpoints"), id+".json"))
	if err != nil {
		return nil, errors.New("找不到这次运行的检查点，无法接着聊：" + id)
	}
	var cp struct {
		Version  int               `json:"version"`
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(data, &cp) != nil || len(cp.Messages) == 0 {
		return nil, errors.New("检查点无效：" + id)
	}
	if err := agent.CheckVersion(id, cp.Version); err != nil {
		return nil, err
	}
	result := &resumeInput{Messages: cp.Messages, WithTools: true}
	// 模型、推理强度与是否检索沿用这条对话最近一次主任务请求。
	bodies := requestBodies(r.events)
	for i := len(r.events) - 1; i >= 0; i-- {
		e := r.events[i]
		if e.Kind != "request" || e.Sub || auxPurpose(e.Purpose) {
			continue
		}
		var request struct {
			Tools  []struct{ Function struct{ Name string } } `json:"tools"`
			Model  string                                     `json:"model"`
			Effort string                                     `json:"reasoning_effort"`
		}
		if json.Unmarshal(bodies[i], &request) == nil {
			result.Model, result.Effort = request.Model, request.Effort
			for _, tool := range request.Tools {
				result.RAG = result.RAG || tool.Function.Name == "search_docs"
			}
		}
		break
	}
	return result, nil
}

// 最近一轮 agent 的检查点ID：来自它的 turn/started 通知（子 agent 的通知包在 subagent/message 里，不会混进来）。
// 之前的存档没有通知，退回到当时打印的 “Checkpoint: id=…” 行。
// 最近一次是撤销时，返回撤销后对话接着的那一轮（空表示连第一轮也撤销了）；
// 最近一次是分叉时接着往前找，找到的是分叉点那一轮——分支的第一轮从它的检查点接着。
func (r *run) lastCheckpoint() string {
	for i := len(r.events) - 1; i >= 0; i-- {
		e := r.events[i]
		if e.Kind == "start" || e.Kind == "continue" {
			return ""
		}
		if e.Kind == "fork" {
			continue
		}
		if e.Kind == "rewind" {
			var p struct {
				Continues string `json:"continues"`
			}
			json.Unmarshal(e.Body, &p)
			return p.Continues
		}
		if e.Kind == "notify" && e.Method == "turn/started" {
			var p struct {
				TurnID string `json:"turn_id"`
			}
			if json.Unmarshal(e.Body, &p) == nil && p.TurnID != "" {
				return p.TurnID
			}
		}
		if m := legacyCheckpointLine.FindStringSubmatch(e.Text); e.Kind == "stdout" && m != nil {
			return m[1]
		}
	}
	return ""
}

var legacyCheckpointLine = regexp.MustCompile(`^Checkpoint: id=([A-Za-z0-9._-]{1,128}) `)

// notes 是上一个 start 或 continue 之后的分叉、撤销：下一轮要在问题后面告诉模型，
// 它看到的记录和磁盘上的文件对不上。不另存，按事件算——事件是唯一的事实来源，重启后算出来的一样。
// 第几轮按 turnIDs 里的位置算，文件清单用“、”连起来。调用方持有 r.mu。
func (r *run) notes() []string {
	turns := r.turnIDs()
	from := 0
	for i := len(r.events) - 1; i >= 0; i-- {
		if kind := r.events[i].Kind; kind == "start" || kind == "continue" {
			from = i + 1
			break
		}
	}
	var notes []string
	for _, e := range r.events[from:] {
		switch e.Kind {
		case "fork":
			var p struct {
				Turn       string   `json:"turn"`
				After      int      `json:"after"`
				Files      []string `json:"files"`
				FilesError string   `json:"files_error"`
			}
			if json.Unmarshal(e.Body, &p) != nil {
				continue
			}
			n := slices.Index(turns, p.Turn) + 1
			switch {
			case len(p.Files) > 0:
				notes = append(notes, fmt.Sprintf("对话接在第 %d 轮之后另起了一支，但之后各轮对这些文件的改动仍在磁盘上，没有撤销：%s。改之前先读当前内容。", n, strings.Join(p.Files, "、")))
			case p.After > 0 && p.FilesError != "":
				notes = append(notes, fmt.Sprintf("对话接在第 %d 轮之后另起了一支，原对话之后又跑了 %d 轮，磁盘上的文件可能已经被改过。改之前先读当前内容。", n, p.After))
			}
		case "rewind":
			var p struct {
				Mode          string   `json:"mode"`
				Restore       []string `json:"restore"`
				Delete        []string `json:"delete"`
				Unsnapshotted []string `json:"unsnapshotted"`
			}
			if json.Unmarshal(e.Body, &p) != nil {
				continue
			}
			n := slices.Index(turns, e.Text) + 1
			switch p.Mode {
			case "chat":
				if changed := slices.Concat(p.Restore, p.Delete, p.Unsnapshotted); len(changed) > 0 {
					notes = append(notes, fmt.Sprintf("对话退回到了第 %d 轮之前，但之后各轮对这些文件的改动仍在磁盘上，没有撤销：%s。改之前先读当前内容。", n, strings.Join(changed, "、")))
				}
			case "files":
				if changed := slices.Concat(p.Restore, p.Delete); len(changed) > 0 {
					notes = append(notes, fmt.Sprintf("用户撤销了第 %d 轮及之后对这些文件的改动，磁盘上已恢复到那一轮之前：%s。对话没有退回：上面记录里对这些文件的修改已经不在磁盘上，改之前先重新读。", n, strings.Join(changed, "、")))
				}
			}
		}
	}
	return notes
}

func (r *run) withEmbedding(result *resumeInput) *resumeInput {
	result.Embedding = "ark"
	// 新存档明确记录选择；旧存档从实际检索结果恢复backend，无法确定时沿用默认ark。
	for i := len(r.events) - 1; i >= 0; i-- {
		e := r.events[i]
		if e.Embedding == "ark" || e.Embedding == "local" {
			result.Embedding = e.Embedding
			return result
		}
	}
	for _, raw := range result.Messages {
		var m struct{ Role, Content string }
		if json.Unmarshal(raw, &m) != nil || m.Role != "tool" {
			continue
		}
		var observation struct {
			Tool   string
			Result struct{ Backend string }
		}
		if json.Unmarshal([]byte(m.Content), &observation) == nil && observation.Tool == "search_docs" && (observation.Result.Backend == "ark" || observation.Result.Backend == "local") {
			result.Embedding = observation.Result.Backend
		}
	}
	return result
}

// 调用方持有r.mu。正常结束的对话从最后一个完整回答接着聊；
// 中途停下的对话（停止按钮、超时、出错）只要 agent 写过检查点，就从检查点接着聊。
func (r *run) canContinue() bool {
	// 人的网络审批（allow/deny）、撤销（rewind）与分叉（fork）可能追加在 exit 之后，不算对话进程的事件。
	i, rewound := len(r.events)-1, false
	for i >= 0 && (r.events[i].Kind == "allow" || r.events[i].Kind == "deny" || r.events[i].Kind == "rewind" || r.events[i].Kind == "fork") {
		rewound = rewound || r.events[i].Kind == "rewind"
		i--
	}
	if r.Source != "" || !r.Done || i < 0 || r.events[i].Kind != "exit" {
		return false
	}
	// 撤销之后从撤销点的上一轮接着聊；连第一轮也撤销了，就没有可以接着的对话。
	if rewound {
		return r.lastCheckpoint() != ""
	}
	if r.events[i].Text == "exit 0" {
		return r.calls > 0
	}
	return r.lastCheckpoint() != ""
}

// 启动时读回 .data/runs/*.jsonl，重启后仍能查看、回放和对比历史运行。
// 早期版本各租户的存档在 .data/tenants/<租户>/runs，这一版只读 .data/runs，租户目录原样留着不动。
func (s *Server) loadRuns() {
	paths, _ := filepath.Glob(filepath.Join(s.runsDir(), "*.jsonl"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		r := &run{s: s, ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Done: true, changed: make(chan struct{})}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var e event
			if json.Unmarshal([]byte(line), &e) != nil {
				continue
			}
			r.events = append(r.events, e)
			r.calls, r.seq = max(r.calls, e.Call), max(r.seq, e.Seq)
			if e.Kind == "start" {
				r.Query, r.Eval, r.Source = e.Text, e.Eval, e.Source
			}
			if e.Kind == "fork" {
				var p struct {
					From string `json:"from"`
				}
				if json.Unmarshal(e.Body, &p) == nil {
					r.ForkOf = p.From
				}
			}
			if e.Kind == "request" && r.Query == "" {
				r.Query = firstUserMessage(e.Body)
			}
		}
		s.runs = append(s.runs, r)
		s.adopt(r)
	}
}

// 早期存档没有 start 事件，从第一次请求的 user 消息取问题。
func firstUserMessage(body json.RawMessage) string {
	var request struct {
		Messages []struct{ Role, Content string }
	}
	json.Unmarshal(body, &request)
	for _, m := range request.Messages {
		if m.Role == "user" {
			return m.Content
		}
	}
	return ""
}

func (r *run) summary() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := map[string]any{"id": r.ID, "query": r.Query, "done": r.Done, "can_continue": r.canContinue(), "eval": r.Eval, "source": r.Source, "fork_of": r.ForkOf}
	// 时间给 Unix 毫秒时间戳，由界面按看的人所在的时区显示。
	if len(r.events) > 0 {
		summary["created_at"], summary["updated_at"] = r.events[0].Time.UnixMilli(), r.events[len(r.events)-1].Time.UnixMilli()
	}
	return summary
}

// 最后一次活动的时间，列表按它排序。对话 ID 是随机的 UUID（早期存档是本地时间字符串），不表示先后。
func (r *run) updated() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return time.Time{}
	}
	return r.events[len(r.events)-1].Time
}

// llmHandler 是 llm.sock 上的模型代理，路由同原来的 POST /llm/<对话ID>：调用方是 agent 进程，凭进程令牌确认身份。
func (s *Server) llmHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /llm/{id}", s.proxy)
	return mux
}

// 转发请求与响应原文；Authorization 只转发，不记录。
func (s *Server) proxy(w http.ResponseWriter, req *http.Request) {
	r := s.find(req.PathValue("id"))
	if r == nil {
		http.Error(w, "未知运行", http.StatusNotFound)
		return
	}
	// 模型代理不走鉴权：调用方是 agent 进程。凭启动时发给它的令牌确认是这条对话的 agent（子 agent 继承同一个环境变量）。
	r.mu.Lock()
	token := r.token
	r.mu.Unlock()
	if token == "" || subtle.ConstantTimeCompare([]byte(req.Header.Get("X-Agent-Token")), []byte(token)) != 1 {
		http.Error(w, "代理令牌无效", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 用途与任务ID由 agent 在请求头明确标记，只记录、不转发给上游；旧版本 agent 没有这两个头。
	purpose := req.Header.Get("X-Agent-Purpose")
	if !purposePattern.MatchString(purpose) {
		purpose = ""
	}
	task := req.Header.Get("X-Agent-Task")
	if !taskPattern.MatchString(task) {
		task = ""
	}
	r.mu.Lock()
	r.calls++
	call := r.calls
	if r.rootTask == "" {
		r.rootTask = task
	}
	sub := task != "" && task != r.rootTask
	r.mu.Unlock()
	// traceparent 是 00-<trace_id>-<span_id>-<flags>；只记 span_id，界面据此把调用链里的 chat span 和这条原始请求对上。
	spanID := ""
	if parts := strings.Split(req.Header.Get("traceparent"), "-"); len(parts) == 4 && spanPattern.MatchString(parts[2]) {
		spanID = parts[2]
	}
	r.add(event{Kind: "request", Call: call, Body: raw(r.compactRequest(task, purpose, s.archiveImages(body))), Purpose: purpose, Task: task, Sub: sub, Span: spanID})

	start := time.Now()
	// 模型路由在 agent 里：它在 X-Agent-Upstream 里声明这次请求的真实地址（方舟、DeepSeek 或 Coconut），代理照此转发。
	// 只接受 https；明文 http 只给不带密钥的请求（本机的 Coconut 不要密钥），密钥不经明文发出。
	key := req.Header.Get("Authorization")
	target, err := url.Parse(req.Header.Get("X-Agent-Upstream"))
	if err == nil && ((target.Scheme != "https" && (target.Scheme != "http" || key != "")) || target.Host == "" || target.User != nil) {
		err = errors.New("X-Agent-Upstream 需要是 https 地址（不带密钥的请求可以是 http）")
	}
	// 上游说 Responses、Messages 协议时，转发前把 chat 形式的请求体换过去；存档留的仍是 chat 形式，界面、续聊和归因不用分协议。
	forward := body
	if err == nil {
		forward, err = llm.ConvertBody(req.Header.Get("X-Agent-Protocol"), body)
	}
	if err != nil {
		r.add(event{Kind: "response", Call: call, Status: http.StatusBadRequest, Text: err.Error(), Purpose: purpose, Task: task, Sub: sub})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	upstream, err := http.NewRequestWithContext(req.Context(), http.MethodPost, target.String(), bytes.NewReader(forward))
	if err == nil {
		upstream.Header.Set("Content-Type", req.Header.Get("Content-Type"))
		if key != "" {
			upstream.Header.Set("Authorization", key)
		}
		var response *http.Response
		if response, err = http.DefaultClient.Do(upstream); err == nil {
			defer response.Body.Close()
			if response.StatusCode == http.StatusOK && strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
				s.relayStream(w, req, r, response, event{Kind: "response", Call: call, Status: response.StatusCode, Purpose: purpose, Task: task, Sub: sub}, start)
				return
			}
			data, readErr := io.ReadAll(response.Body)
			r.add(event{Kind: "response", Call: call, Status: response.StatusCode, Millis: time.Since(start).Milliseconds(), Body: raw(data), Purpose: purpose, Task: task, Sub: sub})
			if readErr == nil {
				w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
				w.WriteHeader(response.StatusCode)
				w.Write(data)
				return
			}
			err = readErr
		}
	}
	// agent 取消了请求（停止按钮、超时）时记为 499（客户端关闭请求），与上游故障的 502 区分开。
	status := http.StatusBadGateway
	if req.Context().Err() != nil {
		status = 499
	}
	r.add(event{Kind: "response", Call: call, Status: status, Millis: time.Since(start).Milliseconds(), Text: err.Error(), Purpose: purpose, Task: task, Sub: sub})
	http.Error(w, err.Error(), http.StatusBadGateway)
}

// 流式响应边收边转发，每行原样写给 agent 并立刻 flush；整段缓冲再转发，agent 就看不到流式了。
// 转发的同时用 agent 的同一份拼接逻辑（llm.Stream）还原完整响应：存档仍是 choices[0].message 的结构，
// 界面、续聊恢复和归因不必区分流式与非流式。逐个 chunk 的原文不留档，只记 chunk 数与首字延迟。
func (s *Server) relayStream(w http.ResponseWriter, req *http.Request, r *run, response *http.Response, e event, start time.Time) {
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(response.StatusCode)
	flusher, _ := w.(http.Flusher)
	var stream llm.Stream
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	var err error
	for scanner.Scan() {
		line := scanner.Text()
		if _, err = io.WriteString(w, line+"\n"); err != nil {
			break // agent 断开了（停止按钮、超时）
		}
		if flusher != nil {
			flusher.Flush()
		}
		// 解析失败不影响转发：判断这次回复能不能用是 agent 的事，存档只是尽量还原。
		if content, reasoning, _ := stream.Add(line); (content != "" || reasoning != "") && e.FirstToken == 0 {
			e.FirstToken = time.Since(start).Milliseconds()
		}
	}
	if err == nil {
		err = scanner.Err()
	}
	e.Millis, e.Chunks = time.Since(start).Milliseconds(), stream.Chunks
	e.Body, _ = json.Marshal(stream.Response())
	if err != nil {
		// 流在中途断了：已转发的部分 agent 收到了，这里如实记下断开原因，499 同样表示 agent 先取消。
		e.Text = err.Error()
		if req.Context().Err() != nil {
			e.Status = 499
		}
	}
	r.add(e)
}

func raw(data []byte) json.RawMessage {
	if json.Valid(data) {
		return data
	}
	text, _ := json.Marshal(string(data))
	return text
}

// 事件写入内存供界面实时读取，同时追加到 runs/<id>.jsonl 留档。
func (r *run) add(e event) {
	r.mu.Lock()
	r.seq++
	e.Seq, e.Time = r.seq, time.Now().UTC() // 统一存 UTC：换时区启动 app-server、或别人打开界面，时间都不会错
	r.events = append(r.events, e)
	if line, err := json.Marshal(e); err == nil && e.Kind != "delta" {
		r.file.Write(append(line, '\n'))
	}
	if e.Kind == "exit" {
		r.Done = true
		r.file.Close()
	}
	close(r.changed)
	r.changed = make(chan struct{})
	r.mu.Unlock()
	// 一轮开始、结束时各告诉所有连接一次，界面据此刷新列表；通知走各连接自己的队列，慢连接不卡这里。
	switch e.Kind {
	case "start", "continue":
		r.s.status(r.ID, "active")
	case "exit":
		r.s.status(r.ID, "idle")
	}
}

// status 告诉所有连接这条对话在跑（agent 在跑）还是闲下来了。
func (s *Server) status(threadID, status string) {
	s.broadcast("thread/status/changed", map[string]string{"threadId": threadID, "status": status})
}

// addRun 把新对话放进列表，并告诉所有连接列表变了（各连接分别发，不等这条对话往下走）。
func (s *Server) addRun(r *run) {
	r.s = s
	s.mu.Lock()
	s.runs = append(s.runs, r)
	s.mu.Unlock()
	s.broadcast("thread/started", r.summary())
}

// turn/interrupt：第一次向 agent 进程组发 SIGINT，agent 回填结果、写好检查点再退出（优雅停止）；
// 第二次发 SIGKILL 强制结束。子 agent 在各自的进程组里，由父 agent 收到 SIGINT 后转发。
func (c *conn) turnInterrupt(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.mu.Lock()
	proc, force := r.proc, r.stopping
	r.stopping = true
	r.mu.Unlock()
	if proc == nil {
		return nil, bad(409, "这条对话没有在运行")
	}
	if force {
		if err := syscall.Kill(-proc.Pid, syscall.SIGKILL); err != nil {
			return nil, bad(500, err.Error())
		}
		r.add(event{Kind: "stop", Text: "强制结束：向 agent 发送 SIGKILL"})
		return nil, nil
	}
	// 第一次：经协议发 turn/interrupt，agent 回填结果、写好检查点再退出（第一段）；协议不通时退回 SIGINT。
	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()
	text := "已请求停止：经协议发送 turn/interrupt，等待 agent 回填结果、写好检查点后退出"
	ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
	defer cancel()
	if conn == nil || conn.Call(ctx, "turn/interrupt", nil, nil) != nil {
		if err := syscall.Kill(-proc.Pid, syscall.SIGINT); err != nil {
			return nil, bad(500, err.Error())
		}
		text = "已请求停止：向 agent 发送 SIGINT，等待它回填结果、写好检查点后退出"
	}
	r.add(event{Kind: "stop", Text: text})
	return nil, nil
}

// 沙箱网络白名单由人审批。请求来自 agent 的 request_network_access（模型发起）或沙箱代理（命令连到不在白名单的域名）；
// 人在界面上点“允许 / 拒绝”，这里记一条 allow/deny 事件（存进对话记录），下一轮续聊时作为 -net-allow 传给 agent。
// “始终允许”另外把域名加进这个账号的个人设置（net_allow），之后新开的对话都带上（同 Claude Code 的 “Yes, and don't ask again”）。
// 模型自己无法调用这个方法：它在断网的沙箱里，也没有访问 app-server 的工具。
var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func (s *Server) decideNetwork(ctx context.Context, r *run, name, by, domain string, allow, always bool) (any, *protocol.Error) {
	if len(domain) > 253 || !domainPattern.MatchString(domain) {
		return nil, bad(400, "domain 需要是小写域名")
	}
	kind := "deny"
	if allow {
		kind = "allow"
	}
	r.mu.Lock()
	if r.Done {
		// 已结束的对话，记录文件已经关了：临时以追加方式打开，写完再关，保证重启后审批仍在。
		file, err := os.OpenFile(filepath.Join(s.runsDir(), r.ID+".jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			r.mu.Unlock()
			return nil, bad(500, "无法写入对话记录")
		}
		r.file = file
		defer file.Close()
	}
	r.mu.Unlock()
	alwaysOn := allow && always
	detail := by
	if alwaysOn {
		detail += "；始终允许：写入个人设置"
	}
	// 联网批准同样先写审计；之后的续聊会把它作为 -net-allow 传给 agent，agent 那边再记一条。
	if err := audit.Write(ctx, audit.Record{Actor: "user", User: name, Event: "user_network_decision", Task: r.rootTask, Conversation: r.ID, Target: domain, Risk: "network", Decision: kind, Detail: detail}); err != nil {
		return nil, bad(500, "审计日志写入失败："+err.Error())
	}
	var body json.RawMessage
	if alwaysOn {
		if err := s.alwaysAllow(name, domain); err != nil {
			return nil, bad(500, "无法写入个人设置："+err.Error())
		}
		body = json.RawMessage(`{"always":true}`)
	}
	r.add(event{Kind: kind, Text: domain, Body: body})
	// 如果 agent 正在等这个域名的审批，当场回复；它会在本轮接着执行。否则只记下来，下一轮续聊时作为 -net-allow 传入。
	r.mu.Lock()
	conn, waiting := r.conn, r.approvals[domain]
	delete(r.approvals, domain)
	r.mu.Unlock()
	for _, id := range waiting {
		if conn != nil {
			conn.Reply(id, map[string]string{"decision": kind}, nil)
		}
	}
	return map[string]any{"net_allow": r.netAllow(), "answered": len(waiting)}, nil
}

// 高风险操作确认。agent 发来 tool/requestApproval 后暂停；人在界面上点“允许 / 拒绝”，这里当场回复。
// 只对正在等待的调用有效：下一项操作要重新确认。外发类的请求带范围时，人可以选“本对话内都允许”（allow_session），
// 由 agent 记下范围，之后同一范围不再询问。决定记进对话记录，界面重开后仍能看到。
func (s *Server) decideTool(ctx context.Context, r *run, name, by, callID string, allow, session bool) (any, *protocol.Error) {
	if callID == "" || len(callID) > 256 {
		return nil, bad(400, "需要 call_id")
	}
	r.mu.Lock()
	conn := r.conn
	wait, waiting := r.toolWaits[callID]
	delete(r.toolWaits, callID)
	r.mu.Unlock()
	if !waiting || conn == nil {
		return nil, bad(409, "这项操作已不在等待确认（可能已超时或 agent 已结束）")
	}
	decision, detail := "deny", by
	if allow {
		decision = "allow"
	}
	if allow && session {
		if wait.Risk != "outbound" || wait.Scope == "" {
			r.mu.Lock()
			r.toolWaits[callID] = wait
			r.mu.Unlock()
			return nil, bad(400, "这项操作只能单次允许")
		}
		decision, detail = "allow_session", by+"；本对话内都允许："+wait.Scope
	}
	// app-server 记下“人点了什么”，与 agent 那边的 approval_decision 互相对照；参数哈希应与 agent 的请求记录一致。
	// 先写审计再回复：审计写不进去，就不把“允许”交给 agent。
	record := audit.Record{Actor: "user", User: name, Event: "user_decision", Task: wait.Task, Conversation: r.ID, CallID: callID, Tool: wait.Tool, Risk: wait.Risk, Decision: decision, Detail: detail}
	record.Args(wait.Arguments)
	if err := audit.Write(ctx, record); err != nil {
		r.mu.Lock()
		r.toolWaits[callID] = wait
		r.mu.Unlock()
		return nil, bad(500, "审计日志写入失败，没有回复 agent："+err.Error())
	}
	r.add(event{Kind: "decision", Text: callID, Body: json.RawMessage(`{"decision":"` + decision + `"}`)})
	conn.Reply(wait.rpc, map[string]string{"decision": decision}, nil)
	return map[string]string{"decision": decision}, nil
}

// 回答模型的提问（ask_user）。回答是人在对话里说的话，和续聊的问题一样记进存档，不写审计（不是授权）。
func (s *Server) answerQuestion(r *run, callID, answer string) (any, *protocol.Error) {
	if callID == "" || strings.TrimSpace(answer) == "" {
		return nil, bad(400, "需要 call_id 和非空的 answer")
	}
	r.mu.Lock()
	conn := r.conn
	rpc, waiting := r.asks[callID]
	delete(r.asks, callID)
	r.mu.Unlock()
	if !waiting || conn == nil {
		return nil, bad(409, "agent 没有在等这个问题的回答（已经回答过、已超时或这一轮已结束）")
	}
	body, _ := json.Marshal(map[string]string{"answer": strings.TrimSpace(answer)})
	r.add(event{Kind: "answer", Text: callID, Body: body})
	conn.Reply(rpc, json.RawMessage(body), nil)
	return nil, nil
}

// approval/respond：界面上的“允许 / 拒绝”“始终允许”“本对话内都允许”与提问的回答，按 kind 分给原来三个接口。
func (c *conn) approvalRespond(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Kind     string `json:"kind"`
		User     string `json:"user"`
		CallID   string `json:"call_id"`
		Allow    bool   `json:"allow"`
		Always   bool   `json:"always"`
		Session  bool   `json:"session"`
		Domain   string `json:"domain"`
		Answer   string `json:"answer"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	switch input.Kind {
	case "tool":
		return c.s.decideTool(c.ctx, r, input.User, c.clientName(), input.CallID, input.Allow, input.Session)
	case "network":
		return c.s.decideNetwork(c.ctx, r, input.User, c.clientName(), input.Domain, input.Allow, input.Always)
	case "question":
		return c.s.answerQuestion(r, input.CallID, input.Answer)
	}
	return nil, bad(400, "kind 取 tool、network 或 question")
}

// turn/steer：运行中插话。agent 把它排进下一次模型请求；这一轮已经结束时返回 409，界面提示人在结束后照常发送。
// 插话真正送进模型时 agent 会发 userMessage（steer=true）的通知，存档里以那条为准。
func (c *conn) turnSteer(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Text     string `json:"text"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	if strings.TrimSpace(input.Text) == "" {
		return nil, bad(400, "需要非空的 text")
	}
	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()
	if conn == nil {
		return nil, bad(409, "这条对话没有在运行")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	if err := conn.Call(ctx, "turn/steer", map[string]string{"text": strings.TrimSpace(input.Text)}, nil); err != nil {
		return nil, bad(409, "没有送到：这一轮已经结束（"+err.Error()+"），结束后照常发送即可")
	}
	return nil, nil
}

// audit/list：审计页只读。每次都从头校验整条哈希链，返回最近的 500 条（可按任务过滤）。
func (c *conn) auditList(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Task string `json:"task"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	result, err := audit.Read("", strings.TrimSpace(input.Task))
	if err != nil {
		return nil, bad(500, err.Error())
	}
	if n := len(result.Records); n > 500 {
		result.Records = result.Records[n-500:]
	}
	return result, nil
}

// 当前允许的域名：从开始时设置里的白名单出发，按时间顺序处理 allow/deny，后一次决定覆盖前一次。
func (r *run) netAllow() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := []string{}
	for _, e := range r.events {
		if e.Kind == "start" {
			list = append(list, e.NetAllow...)
		}
		if e.Kind == "allow" && !slices.Contains(list, e.Text) {
			list = append(list, e.Text)
		}
		if e.Kind == "deny" {
			list = slices.DeleteFunc(list, func(d string) bool { return d == e.Text })
		}
	}
	return list
}

// thread/list：从旧到新（界面再倒过来显示）：最近有活动的对话在最上面，续聊过的旧对话也会回到顶部。
func (c *conn) threadList(protocol.Message) (any, *protocol.Error) {
	s := c.s
	s.mu.Lock()
	runs := append([]*run(nil), s.runs...)
	s.mu.Unlock()
	slices.SortStableFunc(runs, func(a, b *run) int { return a.updated().Compare(b.updated()) })
	list := []map[string]any{}
	for _, r := range runs {
		list = append(list, r.summary())
	}
	return list, nil
}

func (s *Server) find(id string) *run {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.ID == id {
			return r
		}
	}
	return nil
}

var spanPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// trace/spans：读一个 trace 的全部 span（.data/traces/<trace_id>.jsonl，由各进程的 SDK 导出器写入）。
// 运行中也可以读：span 结束一个写一行，还没结束的 span（包括根 span）暂时不在文件里。
func (c *conn) traceSpans(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ID string `json:"id"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if !telemetry.ValidTraceID(input.ID) || !c.s.ownsTrace(input.ID) {
		return nil, bad(404, "找不到这条调用链")
	}
	spans, err := readSpans(filepath.Join(c.s.dir, telemetry.Path(input.ID)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, bad(500, err.Error())
	}
	return spans, nil
}

// 调用链文件是平台数据，但每个 trace 都由某一次对话产生：只给已有对话的 trace。
func (s *Server) ownsTrace(id string) bool {
	s.mu.Lock()
	runs := append([]*run(nil), s.runs...)
	s.mu.Unlock()
	for _, r := range runs {
		r.mu.Lock()
		found := slices.ContainsFunc(r.events, func(e event) bool { return e.Trace == id })
		r.mu.Unlock()
		if found {
			return true
		}
	}
	return false
}

func readSpans(path string) ([]telemetry.Record, error) {
	data, err := os.ReadFile(path)
	spans := []telemetry.Record{}
	for _, line := range strings.Split(string(data), "\n") {
		var record telemetry.Record
		if json.Unmarshal([]byte(line), &record) == nil && record.SpanID != "" {
			spans = append(spans, record)
		}
	}
	return spans, err
}

// trace/metrics：不另起一套采集，从已结束的 span 汇总。名字沿用 GenAI 约定的指标：
// gen_ai.client.operation.duration（模型、向量化）、gen_ai.client.token.usage、execute_tool 的耗时与失败率，以及任务结局。
// conversation 参数只统计这个对话的 trace；不给时统计全部。
func (c *conn) traceMetrics(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Conversation string `json:"conversation"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	conversation := input.Conversation
	paths, _ := filepath.Glob(filepath.Join(c.s.dir, telemetry.Dir, "*.jsonl"))
	type series struct {
		Name     string  `json:"name"`
		Count    int     `json:"count"`
		Errors   int     `json:"errors"`
		P50      float64 `json:"p50_ms"`
		P95      float64 `json:"p95_ms"`
		Input    int     `json:"input_tokens,omitempty"`
		Output   int     `json:"output_tokens,omitempty"`
		Cached   int     `json:"cached_tokens,omitempty"`
		duration []float64
	}
	groups := map[string]map[string]*series{"gen_ai.client.operation.duration": {}, "execute_tool.duration": {}, "agent.outcome": {}}
	traces := 0
	number := func(v any) int { f, _ := v.(float64); return int(f) }
	for _, path := range paths {
		spans, err := readSpans(path)
		if err != nil {
			continue
		}
		if conversation != "" && !slices.ContainsFunc(spans, func(r telemetry.Record) bool { return r.Attributes["gen_ai.conversation.id"] == conversation }) {
			continue
		}
		traces++
		for _, span := range spans {
			op, _ := span.Attributes["gen_ai.operation.name"].(string)
			group, name := "", ""
			switch op {
			case "chat", "embeddings":
				model, _ := span.Attributes["gen_ai.request.model"].(string)
				group, name = "gen_ai.client.operation.duration", op+" "+model
				if purpose, ok := span.Attributes["agent.purpose"].(string); ok {
					name += " · " + purpose
				}
			case "execute_tool":
				tool, _ := span.Attributes["gen_ai.tool.name"].(string)
				group, name = "execute_tool.duration", tool
			case "invoke_agent":
				outcome, _ := span.Attributes["agent.outcome"].(string)
				group, name = "agent.outcome", outcome
			}
			if group == "" {
				continue
			}
			item := groups[group][name]
			if item == nil {
				item = &series{Name: name}
				groups[group][name] = item
			}
			item.Count++
			if span.Status == "error" {
				item.Errors++
			}
			item.duration = append(item.duration, float64(span.End.Sub(span.Start).Microseconds())/1000)
			item.Input += number(span.Attributes["gen_ai.usage.input_tokens"])
			item.Output += number(span.Attributes["gen_ai.usage.output_tokens"])
			item.Cached += number(span.Attributes["gen_ai.usage.cache_read.input_tokens"])
		}
	}
	result := map[string]any{"traces": traces}
	for group, items := range groups {
		list := []*series{}
		for _, item := range items {
			slices.Sort(item.duration)
			// 最近秩法：第 ceil(p×n) 个值。样本少时 P95 就是最大值附近，读数要结合 count。
			rank := func(p float64) float64 { return item.duration[max(0, int(math.Ceil(p*float64(len(item.duration))))-1)] }
			item.P50, item.P95 = rank(0.5), rank(0.95)
			list = append(list, item)
		}
		slices.SortFunc(list, func(a, b *series) int { return b.Count - a.Count })
		result[group] = list
	}
	return result, nil
}

// agent 发来的协议消息。通知原样存成 notify 事件（界面按方法名还原过程）；
// 联网审批请求存成 approval 事件，并记下请求ID，等人在界面上点“允许 / 拒绝”时回复。
func (r *run) handle(m protocol.Message) {
	// 接管时补发的消息里，序号不超过 after 的已经存过：请求只重新登记等待，不再记一遍。
	r.mu.Lock()
	seen := m.Seq > 0 && m.Seq <= r.after
	r.mu.Unlock()
	if m.IsRequest() {
		// agent 退出前报告状态：按顺序处理到这里，说明之前的消息都已记下，答复后它才断开退出。
		if m.Method == "process/exit" {
			var p struct{ Status string }
			json.Unmarshal(m.Params, &p)
			r.mu.Lock()
			r.exitStatus = p.Status
			conn := r.conn
			r.mu.Unlock()
			if conn != nil {
				conn.Reply(*m.ID, nil, nil)
			}
			return
		}
		// 模型的提问（ask_user）：记下等待，界面上的人回答后经 approval/respond 答复。
		if m.Method == "user/requestInput" {
			var p struct {
				CallID string `json:"call_id"`
			}
			json.Unmarshal(m.Params, &p)
			r.mu.Lock()
			if r.asks == nil {
				r.asks = map[string]int64{}
			}
			r.asks[p.CallID] = *m.ID
			r.mu.Unlock()
			if !seen {
				r.add(event{Kind: "approval", Method: m.Method, RPC: *m.ID, Body: m.Params, ASeq: m.Seq})
			}
			return
		}
		if m.Method == "tool/requestApproval" {
			var params struct {
				CallID    string `json:"call_id"`
				Task      string `json:"task_id"` // 子 agent 的请求经父进程转发时带上
				Tool      string `json:"tool"`
				Risk      string `json:"risk"`
				Arguments string `json:"arguments"`
				Scope     string `json:"scope"`
			}
			json.Unmarshal(m.Params, &params)
			r.mu.Lock()
			if params.Task == "" {
				params.Task = r.rootTask
			}
			if r.toolWaits != nil {
				r.toolWaits[params.CallID] = toolWait{*m.ID, params.Task, params.Tool, params.Risk, params.Arguments, params.Scope}
			}
			r.mu.Unlock()
			if !seen {
				r.add(event{Kind: "approval", Method: m.Method, RPC: *m.ID, Body: m.Params, ASeq: m.Seq})
			}
			return
		}
		if m.Method != "network/requestApproval" {
			r.mu.Lock()
			conn := r.conn
			r.mu.Unlock()
			if conn != nil {
				conn.Reply(*m.ID, nil, &protocol.Error{Code: -32601, Message: "app-server 不支持 " + m.Method})
			}
			return
		}
		var params struct {
			Domain string `json:"domain"`
		}
		json.Unmarshal(m.Params, &params)
		r.mu.Lock()
		if r.approvals != nil {
			r.approvals[params.Domain] = append(r.approvals[params.Domain], *m.ID)
		}
		r.mu.Unlock()
		if !seen {
			r.add(event{Kind: "approval", Method: m.Method, RPC: *m.ID, Body: m.Params, ASeq: m.Seq})
		}
		return
	}
	if seen {
		return
	}
	switch m.Method {
	case "log": // 终端日志：和以前从 stderr 读到的一样记成 stdout
		var p struct{ Text string }
		json.Unmarshal(m.Params, &p)
		r.add(event{Kind: "stdout", Text: p.Text, ASeq: m.Seq})
		return
	}
	// item/delta 只给正在看的界面实时显示，不写进运行记录：一次回答会有成百上千条，完整内容已在 response 事件里。
	kind := "notify"
	if protocol.IsDelta(m) {
		kind = "delta"
	}
	r.add(event{Kind: kind, Method: m.Method, Body: m.Params, ASeq: m.Seq})
}
