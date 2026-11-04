package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xuzhao123/tern/internal/hooks"
	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/protocol"
)

// 运行中的人机交互——插话、模型向用户提问、手动压缩。三者都只是协议上多了几条消息，界面（终端客户端、网页端）各自呈现。

// ---------- 插话 ----------
// 一轮进行中用户又说了话（turn/steer）：不打断正在执行的模型请求或工具，排进下一次模型请求，作为一条真正的用户消息。
// 这一轮在用上之前就结束了（模型正在写最终回答），还没用上的随 turn/completed 的 queued 交回界面，由界面当作下一轮的问题。

// steer 是一条排着的插话。clientID 是发它的页面生成的，取走时随 userMessage 发回去，
// 页面据此把它从待发送区挪进对话（见 internal/web/page/composer.js）。chat、子 agent 不传。
type steer struct {
	text     string
	clientID string
	managed  bool
}

var steers struct {
	sync.Mutex
	queue    []steer
	closed   bool
	receipts map[string]SteerReceipt
}

// SteerReceipt 不保留原文；managed 回执随有效上下文一起进入检查点。
type SteerReceipt struct {
	Hash    string `json:"hash"`
	State   string `json:"state"`
	Managed bool   `json:"managed,omitempty"`
}

func steerHash(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }

func Steer(text, clientID string, managed bool) (string, error) {
	steers.Lock()
	defer steers.Unlock()
	if clientID != "" {
		if previous, ok := steers.receipts[clientID]; ok {
			if previous.Hash != steerHash(text) || previous.Managed != managed {
				return "", errors.New("同一 client_id 不能用于不同的补充")
			}
			return previous.State, nil
		}
	}
	if steers.closed {
		return "closed", nil
	}
	if managed && clientID == "" {
		return "", errors.New("managed 补充需要 client_id")
	}
	if steers.receipts == nil {
		steers.receipts = map[string]SteerReceipt{}
	}
	if clientID != "" {
		steers.receipts[clientID] = SteerReceipt{Hash: steerHash(text), State: "pending", Managed: managed}
	}
	steers.queue = append(steers.queue, steer{text: text, clientID: clientID, managed: managed})
	return "pending", nil
}

func SteerStatus(ids []string) any {
	steers.Lock()
	defer steers.Unlock()
	items := map[string]SteerReceipt{}
	for _, id := range ids {
		if receipt, ok := steers.receipts[id]; ok {
			items[id] = receipt
		}
	}
	return map[string]any{"managed": true, "closed": steers.closed, "items": items}
}
func restoreSteerReceipts(receipts map[string]SteerReceipt) {
	steers.Lock()
	defer steers.Unlock()
	if steers.receipts == nil {
		steers.receipts = map[string]SteerReceipt{}
	}
	for id, receipt := range receipts {
		steers.receipts[id] = receipt
	}
	kept := steers.queue[:0]
	for _, item := range steers.queue {
		if receipt, ok := receipts[item.clientID]; !ok || (receipt.State != "consumed" && receipt.State != "blocked") {
			kept = append(kept, item)
		}
	}
	steers.queue = kept
}
func completeSteer(item steer, state string) {
	if item.clientID == "" {
		return
	}
	steers.Lock()
	defer steers.Unlock()
	steers.receipts[item.clientID] = SteerReceipt{Hash: steerHash(item.text), State: state, Managed: item.managed}
}

func takeSteers() []steer {
	steers.Lock()
	defer steers.Unlock()
	queue := steers.queue
	steers.queue = nil
	return queue
}

// closeSteers 在这一轮结束时调用：之后的插话不再接收，还没用上的交回去。
// 只交文字：turn/completed 的 queued 格式不变，chat 接着把它当下一轮的问题。
func closeSteers() ([]string, []string) {
	steers.Lock()
	defer steers.Unlock()
	steers.closed = true
	var texts, managed []string
	for _, item := range steers.queue {
		if item.managed {
			managed = append(managed, item.clientID)
		} else {
			texts = append(texts, item.text)
		}
		if item.clientID != "" {
			receipt := steers.receipts[item.clientID]
			receipt.State = "unconsumed"
			steers.receipts[item.clientID] = receipt
		}
	}
	steers.queue = nil
	return texts, managed
}

// steerItem 是插话在对话里的那条 userMessage：clientID 非空时带上，发它的页面据此把它从待发送区撤掉。
// 被 UserPromptSubmit hook 拦下的那条也带：文字已经换掉，页面不能拿原文去比对。
func steerItem(id, text, clientID string) protocol.Item {
	data := map[string]any{"text": text, "steer": true}
	if clientID != "" {
		data["client_id"] = clientID
	}
	return protocol.Item{ID: id, Type: "userMessage", Data: data}
}

// ---------- 模型向用户提问 ----------
// ask_user 只在有人能当场回答的界面下提供（-ask-user：终端客户端与网页端打开，子 agent 沿父任务开关继承；命令行、队列、评测默认不给）。
// 问题经 user/requestInput 请求发给界面，agent 在这里等；没有界面或界面答不了，就告诉模型把问题写进最终回答。

var AskUser bool

const AskWait = 15 * time.Minute

var AskDefinitions = []map[string]any{{
	"name": "ask_user",
	"description": "向用户提一个问题，等用户回答后再继续。只在缺少必须由用户决定的信息时用：需求有歧义、几种做法都合理而选错代价大、需要用户确认偏好。" +
		"能自己查到的（读代码、看文档、跑命令）先查，不要拿来问；一次只问一个问题。给 2–4 个选项时用户可以直接选，也可以另写。",
	"parameters": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{"type": "string", "description": "问题本身，写清楚要用户决定什么"},
			"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 4, "description": "可选：2–4 个互斥的候选答案"},
		},
		"required":             []string{"question"},
		"additionalProperties": false,
	},
}}

func askUser(ctx context.Context, call llm.ToolCall) (any, error) {
	var args struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil || strings.TrimSpace(args.Question) == "" {
		return nil, llm.Permanent(errors.New("ask_user 需要非空的 question"))
	}
	if len(args.Options) > 4 {
		return nil, llm.Permanent(errors.New("options 最多 4 个"))
	}
	fmt.Printf("Ask user [%s]: %s\n", call.ID, args.Question)
	notifyHook("ask_user", "需要你回答："+args.Question)
	waitCtx, cancel := context.WithTimeout(ctx, AskWait)
	defer cancel()
	var answer struct {
		Answer   string `json:"answer"`
		Decision string `json:"decision"`
	}
	err := protocol.Call(waitCtx, "user/requestInput", map[string]any{"call_id": call.ID, "question": args.Question, "options": args.Options}, &answer)
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(err, protocol.ErrNoClient) || answer.Decision == "unavailable":
		return nil, llm.Permanent(errors.New("现在没有人能当场回答：把需要用户决定的问题写进最终回答"))
	case err != nil:
		fmt.Printf("Ask user [%s]: no answer (%s)\n", call.ID, firstLine(err.Error()))
		return map[string]any{"answered": false, "note": fmt.Sprintf("用户 %s 内没有回答。按最稳妥、最容易撤回的做法继续，并在最终回答里说明还需要用户决定什么", AskWait)}, nil
	}
	fmt.Printf("Ask user [%s]: answered\n", call.ID)
	return map[string]any{"answered": true, "answer": answer.Answer}, nil
}

// notifyHook 在要等人的时候跑 Notification hook（如桌面通知、发消息到手机）：只通知，不等它、不看结果。
func notifyHook(kind, message string) {
	if !hooks.Has("Notification") {
		return
	}
	go hooks.Run(context.Background(), "Notification", "", map[string]any{"notification_type": kind, "message": message})
}

// ---------- 手动压缩 ----------
// 界面在 turn/start 里带 compact=true（终端客户端的 /compact）：这一轮第一次请求模型之前先压缩，不管用量到没到触发线。
// 压缩不成（比如对话还很短，没有可摘要的旧消息）只记一行，照常回答。
var CompactFirst bool
