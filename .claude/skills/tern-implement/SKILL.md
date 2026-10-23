---
name: tern-implement
description: 让 tern（DeepSeek）照 spec/ 里的一份设计实现，再对照 spec review、用真实模型验收、合回主仓库。用户说“交给 tern 做”“让 tern 实现这份 spec”“review tern 的改动”时使用。
---

# 让 tern 实现一份 spec

1. **克隆**：`git clone --local . .data/selfuse/<名字>`。不用 worktree，沙箱里看不到主仓库的 `.git`，`go build` 会失败。主仓库有没提交的改动时，`git diff HEAD --binary | git -C <clone> apply --index`，拷上未跟踪的文件，在 clone 里提交成基线。
2. **预热缓存**：`./bin/tern -workspace <clone> -bash -check "go build -o /dev/null ./... && go vet ./..."`。冷缓存编译要一分钟左右。
3. **发任务**：用用户开着的网页端（默认 8090），qa 登录（账号在 `.env` 的 `OBSERVER_TEST_*`）。`POST /settings/workspaces` 登记 clone，`POST /runs` 带 `provider: deepseek`、`workspace: <clone 目录名>`。不要另起网页端，用户开着的页面看不到另起那份的对话；需要新代码就请用户重启。
4. **任务写法**：读 AGENTS.md 和 spec，按“按文件的改动”逐个改；spec 没写到的先停下来说明；做完不需要真实模型的验收；不要提交。
5. **review**：对照 spec 看 clone 里的 diff。spec 漏了的，先补 spec 并同步进 clone，再用 `POST /runs/<对话ID>/messages` 让 tern 接着补。
6. **验收**：要真实模型的条目，用 clone 编出的程序在沙箱外跑（tern 在沙箱里连不到模型接口）。只导出需要的那个密钥，不要导出整个 `.env`。
7. **合并**：`git -C <clone> diff -- . ':(exclude)spec' > <patch>`，主仓库 `git apply`，再 gofmt、vet、build。最后取消登记工作区，删掉 clone 和临时文件；`rm` 用写死的绝对路径。
