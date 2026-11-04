package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xuzhao123/tern/internal/llm"
)

func fakeMCP(n int) []map[string]any {
	var tools []map[string]any
	for i := range n {
		tools = append(tools, map[string]any{"name": "mcp_demo_tool" + string(rune('a'+i)), "description": strings.Repeat("一个演示用的远程工具，说明写得很长。", 20),
			"parameters": map[string]any{"type": "object"}})
	}
	return tools
}

func toolCall(id, name, args string) llm.ToolCall {
	var c llm.ToolCall
	c.ID, c.Function.Name, c.Function.Arguments = id, name, args
	return c
}

func TestDeferAndUnwrap(t *testing.T) {
	saved, savedDeferred := llm.Tools, Deferred
	defer func() { llm.Tools, Deferred = saved, savedDeferred }()
	llm.Tools = append([]map[string]any{{"name": "read_file"}}, fakeMCP(8)...)
	Deferred = nil
	DeferTools()
	if len(Deferred) != 8 || !offered("tool_search") || !offered("call_tool") || !offered("mcp_demo_toola") || offered("mcp_nothing") {
		t.Fatalf("推迟后 Deferred=%d，工具列表=%v", len(Deferred), llm.Tools)
	}
	for _, args := range []string{`{"name":"mcp_demo_toolb","arguments":{"x":1}}`, `{"name":"mcp_demo_toolb","arguments":"{\"x\":1}"}`} {
		got := unwrapCall(toolCall("c1", "call_tool", args))
		if got.ID != "c1" || got.Function.Name != "mcp_demo_toolb" || got.Function.Arguments != `{"x":1}` {
			t.Errorf("unwrapCall(%s) = %+v", args, got.Function)
		}
	}
	if got := unwrapCall(toolCall("c2", "call_tool", `{"name":"bash","arguments":{}}`)); got.Function.Name != "call_tool" {
		t.Errorf("不是按需加载的工具不能借 call_tool 调用：%+v", got.Function)
	}
	r, _ := toolSearch(`{"query":"select:mcp_demo_toolc,mcp_none"}`)
	if r.(map[string]any)["count"] != 1 {
		t.Errorf("select 结果：%v", r)
	}
	r, _ = toolSearch(`{"query":"toolh 演示"}`)
	b, _ := json.Marshal(r)
	if !strings.HasPrefix(string(r.(map[string]any)["tools"].([]map[string]any)[0]["name"].(string)), "mcp_demo_toolh") {
		t.Errorf("名字命中的应排第一：%s", b)
	}
}

func TestDeferSmallSetKept(t *testing.T) {
	saved, savedDeferred := llm.Tools, Deferred
	defer func() { llm.Tools, Deferred = saved, savedDeferred }()
	llm.Tools, Deferred = []map[string]any{{"name": "mcp_x_y", "description": "小"}}, nil
	DeferTools()
	if len(Deferred) != 0 || offered("tool_search") {
		t.Fatalf("说明很短时不推迟：%v", llm.Tools)
	}
}

func TestToolSearchSelectKeepsCase(t *testing.T) {
	Deferred = []map[string]any{{"name": "mcp_gh_getIssue", "description": "get issue"}}
	defer func() { Deferred = nil }()
	for _, query := range []string{"select:mcp_gh_getIssue", "SELECT:mcp_gh_getIssue", "getissue"} {
		result, err := toolSearch(`{"query":"` + query + `"}`)
		if err != nil || result.(map[string]any)["count"] != 1 {
			t.Errorf("%s：%v %v", query, result, err)
		}
	}
}
