# 看工作区的改动：diff 面板，每轮末尾列出改了哪些文件

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。

## 1. 为什么要改

tern 改了代码以后，网页端只能在一张张工具卡片里看单次 `edit_file` 的前后片段（`internal/workspace/workspace.go` 的 `snippet`）。“这一轮到底改了哪些文件”“现在整个工作区和提交的版本差在哪”都看不到，要回终端敲 `git diff`。review tern 的改动时尤其别扭。

Codex 的 `/diff` 显示 `git diff`（包括未跟踪的文件）；改文件时在对话里显示带颜色的 diff。

## 2. 改什么

### 2.1 app-server：`workspace/diff`

- 参数 `{threadId}`：用这条对话快照里的工作区目录（start 事件的 `workspace_dir`）。没有工作区回 400“这条对话没有工作区”；目录不是 git 仓库回 400“不是 git 仓库，看不了 diff”。
- 在那个目录里跑：
  - `git status --porcelain=v1 -z --untracked-files=all` 拿文件列表；
  - `git diff HEAD --no-color --no-ext-diff` 拿已跟踪文件的 diff（仓库还没有提交时用 `git diff --cached` 加 `git diff`）；
  - `git diff HEAD --numstat` 拿增删行数。
- 未跟踪的文件：每个按“新增”给出内容，前面加 `+`，像 diff 一样；二进制文件、超过 256KB 的只写一行说明，不给内容。
- 返回：
  ```json
  {"dir": "/home/dev/workspace/x", "head": "a1b2c3d 提交说明",
   "files": [{"path": "main.go", "status": "M", "added": 3, "deleted": 1, "diff": "@@ -1,4 +1,6 @@ …"},
             {"path": "new.txt", "status": "??", "added": 2, "deleted": 0, "diff": "+第一行\n+第二行\n"}],
   "truncated": false}
  ```
  `status` 取 porcelain 的两位状态去掉空格（`M`、`A`、`D`、`R`、`??`）；`diff` 是这个文件那一段（从 `diff --git` 那行之后开始）。
- 总量超过 1MB 就停在那里，后面的文件只给路径、状态、行数，`diff` 为空，`truncated` 为 true。
- git 命令加 `GIT_OPTIONAL_LOCKS=0`，不碰仓库里的锁；只读。

### 2.2 网页端：改动面板

- 顶栏“沙箱文件”按钮（`#files-btn`）旁边加“改动”按钮（`#diff-btn`），有工作区的对话才显示。点开是和沙箱文件同一个位置的侧栏（照 `files.js` 的 `renderFilesPanel` 的写法；两个面板互斥，打开一个关掉另一个）。
- 面板顶上写“工作区相对 HEAD 的改动 · <head>”，下面说明“包括所有轮次和人自己的改动，不只这一轮”，再是“刷新”按钮。
- 文件列表：每行“状态 路径 +增 −删”，点开展开那个文件的 diff。diff 按行着色：`+` 绿、`-` 红、`@@` 灰。内容只用 `textContent`，不当 HTML。
- 截断时最后一行“改动太多，后面的文件只列了名字”。
- 这一轮跑完（收到 `exit`）时，面板开着就自动刷新一次。

### 2.3 每轮末尾：改了哪些文件

- 每一轮的回答下面加一行（只在这一轮有成功的 `edit_file`、`write_file` 时）：“这一轮改了 N 个文件：a.go、b.go …（看改动）”。文件名从这一轮的工具调用里取（`path` 参数，`Status` 是 ok 的），去重，最多列 5 个，多了写“等 N 个”。
- 点“看改动”打开改动面板，并展开这几个文件。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/files.go`（或新文件 `internal/appserver/diff.go`） | `workspace/diff` 的实现 |
| `internal/appserver/handle.go` | 方法表加 `"workspace/diff"` |
| `internal/web/server.go` | `GET /runs/{id}/diff` → `workspace/diff`（`threadId` 取路径） |
| `internal/web/page/index.html` | `#diff-btn` 按钮 |
| `internal/web/page/diff.js`（新文件） | 改动面板：取数据、画列表与着色的 diff、展开指定文件 |
| `internal/web/page/agents.js` | 侧栏的分派里加改动面板（和 `ui.filesOpen` 同样的写法，`ui.diffOpen`） |
| `internal/web/page/chat.js` | 每轮末尾“这一轮改了 N 个文件”那一行 |
| `internal/web/page/shell.js` | `#diff-btn` 的显示与点击 |
| `internal/web/page/app.css` | 面板、着色的样式 |
| `README.md` | 网页端那段补一句：“改动”面板看工作区相对 HEAD 的 diff，每轮末尾列出这一轮改过的文件 |

不改：工具卡片里单次编辑的片段、撤销、沙箱文件面板。

## 4. 这一期不做

- 每一轮自己的 diff（这一轮开始前到结束后）：要从快照和之后各轮推算，另做。
- 在面板里直接撤销某个文件、按块暂存。
- 对比任意两个提交。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过和新加的页面脚本。
2. 读代码报告：`workspace/diff` 用的 git 命令和截断的位置。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 在一个有提交的临时仓库上开对话，让模型改一个已有文件、新建一个文件：这一轮末尾有“这一轮改了 2 个文件：…”；点“看改动”，面板里两个文件都在，已有文件的 diff 有 `-`、`+` 行且着色，新文件全是 `+`。
4. 人在仓库里手动再改一个文件，点“刷新”：多出这个文件。
5. 没有工作区的对话不显示“改动”按钮；`GET /runs/<id>/diff` 回 400。
