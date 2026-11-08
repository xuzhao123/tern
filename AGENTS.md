# tern 项目约定

tern 是用 Go 实现的 agent harness，由 learning-agent 演变而来。启动、配置、参数与入口见 [README.md](README.md)。

## 结构

- 一个模块 `github.com/xuzhao123/tern`、一个入口 `main.go`：`go run .` 是 agent，子命令 `app-server`（常驻，管对话，含模型代理）、`web`（网页端，含内置 MCP）、`chat`、`queue`、`metrics`、`eval`、`audit`、`rewind`。代码按功能放在 `internal/` 各包，不另建 go.mod。
- 对话归 app-server：网页端和 chat 都是它的界面，界面这一侧在 `internal/appclient`，自己不启动 agent，也不读写 `.data/` 里的对话与设置。agent 的模型请求经 app-server 的模型代理走 `.data/app-server/llm.sock`（`AGENT_PROXY_SOCKET`），不再经过网页端。
- 远程：界面带 `-remote <ssh 别名>` 时用系统 ssh 连远端的 app-server（`ssh -T -o BatchMode=yes <别名> 'exec "$SHELL" -lc "tern app-server proxy"'`），拿 ssh 的标准输入输出当协议通道；不开端口，鉴权就是 SSH。断线重连也在 `internal/appclient`：本机、远程都按 1、2、4…秒退避、最多 30 秒，本机每次先 `app-server start`、远程重新起 ssh；重连期间请求立刻失败（网页端回 503“正在重连 <host>”，顶栏显示重连中），正在推送的订阅不结束，连上后按最后收到的 seq 重新 `thread/resume` 补齐。
- 网页端可以同时连几台主机，一台一条连接：远端主机的页面与接口带 `/h/<别名>/` 前缀，页面上连过的主机记在 `.data/web-hosts.json`，下次启动在后台自动连，主机菜单里可以断开。
- agent 的协议 socket 参数叫 `-agent-server`；app-server 启动 agent 时不传 `-tenant`，这一版只有平台空间。
- **各包目录下的 AGENTS.md 写着那个包的约束。**tern 读写那个目录的文件时会自动加载；设计或修改某个包之前，先读它的那份。
- 设计在 `spec/`，索引见 `spec/README.md`。内置的命令与 skill 在 `internal/commands/builtin/`、`internal/skills/builtin/`，`hooks/` 里放 hook 脚本，都只放真实使用的；`eval/`、`queue/` 里的题目和示例任务是任务输入，要提交。

## 写代码

- 只做任务要求的改动；短小直接，命名、注释密度和写法与周围代码一致。不为未来需求增加接口、配置、分层和防御性代码。
- 注释讲为什么、怎么运作；不写课程编号（Day 12、D25 一类），不链接仓库里不存在的文档。
- 改了启动、配置、参数或入口，同步改 README。

## 验证

- gofmt、`go vet`、`go build`，改了页面脚本再 `node --check`。
- 会改仓库内容的验证在临时 clone 里做，用完删除；运行 clone 编出的程序时把 `TERN_HOME` 设成 clone 或临时目录，不碰 `~/.tern`。网页端的验证用常驻账号 `qa`（账号名与密码在 `.env` 的 `TERN_QA_USER`、`TERN_QA_PASSWORD`；还没改名的 `.env` 里是旧名字 `OBSERVER_TEST_USER`、`OBSERVER_TEST_PASSWORD`，验收脚本两个都认；不写进提交的文件）。
- 验证不在长期记忆里留下不是用户原话的事实；测试写进去的，用 `-memory-forget` 删掉。

## 数据与密钥

- 数据目录是 `TERN_HOME`：`main` 启动时先算出它（没设时是 `~/.tern`）并 chdir 过去，之后所有相对路径都按它解释。运行数据都在它下面的 `.data/`（不提交），路径由 `internal/tenant` 决定；租户的数据在 `.data/tenants/<租户>/`。
- `$TERN_HOME/AGENTS.md` 是用户的个人指令（类似 Codex 的 `~/.codex/AGENTS.md`）：每个对话都放进系统提示词，排在项目指令前面，没选工作区也带；评测不读（`-personal-instructions=false`）。
- 密钥只放 `.env`（权限 0600）或环境变量。
