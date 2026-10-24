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
