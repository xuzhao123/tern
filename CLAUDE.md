# Claude Code 在 tern 仓库里的角色

共用的约定见 [AGENTS.md](AGENTS.md)，先读它。这份文件只写给 Claude Code；tern 有 AGENTS.md 时不读这里。

- **只做设计。**产出是 `spec/` 下的 Markdown 设计文档；不改 `main.go`、`internal/` 等代码和仓库里的其他文件，代码由 tern 照 spec 实现。用户明确要求时才例外，例外只限那一次。
- **调研不改仓库。**可以读代码、跑只读命令、编译和不改仓库内容的试运行；设计里点名的函数、路径、参数要先在代码里核对存在。
- **写到 tern 能照着做。**按文件列出改动，写明这一期不做什么，每一期给出验收方法（命令和预期结果）；拿不准的地方列成“需要确认的问题”，不替用户拍板。
- **只写 Markdown。**用户要求时才另做 HTML 页面。
- **review 实现。**用户要求时，对照 spec 看 tern 的改动（diff），问题列给用户；要改设计的，改 spec。

## 交给 tern 实现的步骤

用户要求由 Claude Code 驱动 tern 实现某份 spec 时，按下面做（2026-10-23 用这套流程实现了 `spec/` 里两份已实现的设计）。

1. **准备工作区**：在 `.data/selfuse/` 里 `git clone --local` 一份。不要用 git worktree：worktree 的 `.git` 是指向主仓库的文件，沙箱里看不到主仓库，`go build` 取版本信息时会失败。主仓库有没提交的改动时，用 `git diff HEAD --binary | git -C <clone> apply --index`，再把未跟踪的文件拷过去，在 clone 里提交成基线，这样 tern 的 diff 只有它自己的改动。
2. **预热编译缓存**：`./bin/tern -workspace <clone> -bash -check "go build -o /dev/null ./... && go vet ./..."`。沙箱只有 1 个 CPU，冷缓存编译整个模块要 1 分钟左右。
3. **用用户开着的网页端发任务**（默认 8090）：用验证账号 qa 登录，`POST /settings/workspaces` 登记 clone，`POST /runs` 带上 `provider: deepseek` 和 `workspace: <clone 的目录名>`。不要另起网页端：网页端只在启动时读一次对话列表，另起的那份发起的对话，用户开着的页面看不到。需要新代码时，先请用户重启。
4. **任务怎么写**：先读 AGENTS.md 和这份 spec，按“按文件的改动”一节逐个文件改；spec 没写到或要偏离的地方先停下来说明；做完不需要真实模型的那几条验收，把结果写在回答里；不要提交。
5. **review**：对照 spec 看 clone 里的 diff。spec 漏写的，先补 spec（同步进 clone 并提交到基线），再用 `POST /runs/<对话ID>/messages` 在同一条对话里让 tern 补上。
6. **验收**：tern 在沙箱里连不到模型接口，需要真实模型的条目由 Claude Code 用 clone 编出来的程序在沙箱外跑。只导出需要的那个密钥，不要把整个 `.env` 导出成环境变量。
7. **合并与清理**：在 clone 里 `git diff -- . ':(exclude)spec' > <patch>`，到主仓库 `git apply`，再跑 gofmt、vet、build。之后取消登记工作区、删掉 clone 和临时文件；`rm` 用写死的绝对路径，不用 shell 变量。

参考：一份小 spec 大约 40 次模型请求、两分钟，输入约 120 万 token，其中九成以上命中缓存。
