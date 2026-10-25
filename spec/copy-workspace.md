# 在工作区的副本里开对话，做完把改动应用回去

状态：已实现并完成收尾验证（2026-10-25），范围与证据见 [本批收尾记录](closeout-20261025.md)。

## 1. 为什么要改

让 tern 动手改代码时，它直接改的是工作区本身：人同时在这个目录里干活，两边的改动会搅在一起；不满意也只能撤销。Codex 的 `/worktree`：在一个新的 git worktree 里开对话，不碰当前目录。

tern 不用 worktree：worktree 的 `.git` 是一个指向主仓库的文件，沙箱里看不到主仓库的 `.git`，`go build` 这类要读版本信息的命令会失败（`.claude/skills/tern-implement/SKILL.md` 第 1 步就是因此改用 `git clone --local`）。所以这里也用 clone：在副本里开对话，做完点一下把改动应用回原目录。

## 2. 改什么

### 2.1 app-server：建副本

- `thread/start` 加参数 `copy: true`（必须同时选了工作区）。开对话之前：
  1. 副本放在 `$TERN_HOME/.data/copies/<工作区名>-<对话ID 前 8 位>`；
  2. `git clone --local --quiet <工作区目录> <副本>`；
  3. 带上原目录里还没提交的改动：`git -C <原目录> diff HEAD --binary | git -C <副本> apply --index`，未跟踪、没被忽略的文件照路径拷过去；
  4. 在副本里提交成基线：`git -c user.name=tern -c user.email=tern@localhost commit -qam "副本基线"`（没有改动就不提交）。记下基线提交号。
  任何一步失败：删掉副本，回 500 和原因，不开对话。
- 未跟踪文件保留可执行权限；符号链接保留链接本身，不读取链接指向的文件。只复制 Git 列出的文件；若复制时遇到特殊文件则报错并清理副本，不把它们当普通文件读取。
- 这条对话的快照：`workspace` 照旧是工作区的名字，`workspace_dir` 是副本目录；start 事件另记 `copy_of`（原目录）、`copy_base`（基线提交号）。`summary()` 加 `copy: true`。
- 续聊、分叉照旧用快照里的目录（副本）。

### 2.2 app-server：应用回去

- `workspace/applyCopy {threadId}`：
  1. 在副本里 `git add -A`，`git diff --cached --binary <copy_base>` 得到补丁；
  2. 没有改动回 400“副本里没有新的改动”；
  3. 在原目录 `git apply --check`，不通过就回 409，带上 `git apply` 的报错（哪些文件对不上），原目录不动；
  4. 通过就 `git apply`（不 `--index`，改动留在原目录的工作区里，由人决定怎么提交）；
  5. 存档里加一条事件 `{"kind": "copy_applied", "body": {"files": [...]}}`，写审计 `user_apply_copy`；
  6. 返回改了哪些文件。
- 对话在运行时回 409“这一轮在跑，等它结束再应用”。

### 2.3 删除

- conversation-manage.md 的删除：这条对话是副本对话、而且没有别的对话（分叉）的快照还用着这个副本目录时，连副本目录一起删掉。

### 2.4 网页端

- 新对话：工作区下拉旁边一个勾选框“副本”，选了工作区才能勾；悬停写“在这个工作区的副本里跑：不碰原目录，做完可以把改动应用回去”。勾了就在 `thread/start` 里带 `copy: true`。
- 列表里副本对话前面标“副本”。
- 副本对话停下时，顶栏有“应用到 <工作区名>”按钮（`#apply-copy-btn`）。点了先 `confirm()`：“把副本里的改动应用到 <原目录>？只改原目录的文件，不提交。”成功后提示“已应用 N 个文件”；冲突时把 `git apply` 的报错显示出来。
- 改动面板（workspace-diff.md）在副本对话里看的是副本相对基线的改动：`workspace/diff` 对副本对话用 `copy_base` 代替 `HEAD`。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/server.go` | `threadStart` 的 `copy` 参数、建副本；start 事件的 `copy_of`、`copy_base`；`summary()` 的 `copy`；`workspace/applyCopy` |
| `internal/appserver/threads.go` | 删除时连副本目录一起删的判断 |
| `internal/appserver/diff.go`（workspace-diff.md 加的） | 副本对话用 `copy_base` |
| `internal/appserver/handle.go` | 方法表加一项 |
| `internal/web/server.go` | `POST /runs/{id}/apply-copy` → `workspace/applyCopy` |
| `internal/web/page/index.html` | “副本”勾选框、`#apply-copy-btn` |
| `internal/web/page/composer.js` | 新对话带 `copy` |
| `internal/web/page/shell.js` | 勾选框的可用性；列表的“副本”标记；“应用到”按钮 |
| `internal/web/page/app.css` | 样式 |
| `internal/appserver/AGENTS.md` | 补一条：副本对话的目录在 `.data/copies/`，快照里是副本目录，`copy_of`、`copy_base` 记原目录和基线 |
| `README.md` | 网页端讲工作区的地方补：可以勾“副本”在工作区的副本里开对话，做完“应用到”原目录 |

不改：沙箱的挂载规则（副本有自己完整的 `.git`）、撤销（在副本里照常）。

## 4. 这一期不做

- 真正的 `git worktree`。
- 在副本里提交、推分支。
- 应用时自动三方合并（冲突就报出来，由人处理）。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 读代码报告：建副本的四步与失败时的清理；应用回去前的 `--check`。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 临时仓库里有一个没提交的改动。勾“副本”开对话，让模型改一个文件：原目录没变；副本里有原来那个没提交的改动和模型的改动；改动面板只显示模型的改动。
4. 点“应用到”：原目录里多了模型的改动，原来那个没提交的改动还在，没有新提交。
5. 在原目录里把模型要改的那几行先改掉，再让副本对话改同一处，点“应用到”：回 409，显示哪个文件对不上，原目录不动。
6. 删除这条副本对话：副本目录没了，原目录不动。
