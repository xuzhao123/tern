# tern

一个用 Go 实现的 agent harness：命令行 agent、网页端、终端客户端、任务队列、评测与归因，都在同一个程序里。由 learning-agent 演变而来，保留了它的全部功能与提交历史，去掉了只为学习而设的实验入口和课程文档。

名字取自北极燕鸥（Arctic tern）：每年迁徙几万公里，对应长任务、接力与续跑。

`用户问题 → 组织上下文 → 请求模型 → tool_calls → 执行工具 → 结果回填 → 下一轮或结束`

## 快速开始

需要 Go 1.25.5 或以上版本。所有命令从项目根目录执行，文档中的文件路径相对于项目根目录。

```sh
cp .env.example .env
# 在 .env 中填写自己的 ARK_API_KEY
go run . -question '计算 1231+23123'
```

不带 `-question` 时从终端交互输入。启动网页端前先添加登录账号（密码从标准输入读）：

```sh
go run . web -add-user admin                  # 平台账号：看到 .data/ 下原有的数据
go run . web -add-user alice -tenant acme     # 租户账号：只看到 .data/tenants/acme/ 下的数据
go run . web
# 打开 http://127.0.0.1:8090 登录
```

网页端支持新对话、续聊、停止，以及每个模型 Step 的完整输入输出。开发时用 `go run . web -dev`，每次对话从当前源码启动 agent；默认使用当前程序，修改 agent 后需要重启网页端。

需要可执行文件时：

```sh
go build -o bin/tern .
./bin/tern -question '计算 1231+23123'
```

## 模型与工具配置

配置读取根目录 `.env`，同名环境变量优先。密钥只写入本地私有配置，保持文件权限为 0600；`.env` 已忽略提交。

| 模式 | 配置项 | 默认值 |
| --- | --- | --- |
| 方舟聊天模型 | `ARK_API_KEY`（兼容 `LLM_API_KEY`）、`LLM_API_URL`、`LLM_MODEL` | `-provider ark`；`https://ark.cn-beijing.volces.com/api/coding/v3/chat/completions`；`doubao-seed-2.1-pro` |
| DeepSeek 聊天模型 | `DEEPSEEK_API_KEY`、`DEEPSEEK_API_URL`、`DEEPSEEK_MODEL` | `-provider deepseek`；`https://api.deepseek.com/chat/completions`；`deepseek-flash` |
| DeepSeek（Anthropic 接口） | 与 DeepSeek 共用 `DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`；`DEEPSEEK_ANTHROPIC_URL` 可改地址 | `-provider deepseekanthropic`；`https://api.deepseek.com/anthropic/v1/messages`；`deepseek-flash`；Anthropic Messages 协议 |
| Coconut 聊天模型 | `COCONUT_API_URL`（必填，指向本机 Codex 兼容服务的 `.../v1/responses`）、`COCONUT_MODEL`；不要密钥 | `-provider coconut`；没有默认地址；`coconut-gpt-6-1-sol-max`；走 Responses 协议 |
| 线上向量模型 | 与方舟共用 `ARK_API_KEY` | `-embedding ark` |
| 本地向量模型 | 不需要 embedding API 密钥；首次使用会下载模型 | `-embedding local`；聊天请求仍使用选定的线上模型 |
| 浏览器 | 本机安装 Chrome/Chromium；`CHROME_PATH` 可指定可执行文件 | `-browser`；仅环境变量读取浏览器配置 |
| DeepSeek 搜索 | 与 DeepSeek 聊天模型共用 `DEEPSEEK_API_KEY`；Search/Fetch 按成功请求计费 | `-ds-search`；与选用哪家聊天模型无关 |
| Bash 沙箱 | Linux、bubblewrap；cgroup 资源限制依赖 systemd 用户会话 | `-bash` |
| 网页端登录 | 账号在 `.data/users.json`，用 `go run . web -add-user` 添加 | 没有账号时网页端拒绝启动 |
| 调用链追踪 | 无需配置，span 写入 `.data/traces/`；`OTEL_EXPORTER_OTLP_ENDPOINT` 另发给 Jaeger 等 OTLP 平台；`OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true` 采集提示词与工具内容 | 默认只记元数据 |

豆包的地址有两套，不能混用：默认的 `.../api/coding/v3` 是方舟 Coding Plan 订阅入口，模型名用套餐里的 Model Name（如 `doubao-seed-2.1-pro`、`doubao-seed-2.1-lite`、`kimi-k3`、`glm-5.3`）或 `ark-code-latest`（跟随控制台“开通管理”的模型选择，切换后 3–5 分钟生效）；`.../api/v3` 是按量付费入口，模型名带日期版本号（如 `doubao-seed-2-1-pro-260628`）。方舟文档说明 `.../api/v3` 不消耗套餐额度、照按量计费，所以地址不能混用；模型名按文档用 Model Name——套餐入口实测也接受带日期版本号的旧名字，但文档没有列出，是否计入套餐以控制台用量为准。`ark-code-latest` 的响应里 `model` 是 `auto`（实测），看不出实际是哪个模型回答的，评测与归因要固定模型时选具体名字。`.env` 里的 `LLM_API_URL`、`LLM_MODEL` 会覆盖默认值，以前填过老地址老模型的要一起改掉或删掉这两行；启动输出里的 `Model: provider=ark model=... upstream=...` 可以核对实际用的地址与模型。套餐额度按 5 小时、周、月分档，用完返回 429（`hour / week / month allocated quota exceeded`），不是地址配错。

方舟套餐里的模型可以换：命令行用 `-model kimi-k3`（`.env` 或环境变量里的 `LLM_MODEL` 改的是不加 `-model` 时的默认值）；网页端分两级选：一级是供应商与接口（豆包、DeepSeek、DeepSeek 的 Anthropic 接口、Coconut），二级才是具体模型，只有一个模型的供应商不显示第二级。设置页的“默认值”里同样是这两级（`provider`、`model`，`model` 空表示用供应商默认值），新对话在输入框不选就用它；页面另选以页面为准。清单在路由表 `llm.Provider.Models`，经 `GET /models` 给页面，14 个名字 2026-10-20 逐个真实调用过（含一次工具调用）。网页端以 `-model` 传给 agent，它和其他启动参数一起写进检查点，所以一条对话固定一个模型：续聊、中断续跑、归因回放和子 agent 都用同一个。上下文窗口默认按模型（取路由表里这个模型核对过的输入上限），在设置里填了值就以设置里的为准，所以换窗口更小的模型时要检查这一项。

默认使用原生 Tool Calling：工具定义在请求的 `tools` 字段，模型返回 `tool_calls`，回答不限制文本格式。普通任务只启用 calculator、get_current_datetime；检索、记忆、MCP、Skills、子 agent、浏览器、DeepSeek 搜索和 Bash 按需开启。`-disallowed-tools` 去掉其中单个工具（逗号分隔，也可以去掉浏览器的 web_search 或 open_page；名字与 Claude Code 的 `--disallowed-tools` 一致，但只认工具名，按调用拦截用 `-deny` 规则）；网页端在设置里逐个开关，默认关 web_search，搜索用 ds_search，打开网页用 open_page。

推理强度默认 `high`。API 输出上限默认省略，显式设置 `-max-output-tokens` 才发送；本地上下文规划仍保留输出空间，省略 API 参数不代表无限输出。向量检索直接在 agent 进程内执行。

## 代码结构

一个 Go 模块、一个入口 `main.go`，代码按功能放在 `internal/` 下。

| 部分 | 文件或模块 | 关注点 |
| --- | --- | --- |
| 入口 | [main.go](main.go) | 参数、工具开关、agent / web / queue / metrics / eval / audit 的入口 |
| 模型协议 | [internal/llm](internal/llm/) | messages、tools、tool_calls、请求预算与用量 |
| 执行循环 | [internal/agent/react.go](internal/agent/react.go)、[工具分发](internal/agent/dispatch.go)、[确认与循环检测](internal/agent/approval.go) | 结果回填、并发、分类重试与终止；高风险确认、私有数据标记、结果日志与无进展检测 |
| 上下文与恢复 | [上下文管理](internal/agent/context_manager.go)、[检查点](internal/agent/checkpoint.go) | Transcript/View、压缩、写前日志、续跑 |
| 知识与记忆 | [retrieval](internal/retrieval/)、[memory](internal/memory/) | 向量检索、规则写入、两路召回与重排 |
| 工具扩展 | [tools](internal/tools/)、[mcp](internal/mcp/)、[skills](internal/skills/) | 本地工具、协议服务、按需加载知识 |
| 多任务执行 | [queue](internal/queue/)、[子进程](internal/agent/child.go)、[子 agent](internal/agent/subagent.go) | worker、任务 ID、独立上下文与权限继承 |
| 编码工具 | [workspace](internal/workspace/)、[permission](internal/permission/) | 读、找、搜、改；`os.Root` 路径边界、读后才写的版本核对、输出预算；项目指令、环境信息与系统提醒；权限规则与模式 |
| 扩展点 | [hooks](internal/hooks/)、[commands](internal/commands/) | 生命周期 hooks（配置与脚本在 [hooks/](hooks/)）；斜杠命令模板（[commands/](commands/) 与工作区的 `.agents/commands/`） |
| 外部执行 | [browser](internal/browser/)、[sandbox](internal/sandbox/)、[netguard](internal/netguard/) | 页面读取、进程隔离、网络与资源边界、出口地址判断 |
| 网页端 | [web](internal/web/)（页面在 [page](internal/web/page/)，原生 ES 模块）、[protocol](internal/protocol/)、[telemetry](internal/telemetry/)、[metrics](internal/metrics/) | 模型请求代理、agent 与界面的结构化协议、OpenTelemetry span 与导出、核心指标 |
| 评测与归因 | [eval](internal/eval/)、[评测集](eval/cases.jsonl)、[网页端评测页](internal/web/eval.go) | 题目与打分器、多次尝试与 pass^k、评审模型、消融与基线对比、归因卡片与回放验证 |
| 审计 | [audit](internal/audit/) | 只追加的审计记录、哈希链与校验；agent 与网页端共同写入 |
| 多租户与登录 | [tenant](internal/tenant/)、[网页端登录](internal/web/auth.go) | 按租户决定数据路径；账号、密码哈希、会话、租户策略与访问检查 |

一个 agent 进程运行一个任务，功能开关由入口设置。网页端、任务队列和子 agent 用独立子进程执行任务；检索等工具直接在该 agent 进程内调用。

## 参数速查

| 常用参数 | 默认值 | 作用 |
| --- | --- | --- |
| `-question` | 交互输入 | 本轮任务 |
| `-image` | 空 | 随问题附带的图片（PNG、JPEG、GIF、WebP），可写多次，每条消息最多 8 张；网页端输入框可附图 |
| `-provider` | ark | 选择方舟或 DeepSeek |
| `-model` | 空 | 这家下面的具体模型（如 `kimi-k3`）；空时用 `LLM_MODEL` / `DEEPSEEK_MODEL` 或路由表默认值；网页端是输入框旁的第二个下拉 |
| `-max-steps` | 0 | 模型请求预算，含摘要与辅助调用；0 表示不限（靠重复动作/无进展熔断与 Ctrl-C 收住）；队列与评测任务没给预算时补 10 |
| `-parallel` | 4 | 独立工具并发上限 |
| `-rag` / `-memory` | false | 按需启用知识检索或长期记忆 |
| `-resume` | 空 | 按检查点 ID 续跑，配置沿用检查点 |

<details>
<summary>展开全部 agent 参数</summary>

| 参数 | 默认值 | 用途 |
| --- | --- | --- |
| `-question` | 交互输入 | 交给模型的任务 |
| `-image` | 空 | 图片输入：随问题附带的图片路径，可写多次。按文件头只收 PNG、JPEG、GIF、WebP；长边超过 2048 或 32×32 块超过 2500 时等比缩小；按内容寻址存进 `.data/[tenants/<租户>/]attachments/`，消息与检查点里只放 `attachment://` 引用；处理不了的图以提醒说明，不让这一轮失败；每条消息最多 8 张；所选模型不能看图时发请求前换成说明文字。能看图的模型在有工作区或 bash 时另有 `view_image`，看工作区或沙箱里的图片 |
| `-history-stdin` | false | 从stdin接收上次上下文JSON，配合-question续聊（命令行用；网页端改经 `turn/start` 传入） |
| `-continues` | 空 | 续聊时上一轮的检查点ID：本轮用新ID并记下续接关系，子 agent 可按 task_id 引用之前各轮的子任务；网页端自动处理 |
| `-app-server` | false | 以结构化协议与界面通信（JSON-RPC，一行一条）：stdout 只走协议，日志改走 stderr；网页端与父 agent 启动子进程时使用。网页端另给环境变量 `AGENT_APP_SOCKET`，协议与日志改走这个 Unix socket，网页端重启后能接管（） |
| `-parallel` | 4 | 工具执行并发，1到16 |
| `-max-steps` | 0 | 模型请求总预算，包含最终回答与上下文摘要；0 表示不限 |
| `-retries` | 2 | 工具额外重试次数，0到5 |
| `-context-window` | 0 | 总窗口；0 表示按模型取路由表里核对过的输入上限（取不到时 262144），显式给值时才用给的值 |
| `-max-output-tokens` | 0 | 0省略API输出上限；大于0才传max_completion_tokens（含推理） |
| `-reasoning-reserve` | 1024 | 输入容量额外安全余量 |
| `-tool-output-tokens` | 2000 | 工具结果进入View时的粗估上限；超过时完整内容存进 `.data/outputs/`，视图里放预览，模型用 `read_output` 读回 |
| `-keep-groups` | 2 | 最近组超20%配额或挤占摘要空间时逐步减少，可到0 |
| `-reasoning-effort` | high | 所有请求统一的推理强度：minimal、low、medium、high |
| `-rag` | false | 增加 search_docs，项目知识回答先检索并引用来源 |
| `-embedding` | ark | 向量模型：ark线上方舟，local本地纯Go MiniLM；search_docs 与长期记忆共用 |
| `-min-score` | 0.40 | 检索相似度阈值，范围−1到1 |
| `-search-docs` | 空 | 只执行检索，不调用聊天模型；local无需密钥，ark使用同一ARK_API_KEY |
| `-k` | 3 | 配合 search-docs，最多返回1–6条候选 |
| `-memory` | false | 跨进程长期记忆：开头经背景补充、两路召回、RRF、重排后调入，结尾写入原文与来源，增加 remember_memory、search_memory 与 forget_memory |
| `-memory-ttl` | 0 | 本轮“记住”写入的有效期，如 `1m`；0 不过期 |
| `-memory-limit` | 200 | 记忆总量上限，超出时淘汰重要性×新近度最低的一条 |
| `-memory-forget` | 空 | 只删除指定编号的记忆后退出，如 `M3`；不做容量淘汰，不需要密钥 |
| `-memory-search` | 空 | 只执行一次记忆检索，stdout 输出各阶段 JSON；配合 `-memory-mode` 对比 |
| `-memory-mode` | rerank | memory-search 的方式：keyword（旧关键词）、vector、bm25、hybrid（RRF）、rerank（完整流程） |
| `-memory-context-calls` | 4 | 本次运行最多几次背景生成；0 表示全部按原文检索 |
| `-memory-tokens` | 800 | 注入或返回的记忆估算 token 预算，另有最多5条的上限 |
| `-mcp-server` | 空 | MCP server：`http(s)://…` 地址连远程 server（Streamable HTTP），否则当作子进程命令（stdio，按空格切分，不经过shell）；可重复给多个；配合 `-question` 时把它们的工具注册为 `mcp_*`，连不上的 server 打印 `MCP error` 后跳过；`-mcp-list`/`-mcp-raw` 只接受一个 |
| `-mcp-list` | false | 只用 SDK 连接 `-mcp-server`，打印协议版本、capabilities 与工具列表，不请求模型 |
| `-mcp-raw` | 空 | 不用 SDK，手写 JSON-RPC：`legacy`（initialize 握手）或 `modern`（server/discover + 每请求 `_meta`），逐行打印收发 |
| `-mcp-call` / `-mcp-args` | 空 / `{}` | 配合 `-mcp-list` 或 `-mcp-raw` 再调用一次指定工具 |
| `-mcp-serve` | false | 作为 MCP server（stdio）运行，暴露 calculator；须单独使用 |
| `-mcp-http` | 空 | 配合 `-mcp-serve`，改用 Streamable HTTP 在该地址提供 `/mcp`（远程 MCP），如 `127.0.0.1:8091` |
| `-skills` | false | 扫描 `skills/*/SKILL.md`，system 中放索引，增加 load_skill |
| `-skill` | 空 | 配合 `-skills`，只启用这个名字的 skill（可重复）；不给则启用全部 |
| `-skills-list` | false | 只扫描 `skills/`，stdout 输出每个 skill 的元数据、正文与校验错误（JSON），不请求模型；须单独使用 |
| `-timeout` | 0 | 整次运行的时限，如 `2m`；0 不限，仍可 Ctrl+C |
| `-tool-timeout` | 30s | 其他工具单次尝试的时限；到时记为结果未知，只读工具才重试（bash 用 `-bash-timeout`） |
| `-bash-timeout` | 2m | bash 单条命令的时限，大于 0、不超过 `10m`；到时沙箱终止整条命令，返回终止前的输出；写在 bash 的工具描述里 |
| `-task-id` | 随机 UUID | 检查点ID（字母、数字和 `._-`）；已存在时拒绝启动，提示改用 `-resume` |
| `-resume` | 空 | 从该ID的检查点续跑，配置取自检查点；须单独使用；已完成的直接返回存档答案 |
| `-subagents` | false | 增加 spawn_agent：把独立子任务交给全新上下文的子 agent（子进程），每次运行最多4个 |
| `-tenant` | 空 | 多租户：本任务所属的租户，数据都在 `.data/tenants/<租户>/` 下；网页端和带 `tenant` 的队列任务自动加上；可与 `-resume` 一起用 |
| `-private-input` | false | 任务输入已含私有数据，外发类工具执行前要确认；父 agent 派出子任务时自动加上，一般不手动使用 |
| `-subagent-steps` | 6 | 每个子 agent 的模型请求预算，1到20 |
| `-provider` | ark | 模型路由：`ark`（豆包，方舟 Coding Plan）、`deepseek`、`deepseekanthropic`（DeepSeek 的 Anthropic 接口）或 `coconut`（本机 Responses 服务）；子 agent 继承，续跑沿用 |
| `-model` | 空 | 这家供应商下的具体模型，优先于 `LLM_MODEL` / `DEEPSEEK_MODEL`；写进检查点的启动参数，子 agent 继承，续跑与回放沿用 |
| `-bash` | false | 增加 bash 与 request_network_access，命令在 bubblewrap 沙箱里执行（仅 Linux）；工作目录按对话保留，续聊各轮共用，网页端可上传、下载 |
| `-workspace` | 空 | 增加 read_file、glob、grep、edit_file、write_file，只在这个仓库目录里读写；子 agent 继承。配合 `-bash` 时 bash 的 `/workspace` 就是这个仓库（`.git` 只读，`.env`、`.data` 看不到），本对话目录改挂 `/scratch` |
| `-permission-mode` | edit | plan（只读计划）、ask（改动都问）、edit（工作区内编辑与沙箱 bash 直接执行）、full（不问，含高风险确认，由程序放行并写审计；deny、ask 规则照常） |
| `-allow` / `-ask` / `-deny` | 空 | 权限规则，可重复，如 `-allow "Bash(go build *)"`、`-deny "Bash(git push *)"`、`-ask "Edit(internal/agent/**)"`；顺序 deny › ask › allow；子 agent 继承 |
| `-hooks` | 空 | 生命周期 hooks 配置（Claude Code 的 hooks 格式），JSON 原文或文件路径，如 `-hooks hooks/hooks.json`；命令在宿主机上运行，只由用户提供；子 agent 继承。问题以 `/名字` 开头时按 `commands/`、工作区 `.agents/commands/` 的模板展开 |
| `-bash-project-ro` | false | 配合 `-bash`：项目只读挂到 `/project`（`.env`、`.data`、`.git`、`bin` 除外） |
| `-net-allow` | 空 | 配合 `-bash`：沙箱可经代理访问的域名（含子域名，可重复），由用户批准后添加；网页端由设置里的“沙箱联网白名单”（`net_allow`）和审批生成。不在白名单的域名，有界面时代理当场请人批准 |
| `-browser` | false | 增加 web_search 与 open_page，用无头 Chrome 查资料；环境变量 `CHROME_PATH` 指定浏览器，系统不支持 Chrome 沙箱时设 `CHROME_NO_SANDBOX=1` |
| `-ds-search` | false | 增加 ds_search 与 ds_fetch，调用 DeepSeek 的 Search/Fetch API（服务端执行，没有画面）；子 agent 继承。 |
| `-tool-format` / `-coding-prompt` / `-todo` / `-clear-old-results` | json / default / false / false | 消融开关，默认值就是现在的做法：工具结果发给模型时用纯文本、编码提示词的 minimal 或 extended 写法、上下文内的 todo 清单、续聊时清理之前各轮的工具结果 |
| `-check` | 空 | 不请求模型，只在工作区只读挂载的沙箱里跑这条命令，最后一行输出 JSON 的退出码与输出（编码评测的校验命令用它），须配合 `-workspace` 与 `-bash` |
| `-ask-user` | false | 增加 ask_user，模型可以在运行中向人提问（经协议的 `user/requestInput`）；只在界面上有人能当场回答时打开，终端客户端与网页端自动加上，子 agent 不继承 |

</details>

## 其他入口

| 命令 | 作用 |
| --- | --- |
| `go run . chat [-continue 检查点ID] [agent 参数…]` | 终端客户端：每一轮启动一个 `-app-server` 的 agent，过程流式显示，运行中输入的话作为插话；`/help` 列出恢复、分叉、压缩、撤销（`/rewind <轮> [both\|files\|chat]`）、切权限与批准计划等命令；agent 的终端日志写在 `.data/[tenants/<租户>/]chat.log` |
| `go run . web [-dev]` | 网页端：网页界面，需要先用 `-add-user` 添加账号 |
| `go run . queue` | 任务队列：按 `queue/tasks.jsonl` 逐个执行，每个任务一个 agent 子进程，同一个 id 只执行一次 |
| `go run . metrics [-since 168h] [-prefix ID前缀] [-tenant 租户] [-json]` | 统计完成率、耗时、成本等核心指标，只读 `.data/` |
| `go run . eval` / `go run . eval -cases eval/coding-cases.jsonl` | 评测：通用评测集（每题 k 次、DeepSeek 评审，需要 `DEEPSEEK_API_KEY`）或编码任务评测（每个试次一份新的仓库拷贝，校验命令在沙箱里只读运行）；`eval triage`、`eval replay` 做失败归因与回放；网页端左侧“评测与归因”是同一功能的页面 |
| `go run . audit [-tenant 租户] [-task ID] [-verify]` | 列出并校验审计日志（只追加、哈希链）；网页端左侧“审计日志”是只读页面 |
| `go run . rewind [-tenant 租户] [-dry-run] [-mode both\|files\|chat] <轮次ID>` | 撤销这一轮及之后各轮用编码工具改过的文件，对话退回上一轮；`-mode files` 只退文件、`chat` 只退对话；网页端每条消息下的“撤销到这一轮之前”是同一功能 |
