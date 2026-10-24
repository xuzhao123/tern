package appserver

import (
	"context"

	"github.com/xuzhao123/tern/internal/protocol"
)

// 订阅就是原来网页端的一条 SSE 事件流搬到协议上：thread/resume 带上界面自己取的 subscription 编号，
// 之后这条对话的每个事件都用 thread/event 推给这个编号；对话结束时最后一条是 {subscription, threadId, end: true}。
// 编号由界面取，是因为推送可能比回复先到，界面要在发请求之前就登记好。
//
// 每个订阅一个 goroutine 推送，写法同原来的 streamEvents：等 r.changed，把游标之后的事件逐条发出去。
// 一个连接写得慢只卡住它自己的推送，不卡 r.add 和别的连接。连接断开即全部退订。

// thread/resume：先回 {seq}，再把这个对话 after 之后的事件全部推出去，之后接着推新的。
// seq 是订阅那一刻对话最后一个事件的序号：界面据此分清哪些是已有的、哪些是新来的（接一条正在跑的对话时）。
func (c *conn) threadResume(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID     string `json:"threadId"`
		After        int64  `json:"after"`
		Subscription string `json:"subscription"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if input.Subscription == "" {
		return nil, bad(400, "需要 subscription：界面自己取，推送里带回来")
	}
	r, ferr := c.s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	c.mu.Lock()
	_, exists := c.subs[input.Subscription]
	c.mu.Unlock()
	if exists {
		return nil, bad(400, "这个连接已经用过 subscription "+input.Subscription)
	}
	// 序号要在起推送 goroutine 之前读：之后到达的事件都大于它，界面据此分清已有与新的，不会把新事件误当成已有的。
	r.mu.Lock()
	seq := r.seq
	r.mu.Unlock()
	c.subscribe(r, input.After, input.Subscription)
	return map[string]any{"seq": seq}, nil
}

// thread/unsubscribe：界面关掉事件流；推送 goroutine 随即退出。
func (c *conn) threadUnsubscribe(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Subscription string `json:"subscription"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	c.unsubscribe(input.Subscription)
	return nil, nil
}

func (c *conn) unsubscribe(id string) {
	c.mu.Lock()
	cancel := c.subs[id]
	delete(c.subs, id)
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// subscribe 起一个 goroutine 推送这个订阅：先推 after 之后的已有事件，再等新事件；
// 事件发完时对话已经结束（r.Done），就发 end 并结束这个订阅。
func (c *conn) subscribe(r *run, after int64, id string) {
	ctx, cancel := context.WithCancel(c.ctx)
	c.mu.Lock()
	c.subs[id] = cancel
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.subs, id)
			c.mu.Unlock()
			cancel()
		}()
		for sent := after; ; {
			r.mu.Lock()
			// 事件序号只增不减（run.seq），游标也是序号：跳过已经收到的。
			events, changed, done := r.events, r.changed, r.Done
			r.mu.Unlock()
			i := 0
			for i < len(events) && int64(events[i].Seq) <= sent {
				i++
			}
			for _, e := range events[i:] {
				sent = int64(e.Seq)
				c.pc.Notify("thread/event", map[string]any{"subscription": id, "threadId": r.ID, "event": e})
			}
			if done {
				c.pc.Notify("thread/event", map[string]any{"subscription": id, "threadId": r.ID, "end": true})
				return
			}
			select {
			case <-changed:
			case <-ctx.Done():
				return
			case <-c.pc.Closed():
				return
			}
		}
	}()
}
