package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/agent"
	"github.com/xuzhao123/tern/internal/protocol"
)

type managedSteer struct {
	ID    string    `json:"id"`
	User  string    `json:"user"`
	Hash  string    `json:"hash"`
	Text  string    `json:"text,omitempty"`
	Turn  int       `json:"turn"`
	State string    `json:"state"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
	Force bool      `json:"force,omitempty"`
}

func steerPending(state string) bool {
	return state == "received" || state == "sending" || state == "accepted" || state == "unknown" || state == "deferred" || state == "unconsumed"
}
func pendingManagedSteers(items []managedSteer) []managedSteer {
	out := []managedSteer{}
	for _, item := range items {
		if steerPending(item.State) || item.State == "blocked" {
			out = append(out, item)
		}
	}
	return out
}
func (r *run) steerTurnLocked() int {
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Kind == "start" || r.events[i].Kind == "continue" {
			return r.events[i].Seq
		}
	}
	return 0
}

// 仅人明确释放了提交前失败的转交项，才允许下一次主动发送回到已知前序上下文。
func (r *run) releasedSteerResumeLocked() string {
	for i := len(r.events) - 1; i >= 0; i-- {
		e := r.events[i]
		if e.Kind != "start" && e.Kind != "continue" {
			continue
		}
		if len(e.SteerIDs) == 0 || e.ResumeFrom == "" {
			return ""
		}
		for _, id := range e.SteerIDs {
			if !slices.ContainsFunc(r.managedSteers, func(item managedSteer) bool { return item.ID == id && item.State == "released" }) {
				return ""
			}
		}
		return e.ResumeFrom
	}
	return ""
}
func (s *Server) updateManagedMeta(r *run, change func(*runMeta) *protocol.Error) *protocol.Error {
	ferr := s.updateMeta(r, change)
	r.mu.Lock()
	if ferr == nil {
		r.steerError = ""
	} else if ferr.Code >= 500 {
		r.steerError = "补充状态未能保存，后续派发等待核对：" + ferr.Message
	}
	r.mu.Unlock()
	return ferr
}
func (c *conn) managedSteer(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID  string `json:"threadId"`
		Text      string `json:"text"`
		ID        string `json:"client_id"`
		RequestID string `json:"request_id"`
		Turn      int    `json:"turn_seq"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if input.ID != input.RequestID || !requestIDPattern.MatchString(input.ID) {
		return nil, bad(400, "可靠补充需要相同的 client_id 和 request_id")
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.Text == "" {
		return nil, bad(400, "需要非空的 text")
	}
	r, ferr := c.threadToQueue(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	item := managedSteer{ID: input.ID, User: userName(m), Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(input.Text))), Text: input.Text, Turn: input.Turn, State: "received", At: time.Now().UTC()}
	ferr = c.s.updateManagedMeta(r, func(meta *runMeta) *protocol.Error {
		for _, old := range meta.ManagedSteers {
			if old.ID == item.ID {
				if old.Hash != item.Hash || old.User != item.User || old.Turn != item.Turn {
					return bad(409, "client_id 已用于另一条补充")
				}
				return nil
			}
		}
		if item.Turn <= 0 || item.Turn != r.steerTurnLocked() {
			return bad(409, "任务已进入另一轮，请核对当前内容后重新发送补充")
		}
		meta.ManagedSteers = append(meta.ManagedSteers, item)
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	c.s.status(r.ID, r.liveStatus())
	go c.s.flushManagedSteers(r)
	return map[string]any{"id": item.ID, "managed": true, "state": "received"}, nil
}

// 重连查询优先；只有新 agent 明确支持按 ID 去重，才重新传输同一条补充。
func (s *Server) flushManagedSteers(r *run) {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	select {
	case <-s.done:
		return
	default:
	}
	r.mu.Lock()
	turn, done, conn := r.steerTurnLocked(), r.Done, r.conn
	items := []managedSteer{}
	ids := []string{}
	for _, item := range r.managedSteers {
		if item.Turn == turn && steerPending(item.State) {
			items = append(items, item)
			ids = append(ids, item.ID)
		}
	}
	r.mu.Unlock()
	if done {
		if len(items) > 0 {
			go s.followUp(r)
		}
		return
	}
	if conn == nil || len(items) == 0 {
		return
	}
	retry := false
	defer func() {
		if retry {
			time.AfterFunc(time.Second, func() {
				select {
				case <-s.done:
					return
				default:
					go s.flushManagedSteers(r)
				}
			})
		}
	}()
	var status struct {
		Managed bool                          `json:"managed"`
		Closed  bool                          `json:"closed"`
		Items   map[string]agent.SteerReceipt `json:"items"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := conn.Call(ctx, "turn/steer/status", map[string]any{"ids": ids}, &status)
	cancel()
	if err != nil {
		var rpc *protocol.Error
		if errors.As(err, &rpc) && rpc.Code == -32601 {
			s.updateManagedMeta(r, func(meta *runMeta) *protocol.Error {
				for i := range meta.ManagedSteers {
					item := &meta.ManagedSteers[i]
					if item.Turn == turn && item.State == "received" {
						item.State = "deferred"
						item.Error = "当前运行版本不支持可靠补充，将安排到下一轮"
					}
				}
				return nil
			})
			s.status(r.ID, r.liveStatus())
			return
		}
		retry = true
		return
	}
	if !status.Managed {
		return
	}
	for _, item := range items {
		if receipt, ok := status.Items[item.ID]; ok {
			if receipt.Hash != item.Hash {
				s.setManagedState(r, item.ID, "unknown", "agent 回执与原请求不一致，请核对")
				continue
			}
			switch receipt.State {
			case "consumed", "blocked", "unconsumed":
				s.setManagedState(r, item.ID, receipt.State, "")
			default:
				s.setManagedState(r, item.ID, "accepted", "")
			}
			continue
		}
		if status.Closed {
			s.setManagedState(r, item.ID, "unconsumed", "")
			continue
		}
		send := false
		ferr := s.updateManagedMeta(r, func(meta *runMeta) *protocol.Error {
			if r.Done || r.steerTurnLocked() != turn || r.conn != conn {
				return nil
			}
			for i := range meta.ManagedSteers {
				current := &meta.ManagedSteers[i]
				if current.ID == item.ID && steerPending(current.State) {
					current.State = "sending"
					current.Error = ""
					send = true
					break
				}
			}
			return nil
		})
		if ferr != nil {
			retry = true
			continue
		}
		if !send {
			continue
		}
		var response struct {
			State string `json:"state"`
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := conn.Call(ctx, "turn/steer", map[string]any{"text": item.Text, "client_id": item.ID, "managed": true}, &response)
		cancel()
		if err != nil {
			var rpc *protocol.Error
			if errors.As(err, &rpc) && rpc.Code == -32000 {
				s.setManagedState(r, item.ID, "unconsumed", "")
			} else {
				s.setManagedState(r, item.ID, "unknown", "送达结果待确认，正在按 ID 核对")
				retry = true
			}
		} else if response.State == "consumed" || response.State == "blocked" {
			s.setManagedState(r, item.ID, response.State, "")
		} else {
			s.setManagedState(r, item.ID, "accepted", "")
		}
	}
}
func (s *Server) setManagedState(r *run, id, state, reason string) {
	s.updateManagedMeta(r, func(meta *runMeta) *protocol.Error {
		for i := range meta.ManagedSteers {
			item := &meta.ManagedSteers[i]
			if item.ID != id || !steerPending(item.State) {
				continue
			}
			if (r.Done || item.Turn != r.steerTurnLocked()) && (state == "accepted" || state == "sending") {
				continue
			}
			item.State, item.Error = state, reason
			if state == "consumed" || state == "blocked" {
				item.Text = ""
				item.Force = false
			}
		}
		return nil
	})
	s.status(r.ID, r.liveStatus())
}
func (s *Server) observeManagedSteer(r *run, m protocol.Message) {
	if m.Method == "item/completed" {
		var p struct {
			Item struct {
				Type string `json:"type"`
				Data struct {
					ID      string `json:"client_id"`
					Managed bool   `json:"managed"`
					Steer   bool   `json:"steer"`
					State   string `json:"state"`
				} `json:"data"`
			} `json:"item"`
		}
		if json.Unmarshal(m.Params, &p) == nil && p.Item.Type == "userMessage" && p.Item.Data.Managed && (p.Item.Data.State == "consumed" || p.Item.Data.State == "blocked") {
			s.setManagedState(r, p.Item.Data.ID, p.Item.Data.State, "")
		}
		if p.Item.Type == "userMessage" && !p.Item.Data.Steer {
			go s.confirmManagedQueue(r)
		}
	} else if m.Method == "turn/completed" {
		var p struct {
			IDs []string `json:"queued_inputs"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			for _, id := range p.IDs {
				s.setManagedState(r, id, "unconsumed", "")
			}
		}
	}
}

type steerEvidence struct {
	Done       bool
	Blocked    bool
	Submitted  bool
	Checkpoint string
	Unused     map[string]bool
	Applied    map[string]string
	Receipts   map[string]agent.SteerReceipt
}

func (r *run) steerEvidenceLocked(turn int) steerEvidence {
	evidence := steerEvidence{Unused: map[string]bool{}, Applied: map[string]string{}}
	active := false
	for _, e := range r.events {
		if e.Seq == turn {
			active = true
			continue
		}
		if !active {
			continue
		}
		if e.Kind == "start" || e.Kind == "continue" {
			break
		}
		if e.Method == "turn/started" {
			var p struct {
				ID string `json:"turn_id"`
			}
			if json.Unmarshal(e.Body, &p) == nil {
				evidence.Checkpoint = p.ID
			}
		}
		if e.Kind == "request" && !e.Sub && evidence.Checkpoint == "" {
			evidence.Checkpoint = e.Task
		}
		if e.Kind == "request" && !e.Sub && e.Purpose == "" {
			evidence.Submitted = true
		}
		if e.Method == "turn/completed" {
			var p struct {
				IDs     []string `json:"queued_inputs"`
				Outcome string   `json:"outcome"`
			}
			if json.Unmarshal(e.Body, &p) == nil {
				evidence.Blocked = p.Outcome == "hook_blocked"
				for _, id := range p.IDs {
					evidence.Unused[id] = true
				}
			}
		}
		if e.Method == "item/completed" {
			var p struct {
				Item struct {
					Type string `json:"type"`
					Data struct {
						ID      string `json:"client_id"`
						Managed bool   `json:"managed"`
						Steer   bool   `json:"steer"`
						State   string `json:"state"`
					} `json:"data"`
				} `json:"item"`
			}
			if json.Unmarshal(e.Body, &p) == nil && p.Item.Type == "userMessage" && p.Item.Data.Managed && (p.Item.Data.State == "consumed" || p.Item.Data.State == "blocked") {
				evidence.Applied[p.Item.Data.ID] = p.Item.Data.State
			}
			if p.Item.Type == "userMessage" && !p.Item.Data.Steer {
				evidence.Submitted = true
			}
		}
		if e.Kind == "exit" {
			evidence.Done = true
		}
	}
	if evidence.Done && evidence.Checkpoint != "" {
		if cp, err := agent.LoadCheckpointIn("", evidence.Checkpoint); err == nil {
			evidence.Receipts = cp.State.ManagedSteers
			evidence.Blocked = evidence.Blocked || cp.Reason == "hook_blocked"
			evidence.Submitted = evidence.Submitted || cp.Calls > 0
		}
	}
	return evidence
}
func managedQueueItem(items []managedSteer, force bool) queueItem {
	ids, texts := []string{}, []string{}
	for _, item := range items {
		ids = append(ids, item.ID)
		texts = append(texts, item.Text)
	}
	sum := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return queueItem{ID: fmt.Sprintf("steer-%x", sum[:16]), Text: strings.Join(texts, "\n\n"), At: items[0].At, User: items[0].User, Immediate: force, SteerIDs: ids}
}

// 调用方持 turnMu；证据不足时保留 unknown，不把“没有看到”当成“没有执行”。
func (s *Server) reconcileManagedSteers(r *run) *protocol.Error {
	if ferr := s.confirmManagedQueueLocked(r); ferr != nil {
		return ferr
	}
	r.mu.Lock()
	needed := slices.ContainsFunc(r.managedSteers, func(item managedSteer) bool { return steerPending(item.State) || item.State == "queued" })
	r.mu.Unlock()
	if !needed {
		return nil
	}
	ferr := s.updateManagedMeta(r, func(meta *runMeta) *protocol.Error {
		evidence := map[int]steerEvidence{}
		groups := map[string][]managedSteer{}
		order := []string{}
		started := map[string]bool{}
		for _, e := range r.events {
			if e.QueueID != "" {
				started[e.QueueID] = true
			}
		}
		for i := range meta.ManagedSteers {
			item := &meta.ManagedSteers[i]
			if item.State == "queued" {
				for _, e := range r.events {
					if (e.Kind != "start" && e.Kind != "continue") || !slices.Contains(e.SteerIDs, item.ID) {
						continue
					}
					proof, ok := evidence[e.Seq]
					if !ok {
						proof = r.steerEvidenceLocked(e.Seq)
						evidence[e.Seq] = proof
					}
					if proof.Blocked {
						item.State = "blocked"
					} else if proof.Submitted {
						item.State = "delivered"
					}
				}
				continue
			}
			if !steerPending(item.State) {
				continue
			}
			proof, ok := evidence[item.Turn]
			if !ok {
				proof = r.steerEvidenceLocked(item.Turn)
				evidence[item.Turn] = proof
			}
			if state, ok := proof.Applied[item.ID]; ok {
				item.State, item.Text, item.Error, item.Force = state, "", "", false
				continue
			}
			if !proof.Done {
				continue
			}
			if receipt, ok := proof.Receipts[item.ID]; ok && receipt.Hash == item.Hash && (receipt.State == "consumed" || receipt.State == "blocked") {
				item.State, item.Text, item.Error, item.Force = receipt.State, "", "", false
				continue
			}
			if item.State == "received" || item.State == "deferred" || item.State == "unconsumed" || proof.Unused[item.ID] {
				key := fmt.Sprintf("%d:%t", item.Turn, item.Force)
				if _, ok := groups[key]; !ok {
					order = append(order, key)
				}
				groups[key] = append(groups[key], *item)
			} else {
				item.State = "unknown"
				item.Error = "本轮已结束，无法确认补充是否已进入上下文；请核对记录，不会自动重发"
			}
		}
		pending := []queueItem{}
		for _, key := range order {
			items := groups[key]
			q := managedQueueItem(items, items[0].Force)
			exists := started[q.ID] || slices.ContainsFunc(meta.Queue, func(item queueItem) bool { return item.ID == q.ID }) || (meta.QueueClaim != nil && meta.QueueClaim.ID == q.ID)
			if !exists {
				pending = append(pending, q)
			}
			ids := map[string]bool{}
			for _, item := range items {
				ids[item.ID] = true
			}
			for i := range meta.ManagedSteers {
				if ids[meta.ManagedSteers[i].ID] {
					meta.ManagedSteers[i].State = "queued"
					meta.ManagedSteers[i].Text = ""
					meta.ManagedSteers[i].Error = ""
					meta.ManagedSteers[i].Force = false
				}
			}
		}
		if len(pending) > 0 {
			slices.SortStableFunc(pending, func(a, b queueItem) int {
				if a.Immediate && !b.Immediate {
					return -1
				}
				if !a.Immediate && b.Immediate {
					return 1
				}
				return 0
			})
			meta.Queue = append(pending, meta.Queue...)
			meta.QueueVersion++
		}
		return nil
	})
	s.status(r.ID, r.liveStatus())
	return ferr
}
func (c *conn) refreshManagedSteer(m protocol.Message) (any, *protocol.Error) {
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
	c.s.flushManagedSteers(r)
	return r.summary(), nil
}
func (c *conn) resolveManagedSteer(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		ID       string `json:"id"`
		Action   string `json:"action"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if input.Action != "queue" && input.Action != "discard" {
		return nil, bad(400, "action 取 queue 或 discard")
	}
	r, ferr := c.s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	if ferr = c.s.reconcileManagedSteers(r); ferr != nil {
		return nil, ferr
	}
	ferr = c.s.updateManagedMeta(r, func(meta *runMeta) *protocol.Error {
		i := slices.IndexFunc(meta.ManagedSteers, func(item managedSteer) bool { return item.ID == input.ID })
		if i < 0 {
			return bad(404, "找不到这条补充")
		}
		item := &meta.ManagedSteers[i]
		if item.State != "unknown" && item.State != "blocked" {
			return bad(409, "补充状态已变化，请重新核对")
		}
		if item.State == "unknown" && !r.steerEvidenceLocked(item.Turn).Done {
			return bad(409, "当前轮仍可能采纳这条补充，请等待确认")
		}
		if input.Action == "queue" {
			if item.State == "blocked" {
				return bad(409, "被规则拦下的补充不能直接重发")
			}
			q := managedQueueItem([]managedSteer{*item}, false)
			meta.Queue = append(meta.Queue, q)
			meta.QueuePaused = true
			meta.QueueVersion++
			item.State = "queued"
		} else {
			item.State = "discarded"
		}
		item.Text, item.Error, item.Force = "", "", false
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	c.s.status(r.ID, r.liveStatus())
	return r.summary(), nil
}
