# /review 可以指定范围：未提交的改动、对比某个分支、某个提交

状态：已实现并完成收尾验证（2026-10-25），范围与证据见 [本批收尾记录](closeout-20261025.md)。

## 1. 为什么要改

内置的 `/review`（`internal/commands/builtin/review.md`）只评审工作区里还没提交的改动。想评审“这个分支相对 main 改了什么”或者“上一个提交”，得自己把要求写成一段话。

Codex 的 `review/start` 可以选范围：未提交的改动、对比某个分支（`baseBranch`）、某个提交（`commit`），或者一段自定义说明。

## 2. 改什么

只改模板，不改代码。`review.md` 改成：

```markdown
---
description: 评审改动：默认是还没提交的改动；也可以给分支名（对比它）或提交号，按严重程度列出问题，不修改文件
argument-hint: [分支名或提交，可选] [关注点，可选]
---
评审这个仓库里的一组改动。参数：$ARGUMENTS

1. 先定范围。参数的第一个词如果能用 `git -C /workspace rev-parse --verify --quiet <它>^{commit}` 解析：
   - 它是一个分支（`git -C /workspace show-ref --verify --quiet refs/heads/<它>` 或 `refs/remotes/<它>` 成立）：评审当前 HEAD 相对它的改动，`git -C /workspace diff <它>...HEAD`（三个点：从分叉点开始），外加还没提交的改动；
   - 否则当作一个提交：评审这个提交本身，`git -C /workspace show <它>`。
   解析不了，或者没给参数：评审还没提交的改动，`git -C /workspace status --short` 和 `git -C /workspace diff`。
   第一个词当了范围的话，剩下的是关注点；否则整段参数都是关注点。开头用一句话说明这次评审的范围。
2. 没有 bash 时用 grep、read_file 看改动涉及的文件，说明看不到 diff。
3. 需要上下文时用 read_file 读改动所在的函数和直接调用方，不要只看 diff 片段下结论；查到能下结论就停，不另写程序复现。
4. 按“会出错的 › 可能出错的 › 可读性”排序，每条写清文件与行号、问题、触发条件和建议的改法；没有问题就直说没有。
5. 只评审，不修改任何文件。
```

README 里内置命令那张表的 `/review` 一行同步改成：“评审改动：默认是还没提交的改动，`/review main` 对比 main 分支，`/review <提交>` 评审那个提交；按严重程度列出问题，不改文件”。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/commands/builtin/review.md` | 换成第 2 节的全文，一字不改 |
| `README.md` | 内置命令表里 `/review` 那一行 |

不改：`internal/commands` 的代码、`code-review` skill。

## 4. 这一期不做

- 界面上专门的“选评审范围”菜单（Codex 的 `/review` 弹列表）。
- 评审远端的 PR。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `go build ./...` 通过（模板是编进程序的）。
2. `diff` 确认 `review.md` 和第 2 节一致。

### Claude Code 在沙箱外做（真实模型）

3. 临时仓库：`main` 上一个提交，`feature` 分支上再两个提交，其中一个引入明显的 bug；切到 `feature`。`-question '/review main'`：开头说明范围是“相对 main”，指出那个 bug。
4. `-question '/review <引入 bug 的那个提交号>'`：只评审那个提交。
5. 工作区里改一个文件不提交，`-question '/review 关注错误处理'`：范围是未提交的改动，“关注错误处理”当成关注点。
