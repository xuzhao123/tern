package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/mcp"
	"github.com/xuzhao123/tern/internal/protocol"
	"github.com/xuzhao123/tern/internal/skills"
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

// MCP 的 OAuth：令牌文件由 internal/mcp 管（agent 连接远程 server 时也读它），这里只做 MCP 中心的登录流程。
// oauthLogin 是一次进行中的登录：回调按 state 找回是哪台 server、用哪个 code_verifier 与回调地址换令牌。
type oauthLogin struct {
	name     string
	url      string
	redirect string
	verifier string
	expires  time.Time
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
// skill 只按名字列出：磁盘上的目录名加上编进程序的内置 skill，坏文件由 agent 启动时报错跳过。
func (s *Server) enabledCapabilities() ([]string, []mcpServer, error) {
	s.hubMu.Lock()
	config, err := s.readHub()
	s.hubMu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	paths, _ := filepath.Glob(filepath.Join(s.dir, "skills", "*", "SKILL.md"))
	names := []string{}
	for _, path := range paths {
		name := filepath.Base(filepath.Dir(path))
		if !slices.Contains(config.DisabledSkills, name) {
			names = append(names, name)
		}
	}
	for _, name := range skills.BuiltinNames() {
		if !slices.Contains(names, name) && !slices.Contains(config.DisabledSkills, name) {
			names = append(names, name)
		}
	}
	servers := []mcpServer{}
	for _, m := range config.MCPServers {
		if m.Enabled {
			servers = append(servers, m)
		}
	}
	return names, servers, nil
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
	return map[string]any{"skills": skills, "mcp": servers, "mcp_servers": c.s.mcpViews(config.MCPServers), "hosted": "与网页端同一进程，随网页端启动和退出"}, nil
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
	return map[string]any{"path": ".data/hub.json", "skills": listed.Skills, "skill_errors": listed.Errors, "mcp_servers": s.mcpViews(config.MCPServers)}, nil
}

// mcpViews 是 MCP 中心列出的每一项：hub.json 里的 server 加上 auth，只对远程 server 有。
// auth：logged_in 令牌文件里有令牌；required 见过它要求授权（测试连接回了 401，或者刚退出登录）；
// none 没登录过、也没发现要授权。令牌与 client_secret 不经协议发给界面。
func (s *Server) mcpViews(servers []mcpServer) []map[string]any {
	views := []map[string]any{}
	for _, m := range servers {
		view := map[string]any{"name": m.Name, "command": m.Command, "enabled": m.Enabled}
		if mcp.IsURL(m.Command) {
			switch {
			case mcp.HasToken(m.Command):
				view["auth"] = "logged_in"
			case s.authRequired(m.Command):
				view["auth"] = "required"
			default:
				view["auth"] = "none"
			}
		}
		views = append(views, view)
	}
	return views
}

// setOAuthLogin 记下一次进行中的登录，顺手清掉过期的：state 只在内存里，10 分钟不用就作废。
func (s *Server) setOAuthLogin(state string, login oauthLogin) {
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	now := time.Now()
	for key, old := range s.oauth {
		if now.After(old.expires) {
			delete(s.oauth, key)
		}
	}
	s.oauth[state] = login
}

// takeOAuthLogin 按 state 取回这次登录并删掉：一个 state 只能换一次令牌，过期的不再认。
func (s *Server) takeOAuthLogin(state string) (oauthLogin, bool) {
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	login, ok := s.oauth[state]
	delete(s.oauth, state)
	if !ok || time.Now().After(login.expires) {
		return oauthLogin{}, false
	}
	return login, true
}

// markAuthRequired 记住这个 server 要求授权（登录成功、退出登录以后再改回来）：列表里的 auth 取自这里与令牌文件。
func (s *Server) markAuthRequired(url string, required bool) {
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	if required {
		s.oauthRequired[url] = true
		return
	}
	delete(s.oauthRequired, url)
}

func (s *Server) authRequired(url string) bool {
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	return s.oauthRequired[url]
}

// findMCP 按名字取 hub.json 里的一个 server：读不出来回 500，没有这个名字回 404。
func (s *Server) findMCP(name string) (mcpServer, *protocol.Error) {
	s.hubMu.Lock()
	config, err := s.readHub()
	s.hubMu.Unlock()
	if err != nil {
		return mcpServer{}, bad(500, "无法读取 .data/hub.json："+err.Error())
	}
	i := slices.IndexFunc(config.MCPServers, func(m mcpServer) bool { return m.Name == name })
	if i < 0 {
		return mcpServer{}, bad(404, "找不到 server："+name)
	}
	return config.MCPServers[i], nil
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
// 内置的删不掉（它在程序里），只能在中心里关掉。
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
		if slices.Contains(skills.BuiltinNames(), name) {
			return nil, bad(400, "内置的 skill 不能删除，可以在这里关掉")
		}
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
	command := config.MCPServers[i].Command
	cmd := s.agentCommand(ctx, "-mcp-server", command, "-mcp-list")
	output, err := cmd.CombinedOutput()
	result := map[string]any{"ok": err == nil, "output": string(output)}
	// 远程 server 要求授权时 agent 打印“MCP 需要登录”：MCP 中心据此把这一项标成“需要登录”、给“登录”按钮。
	if mcp.IsURL(command) && strings.Contains(string(output), mcp.AuthRequiredMessage) {
		result["auth"] = "required"
		s.markAuthRequired(command, true)
	}
	return result, nil
}

// hub/mcp/login/start：MCP 中心点“登录”。用 server 的地址与网页端给的回调地址建 OAuthHandler，
// 还没有 client_id 时先做动态注册（不支持的 server 让用户把客户端身份填进来），再给出授权地址。
// state 与 code_verifier 只留在内存里，回调（hub/mcp/login/finish）按 state 取回。
func (c *conn) mcpLoginStart(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Name         string `json:"name"`
		Redirect     string `json:"redirect"`
		ClientID     string `json:"client_id"`     // 可选：server 不支持动态注册时由用户填
		ClientSecret string `json:"client_secret"` // 同上；与 client_id 一起存进令牌文件，不进 hub.json
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	server, ferr := c.s.findMCP(strings.TrimSpace(input.Name))
	if ferr != nil {
		return nil, ferr
	}
	if !mcp.IsURL(server.Command) {
		return nil, bad(400, "stdio 的 server 不用登录：它由 agent 启动，凭据写在它的启动命令里")
	}
	redirect := strings.TrimSpace(input.Redirect)
	if u, err := url.Parse(redirect); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, bad(400, "redirect 要写网页端的回调地址，如 http://127.0.0.1:8090/mcp-oauth/callback")
	}
	// 用户在 MCP 中心填了 client_id/client_secret 就存进令牌文件（不进 hub.json）；留空沿用已存下的。
	if err := mcp.SetClientCredentials(server.Command, strings.TrimSpace(input.ClientID), input.ClientSecret); err != nil {
		return nil, bad(500, "令牌文件写不进去："+err.Error())
	}
	handler, err := mcp.NewOAuthHandler(server.Command, redirect)
	if err != nil {
		return nil, bad(500, "读令牌文件失败："+err.Error())
	}
	ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
	defer cancel()
	if handler.GetClientID() == "" {
		if err := handler.RegisterClient(ctx, "tern"); err != nil {
			return nil, bad(400, "这个 MCP server 不支持自动注册客户端：在 MCP 中心填上它给你的 client_id（"+err.Error()+"）")
		}
		if err := mcp.SetClientCredentials(server.Command, handler.GetClientID(), handler.GetClientSecret()); err != nil {
			return nil, bad(500, "令牌文件写不进去："+err.Error())
		}
	}
	verifier, err := mcp.GenerateCodeVerifier()
	if err != nil {
		return nil, bad(500, "生成 PKCE 的 code_verifier 失败："+err.Error())
	}
	state, err := mcp.GenerateState()
	if err != nil {
		return nil, bad(500, "生成 state 失败："+err.Error())
	}
	authURL, err := handler.GetAuthorizationURL(ctx, state, mcp.GenerateCodeChallenge(verifier))
	if err != nil {
		return nil, bad(500, "拿不到授权地址（server 的 OAuth 元数据可能不对）："+err.Error())
	}
	c.s.setOAuthLogin(state, oauthLogin{name: server.Name, url: server.Command, redirect: redirect, verifier: verifier, expires: time.Now().Add(10 * time.Minute)})
	return map[string]any{"url": authURL}, nil
}

// hub/mcp/login/finish：授权页跳回来，用 code 换令牌，存进 .data/mcp-tokens.json。
// 网页端把这个请求转给发起登录的那台主机（它按 state 找得到）。
func (c *conn) mcpLoginFinish(m protocol.Message) (any, *protocol.Error) {
	var input struct{ State, Code string }
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	state, code := strings.TrimSpace(input.State), strings.TrimSpace(input.Code)
	login, ok := c.s.takeOAuthLogin(state)
	if !ok {
		return nil, bad(400, "这次登录已经过期，或者不是从这里发起的：回 MCP 中心重新点“登录”")
	}
	if code == "" {
		return nil, bad(400, "回调里没有 code")
	}
	handler, err := mcp.NewOAuthHandler(login.url, login.redirect)
	if err != nil {
		return nil, bad(500, "读令牌文件失败："+err.Error())
	}
	// 换令牌要核对 state，而 handler 是这回新建的：把这次登录的 state 交给它。
	handler.SetExpectedState(state)
	ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
	defer cancel()
	if err := handler.ProcessAuthorizationResponse(ctx, code, state, login.verifier); err != nil {
		return nil, bad(400, "换令牌失败："+err.Error())
	}
	c.s.markAuthRequired(login.url, false)
	audit.Log(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_mcp_login", Target: login.name, Detail: c.clientName()})
	return map[string]any{"name": login.name}, nil
}

// hub/mcp/logout：从令牌文件里删掉这个 server 的地址（客户端身份与令牌一起删），列表改回“需要登录”。
func (c *conn) mcpLogout(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Name string `json:"name"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	server, ferr := c.s.findMCP(strings.TrimSpace(input.Name))
	if ferr != nil {
		return nil, ferr
	}
	if !mcp.IsURL(server.Command) {
		return nil, bad(400, "stdio 的 server 没有登录状态")
	}
	if err := mcp.DeleteToken(server.Command); err != nil {
		return nil, bad(500, "令牌文件写不进去："+err.Error())
	}
	c.s.markAuthRequired(server.Command, true)
	audit.Log(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_mcp_logout", Target: server.Name, Detail: c.clientName()})
	return nil, nil
}

func (s *Server) resolveSkillRefs(refs []skills.Reference, allowed []string) ([]string, *protocol.Error) {
	if len(refs) == 0 {
		return nil, nil
	}
	enabled, _, err := s.enabledCapabilities()
	if err != nil {
		return nil, bad(500, "无法核对技能启用状态")
	}
	for _, ref := range refs {
		if !slices.Contains(allowed, ref.Name) || !slices.Contains(enabled, ref.Name) {
			return nil, bad(409, "Skill "+ref.Name+" 未在任务中启用，请移除引用或新开任务")
		}
	}
	notes, err := skills.ResolveReferences(filepath.Join(s.dir, "skills"), refs)
	if err != nil {
		return nil, bad(409, err.Error())
	}
	return notes, nil
}
