package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/appclient"
	"github.com/xuzhao123/tern/internal/appserver"

	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// tern mcp-server：stdio 上的 MCP server，别的 agent 客户端（Claude Code、Codex）把一件事交给 tern 做。
// 它和网页端、chat 一样是 app-server 的界面：经 appclient 开对话，自己不启动 agent，也不读 .data/ 里的
// 对话与设置。所以对话进 app-server 的列表（网页端看得到），审批也在网页端答，这一侧只等这一轮结束。

// progressInterval 是等一轮时发进度通知的间隔：一轮可能跑很多分钟，调用方据此知道还活着。
const progressInterval = 10 * time.Second

// Run 解析 mcp-server 自己的参数（-remote），连上 app-server，在 stdio 上提供工具。
// stdout 就是 MCP 的协议通道，这里和它调用的 Start 都不往 stdout 打印。
func Run(args []string) error {
	remote := ""
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if !strings.HasPrefix(args[i], "-") {
			return fmt.Errorf("mcp-server 不接受位置参数：%s", args[i])
		}
		if name != "remote" {
			return fmt.Errorf("mcp-server 不认识 -%s", name)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return fmt.Errorf("-remote 需要一个值")
			}
			i++
			value = args[i]
		}
		remote = value
	}
	// 不带 -remote 时先保证本机的 app-server 在运行，再连上；带 -remote 时经 ssh 连远端（和 tern web 一样）。
	var client *appclient.Client
	var err error
	if remote != "" {
		client, _, err = appclient.DialRemote("tern mcp-server", remote)
	} else {
		if _, err = appclient.Start(false); err != nil {
			return err
		}
		client, _, err = appclient.Dial("tern mcp-server")
	}
	if err != nil {
		return err
	}
	defer client.Close()
	t := &tern{client: client}
	s := server.NewMCPServer("tern", appserver.Version(), server.WithToolCapabilities(false))
	s.AddTool(startTool, t.start)
	s.AddTool(replyTool, t.reply)
	s.AddTool(threadsTool, t.threads)
	return server.ServeStdio(s)
}

// tern 是这些工具手里的连接：所有工具共用 app-server 上的一条连接。
type tern struct {
	client *appclient.Client
}

var startTool = sdk.NewTool("tern_start",
	sdk.WithDescription("让 tern 开一条新对话，等这一轮结束再返回。tern 是一个能读写工作区、跑命令的编码 agent，"+
		"一轮可能跑很多分钟，期间每 10 秒报一次进度；工作区要先用 tern 的网页端登记，这里只填名字。"+
		"对话进 app-server 的列表，网页端看得到，审批也要在网页端答（这一侧答不了）。取消这次调用会停掉这一轮。"+
		"返回 {thread_id, outcome, answer}：outcome 是结局（exit 0、报错，取消时是 cancelled），"+
		"answer 是这一轮的最终回答，没有时是空串。"),
	sdk.WithString("prompt", sdk.Required(), sdk.Description("这一轮要 tern 做什么")),
	sdk.WithString("workspace", sdk.Description("工作区名字（先在 tern 的网页端登记）；不填用设置里的默认工作区")),
	sdk.WithString("provider", sdk.Description("模型供应商；不填用设置里的默认值")),
	sdk.WithString("model", sdk.Description("具体模型；不填用这家供应商的默认模型")),
	sdk.WithString("permission", sdk.Description("权限模式：plan、ask、edit、full；不填用设置里的默认值")),
)

var replyTool = sdk.NewTool("tern_reply",
	sdk.WithDescription("在一条已有的 tern 对话上续聊一轮，等它结束再返回；等的过程、取消与返回的内容同 tern_start。"+
		"thread_id 从 tern_start 的返回或 tern_threads 里拿。"),
	sdk.WithString("thread_id", sdk.Required(), sdk.Description("对话 ID")),
	sdk.WithString("prompt", sdk.Required(), sdk.Description("这一轮要说的话")),
	sdk.WithString("permission", sdk.Description("这一轮的权限模式：plan、ask、edit、full；不填沿用这条对话的设置")),
)

var threadsTool = sdk.NewTool("tern_threads",
	sdk.WithDescription("列出 tern 上最近的对话（新的在前），用来找 tern_reply 要用的 thread_id。"+
		"返回 {threads: [{id, name, query, done, workspace, updated_at}]}：updated_at 是 Unix 毫秒时间戳。"),
	sdk.WithInteger("limit", sdk.Description("最多列几条，默认 20"), sdk.DefaultNumber(20), sdk.Min(1)),
	sdk.WithReadOnlyHintAnnotation(true),
	sdk.WithIdempotentHintAnnotation(true),
)

// start 开一条新对话（thread/start）并等这一轮结束。
func (t *tern) start(ctx context.Context, req sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	var in struct {
		Prompt     string `json:"prompt"`
		Workspace  string `json:"workspace"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		Permission string `json:"permission"`
	}
	if err := req.BindArguments(&in); err != nil {
		return sdk.NewToolResultError("参数不对：" + err.Error()), nil
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" {
		return sdk.NewToolResultError("需要 prompt：这一轮要 tern 做什么"), nil
	}
	// 空着的选项不传：工作区传空串是“这条对话不用工作区”，不是“用默认的”。
	params := map[string]any{"query": in.Prompt}
	if in.Workspace != "" {
		params["workspace"] = in.Workspace
	}
	if in.Provider != "" {
		params["provider"] = in.Provider
	}
	if in.Model != "" {
		params["model"] = in.Model
	}
	if in.Permission != "" {
		params["permission"] = in.Permission
	}
	var started struct {
		ID string `json:"id"`
	}
	if err := t.client.Call(ctx, "thread/start", params, &started); err != nil {
		return sdk.NewToolResultError("开新对话失败：" + err.Error()), nil
	}
	return t.wait(ctx, progressToken(req), started.ID)
}

// reply 在一条已有的对话上续聊一轮（turn/start）并等它结束。
func (t *tern) reply(ctx context.Context, req sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	var in struct {
		ThreadID   string `json:"thread_id"`
		Prompt     string `json:"prompt"`
		Permission string `json:"permission"`
	}
	if err := req.BindArguments(&in); err != nil {
		return sdk.NewToolResultError("参数不对：" + err.Error()), nil
	}
	in.ThreadID, in.Prompt = strings.TrimSpace(in.ThreadID), strings.TrimSpace(in.Prompt)
	if in.ThreadID == "" || in.Prompt == "" {
		return sdk.NewToolResultError("需要 thread_id 与 prompt"), nil
	}
	params := map[string]any{"threadId": in.ThreadID, "query": in.Prompt}
	if in.Permission != "" {
		params["permission"] = in.Permission
	}
	if err := t.client.Call(ctx, "turn/start", params, nil); err != nil {
		return sdk.NewToolResultError("续聊失败：" + err.Error()), nil
	}
	// 发出去以后再订阅：已经结束的对话一订阅就发 end，先订阅就看不到这一轮了。
	return t.wait(ctx, progressToken(req), in.ThreadID)
}

// thread 是 tern_threads 列的一项，字段取自 app-server 的对话摘要。
type thread struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Query     string `json:"query"`
	Done      bool   `json:"done"`
	Workspace string `json:"workspace,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

// threads 列最近的对话（thread/list，新的在前）。评测的试次不进列表：那不是人开的对话，也不该照着续聊。
func (t *tern) threads(ctx context.Context, req sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	limit := req.GetInt("limit", 20)
	if limit <= 0 {
		limit = 20
	}
	var list []struct {
		thread
		Eval string `json:"eval"`
	}
	if err := t.client.Call(ctx, "thread/list", nil, &list); err != nil {
		return sdk.NewToolResultError("列出对话失败：" + err.Error()), nil
	}
	kept := []thread{}
	// thread/list 从旧到新；界面上是新的在上，这里也按新的在前取。
	for i := len(list) - 1; i >= 0 && len(kept) < limit; i-- {
		if list[i].Eval == "" {
			kept = append(kept, list[i].thread)
		}
	}
	return sdk.NewToolResultJSON(map[string]any{"threads": kept})
}

// event 是 app-server 推来的事件里这一侧要用的字段（见 appserver.event）。
type event struct {
	Kind    string          `json:"kind"`
	Method  string          `json:"method"`
	Text    string          `json:"text"`
	Body    json.RawMessage `json:"body"`
	Purpose string          `json:"purpose"`
	Sub     bool            `json:"sub"`
}

// wait 等这条对话的这一轮结束：订阅它的事件，读到 app-server 发来的 end（这一轮记完 exit 之后）为止，
// 期间每 10 秒发一次进度通知；调用方取消时对这条对话发 turn/interrupt。
//
// 订阅用 0：协议上没有只取“现在”序号的接口，订阅会把这条对话已有的事件从头发一遍（同 chat 的 /resume）。
// 从头看正好分得清——看见这一轮的 start/continue 之前的事件都是之前各轮的，不参与结局与回答。
func (t *tern) wait(ctx context.Context, token sdk.ProgressToken, threadID string) (*sdk.CallToolResult, error) {
	sub, err := t.client.Subscribe(ctx, threadID, 0)
	if err != nil {
		return sdk.NewToolResultError("订阅这条对话失败：" + err.Error()), nil
	}
	defer sub.Close()
	// 事件在自己的 goroutine 里取：Next 一直等到有事件为止，主循环还要看取消和进度通知。
	events := make(chan appclient.Event, 64)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(events)
		for {
			e, ok := sub.Next(context.Background())
			if !ok {
				return
			}
			select {
			case events <- e:
			case <-done:
				return
			}
		}
	}()
	var (
		turn    bool   // 看到了这一轮的 start/continue：之后的事件才属于这一轮
		outcome string // 这一轮的结局（exit 事件）
		answer  string // 这一轮的最终回答
		calls   int    // 这一轮的模型请求次数，进度通知里报给调用方
		waiting bool   // 正在等审批
		sent    int    // 发了几次进度通知（progress 只增不减）
	)
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()
	for {
		select {
		case e, ok := <-events:
			if !ok || e.End {
				// 正常是 app-server 在这一轮记完 exit 之后发来的 end；连接断了、订阅被退掉时没等到结局。
				if outcome == "" {
					outcome = "没有收到这一轮的结局（订阅提前结束）"
				}
				return turnResult(threadID, outcome, answer)
			}
			var ev event
			if json.Unmarshal(e.Data, &ev) != nil {
				continue
			}
			if ev.Kind == "start" || ev.Kind == "continue" {
				// 这一轮从这里开始（补发的历史里也有）：结局、回答、进度都按这一轮重新算。
				turn, outcome, answer, calls, waiting = true, "", "", 0, false
				continue
			}
			if !turn {
				continue
			}
			switch ev.Kind {
			case "request":
				if ev.Purpose == "main" && !ev.Sub {
					calls++
				}
			case "approval":
				waiting = true
			case "decision", "answer", "allow", "deny":
				waiting = false // 有人答了（网页端、chat 或超时），这一轮接着跑
			case "notify":
				if ev.Method == "item/completed" {
					if text, ok := finalAnswer(ev.Body); ok {
						answer = text
					}
				}
			case "exit":
				// 这一轮的结局：app-server 在 agent 进程退出后记下的文字（exit 0、报错、被停）。
				outcome = ev.Text
			}
		case <-ticker.C:
			sent++
			t.notify(ctx, token, sent, calls, waiting)
		case <-ctx.Done():
			// 调用方取消了这次工具调用（MCP 的 cancel）：和 chat 的 Ctrl+C 一样停这一轮，不再等下去。
			t.interrupt(threadID)
			return turnResult(threadID, "cancelled", "")
		}
	}
}

// notify 发一次进度通知，告诉调用方还活着：正在第几次模型请求，或者在等审批（审批只能去网页端答）。
// 调用方没在 _meta 里给进度令牌时不发：MCP 的进度通知要带上它。
func (t *tern) notify(ctx context.Context, token sdk.ProgressToken, n, calls int, waiting bool) {
	s := server.ServerFromContext(ctx)
	if s == nil || token == nil {
		return
	}
	message := "tern 刚开始这一轮"
	if calls > 0 {
		message = fmt.Sprintf("第 %d 次模型请求", calls)
	}
	if waiting {
		message = "在等审批：去网页端答复（" + t.client.Host() + "）"
	}
	s.SendNotificationToClient(ctx, string(sdk.MethodNotificationProgress), map[string]any{
		"progressToken": token,
		"progress":      n,
		"message":       message,
	})
}

// interrupt 停这一轮（turn/interrupt，同 chat 的 Ctrl+C）：第一次是让 agent 回填结果、写好检查点后退出。
// 这一轮已经结束、对话不在了都会失败，这时没有调用方可以告诉，忽略。
func (t *tern) interrupt(threadID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.client.Call(ctx, "turn/interrupt", map[string]any{"threadId": threadID}, nil)
}

// turnResult 是一轮结束时的返回：thread_id 是这条对话，outcome 是结局，answer 是最终回答（没有时为空串）。
func turnResult(threadID, outcome, answer string) (*sdk.CallToolResult, error) {
	return sdk.NewToolResultJSON(map[string]any{"thread_id": threadID, "outcome": outcome, "answer": answer})
}

// progressToken 是调用方在 _meta 里给的进度令牌：给了才发进度通知。
func progressToken(req sdk.CallToolRequest) sdk.ProgressToken {
	if req.Params.Meta == nil {
		return nil
	}
	return req.Params.Meta.ProgressToken
}

// finalAnswer 从一条 item/completed 通知里取 agent 的最终回答：harness 只在模型真正结束这一轮时把那段文字
// 标成 final_answer（见 agent 的 announce），过程说明不算回答。
func finalAnswer(body json.RawMessage) (string, bool) {
	var p struct {
		Item struct {
			Type string `json:"type"`
			Data struct {
				Text  string `json:"text"`
				Phase string `json:"phase"`
			} `json:"data"`
		} `json:"item"`
	}
	if json.Unmarshal(body, &p) != nil || p.Item.Type != "agentMessage" || p.Item.Data.Phase != "final_answer" {
		return "", false
	}
	return p.Item.Data.Text, true
}
