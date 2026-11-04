package hooks

import (
	"context"
	"strings"
	"testing"
)

func TestParseEvents(t *testing.T) {
	ok := `{"hooks": {"SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "true"}]}],
		"PreCompact": [{"matcher": "auto|manual", "hooks": [{"type": "command", "command": "true"}]}],
		"SubagentStop": [{"matcher": "explore", "hooks": [{"type": "command", "command": "true"}]}],
		"Notification": [{"hooks": [{"type": "command", "command": "true"}]}],
		"SessionEnd": [{"hooks": [{"type": "command", "command": "true"}]}]}}`
	if _, err := Parse(ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"hooks": {"Notification": [{"matcher": "x", "hooks": [{"type": "command", "command": "true"}]}]}}`,
		`{"hooks": {"SessionEnd": [{"matcher": "x", "hooks": [{"type": "command", "command": "true"}]}]}}`,
		`{"hooks": {"Nope": []}}`,
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("应报错：%s", bad)
		}
	}
}

// 真实运行 sh 命令：matcher 按传入的值匹配，continue:false 让这一轮停下，SessionStart 的纯文本 stdout 是补充上下文。
func TestRunOutputs(t *testing.T) {
	saved := config
	defer func() { config = saved }()
	Dir = t.TempDir()
	load := func(text string) {
		t.Helper()
		parsed, err := Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		config = parsed
	}
	load(`{"hooks": {"SessionStart": [{"matcher": "resume", "hooks": [{"type": "command", "command": "echo 记得先跑测试"}]}]}}`)
	if r := Run(context.Background(), "SessionStart", "startup", map[string]any{}); r.Context != "" {
		t.Errorf("matcher 不匹配时不应运行：%+v", r)
	}
	if r := Run(context.Background(), "SessionStart", "resume", map[string]any{}); r.Context != "记得先跑测试" {
		t.Errorf("SessionStart 的 stdout 应作为补充上下文：%+v", r)
	}
	load(`{"hooks": {"PostToolUse": [{"hooks": [
		{"type": "command", "command": "echo '{\"continue\": false, \"stopReason\": \"够了\", \"systemMessage\": \"已经停下\"}'"},
		{"type": "command", "command": "echo '{\"hookSpecificOutput\": {\"additionalContext\": \"不该运行\"}}'"}]}]}}`)
	r := Run(context.Background(), "PostToolUse", "bash", map[string]any{})
	if !r.Stop || r.Why != "够了" || strings.Contains(r.Context, "不该运行") {
		t.Errorf("continue:false 应停下且不再运行后面的 hook：%+v", r)
	}
}
