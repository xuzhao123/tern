package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/commands"
	"github.com/xuzhao123/tern/internal/hooks"
	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/protocol"
)

// 设置：工具常驻，由这里决定；每个对话只选模型和工作区（参考 Claude Code 的 settings.json、Codex 的 config.toml）。
// 一份设置是一张扁平的“键 → 字符串值”表，按 内置默认 → 平台 → 个人 的顺序合并，后面的覆盖前面的；
// 本机能不能跑（没装 bubblewrap、没找到 Chrome）只会把工具关掉，不会打开。
// 新对话开始时把合并结果拍成快照写进 start 事件，续聊沿用快照，之后改设置不影响已经开始的对话。
// 文件放 .data/settings.json（不提交）。

// 工具开关的键，值是 on 或 off；页面按这个顺序列出。
var toolOrder = []string{"calculator", "get_current_datetime", "rag", "memory", "skills", "mcp", "subagents", "ask_user", "bash", "web_search", "open_page", "ds_search", "coding"}

// singleTools 的开关只管一个工具，键名就是工具名：关掉的写进快照的 disallowed_tools，agent 用 -disallowed-tools 去掉。
var singleTools = []string{"calculator", "get_current_datetime", "web_search", "open_page"}

// 检索和记忆默认关：检索的系统提示词是为知识库问答写的，会要求每个知识问题先查资料；记忆每次新对话都要多发几次模型请求做召回。
// 必应搜索默认关：必应多半不给自动化浏览器结果，
// 搜索交给 ds_search，浏览器只留 open_page 打开要渲染的页面。
var builtin = map[string]string{
	"calculator": "on", "get_current_datetime": "on",
	"rag": "off", "memory": "off", "skills": "on", "mcp": "on", "subagents": "on", "ask_user": "on", "bash": "on", "web_search": "off", "open_page": "on", "ds_search": "on", "coding": "on",
	"embedding": "ark", "memory_ttl": "", "provider": "ark", "model": "", "workspace": "",
	"max_steps": "0", "context_window": "0", "tool_output": "4000",
	"permission_mode": "edit", "permission_rules": "", "net_allow": "",
	"hooks": "",
}

// hooks 在宿主机上以 app-server 的身份运行命令，只能由平台设置；值是配置原文（以 { 开头）或相对 TERN_HOME 的文件路径，
// 对话开始时读成原文拍进快照，之后改文件不影响已经开始的对话。
var hooksBase string

func hooksConfig(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", nil
	}
	if !strings.HasPrefix(text, "{") {
		path := text
		if !filepath.IsAbs(path) {
			path = filepath.Join(hooksBase, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("读取 hooks 配置：%w", err)
		}
		text = string(data)
	}
	_, err := hooks.Parse(text)
	return text, err
}

// 权限规则一行一条：allow: Bash(go build *)、ask: Edit(internal/agent/**)、deny: Bash(git push *)。语法由 agent 的 permission 包校验。
var ruleLine = regexp.MustCompile(`^(allow|ask|deny):\s*([A-Za-z_][A-Za-z0-9_]*(\(.*\))?)$`)

// 设置里的具体模型名：只挡怪字符；是否在这家供应商的清单里要看合并后的 provider，由 effectiveFor 归一（见 modelFor）。
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// 沙箱联网白名单：一行一个域名（含子域名），空白分隔也行。和 -net-allow 一样只收小写域名，不收 IP 与通配符。
// 新对话开始时拍进快照作为 -net-allow；审批时点“始终允许”加到个人设置（见 alwaysAllow）。
func netDomains(text string) []string {
	var out []string
	for _, domain := range strings.Fields(text) {
		if !slices.Contains(out, domain) {
			out = append(out, domain)
		}
	}
	return out
}

func ruleLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

type workspaceDir struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type settingsFile struct {
	Platform   map[string]string            `json:"platform,omitempty"`
	Users      map[string]map[string]string `json:"users,omitempty"`
	Workspaces []workspaceDir               `json:"workspaces,omitempty"`
}

func (s *Server) settingsPath() string { return filepath.Join(s.dir, ".data", "settings.json") }

// 调用方持有 s.hubMu（与 hub.json 共用一把锁，设置和中心配置都很少写）。
func (s *Server) readSettings() (settingsFile, error) {
	var f settingsFile
	data, err := os.ReadFile(s.settingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &f)
	}
	return f, err
}

func (s *Server) writeSettings(f settingsFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.settingsPath()), 0o700); err != nil {
		return err
	}
	tmp := s.settingsPath() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.settingsPath())
}

// valid 检查一个设置值；未知的键直接拒绝，免得拼错的键悄悄不生效。
func valid(key, value string, f settingsFile) error {
	if slices.Contains(toolOrder, key) {
		if value != "on" && value != "off" {
			return fmt.Errorf("%s 只能是 on 或 off", key)
		}
		return nil
	}
	switch key {
	case "embedding":
		if value != "ark" && value != "local" {
			return errors.New("embedding 只能是 ark 或 local")
		}
	case "memory_ttl":
		if d, err := time.ParseDuration(value); value != "" && (err != nil || d <= 0) {
			return errors.New("memory_ttl 是空（永久）或正的时长，如 1h")
		}
	case "provider":
		if _, ok := llm.FindProvider(value); !ok {
			return errors.New("provider 取 " + llm.ProviderNames())
		}
	// 具体模型：空表示用这家供应商的默认值。这里只挡住不像模型名的输入；
	// 它属不属于当前供应商要看合并后的 provider，所以由 effectiveFor 归一。
	case "model":
		if !modelName.MatchString(value) {
			return errors.New("model 是这家供应商清单里的模型名，空表示用供应商默认值")
		}
	case "workspace":
		if value != "" && !slices.ContainsFunc(f.Workspaces, func(w workspaceDir) bool { return w.Name == value }) {
			return fmt.Errorf("没有登记名为 %s 的工作区", value)
		}
	case "permission_mode":
		if !slices.Contains([]string{"plan", "ask", "edit", "full"}, value) {
			return errors.New("permission_mode 取 plan、ask、edit 或 full")
		}
	case "permission_rules":
		for _, line := range ruleLines(value) {
			if !ruleLine.MatchString(line) {
				return fmt.Errorf("权限规则一行一条，格式如 allow: Bash(go build *)：%q", line)
			}
		}
	case "net_allow":
		for _, domain := range netDomains(value) {
			if len(domain) > 253 || !domainPattern.MatchString(domain) {
				return fmt.Errorf("沙箱联网白名单一行一个小写域名（不含协议、路径和 IP）：%q", domain)
			}
		}
	case "hooks":
		if _, err := hooksConfig(value); err != nil {
			return err
		}
	case "max_steps", "context_window", "tool_output":
		limits := map[string][2]int{"max_steps": {0, 1000}, "context_window": {4096, 1048576}, "tool_output": {64, 32768}}[key]
		// context_window 为 0 表示按模型：开对话时取路由表里这个模型的窗口。
		if n, err := strconv.Atoi(value); err != nil || (n < limits[0] && !(key == "context_window" && n == 0)) || n > limits[1] {
			return fmt.Errorf("%s 范围为 %d 到 %d", key, limits[0], limits[1])
		}
	default:
		return fmt.Errorf("未知设置项 %s", key)
	}
	return nil
}

// unavailable 返回本机跑不了这个工具的原因；空串表示可用。只做便宜的检查（找可执行文件、看密钥在不在），不真的启动。
func (s *Server) unavailable(key string, skills []string, servers []mcpServer) string {
	switch key {
	case "bash":
		if runtime.GOOS != "linux" {
			return "Bash 沙箱只支持 Linux"
		}
		if _, err := exec.LookPath("bwrap"); err != nil {
			return "没有找到 bubblewrap（bwrap），请先安装"
		}
	case "web_search", "open_page":
		if os.Getenv("CHROME_PATH") != "" {
			return ""
		}
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
			if _, err := exec.LookPath(name); err == nil {
				return ""
			}
		}
		return "没有找到 Chrome：在启动 app-server 的环境里设置 CHROME_PATH"
	case "ds_search":
		if _, err := llm.LoadConfig("deepseek"); err != nil {
			return "没有 DeepSeek 密钥：在 .env 或环境变量里配置 DEEPSEEK_API_KEY"
		}
	case "skills":
		if len(skills) == 0 {
			return "Skills 中心里没有启用的 skill"
		}
	case "mcp":
		if len(servers) == 0 {
			return "MCP 中心里没有启用的 server"
		}
	}
	return ""
}

// effective 是某个账号此刻的设置：合并后的值、每个值来自哪一层、被关掉的工具及原因。
type effective struct {
	Values     map[string]string `json:"values"`
	Source     map[string]string `json:"source"`     // builtin、platform、user
	Blocked    map[string]string `json:"blocked"`    // 工具 → 被关掉的原因（本机不可用）
	Workspaces []workspaceDir    `json:"workspaces"` // 这个账号能选的工作区
	Skills     []string          `json:"skills"`
	MCP        []mcpServer       `json:"mcp"`
}

// effectiveFor 是某个账号此刻的设置；name 为空（chat 这类不带账号的界面）就是没有个人层。
func (s *Server) effectiveFor(name string) (effective, settingsFile, error) {
	s.hubMu.Lock()
	f, err := s.readSettings()
	s.hubMu.Unlock()
	if err != nil {
		return effective{}, f, err
	}
	skills, servers, err := s.enabledCapabilities()
	if err != nil {
		return effective{}, f, err
	}
	e := effective{Values: maps.Clone(builtin), Source: map[string]string{}, Blocked: map[string]string{}, Skills: skills, MCP: servers}
	for key := range builtin {
		e.Source[key] = "builtin"
	}
	for _, layer := range []struct {
		name   string
		values map[string]string
	}{{"platform", f.Platform}, {"user", f.Users[name]}} {
		for key, value := range layer.values {
			if layer.name == "user" && key == "hooks" {
				continue
			}
			if _, ok := builtin[key]; ok && valid(key, value, f) == nil {
				e.Values[key], e.Source[key] = value, layer.name
			}
		}
	}
	// 具体模型同理：合并后不在当前供应商清单里就当没设（改了供应商、旧模型名留在个人设置里的情况），
	// 否则新对话会按清单外的名字启动、被 startRun 拒绝。
	if kept := modelFor(e.Values["provider"], e.Values["model"]); kept != e.Values["model"] {
		e.Values["model"], e.Source["model"] = "", "builtin"
	}
	for _, key := range toolOrder {
		if reason := s.unavailable(key, skills, servers); reason != "" {
			e.Blocked[key] = reason
		}
	}
	if e.Values["embedding"] == "ark" {
		if _, err := llm.LoadConfig("ark"); err != nil {
			for _, key := range []string{"rag", "memory"} {
				if _, blocked := e.Blocked[key]; !blocked {
					e.Blocked[key] = "线上向量需要 ARK_API_KEY；可改用本地向量模型"
				}
			}
		}
	}
	if _, blocked := e.Blocked["coding"]; !blocked {
		e.Workspaces = f.Workspaces
	}
	if e.Workspaces == nil {
		e.Workspaces = []workspaceDir{}
	}
	return e, f, nil
}

// on 表示这个工具在新对话里会带上：设置打开，而且没有被本机条件关掉。
func (e effective) on(key string) bool {
	_, blocked := e.Blocked[key]
	return e.Values[key] == "on" && !blocked
}

// modelFor 回答“这个模型名在这家供应商的清单里算不算数”：空串（用供应商默认值）与清单里的名字原样返回，
// 其余（改了供应商后留下的旧名字）返回空。清单只在 llm 包里，这里照它判断，不做第二份。
func modelFor(provider, model string) string {
	if model == "" {
		return ""
	}
	if p, ok := llm.FindProvider(provider); ok {
		if _, found := p.FindModel(model); found {
			return model
		}
	}
	return ""
}

// settings/read：合并后的设置、每一层自己的值、内置默认与工具顺序，界面据此画设置页。
func (c *conn) settingsRead(m protocol.Message) (any, *protocol.Error) {
	name := userName(m)
	e, f, err := c.s.effectiveFor(name)
	if err != nil {
		return nil, bad(500, "无法读取设置："+err.Error())
	}
	return map[string]any{"effective": e, "platform": f.Platform, "user": f.Users[name], "builtin": builtin, "tool_order": toolOrder}, nil
}

// settings/write 保存一层设置：参数是这一层想要的完整取值，只存和下一层不同的键，
// 这样平台默认改了，没有单独设过的人跟着变。传 {} 表示这一层恢复为继承。
// layer 与 user 和设置项平铺在同一份参数里：请求体里原来怎么放设置项，现在照样放。
func (c *conn) settingsWrite(m protocol.Message) (any, *protocol.Error) {
	var want map[string]string
	if err := json.Unmarshal(m.Params, &want); err != nil {
		return nil, bad(400, "请求必须是 JSON 对象")
	}
	layer, name := want["layer"], userName(m)
	delete(want, "layer")
	delete(want, "user")
	if layer != "user" && layer != "platform" {
		return nil, bad(400, "layer 取 user 或 platform")
	}
	s := c.s
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	f, err := s.readSettings()
	if err != nil {
		return nil, bad(500, err.Error())
	}
	base := maps.Clone(builtin)
	if layer == "user" {
		maps.Copy(base, f.Platform)
	}
	diff := map[string]string{}
	for key, value := range want {
		if err := valid(key, value, f); err != nil {
			return nil, bad(400, err.Error())
		}
		if layer == "user" && key == "hooks" && value != base[key] {
			return nil, bad(403, "hooks 会在本机上运行命令，只能在平台设置里配置")
		}
		if base[key] != value {
			diff[key] = value
		}
	}
	if layer == "platform" {
		f.Platform = diff
	} else {
		if f.Users == nil {
			f.Users = map[string]map[string]string{}
		}
		f.Users[name] = diff
		if len(diff) == 0 {
			delete(f.Users, name)
		}
	}
	if err := s.writeSettings(f); err != nil {
		return nil, bad(500, err.Error())
	}
	return nil, nil
}

// alwaysAllow 把审批时点了“始终允许”的域名加进这个账号的个人设置：以此刻对这个账号生效的白名单为底（自己设过就是自己的，
// 否则是平台的），追加后存成个人层的值，和在设置页里自己加一行是同一件事。只影响之后的新对话。
func (s *Server) alwaysAllow(name, domain string) error {
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	f, err := s.readSettings()
	if err != nil {
		return err
	}
	value, own := f.Users[name]["net_allow"]
	if !own {
		value = f.Platform["net_allow"]
	}
	list := netDomains(value)
	if slices.Contains(list, domain) {
		return nil
	}
	if f.Users == nil {
		f.Users = map[string]map[string]string{}
	}
	if f.Users[name] == nil {
		f.Users[name] = map[string]string{}
	}
	f.Users[name]["net_allow"] = strings.Join(append(list, domain), "\n")
	return s.writeSettings(f)
}

// workspace/add：登记本机上一个已存在的 git 仓库目录。对话只能从这个列表里选，界面不能直接给路径。
func (c *conn) workspaceAdd(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Path string `json:"path"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if strings.TrimSpace(input.Path) == "" {
		return nil, bad(400, "请填写目录路径")
	}
	s := c.s
	path := strings.TrimSpace(input.Path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.dir, path) // 相对路径按 TERN_HOME 解析，和 agent 的工作目录一致
	}
	path = filepath.Clean(path)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return nil, bad(400, "目录不存在："+path)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return nil, bad(400, "不是 git 仓库（没有 .git）："+path+"；改坏了要能用 git 回退")
	}
	name := filepath.Base(path)
	if !serverName.MatchString(strings.ToLower(name)) {
		name = "workspace"
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	f, err := s.readSettings()
	if err != nil {
		return nil, bad(500, err.Error())
	}
	for _, existing := range f.Workspaces {
		if existing.Path == path {
			return nil, bad(409, "这个目录已经登记过："+existing.Name)
		}
	}
	base := strings.ToLower(name)
	for i := 2; slices.ContainsFunc(f.Workspaces, func(w workspaceDir) bool { return w.Name == name }); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	f.Workspaces = append(f.Workspaces, workspaceDir{Name: name, Path: path})
	if err := s.writeSettings(f); err != nil {
		return nil, bad(500, err.Error())
	}
	return workspaceDir{Name: name, Path: path}, nil
}

// workspace/remove：删掉一个登记的工作区，并清掉指向它的默认值。
func (c *conn) workspaceRemove(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Name string `json:"name"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	s := c.s
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	f, err := s.readSettings()
	if err != nil {
		return nil, bad(500, err.Error())
	}
	n := len(f.Workspaces)
	f.Workspaces = slices.DeleteFunc(f.Workspaces, func(w workspaceDir) bool { return w.Name == input.Name })
	if len(f.Workspaces) == n {
		return nil, bad(404, "找不到这个工作区")
	}
	// 设成默认工作区的层一并清掉，免得指向不存在的名字。
	if f.Platform["workspace"] == input.Name {
		delete(f.Platform, "workspace")
	}
	for _, values := range f.Users {
		if values["workspace"] == input.Name {
			delete(values, "workspace")
		}
	}
	if err := s.writeSettings(f); err != nil {
		return nil, bad(500, err.Error())
	}
	return nil, nil
}

// command/list：斜杠命令列表，输入框打 / 时显示。个人级来自 TERN_HOME 的 commands/，项目级来自所选工作区的 .agents/commands/；
// 展开由 agent 做，这里只列名字和说明。
func (c *conn) commandList(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Workspace string `json:"workspace"`
		User      string `json:"user"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	e, _, err := c.s.effectiveFor(input.User)
	if err != nil {
		return nil, bad(500, err.Error())
	}
	dir := ""
	if name := input.Workspace; name != "" && e.on("coding") {
		if i := slices.IndexFunc(e.Workspaces, func(w workspaceDir) bool { return w.Name == name }); i >= 0 {
			dir = e.Workspaces[i].Path
		}
	}
	return commands.List(c.s.dir, dir), nil
}
