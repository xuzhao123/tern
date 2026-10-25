# 对话的目标：长任务定一个目标，界面上常显，改了告诉模型

状态：已实现并完成收尾验证（2026-10-25），范围与证据见 [本批收尾记录](closeout-20261025.md)。

## 1. 为什么要改

长任务做着做着，问题一句句往下接，最初要干什么在上下文里越来越远，人和模型都容易跑偏。tern 有进度文件（`progress`，目标写在 `goal` 里），但只有模型用 `progress` 时才看得到，界面上没有。Codex 的 app-server 有 `thread/goal/set`、`get`、`clear`，给长任务定目标。

## 2. 改什么

### 2.1 app-server

- 目标存在 `.meta.json`（conversation-manage.md）的 `goal` 里：`{"text": "…", "at": "…", "sent": false}`。`summary()` 加 `goal`（只带 `text`，有才带）。
- `thread/goal/set {threadId, text}`：去掉首尾空白，最长 500 个字；空串等于清除。改了就把 `sent` 记成 false，广播 `thread/status/changed`，写审计 `user_thread_goal`。
- **告诉模型**：`r.notes()`（`internal/appserver/server.go:1079`）在 `goal.sent` 为 false 时加一句：
  - 有目标：“用户给这条对话定了目标（之后各轮都朝这个做，做完了或者做不到要说明）：<目标>”；
  - 清除了：“用户清除了这条对话的目标。”
  这一轮开始（`turn/start` 发出去）以后把 `sent` 记成 true。只在设了、改了之后的下一轮说一次，不是每轮重复。
- 新对话（`thread/start`）可以带 `goal`，第一轮就带上那句提醒。

### 2.2 网页端

- 对话页标题下面一行（`#heading` 里）显示目标：“目标：<目标>”，旁边“改”；没有目标时显示淡色的“＋ 定个目标”。点了用 `prompt()` 改（默认值是现在的目标），留空就是清除。
- 运行中也能改：改的是下一轮开始时告诉模型的，这一轮不打断。
- 导入的对话、评测的试次不显示。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/threads.go` | `runMeta` 加 `Goal`；`thread/goal/set` |
| `internal/appserver/server.go` | `summary()` 加 `goal`；`notes()` 加目标的提醒；`turn/start` 发出后记 `sent`；`thread/start` 收 `goal` |
| `internal/appserver/handle.go` | 方法表加一项 |
| `internal/web/server.go` | `PUT /runs/{id}/goal {text}` → `thread/goal/set` |
| `internal/web/page/shell.js` | 标题下面的目标一行 |
| `internal/web/page/index.html` | 放目标的元素 |
| `internal/web/page/app.css` | 样式 |
| `README.md` | 网页端那段补一句：对话可以定一个目标，常显在标题下面，改了以后下一轮告诉模型 |

不改：进度文件（`progress`）的 `goal`，两者不同步：进度文件是模型拆任务用的，这个是人给的方向。

## 4. 这一期不做

- 模型自己判断目标完成了、自动清除。
- 进度文件和对话目标互相同步。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 读代码报告：`sent` 什么时候变 false、什么时候变 true；清除时的提醒。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 一条对话问一句，然后在标题下面定目标“写一首关于海的五言绝句”，再问“开始吧”：这一轮的请求里有那句提醒，回答是五言绝句；再问一句，请求里不再有提醒。
4. 改成别的目标：下一轮又有一次提醒；清除：下一轮有“清除了”的提醒，标题下面变回“＋ 定个目标”。
5. 刷新页面：目标还在。
