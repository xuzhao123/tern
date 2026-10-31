---
name: tern-implement
description: 让 tern（DeepSeek）照 spec/ 里的一份设计实现，再对照 spec review、用真实模型验收、合回主仓库。用户说“交给 tern 做”“让 tern 实现这份 spec”“review tern 的改动”时使用。
---

# 让 tern 实现一份 spec

1. **克隆**：`git clone --local . .data/selfuse/<名字>`。不用 worktree，沙箱里看不到主仓库的 `.git`，`go build` 会失败。主仓库有没提交的改动时，`git diff HEAD --binary | git -C <clone> apply --index`，拷上未跟踪的文件，在 clone 里提交成基线。
2. **预热缓存**：`tern -workspace <clone> -bash -check "go build -o /dev/null ./... && go vet ./..."`，用装好的 `tern`，和网页端是同一个 `TERN_HOME`，预热的才是它用的缓存。冷缓存编译要一分钟左右。
3. **发任务**：用用户开着的网页端（默认 8090），qa 登录（账号在网页端那份 `TERN_HOME` 的 `.env` 的 `TERN_QA_*`，旧名字 `OBSERVER_TEST_*` 也认）。`POST /settings/workspaces` 登记 clone，`POST /runs` 带 `provider: deepseek`、`workspace: <clone 目录名>`。不要另起网页端，用户开着的页面看不到另起那份的对话；需要新代码就请用户重启。等结束：看 `.data/runs/<对话ID>.jsonl` 里新出现的 `"kind":"exit"`，`"kind":"approval"` 是在等审批；`tail -F | grep --line-buffered` 后面别再接 `cut` 这类不按行刷新的命令。
4. **任务写法**：读 AGENTS.md 和 spec，按“按文件的改动”逐个改；spec 没写到的先停下来说明；做完不需要真实模型的验收；不要提交。
5. **review**：对照 spec 看 clone 里的 diff。tern 在沙箱里看不到工作区的 `.data/`（换成了空的 tmpfs），它说在那里删掉、没留下的东西，到沙箱外核对。spec 漏了的，先补 spec 并同步进 clone，再用 `POST /runs/<对话ID>/messages` 让 tern 接着补。
6. **验收**：要真实模型的条目，用 clone 编出的程序在沙箱外跑（tern 在沙箱里连不到模型接口），`TERN_HOME` 设成临时目录（或者 `HOME` 指到临时目录），不碰 `~/.tern`。只导出需要的那个密钥，不要导出整个 `.env`。
7. **回归**：改到 AGENTS.md「验证」里回归门槛列的那些包时，用 clone 编出的程序在源码目录跑编码评测和通用评测（`-baseline` 指当前基线，见 `spec/harness-next-phase.md` 进度），和基线比；掉了先查原因。只改网页、文档的跳过。
8. **合并**：`git -C <clone> add -A && git -C <clone> diff --cached --binary <基线提交> -- . ':(exclude)spec' > <patch>`（不先 add，新建的文件不在 diff 里；中途在 clone 里提交过也照样从基线算），主仓库 `git apply`，再 gofmt、vet、build。最后取消登记工作区，删掉 clone 和临时文件；`rm` 用写死的绝对路径。
