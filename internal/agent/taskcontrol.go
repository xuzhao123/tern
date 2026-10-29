package agent

import (
	"context"
	"encoding/json"
	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/protocol"
)

var TaskControls bool
var TaskControlDefinitions = []map[string]any{
	{"name": "report_goal", "description": "报告已由用户显式启动的持续Goal进展。先用task_status取id/revision。continue需要进展与下一步；blocked/waiting_user需要原因；complete必须为每条完成标准提供本Goal期间真实成功工具调用ID及证据说明，缺证据不能完成。不启动Goal，不扩预算/权限。", "parameters": map[string]any{"type": "object", "required": []string{"id", "revision", "state", "progress"}, "properties": map[string]any{"id": map[string]string{"type": "string"}, "revision": map[string]string{"type": "integer"}, "state": map[string]any{"type": "string", "enum": []string{"continue", "blocked", "waiting_user", "complete"}}, "progress": map[string]string{"type": "string"}, "next": map[string]string{"type": "string"}, "reason": map[string]string{"type": "string"}, "evidence": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"criterion", "tool_call_id", "detail"}, "properties": map[string]any{"criterion": map[string]string{"type": "integer"}, "tool_call_id": map[string]string{"type": "string"}, "detail": map[string]string{"type": "string"}}}}}}},
	{"name": "task_status", "description": "读取当前主任务的计划版本与持续Goal。只读取，不启动持续执行或修改权限。", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "update_plan", "description": "保存当前主任务的结构化计划。先用task_status获取revision，每次提交完整步骤。计划不是执行授权；只读计划模式必须等用户点击开始实施。", "parameters": map[string]any{"type": "object", "required": []string{"revision", "steps"}, "properties": map[string]any{"revision": map[string]string{"type": "integer"}, "steps": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"content", "status"}, "properties": map[string]any{"content": map[string]string{"type": "string"}, "status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "blocked"}}}}}}}},
}

func taskControl(ctx context.Context, call llm.ToolCall) (any, error) {
	var result any
	if err := protocol.Call(ctx, "task/control", map[string]any{"action": call.Function.Name, "call_id": call.ID, "arguments": json.RawMessage(call.Function.Arguments)}, &result); err != nil {
		return nil, llm.Permanent(err)
	}
	return result, nil
}
