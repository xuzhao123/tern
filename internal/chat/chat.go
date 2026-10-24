package chat

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/appclient"
	"github.com/xuzhao123/tern/internal/protocol"
	"github.com/xuzhao123/tern/internal/tenant"
)

// 终端客户端 tern chat：app-server 的一个界面。它不启动 agent，也不读本地检查点：
// 对话、agent 进程、模型请求、存档都在 app-server 里（经 SSH 时在远端），这里只发协议请求、
// 显示事件、回答 agent 发来的审批与提问。所以同一批对话网页端和 chat 都能看、都能接着聊。
//
// 一轮就是 thread/start（新对话）或 turn/start（续聊），然后订阅这一轮，读到 end 为止；
// 运行中输入的话是插话（turn/steer），Ctrl+C 是 turn/interrupt。退出 chat 不停对话。
// 工具与权限来自 app-server 的设置；除了下面这几个参数，别的 agent 参数不再从命令行传。

const help = `命令：
  /help                      这份说明
  /turns                     列出这条对话的各轮（从第一轮到最新）
  /mode [plan|ask|edit|full] 查看或切换之后各轮的权限模式
  /approve [补充]            批准计划：切到 edit，并按计划开始执行
  /compact                   下一条消息发出前先压缩上下文
  /fork <n>                  从第 n 轮之后另起一支（后面各轮留在原处，文件不动）
  /rewind <n> [both|files|chat]  撤销第 n 轮及之后：文件与对话一起退（默认）、只退文件、只退对话
  /resume <对话ID>            接着某条对话聊（网页端或其他终端开的那条也行）
  /new                       开始新对话
  /exit                      退出
运行中：直接输入的话会排进 agent 的下一次模型请求（插话）；Ctrl+C 停止这一轮，再按一次强制结束。
退出 chat 不停对话，对话在 app-server 里接着跑；工具与权限来自 app-server 的设置。
agent 的终端日志写在 `

// modes 是权限模式的取值，与 agent 的 -permission-mode 一致。
var modes = []string{"plan", "ask", "edit", "full"}

type session struct {
	provider  string  // 新对话的模型供应商（-provider）；空表示用设置里的默认值
	model     string  // 新对话的具体模型（-model）；空表示这家供应商的默认值
	workspace *string // 新对话的工作区名字（-workspace）；nil 表示用设置里的默认值
	mode      string  // 之后各轮的权限模式；空表示用设置里的默认值
	compact   bool    // 下一轮的 turn/start 带 compact：先压缩上下文
	remote    string  // -remote 的 ssh 别名；空表示连本机 app-server

	thread  string      // 当前对话的 ID；空表示下一句开新对话
	last    int64       // 这条对话里最后收到的 seq：续聊的 after
	events  []chatEvent // 这条对话的事件，按 seq 顺序；/turns 与 /rewind 从它算
	asks    []approval  // 等人回答的审批，按到达顺序一个一个问
	next    []string    // 一轮结束后要接着发的话（没用上的插话）
	resumed bool        // 刚接上一条已有对话：已有事件补齐后打印摘要
	logPath string
	log     *os.File
	lines   chan string
	conns   chan appclient.State // 连接状态的变化：断线、重连成功各一条，跟着这一轮的显示打出来
	eof     bool                 // 标准输入结束了：等正在跑的这一轮结束再退出
	sigs    chan os.Signal
	client  *appclient.Client
	sub     *appclient.Subscription // 正在跟的订阅；nil 表示这一侧没在看
	subSeq  int64                   // 订阅时已有的最后序号：不超过它的事件只收下、不显示
	running bool                    // 有一轮在 app-server 里跑（还没收到 end）
	stops   int                     // 这一轮按了几次 Ctrl+C
	v       *view
}

// chatEvent 是 app-server 推来的一个事件，字段见 appserver.event。
type chatEvent struct {
	Seq    int64           `json:"seq"`
	Kind   string          `json:"kind"`
	Method string          `json:"method"`
	Text   string          `json:"text"`
	Body   json.RawMessage `json:"body"`
	End    bool            `json:"-"` // 对话已结束：订阅的最后一推
}

// approval 是 agent 发来、等聊天这边回答的请求：Body 就是原来那条请求的参数。
type approval struct {
	method string
	body   json.RawMessage
}

// Run 解析 chat 自己的参数（-remote、-continue、-provider、-model、-permission-mode、-workspace），连上 app-server。
func Run(args []string) error {
	s := &session{lines: make(chan string), conns: make(chan appclient.State, 8), sigs: make(chan os.Signal, 2), v: &view{}, logPath: tenant.Here("chat.log")}
	continueID := ""
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		takes := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("-%s 需要一个值", name)
			}
			i++
			return args[i], nil
		}
		if !strings.HasPrefix(args[i], "-") {
			return fmt.Errorf("chat 不接受位置参数：%s（问题在启动后输入）", args[i])
		}
		var err error
		switch name {
		case "continue":
			continueID, err = takes()
		case "provider":
			s.provider, err = takes()
		case "model":
			s.model, err = takes()
		case "permission-mode":
			s.mode, err = takes()
			if err == nil && !slices.Contains(modes, s.mode) {
				return fmt.Errorf("权限模式取 plan、ask、edit 或 full")
			}
		case "workspace":
			var v string
			v, err = takes()
			s.workspace = &v
		case "remote":
			s.remote, err = takes()
		default:
			return fmt.Errorf("chat 不认识 -%s：工具与权限来自 app-server 的设置，agent 参数不再从命令行传", name)
		}
		if err != nil {
			return err
		}
	}
	// 对话归 app-server：不带 -remote 时先保证本机的在运行，再连上；带 -remote 时经 ssh 连远端。
	// 自己不起 agent，也不读本地检查点（经 SSH 时检查点在远端）。
	var client *appclient.Client
	var err error
	if s.remote != "" {
		client, _, err = appclient.DialRemote("tern chat", s.remote)
	} else {
		if _, err := appclient.Start(false); err != nil {
			return err
		}
		client, _, err = appclient.Dial("tern chat")
	}
	if err != nil {
		return err
	}
	defer client.Close()
	s.client = client
	// 断线重连由 appclient 负责：这里只把状态变化排进队列，跟着这一轮的显示打一行灰字。
	client.OnState(func(st appclient.State) {
		select {
		case s.conns <- st:
		default:
		}
	})
	s.openLog()
	if s.log != nil {
		defer s.log.Close()
	}
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			s.lines <- scanner.Text()
		}
		close(s.lines)
	}()
	signal.Notify(s.sigs, os.Interrupt)
	fmt.Println("tern 终端客户端。输入问题开始；/help 查看命令。")
	if continueID != "" {
		if err := s.resume(continueID); err != nil {
			return err
		}
		s.follow()
	}
	for {
		// 先把手上的话发完（接一条已结束的对话时，标准输入可能已经到了），再看标准输入是不是结束了。
		// 排着的行逐行按收到的顺序看：以 / 开头的按命令执行，连着的普通输入拼成一个问题——
		// 接上一条对话时补齐事件的那一会儿，标准输入里的命令也会排进这里，不能一起当成问题发给 agent。
		if len(s.next) > 0 {
			var lines []string
			for len(s.next) > 0 && !strings.HasPrefix(s.next[0], "/") {
				lines = append(lines, s.next[0])
				s.next = s.next[1:]
			}
			if len(lines) > 0 {
				question := strings.Join(lines, "\n")
				fmt.Printf("› %s\n", question)
				s.turn(question)
				continue
			}
			line := s.next[0]
			s.next = s.next[1:]
			if quit := s.command(line); quit {
				s.quit()
				return nil
			}
			continue
		}
		if s.eof {
			s.quit()
			return nil
		}
		fmt.Print("› ")
		select {
		case line, ok := <-s.lines:
			if !ok {
				s.eof = true
				continue
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "/") {
				if quit := s.command(line); quit {
					s.quit()
					return nil
				}
				continue
			}
			s.turn(line)
		case st := <-s.conns:
			s.showConn(st)
		case <-s.sigs:
			s.quit()
			return nil
		}
	}
}

// openLog 打开 agent 的终端日志：全新的 TERN_HOME 里还没有 .data/，先建目录再打开。
// 打不开只提示一句，不挡住对话。
func (s *session) openLog() {
	if err := os.MkdirAll(filepath.Dir(s.logPath), 0o700); err != nil {
		fmt.Println("建不了日志目录：", err)
		return
	}
	file, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Println("打不开日志文件：", err)
		return
	}
	s.log = file
}

// quit 退出 chat，不停对话：还有一轮在跑时提示怎么接着看。
func (s *session) quit() {
	fmt.Println()
	if s.running && s.thread != "" {
		fmt.Printf("还在跑，可以 tern chat -continue %s 或在网页端接着看。\n", s.thread)
	}
	if s.sub != nil {
		s.sub.Close()
		s.sub = nil
	}
}

// ---------- 会话命令 ----------

// readLine 读一行给命令自己用（撤销的“确认？[y/N]”）：先取接上对话时排着的输入，排完了再读标准输入。
// 标准输入可能整个已经到了、都在 next 里，只读标准输入会一直等到输入结束，把确认判成“没有撤销”。
func (s *session) readLine() (string, bool) {
	if len(s.next) > 0 {
		line := s.next[0]
		s.next = s.next[1:]
		return line, true
	}
	line, ok := <-s.lines
	return line, ok
}

func (s *session) command(line string) (quit bool) {
	fields := strings.Fields(line)
	switch fields[0] {
	case "/help":
		fmt.Println(help + s.logPath)
	case "/exit", "/quit":
		return true
	case "/new":
		s.newThread()
	case "/mode":
		if len(fields) == 1 {
			fmt.Println("权限模式：" + cmp.Or(s.mode, "edit（默认）"))
			break
		}
		if !slices.Contains(modes, fields[1]) {
			fmt.Println("权限模式取 plan、ask、edit 或 full")
			break
		}
		s.mode = fields[1]
		fmt.Println("之后各轮的权限模式：" + s.mode)
	case "/approve":
		if s.thread == "" {
			fmt.Println("还没有计划可以批准")
			break
		}
		// 计划模式的批准就是下一轮切到 edit；这句话是用户按下“批准”的意思，作为这一轮的问题交给模型。
		s.mode = "edit"
		question := "计划已批准，切到 edit 模式，按计划开始执行。"
		if extra := strings.TrimSpace(strings.TrimPrefix(line, "/approve")); extra != "" {
			question += "\n" + extra
		}
		fmt.Println("权限模式：edit")
		s.turn(question)
	case "/compact":
		if s.thread == "" {
			fmt.Println("新对话没有可压缩的上下文")
			break
		}
		s.compact = true
		fmt.Println("下一条消息发出前会先压缩上下文。")
	case "/turns":
		list := s.turns()
		if len(list) == 0 {
			fmt.Println("还没有轮次")
		}
		for i, r := range list {
			fmt.Printf("%2d. %-8s %-8s %s\n", i+1, cmp.Or(short(r.id), "-"), cmp.Or(r.outcome, "running"), clip(r.question, 70))
		}
	case "/resume":
		if len(fields) != 2 {
			fmt.Println("用法：/resume <对话ID>")
			break
		}
		if err := s.resume(fields[1]); err != nil {
			fmt.Println(err)
			break
		}
		s.follow()
	case "/rewind":
		s.rewind(fields)
	case "/fork":
		s.fork(fields)
	default:
		fmt.Println("不认识的命令；/help 查看")
	}
	return false
}

// newThread 让下一句开一条新对话；这一侧的事件、审批与插话都不再属于它。
func (s *session) newThread() {
	if s.sub != nil {
		s.sub.Close()
		s.sub = nil
	}
	s.thread, s.last, s.events, s.asks, s.next = "", 0, nil, nil, nil
	s.subSeq, s.compact, s.resumed, s.running = 0, false, false, false
	fmt.Println("开始新对话。")
}

// resume 接一条已有的对话：订阅它，之前的事件（序号不超过 thread/resume 回复里的 seq）只收下不显示，
// 用来算轮次、最后的 seq 和还没人答的审批；之后的事件照常显示（对话正在跑时就是接着看）。
// 调用方随后 follow。找不到这条对话时返回 app-server 的 404 文字。
//
// 这时还不知道有没有在跑（补齐的事件里最后一条是 exit 就是已经结束了），所以 running 先为 false：
// 补齐期间到的输入按下一轮的问题留下，不试插话；看到新事件才算在跑，之后的输入才是插话。
// 留下的输入（next）不动：那是还没发出去的话，接上哪条对话就在哪条里发；清掉的话，/fork、/resume
// 后面的输入就丢了（管道一次给全输入时最明显）。
func (s *session) resume(id string) error {
	sub, err := s.client.Subscribe(context.Background(), id, 0)
	if err != nil {
		return err
	}
	if s.sub != nil {
		s.sub.Close()
	}
	s.sub, s.subSeq = sub, sub.Seq()
	s.thread, s.last, s.events, s.asks = id, 0, nil, nil
	s.v, s.stops, s.resumed, s.running = &view{}, 0, true, false
	return nil
}

// rewind 撤销第 n 轮及之后：先预览（会恢复、删除哪些文件，有没有冲突），人确认后再真正撤销。
// mode 取 both（默认，文件与对话一起退）、files（只退文件）、chat（只退对话）；只退一边的提醒由 app-server 在下一轮生成。
func (s *session) rewind(fields []string) {
	list := s.turns()
	if len(fields) != 2 && len(fields) != 3 {
		fmt.Println("用法：/rewind <轮次> [both|files|chat]（/turns 查看编号）")
		return
	}
	mode := "both"
	if len(fields) == 3 {
		mode = fields[2]
	}
	if mode != "both" && mode != "files" && mode != "chat" {
		fmt.Println("mode 取 both、files 或 chat")
		return
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n < 1 || n > len(list) {
		fmt.Printf("轮次取 1 到 %d\n", len(list))
		return
	}
	turn := list[n-1].id
	if turn == "" {
		fmt.Println("这一轮还没有检查点ID（agent 还没开始），不能撤销到它之前")
		return
	}
	preview, err := s.rewindCall(turn, true, mode)
	if err != nil {
		fmt.Println(err)
		return
	}
	switch mode {
	case "chat":
		fmt.Printf("只退对话：退到第 %d 轮之前；%d 轮标成已撤销，文件不动。\n", n, len(preview.Turns))
	case "files":
		fmt.Printf("只退文件：恢复 %d 个、删除 %d 个；对话不退。\n", len(preview.Restore), len(preview.Delete))
	default:
		fmt.Printf("撤销第 %d 轮及之后共 %d 轮：恢复 %d 个文件、删除 %d 个，对话退到第 %d 轮之前。\n", n, len(preview.Turns), len(preview.Restore), len(preview.Delete), n)
	}
	// 只退对话不动文件，冲突不影响。
	if mode != "chat" && len(preview.Conflicts) > 0 {
		fmt.Println("这些文件在最后一次写入后又被改过，整次不做：" + strings.Join(preview.Conflicts, "、"))
		return
	}
	if len(preview.Unsnapshotted) > 0 {
		fmt.Println("bash 改过、没有快照、不会还原：" + strings.Join(preview.Unsnapshotted, "、"))
	}
	fmt.Print("确认？[y/N] ")
	if answer, ok := s.readLine(); !ok || strings.ToLower(strings.TrimSpace(answer)) != "y" {
		fmt.Println("没有撤销。")
		return
	}
	if _, err := s.rewindCall(turn, false, mode); err != nil {
		fmt.Println("撤销失败：", err)
		return
	}
	fmt.Println("已撤销。")
}

// fork 从第 n 轮之后另起一条对话：前面各轮带过去，原对话不变、文件不动；随后接上新对话（同 /resume）。
func (s *session) fork(fields []string) {
	list := s.turns()
	if len(fields) != 2 {
		fmt.Println("用法：/fork <轮次>（/turns 查看编号）")
		return
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n < 1 || n > len(list) {
		fmt.Printf("轮次取 1 到 %d\n", len(list))
		return
	}
	turn := list[n-1].id
	if turn == "" {
		fmt.Println("这一轮还没有检查点ID（agent 还没开始），不能从它分叉")
		return
	}
	from := s.thread
	var summary struct {
		ID string `json:"id"`
	}
	params := map[string]any{"threadId": from, "turn": turn}
	if err := s.client.Call(context.Background(), "thread/fork", params, &summary); err != nil {
		fmt.Println(err)
		return
	}
	// 接上新对话：同 /resume，先订阅它的事件，再跟着显示（分叉的对话已经结束，读完就收到 end）。
	if err := s.resume(summary.ID); err != nil {
		fmt.Println("新对话已经建好，但接不上：", err)
		return
	}
	fmt.Printf("已分叉：新对话 %s，接在第 %d 轮之后；原对话 %s 不变\n", summary.ID, n, from)
	s.follow()
}

// rewindResult 是 thread/rewind 的回复：agent 的 rewind 命令输出，与网页端拿到的一样。
type rewindResult struct {
	Turns         []string `json:"turns"`
	Restore       []string `json:"restore"`
	Delete        []string `json:"delete"`
	Conflicts     []string `json:"conflicts"`
	Unsnapshotted []string `json:"unsnapshotted"`
}

func (s *session) rewindCall(turn string, dryRun bool, mode string) (rewindResult, error) {
	var result rewindResult
	params := map[string]any{"threadId": s.thread, "turn": turn, "dry_run": dryRun, "mode": mode}
	err := s.client.Call(context.Background(), "thread/rewind", params, &result)
	return result, err
}

// round 是 /turns 列的一轮：问题是这一轮 start 或 continue 事件的文字，
// 检查点ID取这一轮 turn/started 通知的 turn_id，结局取这一轮的 exit。
type round struct {
	id, question, outcome string
}

// turns 从这条对话的事件算出各轮，从第一轮到最新。
func (s *session) turns() []round {
	var list []round
	for _, e := range s.events {
		switch {
		case e.Kind == "start" || e.Kind == "continue":
			list = append(list, round{question: e.Text})
		case e.Kind == "notify" && e.Method == "turn/started" && len(list) > 0:
			var p struct {
				TurnID string `json:"turn_id"`
			}
			if json.Unmarshal(e.Body, &p) == nil && p.TurnID != "" {
				list[len(list)-1].id = p.TurnID
			}
		case e.Kind == "exit" && len(list) > 0:
			list[len(list)-1].outcome = e.Text
		}
	}
	return list
}

// ---------- 一轮 ----------

// turn 发一轮：新对话 thread/start，续聊 turn/start；然后订阅这一轮，读到 end 为止。
func (s *session) turn(question string) {
	if s.sub != nil {
		s.sub.Close()
		s.sub = nil
	}
	s.v, s.stops, s.asks = &view{}, 0, nil
	if s.log != nil {
		fmt.Fprintf(s.log, "\n===== %s %s\n", time.Now().Format(time.RFC3339), clip(question, 80))
	}
	ctx := context.Background()
	params := map[string]any{"query": question}
	if s.mode != "" {
		params["permission"] = s.mode
	}
	if s.thread == "" {
		if s.provider != "" {
			params["provider"] = s.provider
		}
		if s.model != "" {
			params["model"] = s.model
		}
		if s.workspace != nil {
			params["workspace"] = *s.workspace
		}
		var summary struct {
			ID string `json:"id"`
		}
		if err := s.client.Call(ctx, "thread/start", params, &summary); err != nil {
			fmt.Println("开新对话失败：", err)
			return
		}
		s.thread, s.last, s.events = summary.ID, 0, nil
	} else {
		params["threadId"] = s.thread
		if s.compact {
			params["compact"] = true
			s.compact = false
		}
		if err := s.client.Call(ctx, "turn/start", params, nil); err != nil {
			fmt.Println("发送失败：", err)
			return
		}
	}
	if err := s.subscribe(s.last); err != nil {
		fmt.Println("订阅这一轮失败：", err)
		return
	}
	s.running = true
	s.follow()
}

// subscribe 订阅当前对话 after 之后的事件：续聊只显示这一轮的新事件。
func (s *session) subscribe(after int64) error {
	sub, err := s.client.Subscribe(context.Background(), s.thread, after)
	if err != nil {
		return err
	}
	s.sub, s.subSeq = sub, after
	return nil
}

// follow 跟着当前的订阅，直到这一轮结束（app-server 发来 end）。
// 序号不超过 subSeq 的事件是接上这条对话时已有的：只收下，不显示。
func (s *session) follow() {
	sub := s.sub
	if sub == nil {
		return
	}
	events := make(chan chatEvent, 1024)
	go func() {
		defer close(events)
		for {
			e, ok := sub.Next(context.Background())
			if !ok {
				return
			}
			ev := chatEvent{End: e.End}
			if !e.End {
				json.Unmarshal(e.Data, &ev)
			}
			events <- ev
		}
	}()
	lines := s.lines
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				s.v.newline()
				fmt.Println("与 app-server 的连接断了。")
				s.end()
				return
			}
			if ev.End {
				s.running = false
				s.asks = nil
				s.showResume()
				s.end()
				return
			}
			existing := ev.Seq <= s.subSeq
			s.record(ev)
			// 别的界面先答了：这一问不用再等。
			if settled(ev) {
				s.answeredElsewhere(ev, existing)
			}
			if existing {
				continue
			}
			// 有新事件就是这一轮在跑（接上一条正在跑的对话时，从这里起输入才算插话）。
			s.running = true
			// 接上一条对话：先打印摘要，还没人答的审批也在这里问出来。
			resumed := s.resumed
			s.showResume()
			if !resumed && ev.Kind == "approval" && len(s.asks) == 1 {
				s.v.prompt(s.asks[0].message())
			}
			s.show(ev)
		case line, ok := <-lines:
			if !ok {
				// 标准输入结束：不再中断这一轮，等它结束再退出。
				lines, s.eof = nil, true
				continue
			}
			s.input(line)
		case st := <-s.conns:
			s.showConn(st)
		case <-s.sigs:
			s.interrupt()
		}
	}
}

// showConn 打一行连接状态：断线时“连接断开，重连中…”，连上后“已重连”，这一轮的显示接着往下走。
func (s *session) showConn(st appclient.State) {
	s.v.newline()
	if st == appclient.StateReconnecting {
		fmt.Println(dim + "连接断开，重连中…" + reset)
		return
	}
	fmt.Println(dim + "已重连" + reset)
}

// end 这一轮结束：退订并清掉轮内的状态。对话留在 app-server 里，之后还可以续聊或换个界面接着看。
func (s *session) end() {
	s.v.newline()
	if s.sub != nil {
		s.sub.Close()
		s.sub = nil
	}
	s.subSeq, s.stops, s.asks = 0, 0, nil
}

// input 处理一轮进行中的一行输入：有待回答的审批就是回答；没有在跑的一轮时留到下一轮发，不试插话；否则是插话。
func (s *session) input(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(s.asks) > 0 {
		s.answer(line)
		return
	}
	if !s.running {
		// 接一条已经结束的对话时，补齐已有事件的那一会儿还没有在跑的一轮：这句话就是下一轮的问题。
		s.next = append(s.next, line)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	params := map[string]any{"threadId": s.thread, "text": line}
	if err := s.client.Call(ctx, "turn/steer", params, nil); err != nil {
		// 这一轮刚结束：留到下一轮发，同原来。
		s.next = append(s.next, line)
		s.v.newline()
		fmt.Println("（这一轮已经结束，这句话作为下一条消息发送）")
	}
}

// interrupt 是运行中的 Ctrl+C：第一次优雅停止，再按一次还是 turn/interrupt（app-server 第二次强制结束）。
func (s *session) interrupt() {
	s.stops++
	s.v.newline()
	if s.stops == 1 {
		fmt.Println("■ 正在停止：agent 回填结果、写好检查点后退出（再按一次强制结束）")
	} else {
		fmt.Println("■ 强制结束")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	params := map[string]any{"threadId": s.thread}
	if err := s.client.Call(ctx, "turn/interrupt", params, nil); err != nil {
		fmt.Println("停止失败：", err)
	}
}

// ---------- 显示 ----------

// record 收下一条事件：记进这条对话的事件列表，记住最后的 seq（续聊的 after），
// 把等人回答的审批排进队列（补齐已有事件时也照此算，见 showResume）。
// delta 只用来实时显示（一轮有上千条），算轮次与审批都用不着，不存。
func (s *session) record(ev chatEvent) {
	if ev.Seq > s.last {
		s.last = ev.Seq
	}
	if ev.Kind == "delta" {
		return
	}
	s.events = append(s.events, ev)
	if ev.Kind == "approval" {
		s.asks = append(s.asks, approval{method: ev.Method, body: ev.Body})
	}
}

// settled：这一问已经有结果了（谁答的都记成这些事件，Text 是调用ID或域名）。
func settled(ev chatEvent) bool {
	return ev.Kind == "decision" || ev.Kind == "answer" || ev.Kind == "allow" || ev.Kind == "deny"
}

// answeredElsewhere 看到别人先答了：不再等这一问。补齐已有事件时不打印。
func (s *session) answeredElsewhere(ev chatEvent, quiet bool) {
	if !slices.ContainsFunc(s.asks, func(a approval) bool { return a.key() == ev.Text }) {
		return
	}
	if !quiet {
		s.v.newline()
		fmt.Println("（这一问已经在别处回答）")
	}
	s.dropAsk(ev.Text)
}

// dropAsk 去掉标识为 key 的审批；去掉的是正在问的那一个时，接着问下一个。
func (s *session) dropAsk(key string) {
	i := slices.IndexFunc(s.asks, func(a approval) bool { return a.key() == key })
	if i < 0 {
		return
	}
	s.asks = slices.Delete(s.asks, i, i+1)
	if i == 0 && len(s.asks) > 0 {
		s.v.prompt(s.asks[0].message())
	}
}

// answer 把一行输入当作正在问的那个审批的回答，交给 approval/respond。
// 审计由 app-server 写（那才是记下“人点了什么”的一方），chat 不自己写。
func (s *session) answer(line string) {
	a := s.asks[0]
	var p struct {
		CallID  string   `json:"call_id"`
		Domain  string   `json:"domain"`
		Scope   string   `json:"scope"`
		Options []string `json:"options"`
	}
	json.Unmarshal(a.body, &p)
	if a.method == "user/requestInput" {
		answer := line
		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(p.Options) {
			answer = p.Options[n-1]
		}
		s.respond(a, map[string]any{"kind": "question", "call_id": p.CallID, "answer": answer})
		return
	}
	// 允许 / 拒绝：y、n（回车也是拒绝）、s 本对话内同一范围都允许（只有 agent 给了范围时才有）。
	allow, always := false, false
	switch strings.ToLower(line) {
	case "y":
		allow = true
	case "n", "":
	case "s":
		if p.Scope == "" {
			s.again(a, p.Scope)
			return
		}
		allow, always = true, true
	default:
		s.again(a, p.Scope)
		return
	}
	params := map[string]any{"call_id": p.CallID, "allow": allow}
	if a.method == "network/requestApproval" {
		params["kind"], params["domain"] = "network", p.Domain
	} else {
		params["kind"], params["session"] = "tool", always
	}
	s.respond(a, params)
}

// respond 把答案交给 app-server；答不上（比如这一轮已经结束）就提示并留着这一问，让人重新回答。
func (s *session) respond(a approval, params map[string]any) {
	params["threadId"] = s.thread
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.client.Call(ctx, "approval/respond", params, nil); err != nil {
		s.v.newline()
		fmt.Println("没有回答：", err)
		return
	}
	s.dropAsk(a.key())
}

// again 看不懂这行回答：把问题再问一遍。
func (s *session) again(a approval, scope string) {
	if a.method == "user/requestInput" {
		fmt.Print("回答：")
		return
	}
	fmt.Print("回答 y 或 n" + map[bool]string{true: "（s：本对话内都允许）", false: ""}[scope != ""] + "：")
}

// key 是这一问的标识：审批用调用ID，联网用域名（与 decision、answer、allow、deny 事件的 Text 对得上）。
func (a approval) key() string {
	var p struct {
		CallID string `json:"call_id"`
		Domain string `json:"domain"`
	}
	json.Unmarshal(a.body, &p)
	if a.method == "network/requestApproval" {
		return p.Domain
	}
	return p.CallID
}

// message 把审批还原成原来 agent 发来的那条请求，交给 view.prompt。
func (a approval) message() protocol.Message {
	return protocol.Message{Method: a.method, Params: a.body}
}

// showResume 在补齐已有事件之后打印接上的这条对话：第一个问题和最后一轮回答的开头；
// 对话还在跑时，把还没人答的审批问出来。
func (s *session) showResume() {
	if !s.resumed {
		return
	}
	s.resumed = false
	question := ""
	for _, e := range s.events {
		if e.Kind == "start" || e.Kind == "continue" {
			question = e.Text
			break
		}
	}
	fmt.Printf("接上对话 %s：%s\n", short(s.thread), clip(question, 70))
	if answer := s.lastAnswer(); answer != "" {
		fmt.Printf("最后一轮回答：%s\n", clip(answer, 70))
	}
	if len(s.asks) > 0 {
		s.v.prompt(s.asks[0].message())
	}
}

// lastAnswer 是最后一轮回答的开头：从后往前找 agent 的最终回答（没有就找最后一条过程说明）。
func (s *session) lastAnswer() string {
	fallback := ""
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Kind != "notify" || e.Method != "item/completed" {
			continue
		}
		var p struct {
			Item struct {
				Type string `json:"type"`
				Data struct {
					Text  string `json:"text"`
					Phase string `json:"phase"`
				} `json:"data"`
			} `json:"item"`
		}
		if json.Unmarshal(e.Body, &p) != nil || p.Item.Type != "agentMessage" {
			continue
		}
		if p.Item.Data.Phase == "final_answer" {
			return p.Item.Data.Text
		}
		if fallback == "" {
			fallback = p.Item.Data.Text
		}
	}
	return fallback
}

// show 显示一条新事件：通知与增量照原来的样子交给 view；终端日志写进日志文件，不显示。
func (s *session) show(ev chatEvent) {
	switch ev.Kind {
	case "notify", "delta":
		if ev.Method == "turn/completed" {
			var p struct{ Queued []string }
			json.Unmarshal(ev.Body, &p)
			s.next = append(s.next, p.Queued...)
		}
		s.v.show(protocol.Message{Method: ev.Method, Params: ev.Body})
	case "stdout":
		if s.log != nil {
			fmt.Fprintln(s.log, ev.Text)
		}
	case "exit":
		if ev.Text != "exit 0" && !s.v.completed {
			s.v.newline()
			fmt.Printf("agent 异常退出（%v），日志见 %s\n", ev.Text, s.logPath)
		}
	}
}

// view 把通知画成终端上的文字：正文与推理边收边打，工具调用一行开始、一行结局。
type view struct {
	field     string // 正在打印的增量：content、reasoning 或空（行首）
	completed bool   // 收到过 turn/completed
}

const dim, reset = "\x1b[2m", "\x1b[0m"

func (v *view) newline() {
	if v.field != "" {
		fmt.Println(reset)
		v.field = ""
	}
}

func (v *view) show(m protocol.Message) {
	var p map[string]any
	json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "item/delta":
		field, _ := p["field"].(string)
		delta, _ := p["delta"].(string)
		if p["purpose"] != "main" {
			return
		}
		if field == "reset" {
			v.newline()
			fmt.Println(dim + "（模型请求中断，重试）" + reset)
			return
		}
		if field != v.field {
			v.newline()
			if field == "reasoning" {
				fmt.Print(dim)
			}
			v.field = field
		}
		fmt.Print(delta)
	case "item/started":
		item, _ := p["item"].(map[string]any)
		data, _ := item["data"].(map[string]any)
		switch item["type"] {
		case "toolCall":
			v.newline()
			fmt.Printf("▸ %s %s\n", data["tool"], clip(fmt.Sprint(data["arguments"]), 100))
		case "compaction":
			v.newline()
			fmt.Println(dim + "（正在压缩上下文…）" + reset)
		}
	case "item/completed":
		item, _ := p["item"].(map[string]any)
		data, _ := item["data"].(map[string]any)
		switch item["type"] {
		case "toolCall":
			o, _ := data["observation"].(map[string]any)
			if o["status"] == "ok" {
				fmt.Printf("  %s✓ %s%s\n", dim, clip(compact(o["result"]), 100), reset)
			} else {
				fmt.Printf("  ✗ %s %s\n", o["status"], clip(fmt.Sprint(o["error"]), 160))
			}
		case "userMessage":
			if data["steer"] == true {
				v.newline()
				fmt.Printf("↳ 插话已送达：%s\n", clip(fmt.Sprint(data["text"]), 100))
			}
		case "compaction":
			if data["error"] != nil {
				fmt.Printf("%s（没有压缩：%s）%s\n", dim, data["error"], reset)
			} else {
				fmt.Printf("%s（上下文已压缩：%s → %s token）%s\n", dim, num(data["before"]), num(data["after"]), reset)
			}
		case "agentMessage":
			v.newline()
		}
	case "subagent/started":
		v.newline()
		fmt.Printf("  ⧉ 子 agent 开始：%s\n", clip(fmt.Sprint(p["task"]), 80))
	case "subagent/completed":
		fmt.Printf("  ⧉ 子 agent 结束：%v\n", p["state"])
	case "turn/completed":
		v.newline()
		v.completed = true
		if outcome := p["outcome"]; outcome != "no_tool_calls" {
			fmt.Printf("■ 这一轮停止：%v %v\n", outcome, p["error"])
		}
		if fc, ok := p["final_check"].(map[string]any); ok {
			fmt.Printf("■ 收尾检查：通过=%v %s\n", fc["passed"], clip(fmt.Sprint(fc["detail"]), 200))
		}
		if u, ok := p["usage"].(map[string]any); ok {
			fmt.Printf("%s模型请求 %s 次 · 输入 %s · 输出 %s · 缓存 %s%s\n", dim, num(p["model_calls"]), num(u["input"]), num(u["output"]), num(u["cached"]), reset)
		}
		if q, ok := p["queued"].([]any); ok && len(q) > 0 {
			fmt.Printf("（%d 条插话没赶上这一轮，接着作为下一条消息发送）\n", len(q))
		}
	}
}

// prompt 显示一个等人回答的请求。
func (v *view) prompt(m protocol.Message) {
	var p struct {
		Domain    string   `json:"domain"`
		Reason    string   `json:"reason"`
		Tool      string   `json:"tool"`
		Risk      string   `json:"risk"`
		Arguments string   `json:"arguments"`
		Scope     string   `json:"scope"`
		Question  string   `json:"question"`
		Options   []string `json:"options"`
	}
	json.Unmarshal(m.Params, &p)
	v.newline()
	switch m.Method {
	case "network/requestApproval":
		fmt.Printf("? 沙箱请求访问 %s（%s）。允许？[y/N] ", p.Domain, p.Reason)
	case "tool/requestApproval":
		fmt.Printf("? %s 要确认：%s\n  参数：%s\n  允许？[y/N%s] ", p.Tool, p.Risk, clip(p.Arguments, 300), map[bool]string{true: "/s=本对话内同一范围都允许", false: ""}[p.Scope != ""])
	case "user/requestInput":
		fmt.Printf("? %s\n", p.Question)
		for i, o := range p.Options {
			fmt.Printf("  %d. %s\n", i+1, o)
		}
		if len(p.Options) > 0 {
			fmt.Print("  输入编号，或直接写回答：")
		} else {
			fmt.Print("  回答：")
		}
	default:
		fmt.Printf("? agent 发来不支持的请求 %s，回车拒绝 ", m.Method)
	}
}

// ---------- 小工具 ----------

// short 取 ID 的前 8 位，够看清是哪一条。
func short(id string) string {
	if r := []rune(id); len(r) > 8 {
		return string(r[:8])
	}
	return id
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// num 打印 JSON 里的整数：解码成 float64 后 %v 会写成 2.035675e+06。
func num(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return fmt.Sprint(v)
}

func compact(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
