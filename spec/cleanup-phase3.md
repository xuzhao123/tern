# 第 3 期清旧账：删除对话漏删子 agent 检查点、Go 的 stat cache 警告、知识库旧名称

状态：已实现并验收（2026-10-31，tern 用 DeepSeek 实现，50 次请求）。D14 四句 tern 逐条对照代码核对过，都还对，只改名称和来源；D15 内容也核对过还对，合并时由 Claude Code 一并把"观测台"和来源改掉。沙箱外：会开子 agent 的对话删除后 `checkpoints/` 清空；分叉共用第一轮时删原对话留着共用的、再删分叉才清空；新提交后沙箱里 `go build` 不再有 stat cache 警告；`rag-retry` 2/2（`e20261031-155952`）。

## 1. 为什么要改

三件旧账，都在 2026-10-31 复现过：

1. **删除对话后子 agent 的检查点还在**（[spec 索引](README.md#未完成与暂缓)"待专项回归"那条）。临时 `TERN_HOME`、DeepSeek，让模型用 `spawn_agent` 开一个子 agent，结束后 `thread/delete`：`.data/checkpoints/` 里剩下 `<轮次ID>-sub-<哈希>.json`、`.lock` 和 `<轮次ID>.calls/`。原因：`internal/appserver/threads.go` 的 `drop` 只删 `<轮次ID>.json`、`.lock`、`.files`，子 agent 的检查点ID是在轮次ID后面加 `-sub-`（`internal/agent/checkpoint.go` 第 120 行附近），`.calls` 目录（`internal/agent/approval.go` 第 190 行附近）也没删。`~/.tern/.data/checkpoints` 里现有 171 个检查点，29 个对不上任何对话的轮次。
2. **沙箱里 `go build` 报 `go: writing stat cache: … read-only file system`**。临时 clone 里新提交一次再 `go build`：出现 1 次（`go vet` 不出现）；同样条件加 `-buildvcs=false`：0 次。原因：Go 按 VCS 给主模块算伪版本，并想把它写进模块缓存的 stat cache，而沙箱里模块缓存只读挂载。
3. **知识库里的旧名称**：`retrieval/corpus.md` 的 D14 还叫"观测台"，来源写 `internal/observer/...`（现在是 `internal/web/...`）；`internal/agent/react.go` 第 998、1002 行附近检索用的提示词还写"learning-agent 学习项目"；`internal/skills/builtin/code-review/SKILL.md` 的 `origin` 写 `learning-agent procedural memory`。

## 2. 改什么

### 2.1 删除对话时连子 agent 的检查点一起删

`drop` 里对每个不被别的对话共用的轮次ID（现在的 `shared` 判断不变），除了现有的几样，再删：

- `checkpoints/<轮次ID>.calls`（目录）；
- `checkpoints/` 下所有以 `<轮次ID>-sub-` 开头的文件和目录（子 agent 的 `.json`、`.lock`、`.files`、`.calls`，以及子 agent 再开的子 agent，名字都以这个前缀开头）。

用 `os.ReadDir` 列 `checkpoints/` 后按前缀匹配，不用 shell 通配。别的对话共用的轮次（分叉）照旧整组跳过。

### 2.2 沙箱里的 Go 不写 VCS 信息

`internal/sandbox/launch_linux.go` 第 340 行附近的 `GOFLAGS=-mod=readonly -p=1` 改成 `GOFLAGS=-mod=readonly -p=1 -buildvcs=false`，注释写明原因（主模块的伪版本要写进只读的模块缓存，会报 stat cache 警告；沙箱里编出的程序不需要 VCS 信息）。`launch_darwin.go` 如果也设了 `GOFLAGS`，同样加；没有就不加。

### 2.3 知识库与提示词的旧名称

- `retrieval/corpus.md` 的 D14：标题和正文的"观测台"改成"网页端"；来源改成现在实际的文件。**先核对 D14 说的行为在现在的代码里还对不对**（续聊恢复什么、预算是否每个新提问重置、Turn 编号、history 展示什么），对的照旧，不对的按现在的代码改，并在最终回答里逐条说明核对结果。别的条目不动。文档编号 `[D14]` 不改（`retrieval/AGENTS.md`）。
- `internal/agent/react.go` 检索提示词里的"learning-agent 学习项目"改成"tern 项目"，"不要给无关问题添加learning-agent等项目词"改成"不要给无关问题添加 tern 等项目词"。
- `internal/skills/builtin/code-review/SKILL.md` 的 `origin` 改成 `tern procedural memory`。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/threads.go` | 2.1 |
| `internal/sandbox/launch_linux.go`（`launch_darwin.go` 视情况） | 2.2 |
| `retrieval/corpus.md` | 2.3 的 D14 |
| `internal/agent/react.go` | 2.3 的提示词 |
| `internal/skills/builtin/code-review/SKILL.md` | 2.3 的 origin |

不要改：评测题、`retrieval/AGENTS.md`、别的语料条目、`drop` 对分叉共用轮次的判断。

## 4. 验收

### tern 自己做（沙箱里）

1. `gofmt -l .` 无输出；`go vet ./...`、`go build ./...` 通过。
2. 读代码报告：`drop` 现在删哪些路径、共用轮次怎么跳过；D14 每一句的核对结果（对照哪段代码）。

### Claude Code 在沙箱外做

1. 临时 `TERN_HOME`、DeepSeek：开一个会用 `spawn_agent` 的对话，删除后 `checkpoints/` 里不再有它的任何文件；同时有一个分叉对话共用前几轮时，删原对话不删共用的。
2. 临时 clone 新提交后在沙箱里 `go build`：没有 stat cache 警告。
3. 通用评测里的 RAG 题（`rag-retry`）照常通过。
