# 跑完、要审批时通知；对话列表的未读和等待标记

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。

## 1. 为什么要改

tern 跑一轮常要几分钟到半小时。人切到别的窗口以后，页面跑完了、或者停在一个联网审批上，什么提示都没有，只能隔一会儿回来看一眼。2026-10-24 实现插话时，串联就因为一个没人答的联网审批停了好一阵。左侧列表也看不出哪条有了新结果、哪条在等人。

Codex 的 TUI 在失焦时发终端通知，`notify` 配置的程序在每轮结束时调用（`codex-rs/core/src/config/mod.rs:798`）；app-server 协议里有 `thread/readState/*` 记哪些对话没看过。

## 2. 改什么

### 2.1 app-server：对话在等什么

- `summary()` 加 `waiting`：
  - `approval`：这条对话有没答复的联网审批或工具审批（`r.approvals`、`r.toolWaits` 不为空）；
  - `question`：有没回答的提问（`r.asks` 不为空）；
  - 两样都有时取 `approval`；都没有就不带这个字段。
- 这三张表加进、删掉条目时（加：`server.go:1911`、`:1933`、`:1956` 附近；删：`:1544`、`:1564`、`:1605` 附近），都广播一次 `thread/status/changed`（`status` 照现在的状态）。网页端收到会让 `/connection` 的 `runs` 计数加一，页面就会重新加载列表（`internal/web/page/connection.js`），不用另开一路推送。

### 2.2 网页端：列表上的标记

- `waiting` 不为空的行：在标题前面加一个醒目的小标签“等你审批”或“等你回答”。
- **未读**：这条对话的 `updated_at` 比这个浏览器上次看它时新，就在行尾加一个小圆点。
  - “看过”记在 `localStorage`（键 `tern-seen`，值是 `{"<主机>|<对话ID>": updated_at}`），只记最近 500 条。
  - 页面打开着这条对话、而且页面在前台（`document.visibilityState === "visible"`）时，每次重画都把它记成看过；切回前台时也补记一次。
  - 新建的对话发出第一句话就算看过，不会自己给自己加圆点。
- **标签页标题**：有在等审批或回答的对话时，标题前面加“(N) ”，N 是条数；没有就去掉。

### 2.3 网页端：浏览器通知

- 设置页加一节“通知”，一个勾选框“浏览器通知：页面不在前台时，对话跑完或在等你审批、回答时提醒”。
  - 勾上时调 `Notification.requestPermission()`。浏览器拒了就把勾去掉，写一行“浏览器没有允许通知”。
  - 这个选项只记在这个浏览器里（`localStorage` 的 `tern-notify`），不进 app-server 的设置：通知是浏览器的事，每台电脑各自决定。
- 每次重新加载列表时，和上一次的列表（按 `主机|对话ID`）比：
  - 原来 `done` 为 false、现在为 true：通知“跑完了：<标题>”；
  - `waiting` 从没有变成有：通知“等你审批：<标题>”或“等你回答：<标题>”。
  - 第一次加载（页面刚打开）只记下状态，不通知。
- 只在开了通知、浏览器允许、而且页面不在前台时发。标题用对话名字，没有名字用第一句话，截到 40 个字。点通知就聚焦这个页面、打开那条对话（别的主机的整页跳过去，照 `runRow` 的写法）。同一条对话同一种通知用 `tag` 去重。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/server.go` | `summary()` 加 `waiting`；三张等待表增删时广播 `thread/status/changed` |
| `internal/web/page/shell.js` | `runRow` 加等待标签和未读圆点；`loadRuns` 记下各条的 `done`、`waiting`，和上一次比较后发通知；标签页标题的计数 |
| `internal/web/page/util.js` | “看过”的读写（`localStorage`，最多 500 条）；通知开关的读写 |
| `internal/web/page/chat.js` 或画当前对话的地方 | 打开着、页面在前台时把当前对话记成看过 |
| `internal/web/page/main.js` | `visibilitychange` 时补记当前对话看过 |
| `internal/web/page/pages.js` | 设置页“通知”一节 |
| `internal/web/page/app.css` | 标签、圆点的样式 |
| `README.md` | 网页端那段补一句：列表上标出等审批、等回答和有新结果的对话；设置里能开浏览器通知 |

不改：审批与提问的流程、`approval/respond`、存档格式、协议版本 `Protocol`（`summary` 只加字段）。

## 4. 这一期不做

- 看过的状态存到 app-server（换浏览器就重新算）。
- `notify` 那样每轮结束调一个外部程序。
- 声音提醒。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 读代码报告：`waiting` 的取值和广播的位置；页面比较两次列表、发通知的条件。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 让对话跑一个要联网的 bash 命令（`curl -sS https://example.com -o /dev/null`）：列表上这一行出现“等你审批”，标签页标题前面是“(1) ”；拒绝以后标签和计数都消失。
4. 未读：在 A 对话页面时，B 对话跑完一轮，B 的行尾出现圆点；打开 B，圆点消失；刷新页面后也不再出现。
5. 通知：用 CDP 给页面授予通知权限，模拟页面在后台（`Emulation.setFocusEmulationEnabled` 或改 `document.visibilityState`），B 对话跑完，页面里有一次 `new Notification`（替换 `window.Notification` 记下调用即可），标题是“跑完了：…”。页面在前台时不发。
