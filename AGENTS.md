# tern 项目约定

tern 是用 Go 实现的 agent harness，由 learning-agent 演变而来。启动、配置、参数与入口见 [README.md](README.md)。

## 结构

- 一个模块 `github.com/xuzhao123/tern`、一个入口 `main.go`：`go run .` 是 agent，子命令 `web`（网页端，含内置 MCP）、`chat`、`queue`、`metrics`、`eval`、`audit`、`rewind`。代码按功能放在 `internal/` 各包，不另建 go.mod。
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

- 运行数据都在 `.data/`（不提交），路径由 `internal/tenant` 决定；租户的数据在 `.data/tenants/<租户>/`。
- 密钥只放 `.env`（权限 0600）或环境变量。
