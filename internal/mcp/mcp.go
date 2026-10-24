package mcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/protocol"
	"github.com/xuzhao123/tern/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// stdio server 是第三方程序，只给它最小的环境（照 Codex 的 DEFAULT_ENV_VARS），再加 go run 起 server 要用的工具链变量。
// 模型密钥、网页端的代理令牌都在本进程的环境里，mcp-go 默认把整个 os.Environ() 传下去，所以命令由这里自己建。
var serverEnvNames = []string{"HOME", "LOGNAME", "PATH", "SHELL", "USER", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "TMPDIR", "TZ",
	"GOPATH", "GOROOT", "GOCACHE", "GOMODCACHE", "GOPROXY", "GOSUMDB", "GOFLAGS", "GOTOOLCHAIN", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "GOINSECURE"}

func serverEnv() []string {
	var env []string
	for _, name := range serverEnvNames {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// -mcp-server 启动的 MCP 连接，每个 server 一个；空表示未启用。
var Conns []*Connection

// 没报名字的 server 用连接的序号命名（server1、server2…）；按成功连接的先后数。
var serverCount int

type Connection struct {
	client *client.Client
	// Name 是 server 自报的名字（serverInfo.name），没报时是本进程里连接的序号 server1、server2…；
	// 模型用它在 list_mcp_resources、read_mcp_resource 里指定 server。
	Name         string
	hasResources bool              // server 声明了 resources capability
	tools        map[string]string // agent 侧工具名 → server 上的原名
	transport    string            // network.transport，stdio 为 pipe，Streamable HTTP 为 tcp
	// server 明确声明 destructiveHint=true 的工具，执行前要用户确认。
	// 注解是 server 自己说的，只用来收紧：声明了破坏性就确认；没声明或自称只读不因此免检（外发检查照常）。
	destructive map[string]bool
	hints       map[string]Hints // server 声明的只读、幂等，用于调度和续跑
}

// Hints 是 server 对一个工具的标注里调度要用的两项；没有标注时都是 false（按规范的默认值：可能写、不幂等）。
type Hints struct{ ReadOnly, Idempotent bool }

// HintsOf 取 agent 侧这个工具的标注。
func HintsOf(name string) Hints {
	for _, conn := range Conns {
		if h, ok := conn.hints[name]; ok {
			return h
		}
	}
	return Hints{}
}

// Destructive 报告 agent 侧的这个工具是否被 server 声明为破坏性操作。
func Destructive(name string) bool {
	for _, conn := range Conns {
		if conn.destructive[name] {
			return true
		}
	}
	return false
}

// -mcp-server 的值是 http(s) 地址时连远程 server（Streamable HTTP），否则当作 stdio 子进程命令。
func IsURL(value string) bool {
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

// 建立传输后完成连接：mcp-go 先发 server/discover（2026-07-28 起的无状态协议），
// 服务端不认识时退回 initialize 握手；两种方式交换的都是协议版本与 capabilities。
// 两种传输之后的协议消息完全相同，区别只在消息怎么送达：stdio 写子进程 stdin，HTTP 每条消息一个 POST。
func Connect(ctx context.Context, command string) (conn *Connection, listed []sdk.Tool, err error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	transportName := "pipe"
	if IsURL(command) {
		transportName = "tcp"
	}
	// 连接（握手 + tools/list）一个 span，挂在本次运行的根 span 下。
	ctx, span := telemetry.Begin(ctx, "initialize", trace.SpanKindClient, attribute.String("mcp.method.name", "initialize"), attribute.String("network.transport", transportName))
	defer func() {
		span.SetAttributes(attribute.Int("mcp.tools.count", len(listed)))
		telemetry.End(span, err, "mcp_connect_error")
	}()
	var c *client.Client
	if IsURL(command) {
		protocol.Event("capability/event", "mcp_connect", "MCP connect: url="+command)
		// 远程 server 不是本进程启动的，Close 只断开连接，不会结束对方。
		// 令牌文件里有这个地址的令牌就用 OAuth 客户端：过期的令牌由 mcp-go 用 refresh token 换新，写回同一个文件。
		// 没有令牌仍是普通客户端，server 回 401 时在下面按“需要登录”处理。
		if HasToken(command) {
			config, configErr := oauthConfig(command, "")
			if configErr == nil {
				c, err = client.NewOAuthStreamableHttpClient(command, config)
			} else {
				err = configErr
			}
		} else {
			c, err = client.NewStreamableHttpClient(command)
		}
		if err == nil {
			err = c.Start(ctx)
		}
		if err != nil {
			return nil, nil, remoteError(command, err)
		}
	} else {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			return nil, nil, errors.New("mcp-server 需要一条命令或 http(s) 地址")
		}
		protocol.Event("capability/event", "mcp_connect", "MCP connect: command="+command)
		// server 的 stderr 是它的日志通道，原样转到本进程 stderr；stdout 只承载协议消息。
		spawn := func(ctx context.Context, command string, _ []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Env = serverEnv()
			return cmd, nil
		}
		c, err = client.NewStdioMCPClientWithOptions(fields[0], nil, fields[1:], transport.WithCommandFunc(spawn), transport.WithCommandStderrWriter(os.Stderr))
		if err != nil {
			return nil, nil, fmt.Errorf("无法启动 MCP server：%w", err)
		}
	}
	request := sdk.InitializeRequest{}
	request.Params.ProtocolVersion = sdk.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = sdk.Implementation{Name: "tern", Version: "0.6.0"}
	info, err := c.Initialize(ctx, request)
	if err != nil {
		c.Close()
		if IsURL(command) {
			return nil, nil, remoteError(command, err)
		}
		return nil, nil, fmt.Errorf("MCP 连接失败：%w", err)
	}
	// MCP 约定的 span 名是“方法 目标”；握手后才知道 server 名。
	span.SetName("initialize " + info.ServerInfo.Name)
	span.SetAttributes(attribute.String("mcp.protocol.version", c.ProtocolVersion()), attribute.String("mcp.server.name", info.ServerInfo.Name))
	capabilities, _ := json.Marshal(info.Capabilities)
	protocol.Event("capability/event", "mcp_initialize", fmt.Sprintf("MCP initialize: protocol=%s server=%s/%s capabilities=%s", c.ProtocolVersion(), info.ServerInfo.Name, info.ServerInfo.Version, capabilities))
	if info.Capabilities.Tools == nil {
		c.Close()
		return nil, nil, errors.New("MCP server 没有声明 tools capability")
	}
	result, err := c.ListTools(ctx, sdk.ListToolsRequest{})
	if err != nil {
		c.Close()
		return nil, nil, fmt.Errorf("tools/list 失败：%w", err)
	}
	protocol.Event("capability/event", "mcp_tools_list", fmt.Sprintf("MCP tools/list: count=%d", len(result.Tools)), "count", len(result.Tools))
	serverCount++
	name := info.ServerInfo.Name
	if name == "" {
		name = fmt.Sprintf("server%d", serverCount)
	}
	return &Connection{client: c, Name: name, hasResources: info.Capabilities.Resources != nil, tools: map[string]string{}, transport: transportName, destructive: map[string]bool{}, hints: map[string]Hints{}}, result.Tools, nil
}

// remoteError 是连远程 server 失败时给人看的话：要求授权不是“连不上”，提示去 MCP 中心点“登录”，
// 这个 server 和连不上的一样跳过（MCP 中心的“测试连接”也据这句话认出要登录）。
func remoteError(command string, err error) error {
	if client.IsAuthorizationRequiredError(err) {
		return fmt.Errorf("%s：%s，到 MCP 中心点“登录”", AuthRequiredMessage, command)
	}
	return fmt.Errorf("无法连接远程 MCP server：%w", err)
}

var invalidToolChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// MCP tool → 方舟 function 定义：name 加 mcp_ 前缀避免和本地工具重名，
// inputSchema 本身就是 JSON Schema，原样作为 parameters。
func (m *Connection) Definitions(tools []sdk.Tool) []map[string]any {
	taken := map[string]bool{}
	for _, definition := range llm.Tools {
		taken[definition["name"].(string)] = true
	}
	definitions := []map[string]any{}
	for _, tool := range tools {
		// 方舟函数名只允许字母、数字、_、-，最长64；MCP 还允许“.”，替换掉。
		name := "mcp_" + invalidToolChars.ReplaceAllString(tool.Name, "_")
		if len(name) > 64 || taken[name] {
			protocol.Event("capability/event", "mcp_skip", "MCP skip: tool="+tool.Name+" reason=名称过长或与已有工具重名")
			continue
		}
		data, err := json.Marshal(tool)
		var decoded struct {
			InputSchema map[string]any `json:"inputSchema"`
		}
		if err != nil || json.Unmarshal(data, &decoded) != nil || decoded.InputSchema == nil {
			protocol.Event("capability/event", "mcp_skip", "MCP skip: tool="+tool.Name+" reason=inputSchema 无效")
			continue
		}
		taken[name] = true
		m.tools[name] = tool.Name
		if hint := tool.Annotations.DestructiveHint; hint != nil && *hint {
			m.destructive[name] = true
		}
		hints := tool.Annotations
		m.hints[name] = Hints{ReadOnly: hints.ReadOnlyHint != nil && *hints.ReadOnlyHint, Idempotent: hints.IdempotentHint != nil && *hints.IdempotentHint}
		definitions = append(definitions, map[string]any{"name": name, "description": tool.Description, "parameters": decoded.InputSchema})
		protocol.Event("capability/event", "mcp_register", "MCP register: "+tool.Name+" → "+name, "tool", name)
	}
	return definitions
}

// tools/call 的两种失败分开：协议错误（JSON-RPC error，如未知工具）与工具执行错误（isError: true）。
// 两者都回填给模型；执行错误里通常写着怎么改参数。
func (m *Connection) Call(ctx context.Context, name, arguments string) (output any, err error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil, errors.New("工具参数需要是 JSON 对象")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// client 一侧的 tools/call span。errorType 区分协议错误与 server 明确返回的工具执行错误。
	ctx, span := telemetry.Begin(ctx, "tools/call "+m.tools[name], trace.SpanKindClient, attribute.String("mcp.method.name", "tools/call"),
		semconv.GenAIToolName(m.tools[name]), attribute.String("network.transport", m.transport))
	errorType := "tool_error"
	defer func() { telemetry.End(span, err, errorType) }()
	request := sdk.CallToolRequest{}
	request.Params.Name, request.Params.Arguments = m.tools[name], args
	// SEP-414：trace context 放在 params._meta 的 traceparent/tracestate 里，server 取出后作为远程父 span。
	// 不支持的 server 会忽略未知的 _meta 键。
	meta := map[string]any{}
	for key, value := range telemetry.Carrier(ctx) {
		meta[key] = value
	}
	request.Params.Meta = sdk.NewMetaFromMap(meta)
	result, err := m.client.CallTool(ctx, request)
	if err != nil {
		errorType = "protocol_error"
		return nil, fmt.Errorf("MCP 协议错误：%w", err)
	}
	texts := []string{}
	for _, content := range result.Content {
		if text, ok := sdk.AsTextContent(content); ok {
			texts = append(texts, text.Text)
		} else {
			texts = append(texts, fmt.Sprintf("[非文本内容 %T 未转给模型]", content))
		}
	}
	if result.IsError {
		// server 已经明确拒绝（多为参数错误），原样再发一次结果相同，不重试。
		return nil, llm.Permanent(errors.New("MCP 工具执行错误：" + strings.Join(texts, "\n")))
	}
	value := map[string]any{"content": texts}
	if result.StructuredContent != nil {
		value["structured"] = result.StructuredContent
	}
	return value, nil
}

// Owns 报告 agent 侧工具名是否属于这个 server。
func (m *Connection) Owns(name string) bool { return m.tools[name] != "" }

// 两个资源工具的定义：模型用它们看 server 提供的只读内容。性质（只读、可重做）声明在 agent 的 toolspec.go。
var ResourceDefinitions = []map[string]any{
	{"name": "list_mcp_resources", "description": "列出已连接的 MCP server 提供的 resources（只读的文档、表结构、文件等）。server 不给就列全部。每项含 server、uri、name、description、mimeType；读内容用 read_mcp_resource。", "parameters": map[string]any{
		"type":       "object",
		"properties": map[string]any{"server": map[string]string{"type": "string", "description": "可选：MCP server 的名字，如 server1；不给就列全部"}},
	}},
	{"name": "read_mcp_resource", "description": "读取 MCP server 的一个 resource。server 与 uri 取自 list_mcp_resources；文本内容原样返回，二进制只返回类型和大小。", "parameters": map[string]any{
		"type": "object", "required": []string{"server", "uri"},
		"properties": map[string]any{
			"server": map[string]string{"type": "string", "description": "MCP server 的名字，取自 list_mcp_resources"},
			"uri":    map[string]string{"type": "string", "description": "资源的 uri，取自 list_mcp_resources"},
		},
	}},
}

// HasResources 报告是否有一个已连接的 server 声明了 resources。有就把两个资源工具加进工具列表（只加一次，不按 server 加）。
func HasResources() bool {
	for _, conn := range Conns {
		if conn.hasResources {
			return true
		}
	}
	return false
}

// 每个 server 最多列这么多：资源可能是成百上千个模板实例，全部列进上下文没有意义。
const maxResources = 200

// connNamed 按模型给的名字找连接。
func connNamed(name string) (*Connection, error) {
	for _, conn := range Conns {
		if conn.Name == name {
			return conn, nil
		}
	}
	names := make([]string, 0, len(Conns))
	for _, conn := range Conns {
		names = append(names, conn.Name)
	}
	return nil, llm.Permanent(fmt.Errorf("没有名为 %q 的 MCP server；已连接的：%s", name, strings.Join(names, "、")))
}

// resources 翻完一个 server 的分页（cursor 由 mcp-go 传），最多留 maxResources 项；more 表示还有没列出来的。
func (m *Connection) resources(ctx context.Context) (items []sdk.Resource, more bool, err error) {
	request := sdk.ListResourcesRequest{}
	for {
		result, err := m.client.ListResourcesByPage(ctx, request)
		if err != nil {
			return nil, false, err
		}
		for _, resource := range result.Resources {
			if len(items) == maxResources {
				return items, true, nil
			}
			items = append(items, resource)
		}
		if result.NextCursor == "" {
			return items, false, nil
		}
		request.Params.Cursor = result.NextCursor
	}
}

// ListResources 实现 list_mcp_resources：server 为空就列全部已连接的 server。
func ListResources(ctx context.Context, server string) (any, error) {
	targets := Conns
	if server = strings.TrimSpace(server); server != "" {
		conn, err := connNamed(server)
		if err != nil {
			return nil, err
		}
		targets = []*Connection{conn}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	resources := []map[string]any{}
	notes := []string{}
	for _, conn := range targets {
		if !conn.hasResources {
			if server != "" {
				notes = append(notes, conn.Name+" 没有声明 resources")
			}
			continue
		}
		items, more, err := conn.resources(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s 的 resources/list 失败：%w", conn.Name, err)
		}
		for _, resource := range items {
			resources = append(resources, map[string]any{"server": conn.Name, "uri": resource.URI, "name": resource.Name,
				"description": resource.Description, "mimeType": resource.MIMEType})
		}
		if more {
			notes = append(notes, fmt.Sprintf("%s 超过 %d 项，只列了前面的", conn.Name, maxResources))
		}
	}
	value := map[string]any{"resources": resources}
	if len(notes) > 0 {
		value["note"] = strings.Join(notes, "；")
	}
	return value, nil
}

// ReadResource 实现 read_mcp_resource：文本原样给模型，二进制只报类型和大小（塞进上下文也读不懂，还占预算）。
func ReadResource(ctx context.Context, server, uri string) (any, error) {
	conn, err := connNamed(strings.TrimSpace(server))
	if err != nil {
		return nil, err
	}
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil, llm.Permanent(errors.New("uri 不能为空，取自 list_mcp_resources"))
	}
	if !conn.hasResources {
		return nil, llm.Permanent(fmt.Errorf("%s 没有声明 resources", conn.Name))
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	request := sdk.ReadResourceRequest{}
	request.Params.URI = uri
	result, err := conn.client.ReadResource(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("MCP 协议错误：%w", err)
	}
	contents := []any{}
	for _, content := range result.Contents {
		switch content := content.(type) {
		case sdk.TextResourceContents:
			contents = append(contents, content.Text)
		case sdk.BlobResourceContents:
			// 解一遍拿到准确字节数；base64 不合法时退回按长度估算。
			size := base64.StdEncoding.DecodedLen(len(content.Blob))
			if data, err := base64.StdEncoding.DecodeString(content.Blob); err == nil {
				size = len(data)
			}
			contents = append(contents, map[string]any{"mimeType": content.MIMEType, "bytes": size, "note": "二进制内容不放进上下文"})
		default:
			contents = append(contents, fmt.Sprintf("[未转给模型的内容 %T]", content))
		}
	}
	return map[string]any{"server": conn.Name, "uri": uri, "contents": contents}, nil
}

func (m *Connection) Close() {
	// stdio：Close 关闭子进程 stdin 并等待退出，正常关闭信号就是 EOF；HTTP：只取消进行中的请求。
	if err := m.client.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "MCP close:", err)
	}
}

// 第2步：只用 SDK 走连接 → tools/list →（可选）tools/call，不请求模型。
func Inspect(ctx context.Context, command, callName, callArgs string) error {
	conn, tools, err := Connect(ctx, command)
	if err != nil {
		return err
	}
	defer conn.Close()
	for i, tool := range tools {
		schema, _ := json.Marshal(tool.InputSchema)
		fmt.Printf("  %d. %s — %s\n     inputSchema=%s\n", i+1, tool.Name, tool.Description, schema)
	}
	// 资源数目只在 server 声明了 resources 时列；网页端 MCP 中心的“测试连接”显示的就是这段输出。
	if conn.hasResources {
		items, more, err := conn.resources(ctx)
		if err != nil {
			fmt.Printf("  resources：列出失败：%v\n", err)
		} else if more {
			fmt.Printf("  resources：%d 个（还有更多）\n", len(items))
		} else {
			fmt.Printf("  resources：%d 个\n", len(items))
		}
	}
	if callName == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(callArgs), &args); err != nil {
		return errors.New("mcp-args 需要是 JSON 对象")
	}
	request := sdk.CallToolRequest{}
	request.Params.Name, request.Params.Arguments = callName, args
	result, err := conn.client.CallTool(ctx, request)
	if err != nil {
		return fmt.Errorf("tools/call 协议错误：%w", err)
	}
	data, _ := json.Marshal(result)
	fmt.Printf("MCP tools/call: name=%s isError=%t result=%s\n", callName, result.IsError, data)
	return nil
}

// 进阶：不用 SDK，自己拼 JSON-RPC 写进子进程 stdin、逐行读 stdout，看清线上的每一条消息。
// legacy：initialize → notifications/initialized → 请求不带 _meta；
// modern：server/discover，之后每个请求的 _meta 都带协议版本、客户端身份与 capabilities。
func Raw(ctx context.Context, command, era, callName, callArgs string) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return errors.New("mcp-server 需要一条命令")
	}
	if callName != "" && !json.Valid([]byte(callArgs)) {
		return errors.New("mcp-args 需要是有效 JSON（可以故意传错类型，观察 server 端校验）")
	}
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	cmd.Env = serverEnv()
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法启动 MCP server：%w", err)
	}
	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 8<<20)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	send := func(message map[string]any) error {
		message["jsonrpc"] = "2.0"
		data, err := json.Marshal(message)
		if err != nil {
			return err
		}
		fmt.Printf("→ %s\n", data)
		// 换行就是 stdio 的消息分隔符，所以单条 JSON 内部不能有换行（Marshal 不会产生）。
		_, err = stdin.Write(append(data, '\n'))
		return err
	}
	// 等待指定 id 的响应；途中收到的通知也打印出来。
	receive := func(id int) (map[string]json.RawMessage, error) {
		timeout := time.After(10 * time.Second)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					return nil, errors.New("server 关闭了 stdout")
				}
				fmt.Printf("← %s\n", line)
				var message map[string]json.RawMessage
				if json.Unmarshal([]byte(line), &message) != nil {
					return nil, errors.New("server 在 stdout 写了非 JSON-RPC 内容")
				}
				if string(message["id"]) == strconv.Itoa(id) {
					return message, nil
				}
			case <-timeout:
				return nil, fmt.Errorf("等待 id=%d 的响应超时", id)
			}
		}
	}
	request := func(id int, method string, params map[string]any) error {
		if era == "modern" {
			params["_meta"] = map[string]any{
				"io.modelcontextprotocol/protocolVersion":    sdk.ProtocolVersion20260728,
				"io.modelcontextprotocol/clientInfo":         map[string]string{"name": "tern-raw", "version": "0.6.0"},
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			}
		}
		if err := send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			return err
		}
		response, err := receive(id)
		if err == nil && response["error"] != nil {
			fmt.Printf("   ↳ JSON-RPC error：%s\n", response["error"])
		}
		return err
	}
	run := func() error {
		if era == "modern" {
			if err := request(1, "server/discover", map[string]any{}); err != nil {
				return err
			}
		} else {
			// 握手只做一次：客户端报上自己想用的版本和能力，服务端回它选定的版本和能力。
			if err := request(1, "initialize", map[string]any{
				"protocolVersion": sdk.ProtocolVersion20251125,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]string{"name": "tern-raw", "version": "0.6.0"},
			}); err != nil {
				return err
			}
			// 通知没有 id，也不会有响应。
			if err := send(map[string]any{"method": "notifications/initialized"}); err != nil {
				return err
			}
		}
		if err := request(2, "tools/list", map[string]any{}); err != nil {
			return err
		}
		if callName != "" {
			return request(3, "tools/call", map[string]any{"name": callName, "arguments": json.RawMessage(callArgs)})
		}
		return nil
	}
	runErr := run()
	stdin.Close()
	waitErr := cmd.Wait()
	if runErr != nil {
		return runErr
	}
	if waitErr != nil && ctx.Err() == nil {
		return fmt.Errorf("MCP server 退出异常：%w", waitErr)
	}
	return nil
}

// 把 calculate 暴露成 calculator 的 MCP server。server 与传输分开：
// Serve 用 stdio 或独立 HTTP 端口运行；Handler 让网页端在自己的 /mcp 上直接提供（同一进程，不转发）。
func newServer(logger *log.Logger) *server.MCPServer {
	s := server.NewMCPServer("tern-calculator", "0.7.0",
		server.WithToolCapabilities(false),
		// 第一道：按 inputSchema 校验（类型、必填、长度、多余字段），失败返回 isError 结果。
		server.WithInputSchemaValidation(),
		server.WithStrictInputSchemaDefault(),
		server.WithRecovery(),
	)
	tool := sdk.NewTool("calculator",
		sdk.WithDescription(calculatorDescription),
		// 如实声明只读、无破坏性、不访问外部（mcp-go 的默认值按规范是“可能破坏、会访问外部”）。
		sdk.WithReadOnlyHintAnnotation(true), sdk.WithDestructiveHintAnnotation(false), sdk.WithIdempotentHintAnnotation(true), sdk.WithOpenWorldHintAnnotation(false),
		sdk.WithString("expression", sdk.Required(), sdk.MaxLength(1024), sdk.Description("数学表达式，例如 (1234*5678) 或 floor(sqrt(2))")),
	)
	s.AddTool(tool, func(ctx context.Context, request sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		// server 一侧的 span。父 span 来自 client 放在 _meta 里的 traceparent；没有时是一个新的 trace。
		if request.Params.Meta != nil {
			ctx = telemetry.Extract(ctx, request.Params.Meta.AdditionalFields)
		}
		_, span := telemetry.Begin(ctx, "tools/call calculator", trace.SpanKindServer, attribute.String("mcp.method.name", "tools/call"), semconv.GenAIToolName("calculator"))
		var failure error
		defer func() { telemetry.End(span, failure, "tool_error") }()
		// 第二道：handler 自己取参。schema 校验是可关的选项，不能当成唯一防线。
		expression, err := request.RequireString("expression")
		if err != nil {
			logger.Printf("calculator rejected: %v", err)
			failure = err
			return sdk.NewToolResultError(err.Error()), nil
		}
		// 第三道：calculate 只解释数学 AST，限制长度与可用函数。
		value, err := calculate(expression)
		if err != nil {
			logger.Printf("calculator error: expression=%q error=%v", expression, err)
			failure = err
			return sdk.NewToolResultError(err.Error()), nil
		}
		logger.Printf("calculator ok: expression=%q result=%v", expression, value)
		return sdk.NewToolResultStructured(map[string]any{"result": value}, strconv.FormatFloat(value, 'g', -1, 64)), nil
	})
	return s
}

// -mcp-serve：stdio 模式下 stdout 被协议独占，这条路径上不能有任何 fmt.Print，日志只写 stderr。
// httpAddr 非空时改用 Streamable HTTP，在独立端口提供 /mcp。
func Serve(httpAddr string) error {
	logger := log.New(os.Stderr, "[mcp-serve] ", log.Ltime)
	s := newServer(logger)
	if httpAddr != "" {
		// SDK 默认拒绝“本机连接但 Host 不是本机”的请求（防 DNS 重绑定）。
		logger.Printf("serving calculator over streamable HTTP at http://%s/mcp", httpAddr)
		return server.NewStreamableHTTPServer(s).Start(httpAddr)
	}
	logger.Printf("serving calculator over stdio")
	return server.ServeStdio(s, server.WithErrorLogger(logger))
}

// Handler 是同一个 server 的 Streamable HTTP 入口，网页端挂在自己的 /mcp 上；日志打印到网页端终端。
func Handler() http.Handler {
	return server.NewStreamableHTTPServer(newServer(log.New(os.Stdout, "[mcp] ", log.Ltime)))
}
