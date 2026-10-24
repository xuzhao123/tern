package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/protocol"
)

// Skills 中心与 MCP 中心：app-server 只保存“装了哪些、开没开”，解析、校验与连接都交给 agent 自己的命令行模式，
// 不在 app-server 另写一份 SKILL.md 解析或 MCP client。配置放 TERN_HOME 的 .data/hub.json（已忽略提交）。
type mcpServer struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Enabled bool   `json:"enabled"`
}

type hubConfig struct {
	MCPServers     []mcpServer `json:"mcp_servers"`
	DisabledSkills []string    `json:"disabled_skills"`
}

var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var serverName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func (s *Server) hubPath() string { return filepath.Join(s.dir, ".data", "hub.json") }

// 调用方持有 s.hubMu。文件不存在时是空配置：新 skill 默认启用，MCP server 需要手动添加。
func (s *Server) readHub() (hubConfig, error) {
	config := hubConfig{MCPServers: []mcpServer{}, DisabledSkills: []string{}}
	data, err := os.ReadFile(s.hubPath())
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &config)
	}
	return config, err
}

// 先写临时文件再改名，写到一半退出也不会留下半个 JSON。
func (s *Server) writeHub(config hubConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.hubPath()), 0o700); err != nil {
		return err
	}
	tmp := s.hubPath() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.hubPath())
}

// updateHub 读改写 .data/hub.json；change 返回的错误按参数不对处理（400）。
func (s *Server) updateHub(change func(*hubConfig) error) *protocol.Error {
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	config, err := s.readHub()
	if err == nil {
		err = change(&config)
	}
	if err != nil {
		return bad(400, err.Error())
	}
	if err := s.writeHub(config); err != nil {
		return bad(500, err.Error())
	}
	return nil
}

// 新对话开始时取一次快照，写进 start 事件；续聊沿用快照，中心里之后的改动只影响新对话。
// skill 只按目录名列出，坏文件由 agent 启动时报错跳过。
func (s *Server) enabledCapabilities() ([]string, []mcpServer, error) {
	s.hubMu.Lock()
	config, err := s.readHub()
	s.hubMu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	paths, _ := filepath.Glob(filepath.Join(s.dir, "skills", "*", "SKILL.md"))
	skills := []string{}
	for _, path := range paths {
		name := filepath.Base(filepath.Dir(path))
		if !slices.Contains(config.DisabledSkills, name) {
			skills = append(skills, name)
		}
	}
	servers := []mcpServer{}
	for _, m := range config.MCPServers {
		if m.Enabled {
			servers = append(servers, m)
		}
	}
	return skills, servers, nil
}

func appendCapabilityArgs(args, skills []string, servers []mcpServer) []string {
	if len(skills) > 0 {
		args = append(args, "-skills")
	}
	for _, name := range skills {
		args = append(args, "-skill", name)
	}
	for _, m := range servers {
		args = append(args, "-mcp-server", m.Command)
	}
	return args
}

// hub/enabled：输入框显示新对话会带上的数量（skill 按目录名计数），MCP 中心列出全部 server；都不启动 agent。
func (c *conn) hubEnabled(protocol.Message) (any, *protocol.Error) {
	skills, servers, err := c.s.enabledCapabilities()
	c.s.hubMu.Lock()
	config, readErr := c.s.readHub()
	c.s.hubMu.Unlock()
	if err = errors.Join(err, readErr); err != nil {
		return nil, bad(500, err.Error())
	}
	return map[string]any{"skills": skills, "mcp": servers, "mcp_servers": config.MCPServers, "hosted": "与网页端同一进程，随网页端启动和退出"}, nil
}

// hub/list：列表直接来自 agent 的 -skills-list：和运行时用的是同一套 frontmatter 解析与校验。
func (c *conn) hubList(protocol.Message) (any, *protocol.Error) {
	s := c.s
	s.hubMu.Lock()
	config, err := s.readHub()
	s.hubMu.Unlock()
	if err != nil {
		return nil, bad(500, "无法读取 .data/hub.json："+err.Error())
	}
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Minute)
	defer cancel()
	cmd := s.agentCommand(ctx, "-skills-list")
	output, err := cmd.Output()
	var listed struct {
		Skills []map[string]any `json:"skills"`
		Errors []map[string]any `json:"errors"`
	}
	if err == nil {
		err = json.Unmarshal(output, &listed)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			err = errors.New(strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, bad(500, "agent -skills-list 失败："+err.Error())
	}
	for _, skill := range listed.Skills {
		skill["enabled"] = !slices.Contains(config.DisabledSkills, skill["name"].(string))
	}
	return map[string]any{"path": ".data/hub.json", "skills": listed.Skills, "skill_errors": listed.Errors, "mcp_servers": config.MCPServers}, nil
}

// hub/skill/create：新建知识型 skill，只写一个 SKILL.md。写完立刻用 agent 的校验检查，不合规就删掉并报出原因。
func (c *conn) skillCreate(m protocol.Message) (any, *protocol.Error) {
	var input struct{ Name, Description, Body string }
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	input.Name, input.Description, input.Body = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), strings.TrimSpace(input.Body)
	switch {
	case len(input.Name) > 64 || !skillName.MatchString(input.Name):
		return nil, bad(400, "name 只能用小写字母、数字和单个连字符，最长64")
	case input.Description == "" || strings.ContainsAny(input.Description, "\r\n"):
		return nil, bad(400, "description 必填，且写在一行内（frontmatter 不支持多行值）")
	case input.Body == "":
		return nil, bad(400, "正文不能为空：写清楚什么时候用、按什么步骤做")
	}
	s := c.s
	// 全新的 TERN_HOME 里还没有 skills/：先照“文件不存在”建出来（同 skill 目录本身的权限），再建这个 skill 的目录。
	if err := os.MkdirAll(filepath.Join(s.dir, "skills"), 0o755); err != nil {
		return nil, bad(500, "无法创建 skills/："+err.Error())
	}
	dir := filepath.Join(s.dir, "skills", input.Name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, bad(409, "无法创建 skills/"+input.Name+"（可能已存在）")
	}
	content := "---\nname: " + input.Name + "\ndescription: " + input.Description + "\n---\n\n" + input.Body + "\n"
	err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644)
	if err == nil {
		cmd := s.agentCommand(c.ctx, "-skills-list")
		var output []byte
		var listed struct{ Errors []struct{ Dir, Error string } }
		if output, err = cmd.Output(); err == nil {
			err = json.Unmarshal(output, &listed)
		}
		for _, failure := range listed.Errors {
			if failure.Dir == input.Name {
				err = errors.New(failure.Error)
			}
		}
	}
	if err != nil {
		os.RemoveAll(dir)
		return nil, bad(400, "skill 未通过校验，已撤销："+err.Error())
	}
	return nil, nil
}

// hub/skill/delete：删掉整个 skills/<name> 目录；名字先按 spec 校验，不会拼出 skills/ 之外的路径。
func (c *conn) skillDelete(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Name string `json:"name"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	name := input.Name
	if !skillName.MatchString(name) {
		return nil, bad(400, "skill 名字不合规")
	}
	dir := filepath.Join(c.s.dir, "skills", name)
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return nil, bad(404, "找不到 skills/"+name+"/SKILL.md")
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, bad(500, err.Error())
	}
	if ferr := c.s.updateHub(func(config *hubConfig) error {
		config.DisabledSkills = slices.DeleteFunc(config.DisabledSkills, func(n string) bool { return n == name })
		return nil
	}); ferr != nil {
		return nil, ferr
	}
	return nil, nil
}

func (c *conn) skillToggle(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if !skillName.MatchString(input.Name) {
		return nil, bad(400, `需要合规的 skill 名字与 {"enabled": true|false}`)
	}
	name := input.Name
	if ferr := c.s.updateHub(func(config *hubConfig) error {
		config.DisabledSkills = slices.DeleteFunc(config.DisabledSkills, func(n string) bool { return n == name })
		if !input.Enabled {
			config.DisabledSkills = append(config.DisabledSkills, name)
		}
		return nil
	}); ferr != nil {
		return nil, ferr
	}
	return nil, nil
}

func (c *conn) mcpAdd(m protocol.Message) (any, *protocol.Error) {
	var input struct{ Name, Command string }
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	input.Name, input.Command = strings.TrimSpace(input.Name), strings.TrimSpace(input.Command)
	if !serverName.MatchString(input.Name) {
		return nil, bad(400, "name 用小写字母、数字、_ 或 -，最长32")
	}
	// 与 agent 一致：http(s) 地址是远程 server；其他当作 stdio 命令，按空格切分直接执行，不经过 shell。
	if input.Command == "" || len(input.Command) > 1024 || strings.ContainsAny(input.Command, "\r\n") {
		return nil, bad(400, "command 是一行 stdio 启动命令或 http(s) 地址，最长1024")
	}
	if ferr := c.s.updateHub(func(config *hubConfig) error {
		for _, m := range config.MCPServers {
			if m.Name == input.Name {
				return errors.New("已有同名 server：" + input.Name)
			}
		}
		config.MCPServers = append(config.MCPServers, mcpServer{Name: input.Name, Command: input.Command, Enabled: true})
		return nil
	}); ferr != nil {
		return nil, ferr
	}
	return nil, nil
}

func (c *conn) changeMCP(name string, change func(config *hubConfig, i int)) *protocol.Error {
	return c.s.updateHub(func(config *hubConfig) error {
		i := slices.IndexFunc(config.MCPServers, func(m mcpServer) bool { return m.Name == name })
		if i < 0 {
			return errors.New("找不到 server：" + name)
		}
		change(config, i)
		return nil
	})
}

func (c *conn) mcpDelete(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Name string `json:"name"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if ferr := c.changeMCP(input.Name, func(config *hubConfig, i int) {
		config.MCPServers = slices.Delete(config.MCPServers, i, i+1)
	}); ferr != nil {
		return nil, ferr
	}
	return nil, nil
}

func (c *conn) mcpToggle(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if ferr := c.changeMCP(input.Name, func(config *hubConfig, i int) { config.MCPServers[i].Enabled = input.Enabled }); ferr != nil {
		return nil, ferr
	}
	return nil, nil
}

// hub/mcp/test：测试连接复用 agent 的 -mcp-list：SDK 连接、协商、列出工具，不请求模型。
func (c *conn) mcpTest(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Name string `json:"name"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	s := c.s
	s.hubMu.Lock()
	config, err := s.readHub()
	s.hubMu.Unlock()
	i := slices.IndexFunc(config.MCPServers, func(m mcpServer) bool { return m.Name == input.Name })
	if err != nil || i < 0 {
		return nil, bad(404, "找不到这个 server")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Minute)
	defer cancel()
	cmd := s.agentCommand(ctx, "-mcp-server", config.MCPServers[i].Command, "-mcp-list")
	output, err := cmd.CombinedOutput()
	return map[string]any{"ok": err == nil, "output": string(output)}, nil
}
