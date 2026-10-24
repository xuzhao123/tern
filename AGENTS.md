# tern 项目约定

tern 是用 Go 实现的 agent harness，由 learning-agent 演变而来。启动、配置、参数与入口见 [README.md](README.md)。

## 结构

- 一个模块 `github.com/xuzhao123/tern`、一个入口 `main.go`：`go run .` 是 agent，子命令 `app-server`（常驻，管对话，含模型代理）、`web`（网页端，含内置 MCP）、`chat`、`queue`、`metrics`、`eval`、`audit`、`rewind`。代码按功能放在 `internal/` 各包，不另建 go.mod。
- 对话归 app-server：网页端和 chat 都是它的界面，界面这一侧在 `internal/appclient`，自己不启动 agent，也不读写 `.data/` 里的对话与设置。agent 的模型请求经 app-server 的模型代理走 `.data/app-server/llm.sock`（`AGENT_PROXY_SOCKET`），不再经过网页端。
- 远程：界面带 `-remote <ssh 别名>` 时用系统 ssh 连远端的 app-server（`ssh -T -o BatchMode=yes <别名> 'exec "$SHELL" -lc "tern app-server proxy"'`），拿 ssh 的标准输入输出当协议通道；不开端口，鉴权就是 SSH。断线重连也在 `internal/appclient`：本机、远程都按 1、2、4…秒退避、最多 30 秒，本机每次先 `app-server start`、远程重新起 ssh；重连期间请求立刻失败（网页端回 503“正在重连 <host>”，顶栏显示重连中），正在推送的订阅不结束，连上后按最后收到的 seq 重新 `thread/resume` 补齐。
- agent 的协议 socket 参数叫 `-agent-server`；app-server 启动 agent 时不传 `-tenant`，这一版只有平台空间。
- **各包目录下的 AGENTS.md 写着那个包的约束。**tern 读写那个目录的文件时会自动加载；设计或修改某个包之前，先读它的那份。
- 设计在 `spec/`，索引见 `spec/README.md`。`skills/`、`hooks/`、`commands/` 只放真实使用的；`eval/`、`queue/` 里的题目和示例任务是任务输入，要提交。

## 分工

- 新功能先由 Claude Code 在 `spec/` 写设计，用户确认后由 tern 照着实现。要偏离 spec，先把改动和原因写回 spec，再改代码。
- spec 按文件列出改动，写明这一期不做什么，给出验收命令和预期结果。实现在分支或 clone 上做，提交前由人 review。

## 写代码

- 只做任务要求的改动；短小直接，命名、注释密度和写法与周围代码一致。不为未来需求增加接口、配置、分层和防御性代码。
- 注释讲为什么、怎么运作；不写课程编号（Day 12、D25 一类），不链接仓库里不存在的文档。
- 改了启动、配置、参数或入口，同步改 README。

## 验证

- gofmt、`go vet`、`go build`，改了页面脚本再 `node --check`；然后用真实模型实际运行。不写 mock、脚本模型，不写死模型的工具决策与答案。目前不写 `*_test.go`，要引入先问用户。
- 会改仓库内容的验证在临时 clone 里做，用完删除。网页端的验证用常驻账号 `qa`（账号名与密码在 `.env` 的 `OBSERVER_TEST_USER`、`OBSERVER_TEST_PASSWORD`，不写进提交的文件）。
- 验证不在长期记忆里留下不是用户原话的事实；测试写进去的，用 `-memory-forget` 删掉。

## 数据与密钥

- 数据目录是 `TERN_HOME`：`main` 启动时先算出它（源码根目录里就是当前目录，别处是 `~/.tern`）并 chdir 过去，之后所有相对路径都按它解释。运行数据都在它下面的 `.data/`（不提交），路径由 `internal/tenant` 决定；租户的数据在 `.data/tenants/<租户>/`。
- 密钥只放 `.env`（权限 0600）或环境变量。
