package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/xuzhao123/tern/internal/llm"
)

// 工具按需加载（同 Claude Code 的 ToolSearch、Codex 的 tool_search）：MCP 工具多的时候，完整的参数说明不放进工具列表，
// 只在系统提示词里列出名字和一句用途；模型用 tool_search 查到完整说明，再用 call_tool 调用。
// 工具列表从第一次请求起就固定（提示缓存的前缀不变，也是执行器的允许列表），所以不在中途往列表里加工具：
// 被推迟的工具记在 Deferred 里，同样算本轮允许的工具；call_tool 在执行前还原成原来的工具，hook、权限、审计、
// 并行与重做的判断都按原来的工具名走。

// deferThreshold：MCP 工具的说明合计超过这么多 token 才推迟；少的时候直接给，省一次查找。
const deferThreshold = 2000

// Deferred 是这次推迟加载的工具定义。
var Deferred []map[string]any

var ToolSearchDefinitions = []map[string]any{
	{"name": "tool_search", "description": "查找系统提示词里“按需加载的工具”的完整说明（参数格式）。query 写关键词（匹配工具名和用途），或写 select:名字1,名字2 直接取这几个。返回最多 5 个工具的名字、说明和参数；查到之后用 call_tool 调用。",
		"parameters": map[string]any{"type": "object", "required": []string{"query"}, "properties": map[string]any{
			"query": map[string]string{"type": "string", "description": "关键词，或 select:工具名,工具名"},
		}}},
	{"name": "call_tool", "description": "调用一个按需加载的工具。先用 tool_search 看过它的参数，name 写工具名，arguments 按它的参数格式写成 JSON 对象。",
		"parameters": map[string]any{"type": "object", "required": []string{"name", "arguments"}, "properties": map[string]any{
			"name":      map[string]string{"type": "string", "description": "工具名，如 mcp_github_create_issue"},
			"arguments": map[string]string{"type": "object", "description": "这个工具的参数"},
		}}},
}

// DeferTools 在所有工具都加进来之后调用：MCP 工具的说明合计超过阈值时，把它们移出工具列表，换成 tool_search、call_tool。
func DeferTools() {
	var mcpTools []map[string]any
	for _, t := range llm.Tools {
		if name, _ := t["name"].(string); strings.HasPrefix(name, "mcp_") {
			mcpTools = append(mcpTools, t)
		}
	}
	data, _ := json.Marshal(mcpTools)
	if len(mcpTools) == 0 || llm.TextTokens(string(data)) <= deferThreshold {
		return
	}
	Deferred = mcpTools
	llm.Tools = slices.DeleteFunc(llm.Tools, func(t map[string]any) bool {
		name, _ := t["name"].(string)
		return strings.HasPrefix(name, "mcp_")
	})
	llm.Tools = append(llm.Tools, ToolSearchDefinitions...)
	fmt.Printf("Tool search: deferred=%d tokens=%d\n", len(Deferred), llm.TextTokens(string(data)))
}

// deferredRules 写进系统提示词：有哪些按需加载的工具。
func deferredRules() string {
	if len(Deferred) == 0 {
		return ""
	}
	lines := []string{"\n按需加载的工具（只列名字和用途；要用时先 tool_search 取参数说明，再 call_tool 调用）："}
	for _, t := range Deferred {
		desc, _ := t["description"].(string)
		desc = strings.Join(strings.Fields(desc), " ")
		if r := []rune(desc); len(r) > 80 {
			desc = string(r[:80]) + "…"
		}
		lines = append(lines, fmt.Sprintf("- %v：%s", t["name"], desc))
	}
	return strings.Join(lines, "\n")
}

func deferredTool(name string) map[string]any {
	for _, t := range Deferred {
		if t["name"] == name {
			return t
		}
	}
	return nil
}

func toolSearch(arguments string) (any, error) {
	var args struct{ Query string }
	if err := json.Unmarshal([]byte(arguments), &args); err != nil || strings.TrimSpace(args.Query) == "" {
		return nil, llm.Permanent(errors.New("query 需要是非空字符串"))
	}
	raw := strings.TrimSpace(args.Query)
	query := strings.ToLower(raw)
	var found []map[string]any
	if strings.HasPrefix(query, "select:") {
		// 工具名区分大小写（MCP server 起的名字可能是 getIssue），用原文。
		for _, name := range strings.Split(raw[len("select:"):], ",") {
			if t := deferredTool(strings.TrimSpace(name)); t != nil {
				found = append(found, t)
			}
		}
	} else {
		// 按命中的关键词个数排序：名字里命中的算两次。
		type scored struct {
			tool  map[string]any
			score int
		}
		var hits []scored
		for _, t := range Deferred {
			name, _ := t["name"].(string)
			desc, _ := t["description"].(string)
			name, desc = strings.ToLower(name), strings.ToLower(desc)
			score := 0
			for _, word := range strings.Fields(query) {
				if strings.Contains(name, word) {
					score += 2
				} else if strings.Contains(desc, word) {
					score++
				}
			}
			if score > 0 {
				hits = append(hits, scored{t, score})
			}
		}
		slices.SortStableFunc(hits, func(a, b scored) int { return b.score - a.score })
		for _, h := range hits {
			found = append(found, h.tool)
		}
	}
	if len(found) > 5 {
		found = found[:5]
	}
	result := map[string]any{"tools": found, "count": len(found)}
	if len(found) == 0 {
		result["note"] = "没有找到。换个关键词，或照系统提示词里的工具名写 select:名字"
	}
	return result, nil
}

// unwrapCall 把 call_tool 还原成它要调用的那个工具（同一个调用ID）；之后的检查和执行都按原工具。
// 名字不是按需加载的工具、参数不对时原样返回，由 runTool 报错。
func unwrapCall(call llm.ToolCall) llm.ToolCall {
	if call.Function.Name != "call_tool" {
		return call
	}
	var args struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || deferredTool(args.Name) == nil {
		return call
	}
	inner := strings.TrimSpace(string(args.Arguments))
	if inner == "" || inner == "null" {
		inner = "{}"
	}
	// 有的模型把 arguments 写成 JSON 字符串：解开一层。
	var s string
	if json.Unmarshal([]byte(inner), &s) == nil {
		inner = s
	}
	call.Function.Name, call.Function.Arguments = args.Name, inner
	return call
}

func callToolError(arguments string) error {
	var args struct{ Name string }
	json.Unmarshal([]byte(arguments), &args)
	return llm.Permanent(fmt.Errorf("%q 不是按需加载的工具；照系统提示词里的名字写，或先用 tool_search 查", args.Name))
}
