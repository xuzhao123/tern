package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/agent"
	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/protocol"
)

func (c *conn) subagentControl(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Action string `json:"action"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if input.Action == "status" {
		return c.subagentControlOnce(m)
	}
	return c.requestOnce(m, (*conn).subagentControlOnce)
}
func (c *conn) subagentControlOnce(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
		Task     string `json:"task_id"`
		Call     string `json:"call_id"`
		Action   string `json:"action"`
		Text     string `json:"text"`
		Request  string `json:"request_id"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	if !taskPattern.MatchString(p.Task) || p.Call == "" || !requestIDPattern.MatchString(p.Request) || len(p.Text) > 32000 || p.Action != "send" && p.Action != "stop" && p.Action != "status" || p.Action == "send" && strings.TrimSpace(p.Text) == "" {
		return nil, bad(400, "需要有效目标、操作、正文及请求ID")
	}
	r, ferr := c.s.threadOf(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.mu.Lock()
	pc, done := r.conn, r.Done
	matched, active := false, false
	for _, e := range r.events {
		if e.Kind == "start" || e.Kind == "continue" {
			active = false
		}
		if e.Method != "subagent/started" && e.Method != "subagent/completed" {
			continue
		}
		var body struct {
			Task string `json:"task_id"`
			Call string `json:"call_id"`
		}
		if json.Unmarshal(e.Body, &body) == nil && body.Task == p.Task {
			if e.Method == "subagent/started" {
				matched = matched || body.Call == p.Call
				active = body.Call == p.Call
			} else if body.Call == p.Call {
				active = false
			}
		}
	}
	r.mu.Unlock()
	if !matched {
		return nil, bad(409, "子任务或尝试版本不属于当前记录，请刷新")
	}
	if p.Action == "status" {
		if cp, err := agent.LoadCheckpointIn("", p.Task); err == nil {
			if receipt, ok := cp.State.ManagedSteers[p.Request]; ok && (receipt.State == "consumed" || receipt.State == "blocked") {
				return map[string]any{"state": receipt.State}, nil
			}
		}
	}
	if done || !active || pc == nil {
		if p.Action == "status" {
			return map[string]string{"state": "unknown", "note": "子任务不再活跃，未找到采纳证据；可恢复草稿核对"}, nil
		}
		return nil, bad(409, "子任务不在当前父轮运行，可让主任务按原ID续跑")
	}
	if p.Action != "status" {
		record := audit.Record{Actor: "user", User: userName(m), Event: "user_subagent_" + p.Action, Task: p.Task, Conversation: r.ID, Target: p.Call, Detail: c.clientName()}
		record.Args(p.Text)
		if err := audit.Write(c.ctx, record); err != nil {
			return nil, bad(500, "审计保存失败，没有操作子任务")
		}
	}
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	var result json.RawMessage
	if err := pc.Call(ctx, "subagent/control", map[string]string{"task_id": p.Task, "call_id": p.Call, "action": p.Action, "text": p.Text, "request_id": p.Request}, &result); err != nil {
		return map[string]string{"state": "unknown", "note": "控制结果待核对；未自动重发。旧主机可能不支持此能力。"}, nil
	}
	return result, nil
}
