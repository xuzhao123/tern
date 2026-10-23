# tern 项目约定

tern 是一个用 Go 实现的 agent harness，由 learning-agent 演变而来（保留了提交历史）。公共的启动、配置、参数与入口见 [README.md](README.md)。

## 项目结构

- 一个 Go 模块 `github.com/xuzhao123/tern`、一个入口 `main.go`：`go run .` 是 agent，`web` 是网页端（含内置 MCP），`chat` 是终端客户端，`queue` 是任务队列，`metrics` 统计指标，`eval` 跑评测与归因，`audit` 查看并校验审计日志，`rewind` 按轮撤销文件改动。
- 代码按功能放在 `internal/` 对应的包里，新功能沿用这个划分，不另建 go.mod。
- 设计文档放 `spec/`，用 Markdown 写；只有用户要求时才另配 HTML 页面，两者内容保持一致，有出入以 Markdown 为准。
- 仓库里只放真实使用的 `skills/`、`hooks/`、`commands/`；评测题、编码评测的小仓库、队列示例任务属于任务输入，提交。`retrieval/corpus.md` 是知识库检索的资料，`[D01]` 这类是文档编号，不是注释。

## 设计与实现的分工

- 新功能先写设计：Claude Code 在 `spec/` 下写一份 Markdown，用户确认后再实现。
- 实现由 tern 照着 spec 完成。spec 没写到的、或者实现时发现要偏离的，先把改动写回 spec 并说明原因，再改代码；不要在代码里悄悄换做法。
- spec 要写到能直接照着做：按文件列出改动、写明这一期不做什么、每一期给出验收方法（命令和预期结果）。
- 实现在分支或 git worktree 上做，提交前由人 review。

## 写代码

- 只做当前任务要求的改动；保持短小直接，命名、注释密度和写法与周围代码一致。
- 注释讲清为什么、怎么运作；不写课程编号（Day 12、D25 一类），不链接仓库里不存在的文档。
- 不为未来需求、测试注入增加接口、通用配置、复杂分层和大量防御性代码。
- 改实现时同步改 README 里的启动、配置、参数和入口说明。
- 网页端页面在 `internal/web/page/`：浏览器原生 ES 模块，不打包，不引入构建工具与前端框架，按功能分文件；模块顶层只定义与挂事件，启动时的调用放 `main.js`。主页面的第三方脚本固定版本并带 SRI；模型写的可视化只在沙箱 iframe（`allow-scripts`，不给 `allow-same-origin`）里运行。

## 验证

- 先格式化（gofmt）、`go vet`、`go build`，改了页面脚本再 `node --check`；然后用真实模型、实际工具执行验证行为。
- 不写脚本模型、mock，不写死模型的工具决策、执行顺序与答案。
- 目前不写 `*_test.go`（沿用 learning-agent 的做法）；要引入测试，先和用户确认。
- 会改仓库内容的验证在临时 clone 或 worktree 里做，用完删除。
- 网页端的验证一律用常驻验证账号 `qa` 登录（账号名与密码在 `.env` 的 `OBSERVER_TEST_USER`、`OBSERVER_TEST_PASSWORD`，不写进提交的文件），记录留给用户查看。只有验证租户隔离时才临时建租户账号，用完连同租户数据删除。
- 验证不能在长期记忆里留下不是用户原话的事实；测试写进去的，验证完用 `-memory-forget` 删掉。

## 数据与密钥

- 运行数据都在 `.data/`（不提交），路径由 `internal/tenant` 决定：平台空间是 `.data/`，租户是 `.data/tenants/<租户>/`。
- 模型与服务的密钥只放 `.env` 或环境变量。沙箱里清空环境变量，工作区的 `.env` 换成空文件。
- 附件按内容寻址存进 `.data/[tenants/<租户>/]attachments/`（目录 0700、文件 0600）；消息、检查点、存档与结果日志里只放 `attachment://` 引用，发请求时才展开。

## 各模块的约束

改动时守住这些不变量。

**协议与界面**
- agent 以 `-app-server` 运行时 stdout 只走 JSON-RPC（一行一条），日志走 stderr。网页端、`chat` 和 `agent.RunChild` 是界面；界面需要的信息只读协议通知，不解析终端文本。
- 网页端启动的 agent 经 `AGENT_APP_SOCKET` 走 `.data/[tenants/<租户>/]live/` 下的 Unix socket：网页端退出时 agent 照常运行，重启后接管；agent 已经不在时，从会话记录 `.data/[tenants/<租户>/]sessions/` 补读，已写的记录不改写。
- 审批与提问（`network/requestApproval`、`tool/requestApproval`、`user/requestInput`）只能由界面上的人回答；没有界面时如实回答 unavailable。人的审批先写审计。

**模型**
- 请求一律 `stream: true` 并要 usage；拼接逻辑只有 `llm.Stream` 一份，agent 与网页端代理共用；截断等检查在流结束后对完整回复做。
- 协议转换只在 `llm.ConvertBody` 一处；内部消息、检查点与存档只用 chat 形式。
- 只对暂时故障（429 非额度、5xx、连接失败、断流）退避重试，最多 3 次、不占请求预算、遵守 Retry-After。
- 路由表在 `llm.Providers`，`-provider`、`-model` 手动选；网页端按 agent 在 `X-Agent-Upstream` 里声明的地址转发，不复制路由表。`Model.Window`、`Model.Vision` 只填核对或实测过的值，拿不准不填。
- Coconut 的地址只从 `COCONUT_API_URL` 读；明文 http 只允许不带密钥的请求。新增模型协议须有对应供应商的密钥，没有就不接，也不模拟。

**工具、权限与审计**
- 工具的性质（只读、串行、可重做、私有来源、外发、不可撤回、权限类别、时限）只在 `internal/agent/toolspec.go` 声明。工具列表也是允许列表，执行前最先检查；工具从第一次请求就固定，中途不改。工具结果标记来源，不当作新的授权。
- MCP server 的标注只用于并行与重做；外发与权限只收紧，不放宽。
- 权限按用户规则（deny › ask › allow）与权限模式（plan、ask、edit、full，默认 edit）决定；询问与高风险确认走同一条 `tool/requestApproval`。full 模式不再询问，但照常判断、写审计后由程序放行；用户的 ask 规则与 hook 要求的确认仍然询问。
- 审计日志 `.data/audit/audit.jsonl` 只追加、哈希链、文件锁、写后 fsync；参数只存哈希；只读工具不记。高风险确认与联网审批写不进审计就不执行。

**沙箱**
- bubblewrap + seccomp（纯 Go 生成 BPF）+ systemd-run cgroup，不用 Docker。每次 bash 调用一个新沙箱，后台命令也是：后台起的服务，之后的命令连不上（同 Claude Code 在 Linux 上的沙箱）。后台命令只活到这一轮结束。
- 工作区挂到 `/workspace`（plan 模式只读；`.git` 只读、`.env` 换空、`.data` 换空目录），本对话目录挂到 `/scratch`。
- 默认断网，只经 agent 进程里的白名单代理出去；白名单只能由用户添加（`-net-allow`、网页端设置 `net_allow` 或当场审批），full 模式由程序放行、写审计，端口、IP 与内网地址照常拒绝。浏览器与沙箱代理的出口判断共用 `internal/netguard`。

**检查点、上下文与文件改动**
- 每轮用新的检查点ID，以 `continues` 串成续接链，旧检查点不覆盖。影响下一步、要带进下一轮和续跑的状态只放检查点的 `state` 一处；格式改动要加 `CheckpointVersion` 并在读取时迁移旧格式。
- 有工作区时根目录 AGENTS.md（没有时 CLAUDE.md）进系统提示词；环境信息与运行中的变化以 `<system-reminder>` 用户消息追加在末尾，不改写已发出的内容；提醒不是用户原话。
- 过长的工具输出存进 `.data/outputs/<对话第一轮ID>/`，模型用 `read_output` 读回。
- edit_file、write_file 写入前存快照，存不下就不写。撤销是人的动作（命令行或网页端按钮），先写审计；最后一次写入后又被改过的，整次不做。

**子 agent 与队列**
- 任务队列、子 agent、评测试次都经 `agent.RunChild` 为每个任务启动一个子进程，一个进程只跑一个任务。
- 后台子 agent、等待、插话、停止、分叉都只经父子之间的协议实现；后台子 agent 只活到这一轮结束。

**扩展**
- 长期记忆的事实只来自用户原话和工具最终失败；模型可以登记原话，由程序校验与提交，不让模型生成新的记忆事实。存为本地 JSON，不引入数据库。
- hooks 沿用 Claude Code 的格式，只来自 `-hooks`（用户）或网页端平台设置，不读工作区里的配置；命令在宿主机运行，工作目录是工作区（没有工作区时是本对话的沙箱目录），只给最少的环境变量。斜杠命令只是提示词模板，不执行命令。
- Skills 只做知识型：手写 frontmatter 解析，不引入 YAML 库，不执行 skill 自带的脚本。
- MCP 用 mcp-go（client 与 server），支持 stdio 与 Streamable HTTP。
- 浏览器用 chromedp，只做读取型工具，每次调用一个标签页；不伪装 User-Agent、不绕过验证码。`CHROME_PATH`、`CHROME_NO_SANDBOX` 只从环境变量读。
- 调用链用 OpenTelemetry Go SDK，属性按 GenAI 语义约定，span 写 `.data/traces/`；提示词与工具内容默认不采集，外部平台只通过 `OTEL_*` 环境变量接入。

**网页端**
- 必须登录：账号在 `.data/users.json`（PBKDF2），平台账号与租户账号两种；按编号访问的接口都核对租户，不属于时返回 404。模型代理凭每次启动的进程令牌。
- 设置在 `.data/settings.json`，按 内置默认 › 平台 › 个人 合并，租户策略与本机条件只会关掉工具；对话开始时把设置拍成快照写进 start 事件，续聊照快照启动。工作区只能由平台账号登记本机 git 仓库，租户须明确开通 `workspace`。
- 导入对话只接受 Codex 的 rollout 与 Claude Code 的会话 JSONL，只读、不续聊。

**评测**
- 题目在 `eval/cases.jsonl` 与 `eval/coding-cases.jsonl`：固定题目、参考答案与打分器，不写死模型回答或工具顺序；运行记录、人工归因与回放放 `.data/eval/`。
- 先用确定性检查，开放题由与被测模型不同的供应商评审；结局与用量以检查点为准。编码题每个试次一份新的仓库拷贝，校验命令只在工作区只读挂载的沙箱里运行。
- 回放验证从检查点分叉（`agent.ForkCheckpoint`、`agent.ForkSnapshot`），不改原检查点。
