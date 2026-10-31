package appserver

import (
	"errors"
	"time"

	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/preview"
	"github.com/xuzhao123/tern/internal/protocol"
)

// previewError 把 preview 包的错误翻成协议错误：code 就是它的 HTTP 状态码（403、404、409、429）。
func previewError(err error) *protocol.Error {
	var pe *preview.Error
	if errors.As(err, &pe) {
		return bad(pe.Code, pe.Msg)
	}
	return bad(500, err.Error())
}

// previewThread 是能开预览的对话：评测的试次、导入的对话只读，按 409 拒绝（和排队、管理一致）。
func (c *conn) previewThread(id string) (*run, *protocol.Error) {
	r, ferr := c.s.threadOf(id)
	if ferr != nil {
		return nil, ferr
	}
	if r.Eval != "" {
		return nil, bad(409, "评测的试次不能开预览")
	}
	if r.Source != "" {
		return nil, bad(409, "导入的对话只读，不能开预览")
	}
	return r, nil
}

// preview/open：网页端的已认证用户给这条对话授权一个本机开发服务的地址。授权只能这样创建，模型输出的链接不创建授权。
func (c *conn) previewOpen(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
		URL      string `json:"url"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.previewThread(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	status, err := c.s.preview.Open(p.ThreadID, p.URL, p.Width, p.Height)
	if err != nil {
		return nil, previewError(err)
	}
	// 授权是用户做的：先写审计，写不进去就不要这个会话。Target 只写 origin，不写路径和查询参数。
	if err := audit.Write(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_preview_open", Task: r.rootTask, Conversation: r.ID, Target: status.Origin, Detail: c.clientName()}); err != nil {
		c.s.preview.Close(p.ThreadID)
		return nil, bad(500, "审计日志写入失败，没有开预览："+err.Error())
	}
	return status, nil
}

// preview/get：这条对话的会话描述；没有会话回 404（界面据此显示地址栏）。
func (c *conn) previewGet(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	status, ok := c.s.preview.Get(p.ThreadID)
	if !ok {
		return nil, bad(404, "这条对话没有预览会话")
	}
	return status, nil
}

// preview/action：一个受限动作。同一个 client 里 seq 重复或过期、旧视口版本的鼠标动作回 409，队列满了回 429。
func (c *conn) previewAction(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID  string   `json:"threadId"`
		Action    string   `json:"action"`
		Client    string   `json:"client"`
		Seq       int64    `json:"seq"`
		Viewport  int64    `json:"viewport"`
		URL       string   `json:"url"`
		Width     int      `json:"width"`
		Height    int      `json:"height"`
		Type      string   `json:"type"`
		X         float64  `json:"x"`
		Y         float64  `json:"y"`
		Button    string   `json:"button"`
		Clicks    int      `json:"clicks"`
		DX        float64  `json:"dx"`
		DY        float64  `json:"dy"`
		Modifiers []string `json:"modifiers"`
		Key       string   `json:"key"`
		Code      string   `json:"code"`
		Text      string   `json:"text"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	err := c.s.preview.Action(p.ThreadID, preview.Action{Kind: p.Action, Client: p.Client, Seq: p.Seq, Viewport: p.Viewport, URL: p.URL,
		Width: p.Width, Height: p.Height, Type: p.Type, X: p.X, Y: p.Y, Button: p.Button, Clicks: p.Clicks,
		DX: p.DX, DY: p.DY, Modifiers: p.Modifiers, Key: p.Key, Code: p.Code, Text: p.Text})
	if err != nil {
		return nil, previewError(err)
	}
	return map[string]any{"ok": true}, nil
}

// preview/frame：有比 after 新的帧就立刻回这一帧，没有就等到 wait_ms（最多 10 秒）回带 after 的空帧。
func (c *conn) previewFrame(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
		After    int64  `json:"after"`
		WaitMS   int    `json:"wait_ms"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	frame, err := c.s.preview.Frame(c.ctx, p.ThreadID, p.After, time.Duration(p.WaitMS)*time.Millisecond)
	if err != nil {
		return nil, previewError(err)
	}
	return frame, nil
}

// preview/close：关掉这条对话的会话。本来就没有会话时照样回成功，审计只记真关掉的。
func (c *conn) previewClose(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	status, ok := c.s.preview.Get(p.ThreadID)
	if !ok {
		return nil, nil
	}
	var task string
	if r, ferr := c.s.threadOf(p.ThreadID); ferr == nil {
		task = r.rootTask
	}
	if err := audit.Write(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_preview_close", Task: task, Conversation: p.ThreadID, Target: status.Origin, Detail: c.clientName()}); err != nil {
		return nil, bad(500, "审计日志写入失败，没有关预览："+err.Error())
	}
	c.s.preview.Close(p.ThreadID)
	return nil, nil
}
