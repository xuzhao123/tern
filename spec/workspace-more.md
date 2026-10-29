# 工作区：候选目录、按工作区筛对话、改名、不是 git 仓库的目录

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。

## 1. 为什么要改

2026-10-25 后续改造：原实现的按名字筛选与历史旧名语义保留为历史背景；[web-product 项目导航](web-product/09-project-navigation.md)正在增加目录身份、项目树和个人置顶，已有任务从目录快照关联当前项目显示名，不改执行位置。以该实现记录的验收状态为准。

workspace-picker.md 有了浏览目录来选工作区，还差几样：
- **1.3**：每次都要自己一层层点进去找仓库。
- **1.4**：对话列表不分工作区，几个项目的对话混在一起。
- **1.5**：工作区名字取目录名，重名就加 `-2`，改不了。
- **1.6**：不是 git 仓库的目录直接拒绝。

Codex 把目录当“项目”管：有项目列表（`project/list`）、能改名（`project/update`），对话挂在项目下；不是 git 仓库的目录也能用，TUI 先问要不要信任这个目录。

## 2. 改什么

### 2.1 候选目录（1.3）

- app-server 新方法 `workspace/candidates`（无参数），找还没登记的 git 仓库：
  - 在这些目录下各看**一层**：已登记的工作区的上一级目录，以及家目录下存在的 `workspace`、`code`、`src`、`projects`、`repos`、`dev`。
  - 子目录里有 `.git` 且没登记过，就是候选。
  - 去重，按目录的修改时间从新到旧，最多 20 个：`{"candidates": [{"name", "path"}]}`。
- 选目录对话框（`#dir-dialog`）打开时，在目录列表上面多一节“可能要找的仓库”，点一下就把路径填进去、跳到那个目录（还要点“登记这个目录”才登记）。没有候选就不显示这一节。
- 设置页的工作区一节，在“浏览…”旁边列出前 5 个候选，每个后面一个“登记”。

### 2.2 按工作区筛对话（1.4）

- `summary()` 加 `workspace`（start 事件里的工作区名字，没有就不带）。
- 左侧列表上面、搜索框（thread-search.md）旁边加一个筛选下拉：“全部工作区 / <各个工作区名字> / 无工作区”。选了以后列表只显示那个工作区的对话（已归档的那一节也一样）。
- 选项来自列表里出现过的工作区名字加上设置里登记的，按名字排序。选择记在 `localStorage`（`tern-ws-filter`），刷新后还在；选的工作区已经不在了就回到“全部”。
- 只按名字筛：工作区改名以后，改名前的对话显示的还是旧名字。

### 2.3 工作区改名（1.5）

- app-server 新方法 `workspace/rename {name, newName}`：
  - `newName` 照 `serverName` 的规则（小写字母、数字、`-`、`_`，最长 32），不能和别的工作区重名，否则回 400。
  - 设置里登记的名字改掉；平台层和个人层里“默认工作区”（`workspace` 键）等于旧名字的，一起改成新名字。
- 已有对话的快照里存着旧名字和目录（`workspace`、`workspace_dir`），续聊用的是目录（`internal/appserver/server.go:66` 的注释），不受影响，列表里照旧显示旧名字。
- 设置页每个工作区后面加“改名”（`prompt()`，默认值是现在的名字）。
- 写审计：`Event: "user_workspace_rename"`，`Target` 是“旧名 → 新名”。

### 2.4 不是 git 仓库的目录（1.6）

- `workspace/add` 加参数 `git`：
  - 空：照旧，不是 git 仓库就拒绝；
  - `init`：先在那个目录里 `git init`，再登记；
  - `skip`：不是 git 仓库也登记。
- 选目录对话框里当前目录不是 git 仓库时，“登记这个目录”换成两个按钮：
  - “git init 后登记”；
  - “不用 git，直接登记”：点了先 `confirm()`：“这个目录不是 git 仓库：撤销只能退回 edit_file、write_file 改过的文件，bash 改的退不回来，也看不了改动面板。仍然登记？”
- 设置页里不是 git 仓库的工作区后面标“不是 git 仓库”（`workspace/browse` 那样按 `.git` 判断）。
- 沙箱照旧：没有 `.git` 就不挂（`sandbox.go` 本来就这么判断）。改动面板（workspace-diff.md）照旧回“不是 git 仓库，看不了 diff”。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/settings.go` | `workspace/candidates`、`workspace/rename`；`workspaceAdd` 的 `git` 参数 |
| `internal/appserver/server.go` | `summary()` 加 `workspace` |
| `internal/appserver/handle.go` | 方法表加两项 |
| `internal/web/server.go` | `GET /workspaces/candidates`、`PUT /settings/workspaces/{name}/name {newName}` 两条路由 |
| `internal/web/page/pages.js` | 对话框里的候选一节、非 git 的两个按钮；设置页的候选、改名、“不是 git 仓库”标记 |
| `internal/web/page/shell.js` | 列表的工作区筛选 |
| `internal/web/page/index.html` | 筛选下拉 |
| `internal/web/page/app.css` | 相应样式 |
| `README.md` | 讲工作区的地方补：候选目录、改名、可以登记不是 git 仓库的目录（以及代价）、列表能按工作区筛 |

不改：已有对话的快照、`workspace/browse` 的返回格式、chat 的 `-workspace 名字`。

## 4. 这一期不做

- 对话按工作区分组显示（只做筛选）。
- 记住“最近用过”的目录（候选按目录修改时间排）。
- 在 worktree 里开对话（计划 1.7，第 5 批）。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 读代码报告：候选扫描的范围（只看一层）、改名时一起改的“默认工作区”、`git` 参数三种取值的分支。

### Claude Code 在沙箱外做（无头 Chrome，临时 `HOME`）

3. 临时家目录下 `workspace/` 里放三个 git 仓库、一个普通目录：对话框里“可能要找的仓库”列出三个；登记一个以后再打开，只剩两个。
4. 筛选：两个工作区各开一条对话、再开一条没有工作区的：选某个工作区只剩它的那条；选“无工作区”只剩那条；刷新后筛选还在。
5. 改名：把工作区 `a` 改成 `alpha`，默认工作区原来是 `a` 的跟着变；`a` 下原来的对话还能续聊。改成已有的名字回 400。
6. 普通目录：点“git init 后登记”，目录里有了 `.git`，登记成功；另一个普通目录点“不用 git，直接登记”，确认后登记成功，设置页标“不是 git 仓库”，在它上面开对话能用编码工具改文件。
