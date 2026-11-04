package agent

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/skills"
	"github.com/xuzhao123/tern/internal/workspace"
)

// 子 agent 的类型（同 Claude Code 的 subagent）：一个 Markdown 文件定义一种子 agent，frontmatter 写名字、用途、
// 能用的工具和模型，正文是它的提示词，追加在子 agent 的系统提示词后面。spawn_agent 用 agent_type 选；不选就是通用的子 agent。
// 三层，同名时后面的整份覆盖前面的：内置（agents/，编进程序）→ 个人（$TERN_HOME/agents/）→ 项目（<工作区>/.agents/agents/）。
//
//	---
//	name: reviewer
//	description: 什么时候用它（写给父 agent 看）
//	tools: read_file, grep, glob, bash   # 可选：只给这些工具；不写就和父 agent 一样
//	model: deepseek-v4-pro               # 可选：同一家供应商下的另一个模型；不写就和父 agent 一样
//	---
//	提示词正文……

//go:embed agents/*.md
var builtinAgents embed.FS

type AgentType struct {
	Name, Description, Model, Prompt, Source string
	Tools                                    []string // 空表示不限制
}

const maxAgentBytes = 32 << 10

// AgentPrompt、AgentTools 由子进程的 -agent-type 设置：提示词进系统提示词，工具清单在 main 里收窄。
var (
	AgentName   string
	AgentPrompt string
)

// AgentTypes 读出三层的子 agent 类型，按名字排序。读不了、格式不对的文件报给调用方，不让一个坏文件拖垮其他的。
func AgentTypes() ([]AgentType, []error) {
	found := map[string]AgentType{}
	var errs []error
	add := func(source, file string, data []byte) {
		t, err := parseAgentType(source, file, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", file, err))
			return
		}
		found[t.Name] = t
	}
	entries, _ := builtinAgents.ReadDir("agents")
	for _, e := range entries {
		data, _ := builtinAgents.ReadFile(path.Join("agents", e.Name()))
		add("builtin", e.Name(), data)
	}
	dirs := [][2]string{{"personal", "agents"}}
	if workspace.Dir != "" {
		dirs = append(dirs, [2]string{"project", filepath.Join(workspace.Dir, ".agents", "agents")})
	}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d[1], "*.md"))
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err == nil && len(data) > maxAgentBytes {
				err = fmt.Errorf("超过 %d 字节", maxAgentBytes)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s：%w", file, err))
				continue
			}
			add(d[0], file, data)
		}
	}
	list := make([]AgentType, 0, len(found))
	for _, t := range found {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, errs
}

func parseAgentType(source, file string, data []byte) (AgentType, error) {
	fields, body, err := skills.ParseFrontmatter(string(data))
	if err != nil {
		return AgentType{}, err
	}
	name := fields["name"]
	switch {
	case name == "" || len(name) > 64 || !agentNamePattern(name):
		return AgentType{}, fmt.Errorf("name %q 不合规：只能用小写字母、数字和单个连字符", name)
	case strings.TrimSuffix(filepath.Base(file), ".md") != name:
		return AgentType{}, fmt.Errorf("name %q 和文件名不一致", name)
	case fields["description"] == "":
		return AgentType{}, errors.New("缺少 description")
	case body == "":
		return AgentType{}, errors.New("正文（提示词）是空的")
	}
	t := AgentType{Name: name, Description: fields["description"], Model: fields["model"], Prompt: body, Source: source}
	for _, tool := range strings.Split(fields["tools"], ",") {
		if tool = strings.TrimSpace(tool); tool != "" {
			t.Tools = append(t.Tools, tool)
		}
	}
	return t, nil
}

func agentNamePattern(name string) bool {
	for i, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' && i > 0 && i < len(name)-1 && name[i-1] != '-') {
			return false
		}
	}
	return true
}

// FindAgentType 按名字找一种子 agent。
func FindAgentType(name string) (AgentType, error) {
	list, _ := AgentTypes()
	for _, t := range list {
		if t.Name == name {
			return t, nil
		}
	}
	names := make([]string, len(list))
	for i, t := range list {
		names[i] = t.Name
	}
	return AgentType{}, fmt.Errorf("没有叫 %s 的子 agent 类型，可用的有：%s", name, strings.Join(names, "、"))
}

// UseAgentType 在子进程启动时调用（-agent-type）：记下提示词，把工具清单收窄到它列出的那些。
// read_output 总是保留：结果太长被转存时，没有它就读不回来。列了但这次没提供的工具忽略（比如没开 bash）。
func UseAgentType(name string) error {
	t, err := FindAgentType(name)
	if err != nil {
		return err
	}
	AgentName, AgentPrompt = t.Name, t.Prompt
	if len(t.Tools) > 0 {
		Deferred = slices.DeleteFunc(Deferred, func(tool map[string]any) bool {
			name, _ := tool["name"].(string)
			return !slices.Contains(t.Tools, name)
		})
		llm.Tools = slices.DeleteFunc(llm.Tools, func(tool map[string]any) bool {
			name, _ := tool["name"].(string)
			if name == "tool_search" || name == "call_tool" {
				return len(Deferred) == 0 // 按需加载的工具还剩下时，这两个跟着留
			}
			return name != "read_output" && !slices.Contains(t.Tools, name)
		})
	}
	return nil
}

// agentTypeRules 写进 spawn_agent 的描述：有哪些类型、各自什么时候用。
func agentTypeRules() string {
	list, _ := AgentTypes()
	if len(list) == 0 {
		return ""
	}
	lines := []string{"agent_type 选一种专门的子 agent（不填就是和你一样的通用子 agent）："}
	for _, t := range list {
		lines = append(lines, "- "+t.Name+"："+t.Description)
	}
	return "\n" + strings.Join(lines, "\n")
}
