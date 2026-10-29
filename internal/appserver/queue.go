package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xuzhao123/tern/internal/protocol"
)

type queueInput struct {
	ThreadID  string   `json:"threadId"`
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	IDs       []string `json:"ids"`
	Version   *uint64  `json:"version"`
	All       bool     `json:"all"`
	Action    string   `json:"action"`
	RequestID string   `json:"request_id"`
}

func queueVersion(meta *runMeta, version *uint64, required bool) *protocol.Error {
	if version == nil && required {
		return bad(400, "需要当前队列 version")
	}
	if version != nil && *version != meta.QueueVersion {
		return bad(409, "队列已被另一页面或执行过程修改，请核对最新内容后重试")
	}
	return nil
}

func (c *conn) threadQueueAdd(m protocol.Message) (any, *protocol.Error) {
	var input queueInput
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.threadToQueue(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.Text == "" {
		return nil, bad(400, "请填写要排队的消息")
	}
	item := queueItem{ID: uuid.NewString(), Text: input.Text, At: time.Now().UTC(), User: userName(m), RequestID: input.RequestID}
	ferr = c.s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		if r.Done || (r.proc == nil && r.conn == nil) {
			return bad(409, "这条对话没在跑，直接发送就行")
		}
		meta.Queue = append(meta.Queue, item)
		meta.QueueVersion++
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	c.s.status(r.ID, r.liveStatus())
	return item, nil
}

func (c *conn) threadQueueUpdate(m protocol.Message) (any, *protocol.Error) {
	return c.changeQueue(m, "update")
}
func (c *conn) threadQueueDelete(m protocol.Message) (any, *protocol.Error) {
	return c.changeQueue(m, "delete")
}
func (c *conn) threadQueueReorder(m protocol.Message) (any, *protocol.Error) {
	return c.changeQueue(m, "reorder")
}
func (c *conn) changeQueue(m protocol.Message, operation string) (any, *protocol.Error) {
	var input queueInput
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.threadToQueue(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	input.Text = strings.TrimSpace(input.Text)
	if operation == "update" && input.Text == "" {
		return nil, bad(400, "排队消息不能为空")
	}
	ferr = c.s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		if ferr := queueVersion(meta, input.Version, operation != "delete"); ferr != nil {
			return ferr
		}
		if operation == "reorder" {
			if len(input.IDs) != len(meta.Queue) {
				return bad(400, "排序必须包含全部待发项且不能重复")
			}
			order := make([]queueItem, 0, len(input.IDs))
			seen := map[string]bool{}
			for _, id := range input.IDs {
				i := slices.IndexFunc(meta.Queue, func(q queueItem) bool { return q.ID == id })
				if seen[id] || i < 0 {
					return bad(400, "排序包含重复、未知或已领取的项")
				}
				seen[id] = true
				order = append(order, meta.Queue[i])
			}
			meta.Queue = order
		} else {
			if meta.QueueClaim != nil && meta.QueueClaim.ID == input.ID {
				return bad(409, "这一项已被领取，不能再修改")
			}
			i := slices.IndexFunc(meta.Queue, func(q queueItem) bool { return q.ID == input.ID })
			if i < 0 {
				return bad(404, "队列里没有这一项，可能已经被删掉或发出")
			}
			if operation == "update" {
				meta.Queue[i].Text = input.Text
			} else {
				meta.Queue = slices.Delete(meta.Queue, i, i+1)
			}
		}
		meta.QueueVersion++
		if len(meta.Queue) == 0 && meta.QueueClaim == nil {
			meta.QueueError = ""
		}
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	c.s.status(r.ID, r.liveStatus())
	if operation == "delete" {
		return nil, nil
	}
	return r.summary(), nil
}

func (c *conn) threadQueueSend(m protocol.Message) (any, *protocol.Error) {
	var input queueInput
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if input.ID == "" || input.Version == nil {
		return nil, bad(400, "需要当前队首 id 和 version")
	}
	r, ferr := c.threadToQueue(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	return c.s.deliverQueueLocked(r, input.ID, input.Version, input.All, false, input.RequestID, userName(m))
}

// 调用方持 turnMu；元数据先认领、启动记录先持久化，再执行模型。
func (s *Server) deliverQueueLocked(r *run, id string, version *uint64, all, automatic bool, requestID, user string) (any, *protocol.Error) {
	var item queueItem
	ferr := s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		if ferr := queueVersion(meta, version, !automatic); ferr != nil {
			return ferr
		}
		if meta.QueueClaim != nil {
			return bad(409, "上一项领取结果待确认，请先核对状态")
		}
		if !r.canContinue() || r.shellBusy {
			return bad(409, "当前仍有执行或暂不能续聊")
		}
		if automatic && (meta.QueuePaused || r.stopping) {
			return bad(409, "队列已暂停")
		}
		if len(meta.Queue) == 0 {
			return bad(409, "队列已空")
		}
		if id != "" && meta.Queue[0].ID != id {
			return bad(409, "队首已改变，请核对最新队列")
		}
		item = meta.Queue[0]
		if item.GoalID != "" && (meta.Continuous == nil || meta.Continuous.State != "running" || meta.Continuous.ID != item.GoalID || meta.Continuous.Revision != item.GoalRevision) {
			return bad(409, "Goal项已失效，请核对当前目标")
		}
		if slices.ContainsFunc(r.events, func(e event) bool { return e.QueueID == item.ID && (e.Kind == "start" || e.Kind == "continue") }) {
			return bad(409, "这一项已有启动记录，请先核对领取状态，不能重复发送")
		}
		if !automatic {
			item.DispatchID, item.DispatchUser = requestID, user
		}
		claim := item
		meta.QueueClaim = &claim
		meta.Queue = meta.Queue[1:]
		if !automatic {
			meta.QueuePaused = !all
		}
		meta.QueueError = ""
		meta.QueueVersion++
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	s.status(r.ID, r.liveStatus())
	if automatic {
		requestID, user = item.RequestID, item.User
	}
	_, startErr := s.startTurnLocked(r, turnInput{GoalID: item.GoalID, GoalRevision: item.GoalRevision, Query: item.Text, QueueID: item.ID, SteerIDs: item.SteerIDs, RequestID: requestID}, user)
	ferr = s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		if meta.QueueClaim == nil || meta.QueueClaim.ID != item.ID {
			return bad(409, "领取状态已变化")
		}
		if startErr != nil {
			item.DispatchID, item.DispatchUser = "", ""
			meta.Queue = append([]queueItem{item}, meta.Queue...)
			meta.QueuePaused = true
			meta.QueueError = startErr.Message
		} else if len(item.SteerIDs) > 0 {
			// continue 只证明开始交接；原文保留到提交规则的接收/拦截证据出现。
			s.settleManagedClaimLocked(r, meta)
			return nil
		} else if at, _ := r.lastTurnEvent(); r.Done && at >= 0 && r.events[at].Kind == "exit" && r.events[at].Text != "exit 0" {
			meta.QueuePaused = true
			meta.QueueError = "本项已进入轮次但启动或执行失败，请查看历史；剩余队列已暂停"
		}
		meta.QueueClaim = nil
		meta.QueueVersion++
		return nil
	})
	if ferr != nil {
		r.mu.Lock()
		r.queueError = "领取记录未能更新，请核对状态；不会重复执行：" + ferr.Message
		r.mu.Unlock()
	}
	s.status(r.ID, r.liveStatus())
	if startErr != nil {
		return nil, startErr
	}
	return map[string]any{"id": r.ID, "queue_id": item.ID}, nil
}

// 调用方持 r.mu；启动事件不能替代提交确认，失败时仍需把原文交还给人核对。
func (s *Server) settleManagedClaimLocked(r *run, meta *runMeta) {
	claim := meta.QueueClaim
	if claim == nil || len(claim.SteerIDs) == 0 {
		return
	}
	for _, e := range r.events {
		if e.QueueID != claim.ID || (e.Kind != "start" && e.Kind != "continue") {
			continue
		}
		proof := r.steerEvidenceLocked(e.Seq)
		if proof.Blocked || proof.Submitted {
			state := "delivered"
			if proof.Blocked {
				state = "blocked"
			}
			for i := range meta.ManagedSteers {
				item := &meta.ManagedSteers[i]
				if item.State == "queued" && slices.Contains(claim.SteerIDs, item.ID) {
					item.State, item.Text, item.Error = state, "", ""
				}
			}
			meta.QueueClaim, meta.QueueError = nil, ""
			meta.QueueVersion++
		} else if proof.Done {
			message := "补充转交已结束，但没有提交确认；原文仍在领取项中，请恢复草稿并核对历史，不会自动重发"
			if !meta.QueuePaused || meta.QueueError != message {
				meta.QueuePaused, meta.QueueError = true, message
				meta.QueueVersion++
			}
		}
		return
	}
}

func (s *Server) confirmManagedQueueLocked(r *run) *protocol.Error {
	r.mu.Lock()
	needed := r.queueClaim != nil && len(r.queueClaim.SteerIDs) > 0
	r.mu.Unlock()
	if !needed {
		return nil
	}
	ferr := s.recoverQueue(r)
	if ferr != nil {
		r.mu.Lock()
		r.queueError = "领取结果未能保存，原文已保留，请核对状态：" + ferr.Message
		r.mu.Unlock()
	}
	return ferr
}

func (s *Server) confirmManagedQueue(r *run) {
	r.turnMu.Lock()
	s.confirmManagedQueueLocked(r)
	r.turnMu.Unlock()
	s.status(r.ID, r.liveStatus())
}

// 只有已持久化的 queue_id 能证明已进入某一轮；恢复本身绝不启动执行。
func (s *Server) recoverQueue(r *run) *protocol.Error {
	r.mu.Lock()
	needed := r.queueClaim != nil || len(r.queue) > 0
	r.mu.Unlock()
	if !needed {
		return nil
	}
	return s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		consumed := map[string]bool{}
		for _, e := range r.events {
			if e.QueueID != "" && (e.Kind == "start" || e.Kind == "continue") {
				consumed[e.QueueID] = true
			}
		}
		before := len(meta.Queue)
		meta.Queue = slices.DeleteFunc(meta.Queue, func(q queueItem) bool { return consumed[q.ID] })
		if before != len(meta.Queue) {
			meta.QueueVersion++
		}
		if meta.QueueClaim == nil {
			return nil
		}
		found := consumed[meta.QueueClaim.ID]
		if found && len(meta.QueueClaim.SteerIDs) > 0 {
			s.settleManagedClaimLocked(r, meta)
			return nil
		}
		if !found {
			claim := *meta.QueueClaim
			if claim.DispatchID != "" {
				s.requestMu.Lock()
				path := s.receiptPath(claim.DispatchUser, claim.DispatchID)
				receipt, err := readReceipt(path)
				if err == nil && receipt.State == "accepted" {
					s.requestMu.Unlock()
					return bad(409, "回执已确认接收但启动记录缺失，请核对历史，不能自动重发")
				}
				if err == nil && receipt.State == "pending" {
					receipt.State, receipt.Error = "rejected", bad(409, "领取后没有进入轮次，已暂停，请核对队列后重新发送")
					err = writeReceipt(path, receipt)
				}
				s.requestMu.Unlock()
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return bad(500, "无法确认领取回执："+err.Error())
				}
			}
			claim.DispatchID, claim.DispatchUser = "", ""
			meta.Queue = append([]queueItem{claim}, meta.Queue...)
			meta.QueuePaused = true
			meta.QueueError = "未发现该项的启动记录，已放回队首并暂停，请核对后手动发送"
		} else {
			meta.QueueError = ""
		}
		meta.QueueClaim = nil
		meta.QueueVersion++
		return nil
	})
}
func (c *conn) threadQueueRecover(m protocol.Message) (any, *protocol.Error) {
	var input queueInput
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if input.Action != "" && input.Action != "release" {
		return nil, bad(400, "action 只能是 release，省略时只核对状态")
	}
	r, ferr := c.threadToQueue(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	if ferr := c.s.recoverQueue(r); ferr != nil {
		return nil, ferr
	}
	if input.Action == "release" {
		ferr := c.s.updateMeta(r, func(meta *runMeta) *protocol.Error {
			if ferr := queueVersion(meta, input.Version, true); ferr != nil {
				return ferr
			}
			claim := meta.QueueClaim
			if claim == nil || claim.ID != input.ID || len(claim.SteerIDs) == 0 {
				return bad(409, "领取项已变化，请核对最新状态")
			}
			ended := false
			for _, e := range r.events {
				if e.QueueID == claim.ID && (e.Kind == "start" || e.Kind == "continue") {
					proof := r.steerEvidenceLocked(e.Seq)
					ended = proof.Done && !proof.Submitted && !proof.Blocked
				}
			}
			if !r.Done || !ended {
				return bad(409, "这一轮仍可能接收输入，暂不能释放领取项")
			}
			for i := range meta.ManagedSteers {
				item := &meta.ManagedSteers[i]
				if item.State == "queued" && slices.Contains(claim.SteerIDs, item.ID) {
					item.State, item.Text, item.Error = "released", "", ""
				}
			}
			meta.QueueClaim, meta.QueueError, meta.QueuePaused = nil, "", true
			meta.QueueVersion++
			return nil
		})
		if ferr != nil {
			return nil, ferr
		}
	}
	c.s.status(r.ID, r.liveStatus())
	return r.summary(), nil
}

func (r *run) addQueueStart(e event) error {
	r.mu.Lock()
	e.Seq, e.Time = r.seq+1, time.Now().UTC()
	line, err := json.Marshal(e)
	offset, seekErr := r.file.Seek(0, io.SeekEnd)
	if err == nil {
		err = seekErr
	}
	if err == nil {
		_, err = r.file.Write(append(line, '\n'))
	}
	if err == nil {
		err = r.file.Sync()
	}
	if err != nil {
		if seekErr == nil {
			if truncateErr := r.file.Truncate(offset); truncateErr != nil {
				err = fmt.Errorf("%w；回退记录失败：%v", err, truncateErr)
			}
		}
		r.mu.Unlock()
		return err
	}
	r.seq = e.Seq
	r.events = append(r.events, e)
	close(r.changed)
	r.changed = make(chan struct{})
	r.mu.Unlock()
	r.s.status(r.ID, "active")
	return nil
}
