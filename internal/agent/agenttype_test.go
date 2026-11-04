package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/workspace"
)

func TestAgentTypes(t *testing.T) {
	saved := workspace.Dir
	defer func() { workspace.Dir = saved }()
	workspace.Dir = t.TempDir()
	t.Chdir(t.TempDir()) // 个人目录 agents/ 相对 TERN_HOME（当前目录）：测试时不能读到包目录里的内置文件
	project := filepath.Join(workspace.Dir, ".agents", "agents")
	os.MkdirAll(project, 0o755)
	os.WriteFile(filepath.Join(project, "explore.md"), []byte("---\nname: explore\ndescription: 项目里改过的搜索\ntools: grep\nmodel: m2\n---\n项目的提示词"), 0o644)
	os.WriteFile(filepath.Join(project, "Bad.md"), []byte("---\nname: Bad\ndescription: x\n---\nbody"), 0o644)
	os.WriteFile(filepath.Join(project, "empty.md"), []byte("---\nname: empty\ndescription: x\n---\n"), 0o644)
	list, errs := AgentTypes()
	if len(errs) != 2 {
		t.Errorf("两个坏文件应各报一个错：%v", errs)
	}
	got := map[string]AgentType{}
	for _, a := range list {
		got[a.Name] = a
	}
	if got["plan"].Source != "builtin" || got["explore"].Source != "project" || got["explore"].Model != "m2" || got["explore"].Prompt != "项目的提示词" {
		t.Errorf("项目里的同名类型应整份覆盖内置的：%+v", got)
	}

	savedTools, savedName, savedPrompt := llm.Tools, AgentName, AgentPrompt
	defer func() { llm.Tools, AgentName, AgentPrompt = savedTools, savedName, savedPrompt }()
	llm.Tools = []map[string]any{{"name": "grep"}, {"name": "bash"}, {"name": "read_output"}, {"name": "edit_file"}}
	if err := UseAgentType("explore"); err != nil {
		t.Fatal(err)
	}
	if len(llm.Tools) != 2 || !offered("grep") || !offered("read_output") || offered("bash") {
		t.Errorf("工具应收窄到 grep（read_output 保留）：%v", llm.Tools)
	}
	if UseAgentType("nobody") == nil {
		t.Error("不存在的类型应报错")
	}
}
