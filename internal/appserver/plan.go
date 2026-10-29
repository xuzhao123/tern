package appserver

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/protocol"
)

type planStep struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}
type taskPlan struct {
	Revision int         `json:"revision"`
	Approved int         `json:"approved"`
	Steps    []planStep  `json:"steps"`
	Updated  time.Time   `json:"updated"`
	CallID   string      `json:"call_id,omitempty"`
	Previous []*taskPlan `json:"previous,omitempty"`
}

func validatePlan(steps []planStep) *protocol.Error {
	if len(steps) == 0 || len(steps) > 50 {
		return bad(400, "计划需要1–50个步骤")
	}
	running := 0
	for _, step := range steps {
		if strings.TrimSpace(step.Content) == "" || len(step.Content) > 2000 || !slices.Contains([]string{"pending", "in_progress", "completed", "blocked"}, step.Status) {
			return bad(400, "步骤内容或状态无效")
		}
		if step.Status == "in_progress" {
			running++
		}
	}
	if running > 1 {
		return bad(400, "同一时刻只能有一个进行中的步骤")
	}
	return nil
}
func (r *run) taskStatus() any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return map[string]any{"plan": planSummary(r.plan), "continuous_goal": r.goalViewLocked()}
}
func (r *run) savePlan(revision int, steps []planStep, callID string) (any, *protocol.Error) {
	if ferr := validatePlan(steps); ferr != nil {
		return nil, ferr
	}
	ferr := r.s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		old := meta.Plan
		current := 0
		if old != nil {
			current = old.Revision
			if callID != "" && old.CallID == callID {
				return nil
			}
		}
		if current != revision {
			return bad(409, "计划版本已变化，请重新读取")
		}
		next := &taskPlan{Revision: current + 1, Steps: slices.Clone(steps), Updated: time.Now().UTC(), CallID: callID}
		if old != nil {
			previous := *old
			previous.Previous = nil
			next.Previous = append(slices.Clone(old.Previous), &previous)
			if len(next.Previous) > 20 {
				next.Previous = next.Previous[len(next.Previous)-20:]
			}
			if old.Approved == old.Revision && len(old.Steps) == len(steps) {
				same := true
				for i := range steps {
					same = same && steps[i].Content == old.Steps[i].Content
				}
				if same {
					next.Approved = next.Revision
				}
			}
		}
		meta.Plan = next
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	r.s.status(r.ID, r.liveStatus())
	return r.taskStatus(), nil
}
func (r *run) taskControl(m protocol.Message) {
	var p struct {
		Action    string          `json:"action"`
		CallID    string          `json:"call_id"`
		Arguments json.RawMessage `json:"arguments"`
	}
	var result any
	var ferr *protocol.Error
	if json.Unmarshal(m.Params, &p) != nil {
		ferr = bad(400, "任务控制参数无效")
	} else {
		switch p.Action {
		case "task_status":
			result = r.taskStatus()
		case "report_goal":
			result, ferr = r.reportGoal(p.CallID, p.Arguments)
		case "update_plan":
			var input struct {
				Revision int        `json:"revision"`
				Steps    []planStep `json:"steps"`
			}
			if json.Unmarshal(p.Arguments, &input) != nil {
				ferr = bad(400, "计划参数无效")
			} else {
				result, ferr = r.savePlan(input.Revision, input.Steps, p.CallID)
			}
		default:
			ferr = bad(400, "未知任务控制操作")
		}
	}
	r.mu.Lock()
	pc := r.conn
	r.mu.Unlock()
	if pc != nil {
		pc.Reply(*m.ID, result, ferr)
	}
}
func (c *conn) planGet(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.s.threadOf(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return map[string]any{"plan": r.plan, "continuous_goal": r.goalViewLocked()}, nil
}
func (c *conn) planSet(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string     `json:"threadId"`
		Revision int        `json:"revision"`
		Steps    []planStep `json:"steps"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.s.threadOf(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	return r.savePlan(p.Revision, p.Steps, "")
}
func (c *conn) planApprove(m protocol.Message) (any, *protocol.Error) {
	return c.requestOnce(m, (*conn).planApproveOnce)
}
func (c *conn) planApproveOnce(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID   string `json:"threadId"`
		Revision   int    `json:"revision"`
		Permission string `json:"permission"`
		RequestID  string `json:"request_id"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	if p.Permission != "ask" && p.Permission != "edit" {
		return nil, bad(400, "开始实施须明确选择 ask 或 edit 权限")
	}
	r, ferr := c.s.threadOf(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	if err := audit.Write(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_plan_approve", Conversation: r.ID, Target: p.Permission}); err != nil {
		return nil, bad(500, "审计保存失败，没有实施计划")
	}
	ferr = c.s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		if !r.canContinue() {
			return bad(409, "请等当前轮结束")
		}
		if meta.Plan == nil || meta.Plan.Revision != p.Revision {
			return bad(409, "计划版本已变化，请重新审阅")
		}
		next := *meta.Plan
		next.Approved = next.Revision
		meta.Plan = &next
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	return c.s.startTurnLocked(r, turnInput{Query: "请按已批准的计划版本实施；先用 task_status 核对计划，逐项更新状态，完成后提供验收证据。", Permission: p.Permission, Sandbox: "sandbox", RequestID: p.RequestID}, userName(m))
}

func planSummary(plan *taskPlan) *taskPlan {
	if plan == nil {
		return nil
	}
	copy := *plan
	copy.Previous = nil
	return &copy
}
