package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/xuzhao123/tern/internal/protocol"
)

type childControl struct {
	call string
	conn *protocol.Conn
}

var childControls = struct {
	sync.Mutex
	live map[string]childControl
}{live: map[string]childControl{}}

func registerChildControl(id, call string, conn *protocol.Conn) {
	childControls.Lock()
	defer childControls.Unlock()
	childControls.live[id] = childControl{call, conn}
}
func removeChildControl(id, call string) {
	childControls.Lock()
	defer childControls.Unlock()
	if childControls.live[id].call == call {
		delete(childControls.live, id)
	}
}

// 只操作当前父进程实际拥有的连接；task 文本或旧尝试不能代替目标身份。
func ControlChild(id, call, action, text, requestID string) (any, error) {
	childControls.Lock()
	child, ok := childControls.live[id]
	childControls.Unlock()
	if !ok || child.call != call {
		return nil, errors.New("子任务已结束、尚未连接或尝试版本已改变")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var result any
	switch action {
	case "send":
		if text == "" || requestID == "" {
			return nil, errors.New("补充需要正文与请求ID")
		}
		if err := child.conn.Call(ctx, "turn/steer", map[string]any{"text": text, "client_id": requestID, "managed": true}, &result); err != nil {
			return nil, err
		}
	case "status":
		if err := child.conn.Call(ctx, "turn/steer/status", map[string]any{"ids": []string{requestID}}, &result); err != nil {
			return nil, err
		}
	case "stop":
		if err := child.conn.Call(ctx, "turn/interrupt", nil, nil); err != nil {
			return nil, err
		}
		protocol.Notify("subagent/stopping", map[string]string{"task_id": id, "call_id": call})
		result = map[string]string{"state": "stopping"}
	default:
		return nil, errors.New("不支持的子任务操作")
	}
	return result, nil
}
