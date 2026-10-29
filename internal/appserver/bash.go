package appserver

import (
	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/protocol"
)

// 显式修复旧任务关闭的命令能力，不重写快照、不启动下一轮或改变权限。
func (c *conn) threadBashEnable(m protocol.Message) (any, *protocol.Error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &p); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.threadToManage(p.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	ferr = c.s.updateMeta(r, func(meta *runMeta) *protocol.Error {
		if !r.Done || r.shellBusy {
			return bad(409, "请等当前轮和命令结束后再启用")
		}
		if r.Source != "" || r.Eval != "" {
			return bad(409, "导入对话和评测试次不能启用命令")
		}
		if meta.BashEnabled {
			return nil
		}
		if err := audit.Write(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_enable_bash", Conversation: r.ID, Detail: c.clientName()}); err != nil {
			return bad(500, "审计写入失败，命令未启用")
		}
		meta.BashEnabled = true
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	c.s.status(r.ID, r.liveStatus())
	return map[string]any{"id": r.ID, "bash_enabled": true}, nil
}
