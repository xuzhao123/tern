package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/protocol"
)

type goalEvidence struct {
	Criterion  int    `json:"criterion"`
	ToolCallID string `json:"tool_call_id"`
	Detail     string `json:"detail"`
	Seq        int    `json:"seq,omitempty"`
	Hash       string `json:"hash,omitempty"`
}
type continuousGoal struct {
	ID           string            `json:"id"`
	Revision     int               `json:"revision"`
	Owner        string            `json:"owner"`
	Text         string            `json:"text"`
	Constraints  string            `json:"constraints"`
	Criteria     []string          `json:"criteria"`
	State        string            `json:"state"`
	Reason       string            `json:"reason,omitempty"`
	Progress     string            `json:"progress,omitempty"`
	Next         string            `json:"next,omitempty"`
	Evidence     []goalEvidence    `json:"evidence,omitempty"`
	Started      time.Time         `json:"started"`
	Updated      time.Time         `json:"updated"`
	StartSeq     int               `json:"start_seq"`
	Turns        int               `json:"turns"`
	MaxTurns     int               `json:"max_turns"`
	MaxTokens    int64             `json:"max_tokens"`
	MaxSeconds   int               `json:"max_seconds"`
	UsedTokens   int64             `json:"used_tokens"`
	UsageUnknown bool              `json:"usage_unknown,omitempty"`
	ReportTurn   string            `json:"report_turn,omitempty"`
	ReportCall   string            `json:"report_call,omitempty"`
	NoProgress   int               `json:"no_progress"`
	History      []*continuousGoal `json:"history,omitempty"`
}

func (r *run) goalUsageLocked(g *continuousGoal) (int64, bool) {
	var total int64
	unknown := false
	for _, e := range r.events {
		if e.Seq <= g.StartSeq || e.Kind != "response" {
			continue
		}
		var p struct {
			Usage *struct {
				Total  int64 `json:"total_tokens"`
				Input  int64 `json:"prompt_tokens"`
				Output int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(e.Body, &p) != nil || p.Usage == nil {
			if e.Status == 200 || e.Status == 499 || e.Status == 502 {
				unknown = true
			}
			continue
		}
		n := p.Usage.Total
		if n == 0 {
			n = p.Usage.Input + p.Usage.Output
		}
		total += max(n, 0)
	}
	return total, unknown
}
func (r *run) goalViewLocked() *continuousGoal {
	if r.continuous == nil {
		return nil
	}
	g := *r.continuous
	g.History = nil
	g.UsedTokens, g.UsageUnknown = r.goalUsageLocked(&g)
	if g.State == "running" && (len(r.asks) > 0 || len(r.toolWaits) > 0 || len(r.approvals) > 0) {
		g.State = "waiting_user"
		g.Reason = "等待实际审批或用户回答；没有自动续轮"
	}
	return &g
}
func goalExpired(g *continuousGoal) bool {
	return g.MaxSeconds > 0 && time.Since(g.Started) >= time.Duration(g.MaxSeconds)*time.Second
}
func goalActive(g *continuousGoal) bool {
	return g != nil && (g.State == "running" || g.State == "pausing")
}
func (c *conn) continuousGet(m protocol.Message) (any, *protocol.Error) { return c.planGet(m) }
func (c *conn) continuousUpdate(m protocol.Message) (any, *protocol.Error) {
	return c.requestOnce(m, (*conn).continuousUpdateOnce)
}
func (c *conn) continuousUpdateOnce(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID    string   `json:"threadId"`
		Action      string   `json:"action"`
		Revision    int      `json:"revision"`
		Text        string   `json:"text"`
		Constraints string   `json:"constraints"`
		Criteria    []string `json:"criteria"`
		MaxTurns    int      `json:"max_turns"`
		MaxTokens   int64    `json:"max_tokens"`
		MaxSeconds  int      `json:"max_seconds"`
		RequestID   string   `json:"request_id"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	if !requestIDPattern.MatchString(p.RequestID) {
		return nil, bad(400, "需要有效 request_id")
	}
	if !slices.Contains([]string{"start", "pause", "resume", "modify", "end"}, p.Action) {
		return nil, bad(400, "Goal 操作无效")
	}
	if p.Action == "start" || p.Action == "modify" {
		if strings.TrimSpace(p.Text) == "" || len(p.Text) > 10000 || len(p.Constraints) > 10000 || len(p.Criteria) == 0 || len(p.Criteria) > 20 || p.MaxTurns < 0 || p.MaxTurns > 100 || p.MaxTokens < 0 || p.MaxTokens > 1000000000 || p.MaxSeconds < 0 || p.MaxSeconds > 604800 {
			return nil, bad(400, "目标、完成标准或预算无效；轮次最多100，时间最多7天")
		}
		for _, criterion := range p.Criteria {
			if strings.TrimSpace(criterion) == "" || len(criterion) > 2000 {
				return nil, bad(400, "完成标准不能为空且每项最多2000字节")
			}
		}
	}
	r, ferr := c.s.threadOf(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	if err := audit.Write(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_continuous_" + p.Action, Conversation: r.ID, Target: p.Action}); err != nil {
		return nil, bad(500, "审计保存失败，Goal未改变")
	}
	ferr = c.s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		old := meta.Continuous
		rev := 0
		if old != nil {
			rev = old.Revision
		}
		if p.Revision != rev {
			return bad(409, "Goal版本已变化，请重新读取")
		}
		var g continuousGoal
		if p.Action == "start" {
			if !r.canContinue() || r.Source != "" {
				return bad(409, "请先选择可续聊且已结束当前轮的任务")
			}
			if old != nil && !slices.Contains([]string{"completed", "ended"}, old.State) {
				return bad(409, "已有未结束Goal，请恢复、修改或结束它")
			}
			g = continuousGoal{ID: uuid.NewString(), Owner: userName(m), Started: time.Now().UTC(), StartSeq: r.seq, State: "running"}
			if old != nil {
				copy := *r.goalViewLocked()
				copy.History = nil
				g.History = append(slices.Clone(old.History), &copy)
				if len(g.History) > 20 {
					g.History = g.History[len(g.History)-20:]
				}
			}
		} else {
			if old == nil {
				return bad(409, "尚未显式启动Goal")
			}
			g = *old
		}
		switch p.Action {
		case "start", "modify":
			if p.Action == "modify" && !r.Done && (p.MaxTokens != g.MaxTokens || p.MaxSeconds != g.MaxSeconds || p.MaxTurns != g.MaxTurns) {
				return bad(409, "运行中只能修改目标与标准，调整预算请先暂停并等当前轮结束")
			}
			g.Text, g.Constraints, g.Criteria = p.Text, p.Constraints, slices.Clone(p.Criteria)
			g.MaxTokens, g.MaxSeconds, g.MaxTurns = p.MaxTokens, p.MaxSeconds, p.MaxTurns
			g.ReportTurn, g.ReportCall = "", ""
			g.Evidence = nil
			g.NoProgress = 0
			if p.Action == "modify" && slices.Contains([]string{"completed", "ended"}, g.State) {
				return bad(409, "已结束Goal请显式开始新的Goal")
			}
		case "pause":
			if g.State == "completed" || g.State == "ended" {
				return bad(409, "Goal已经结束")
			}
			g.State = "paused"
			if !r.Done {
				g.State = "pausing"
			}
			g.Reason = "用户暂停"
		case "resume":
			if !r.canContinue() {
				return bad(409, "等待当前轮安全结束后再恢复")
			}
			if g.State == "completed" || g.State == "ended" {
				return bad(409, "Goal已结束，不能恢复")
			}
			g.State, g.Reason, g.ReportTurn, g.ReportCall = "running", "", "", ""
		case "end":
			g.State, g.Reason = "ended", "用户结束，目标未完成"
		}
		g.Revision = rev + 1
		g.Updated = time.Now().UTC()
		meta.Continuous = &g
		meta.Queue = slices.DeleteFunc(slices.Clone(meta.Queue), func(q queueItem) bool { return q.GoalID != "" })
		meta.QueueVersion++
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	c.s.status(r.ID, r.liveStatus())
	if p.Action == "pause" || p.Action == "end" {
		r.mu.Lock()
		pc := r.conn
		r.mu.Unlock()
		if pc != nil {
			ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
			pc.Call(ctx, "turn/interrupt", nil, nil)
			cancel()
		}
	}
	if p.Action == "start" || p.Action == "resume" {
		r.mu.Lock()
		r.stopping = false
		r.mu.Unlock()
		go c.s.followUp(r)
	}
	return r.taskStatus(), nil
}
func (r *run) reportGoal(callID string, arguments json.RawMessage) (any, *protocol.Error) {
	var p struct {
		ID       string         `json:"id"`
		Revision int            `json:"revision"`
		State    string         `json:"state"`
		Progress string         `json:"progress"`
		Next     string         `json:"next"`
		Reason   string         `json:"reason"`
		Evidence []goalEvidence `json:"evidence"`
	}
	if json.Unmarshal(arguments, &p) != nil || !slices.Contains([]string{"continue", "blocked", "waiting_user", "complete"}, p.State) || len(p.Progress) > 8000 || len(p.Next) > 8000 || len(p.Reason) > 8000 || len(p.Evidence) > 40 {
		return nil, bad(400, "Goal报告格式无效")
	}
	ferr := r.s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		if meta.Continuous == nil || meta.Continuous.ID != p.ID || meta.Continuous.Revision != p.Revision {
			return bad(409, "Goal身份或版本已改变，请重新读取task_status")
		}
		g := *meta.Continuous
		if callID != "" && g.ReportCall == callID {
			return nil
		}
		if g.State != "running" {
			return bad(409, "Goal不在运行状态，不接受旧报告")
		}
		if p.State == "complete" {
			seen := map[int]bool{}
			for i, proof := range p.Evidence {
				if proof.Criterion < 1 || proof.Criterion > len(g.Criteria) || strings.TrimSpace(proof.Detail) == "" || len(proof.Detail) > 4000 {
					return bad(400, "每条证据需要有效完成标准编号和说明")
				}
				found := false
				for _, e := range r.events {
					if e.Seq <= g.StartSeq || e.Method != "item/completed" || e.Sub {
						continue
					}
					var body struct {
						Item struct {
							ID   string `json:"id"`
							Type string `json:"type"`
							Data struct {
								Observation struct {
									Status string `json:"status"`
								} `json:"observation"`
							} `json:"data"`
						} `json:"item"`
					}
					if json.Unmarshal(e.Body, &body) == nil && body.Item.Type == "toolCall" && body.Item.ID == proof.ToolCallID && body.Item.Data.Observation.Status == "ok" {
						p.Evidence[i].Seq = e.Seq
						p.Evidence[i].Hash = fmt.Sprintf("%x", sha256.Sum256(e.Body))
						found = true
						break
					}
				}
				if !found {
					return bad(400, "证据不是本Goal期间真实成功的工具调用："+proof.ToolCallID)
				}
				seen[proof.Criterion] = true
			}
			if len(seen) != len(g.Criteria) {
				return bad(400, "缺少完成标准对应的真实工具证据，不能完成Goal")
			}
			g.State = "completed"
			g.Evidence = slices.Clone(p.Evidence)
			g.Reason = "完成标准已附真实调用证据；请核对语义与结果"
		} else if p.State == "continue" {
			if strings.TrimSpace(p.Progress) == "" || strings.TrimSpace(p.Next) == "" {
				return bad(400, "继续报告需要进展和下一步")
			}
			if p.Progress == g.Progress {
				g.NoProgress++
			} else {
				g.NoProgress = 0
			}
			if g.NoProgress >= 3 {
				g.State, g.Reason = "blocked", "连续三轮未报告新的进展"
			}
		} else {
			if strings.TrimSpace(p.Reason) == "" {
				return bad(400, "受阻/等待需要说明原因")
			}
			g.State, g.Reason = p.State, p.Reason
		}
		g.Progress, g.Next = p.Progress, p.Next
		g.ReportTurn = r.lastCheckpoint()
		g.ReportCall = callID
		g.Updated = time.Now().UTC()
		meta.Continuous = &g
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	r.s.status(r.ID, r.liveStatus())
	return r.taskStatus(), nil
}

// 调用方持turnMu。Goal项也经现有队列领取和queue_id启动日志，故重启不另开调度所有者。
func (s *Server) continueGoalLocked(r *run, normal bool) {
	r.mu.Lock()
	exists := r.continuous != nil
	r.mu.Unlock()
	if !exists {
		return
	}
	enqueue := false
	ferr := s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		if meta.Continuous == nil {
			return nil
		}
		g := *meta.Continuous
		if g.State == "pausing" {
			g.State = "paused"
			meta.Continuous = &g
			return nil
		}
		if g.State != "running" {
			return nil
		}
		g.UsedTokens, g.UsageUnknown = r.goalUsageLocked(&g)
		reason := ""
		var latest event
		for i := len(r.events) - 1; i >= 0; i-- {
			if r.events[i].Kind == "start" || r.events[i].Kind == "continue" {
				latest = r.events[i]
				break
			}
		}
		if latest.GoalID != g.ID || latest.GoalRevision != g.Revision {
			normal = true
		}
		if !normal {
			g.State = "blocked"
			reason = "上一轮异常结束，请核对历史后显式恢复"
		} else if goalExpired(&g) || g.MaxTokens > 0 && (g.UsedTokens >= g.MaxTokens || g.UsageUnknown) || g.Turns >= 100 || g.MaxTurns > 0 && g.Turns >= g.MaxTurns {
			g.State = "exhausted"
			reason = "预算已到达或用量未知，自动续轮已停止"
		} else if meta.QueueClaim != nil || len(meta.Queue) > 0 {
			g.State = "waiting_user"
			reason = "用户队列或领取记录待处理，Goal没有越过它继续"
		} else {
			var start event
			for i := len(r.events) - 1; i >= 0; i-- {
				if r.events[i].Kind == "start" || r.events[i].Kind == "continue" {
					start = r.events[i]
					break
				}
			}
			if start.GoalID == g.ID && start.GoalRevision == g.Revision && strings.HasPrefix(start.QueueID, "goal-"+g.ID+"-") && g.ReportTurn != r.lastCheckpoint() {
				g.State = "blocked"
				reason = "本轮没有当前Goal版本的进展报告，不自动空转"
			} else {
				g.Turns++
				item := queueItem{ID: fmt.Sprintf("goal-%s-%d-%d", g.ID, g.Revision, g.Turns), Text: "继续当前持续Goal。先调用task_status核对目标版本和完成标准；推进实际工作，结束本轮前用report_goal报告进展、受阻原因或完成证据。", At: time.Now().UTC(), User: g.Owner, GoalID: g.ID, GoalRevision: g.Revision}
				meta.Queue = append(meta.Queue, item)
				meta.QueuePaused = false
				meta.QueueVersion++
				enqueue = true
			}
		}
		if reason != "" {
			g.Reason = reason
		}
		g.Updated = time.Now().UTC()
		meta.Continuous = &g
		return nil
	})
	if ferr != nil {
		return
	}
	s.status(r.ID, r.liveStatus())
	if enqueue {
		s.deliverQueueLocked(r, "", nil, false, true, "", "")
	}
}
func (s *Server) watchGoals() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.mu.Lock()
			runs := slices.Clone(s.runs)
			s.mu.Unlock()
			for _, r := range runs {
				r.mu.Lock()
				expired := goalActive(r.continuous) && goalExpired(r.continuous)
				r.mu.Unlock()
				if !expired {
					continue
				}
				r.turnMu.Lock()
				goalID := ""
				ferr := s.updateMeta(r, func(meta *runMeta) *protocol.Error {
					if !goalActive(meta.Continuous) || !goalExpired(meta.Continuous) {
						return nil
					}
					g := *meta.Continuous
					goalID = g.ID
					g.State, g.Reason = "exhausted", "墙钟时间预算已到达"
					g.Updated = time.Now().UTC()
					meta.Continuous = &g
					return nil
				})
				r.mu.Lock()
				pc := r.conn
				owned := false
				for i := len(r.events) - 1; i >= 0; i-- {
					if r.events[i].Kind == "start" || r.events[i].Kind == "continue" {
						owned = goalID != "" && r.events[i].GoalID == goalID
						break
					}
				}
				r.mu.Unlock()
				if ferr == nil && owned && pc != nil {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					pc.Call(ctx, "turn/interrupt", nil, nil)
					cancel()
				}
				r.turnMu.Unlock()
				s.status(r.ID, r.liveStatus())
			}
		}
	}
}

func (s *Server) budgetStopped(r *run, g continuousGoal, reason string) {
	s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		if meta.Continuous != nil && meta.Continuous.ID == g.ID && meta.Continuous.Revision == g.Revision && meta.Continuous.State == "running" {
			next := *meta.Continuous
			next.State, next.Reason = "exhausted", reason
			next.Updated = time.Now().UTC()
			meta.Continuous = &next
		}
		return nil
	})
	s.status(r.ID, r.liveStatus())
}

// 有token预算时每次请求在同一任务内串行预留，避免父子同时越过剩余预算。
func (s *Server) goalProxy(r *run, req *http.Request, body []byte) (*http.Request, []byte, func(), error) {
	r.mu.Lock()
	g := r.continuous
	active := false
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Kind == "start" || r.events[i].Kind == "continue" {
			active = g != nil && r.events[i].GoalID == g.ID
			break
		}
	}
	r.mu.Unlock()
	if !active {
		return req, body, func() {}, nil
	}
	// 只有 token 预算要在请求间串行预留；没设时不排队，并行的子 agent 照常并行。
	r.mu.Lock()
	budget := r.continuous.MaxTokens > 0
	r.mu.Unlock()
	release := func() {}
	if budget {
		r.goalModelMu.Lock()
		release = r.goalModelMu.Unlock
	}
	r.mu.Lock()
	view := *r.continuous
	spent, unknown := r.goalUsageLocked(&view)
	r.mu.Unlock()
	if slices.Contains([]string{"pausing", "paused", "exhausted", "ended"}, view.State) || goalExpired(&view) {
		release()
		return req, body, func() {}, fmt.Errorf("Goal已暂停、结束或时间预算耗尽")
	}
	if view.MaxTokens > 0 {
		var data map[string]json.RawMessage
		if json.Unmarshal(body, &data) != nil {
			release()
			return req, body, func() {}, fmt.Errorf("无法核对模型请求预算")
		}
		var messages []struct {
			Content json.RawMessage `json:"content"`
		}
		json.Unmarshal(data["messages"], &messages)
		for _, message := range messages {
			var parts []struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(message.Content, &parts) == nil {
				for _, part := range parts {
					if strings.Contains(part.Type, "image") {
						s.budgetStopped(r, view, "图像token上界不可核验，请明确调整预算方式后恢复")
						release()
						return req, body, func() {}, fmt.Errorf("图像token上界未知，不能在硬token预算下发起此请求")
					}
				}
			}
		}
		bound := int64(len(body) + 4096 + len(messages)*64)
		left := view.MaxTokens - spent - bound
		if unknown || left < 256 {
			s.budgetStopped(r, view, "剩余token不足以覆盖保守输入上界和输出，或历史用量未知")
			release()
			return req, body, func() {}, fmt.Errorf("Goal剩余token不足以覆盖保守输入上界与输出，或已有用量未知")
		}
		maximum := int64(4096)
		if existing := data["max_completion_tokens"]; len(existing) > 0 {
			json.Unmarshal(existing, &maximum)
		}
		maximum = min(max(maximum, 1), left)
		data["max_completion_tokens"], _ = json.Marshal(maximum)
		body, _ = json.Marshal(data)
	}
	if view.MaxSeconds > 0 {
		ctx, cancel := context.WithDeadline(req.Context(), view.Started.Add(time.Duration(view.MaxSeconds)*time.Second))
		req = req.WithContext(ctx)
		unlock := release
		release = func() { cancel(); unlock() }
	}
	return req, body, release, nil
}
