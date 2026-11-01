# 输入框里 @ 提到文件

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。

## 1. 为什么要改

让 tern 看某个文件，现在要自己记住并敲出完整路径（`internal/web/page/shell.js` 这种）。Codex 的输入框敲 `@` 就弹出工作区文件的模糊搜索，选中把路径插进去（协议里的 `fuzzyFileSearch`）。

## 2. 改什么

### 2.1 app-server：`workspace/files`

- 参数：`{workspace, threadId, query}`，`workspace`（工作区名字，新对话用）和 `threadId`（已有对话，用它快照里的 `workspace_dir`）给一个。都没有工作区回 400“没有工作区”。
- 文件来源：在工作区目录里跑 `git ls-files --cached --others --exclude-standard -z`（已跟踪的加没被忽略的新文件），加 `GIT_OPTIONAL_LOCKS=0`。不是 git 仓库时回 400。结果在内存里缓存 10 秒（按目录），连续敲字不用每次都跑 git。
- 匹配：`query` 去掉空白、转小写；路径（小写）里能按顺序找到 `query` 的每个字符就算匹配（子序列）。打分，从高到低：
  1. 文件名（最后一段）以 `query` 开头；
  2. 文件名里连续出现 `query`；
  3. 路径里连续出现 `query`；
  4. 只是子序列。
  同一档里路径短的在前，再按字母序。
- `query` 为空时按路径排序给前面的。最多返回 20 个：`{"files": ["internal/web/page/shell.js", …]}`。

### 2.2 网页端：`@` 菜单

- 在输入框里，光标前面是“行首或空白 + `@` + 若干非空白字符”时，弹出菜单，位置和斜杠命令菜单（`#slash`，`composer.js` 的 `syncSlash`）一样，用同一套样式。
  - 内容是 `workspace/files` 的结果，敲字时跟着更新：请求做 150 毫秒去抖，只用最后一次的结果。
  - 每项显示文件名，后面淡色的目录。
- 操作：上下键选，回车或 Tab 插入，Esc 关掉，点一下也插入。菜单开着时回车不发送消息。
- 插入：把 `@` 加后面敲的字换成路径，后面跟一个空格（路径里有空格时用反引号括起来）。
- 用哪个工作区：进行中、续聊的对话用这条对话的（`threadId`）；新对话用工作区下拉当前选的。没有工作区时不弹菜单，`@` 就是普通字符。
- 斜杠菜单的逻辑不变；两个菜单不会同时出现（`/` 只认整个输入框开头）。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/files.go`（或新文件） | `workspace/files` 的实现与 10 秒缓存 |
| `internal/appserver/handle.go` | 方法表加 `"workspace/files"` |
| `internal/web/server.go` | `GET /workspaces/files?workspace=&threadId=&q=` → `workspace/files`（`q` 对应 `query`） |
| `internal/web/page/composer.js` | `@` 菜单：检测、取数据、键盘操作、插入 |
| `internal/web/page/app.css` | 复用 `#slash` 的样式；菜单项里目录的淡色 |
| `README.md` | 网页端那段补一句：输入框里敲 `@` 搜工作区的文件，选中插入路径 |

不改：发给 agent 的内容（就是文字，路径照原样）、斜杠命令。

## 4. 这一期不做

- 搜目录、搜符号。
- 插入后把文件内容自动附进上下文（模型看到路径会自己去读）。
- 没有工作区的对话搜沙箱里的文件。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check internal/web/page/composer.js` 通过。
2. 读代码报告：打分的几档和排序；去抖与只用最后一次结果的写法。

### Claude Code 在沙箱外做（无头 Chrome，临时 `HOME`，用 tern 自己的仓库当工作区）

3. 新对话选工作区，输入框敲 `看一下 @shelljs`：菜单第一项是 `internal/web/page/shell.js`；回车后输入框是“看一下 internal/web/page/shell.js ”，消息没有发出去。
4. 敲 `@` 再敲 Esc：菜单关掉，`@` 留在输入框里。
5. 没选工作区时敲 `@`：没有菜单。
6. `GET /workspaces/files?workspace=<名字>&q=commands` 的结果里有 `internal/commands/commands.go`，不超过 20 个。
