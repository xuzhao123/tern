# 网页端插话照 Codex：发出去马上看得到，没赶上的接着发

状态：已实现（2026-10-24，tern 实现；第 5 节验收全部通过，第 4–8 条由 Claude Code 用无头 Chrome 驱动真实页面、真实模型跑）。review 时补了第 2.1 节第 7 条（自动接着发失败时放回输入框）。

## 1. 为什么要改

2026-10-24 用户说网页端插话“感觉不对”：对话框里的文案有点呆；点了发送要等好久页面才出来；体验要和 Codex 一致。

现在的做法（`internal/web/page/composer.js:101` 的 `steer`）：POST `/runs/<id>/steer` 成功后只清空输入框，这句话不出现在页面上。agent 在下一次请求模型之前才把它取走（`internal/agent/react.go:338`），发出 `userMessage`（`steer: true`）的通知，页面收到这条通知才在对话里画出来（`internal/web/page/runs.js:137`）。如果这时正在跑一条 bash（最长 2 分钟）或者模型在长篇输出，人就对着一个清空的输入框干等，什么反馈都没有。

还有一个没被发现的丢消息问题：这一轮如果在 agent 取走插话之前就结束了（比如插话时模型正在写最后的回答），没用上的插话放在 `turn/completed` 的 `queued` 里交回界面（`react.go:138`）。chat 会接着当下一条消息发（`internal/chat/chat.go:949`），网页端不看这个字段（`runs.js:166`），这句话就丢了。

Codex 的做法（`~/workspace/codex/codex-rs/tui/src/bottom_pane/pending_input_preview.rs`、`chatwidget.rs:1368`）：
- 运行中提交的话**马上**显示在输入框上方的“待发送”区：`Messages to be submitted after next tool call (press Esc to interrupt and send immediately)`，下面每条一行 `↳ …`，超过 3 行省略。
- 服务端回执里带着客户端给的 `client_id`。回执一到，这条就从待发送区挪进对话记录。
- 没赶上这一轮的，这一轮结束时自动作为下一条消息发（`Messages to be submitted at end of turn`）。
- Esc：停下正在做的，马上把待发送的发出去。

## 2. 改什么

### 2.1 行为

1. **发出去马上看得到**：运行中按回车，输入框清空，这句话立刻出现在输入框上方的待发送区（`#steer-strip`）。
2. **送到模型时挪进对话**：agent 取走它、发出带同一个 `client_id` 的 `userMessage`（`steer: true`）通知时，从待发送区去掉；对话里在那次请求之前显示用户气泡（现有的画法不变）。
3. **没赶上这一轮的接着发**：这一轮结束（收到 `exit`）时，待发送区里还剩的，按顺序用空行拼成一条，作为下一条消息发出去（POST `/runs/<id>/messages`，和人手动接着问一样）。
4. **立即发送**：待发送区右侧有“立即发送”按钮。点了就和点停止一样（POST `/runs/<id>/stop`），等这一轮结束，再按第 3 条发出去。
5. **停止**：点输入框旁的停止按钮，也就是没点“立即发送”时：待发送区里还剩的不自动发，按顺序拼好放回输入框，人自己决定发不发。
6. **没送到**：
   - 返回 409（这一轮刚好结束）：这条留在待发送区，等 `exit` 以后按第 3 条发。
   - 其他错误：这条从待发送区去掉，文字放回输入框，`#hint` 显示“没发出去：<原因>”。
   - 不再用 `alert`。
7. **自动接着发失败**（第 3、4 条发 `/runs/<id>/messages` 没成功）：文字放回输入框（追加在已有内容后面），`#hint` 显示“没发出去：<原因>”，不能丢。（review 时补）

待发送区只在发出这句话的那个页面里有：别的标签页、chat 看不到，等送到模型时在对话里看到。这和 Codex 一样，是本地的。

### 2.2 `client_id`

照 Codex，用客户端生成的 id 认回执，不靠比对文字：
- 文字可能重复。
- 被 `UserPromptSubmit` hook 拦下的插话，通知里的 `text` 已经换成“（这条插话被 UserPromptSubmit hook 拦下）”，原文不能写回存档（hook 拦它多半就是因为里面有密钥）。

链路：网页端 `crypto.randomUUID()` → POST `/runs/<id>/steer` 的 `{text, client_id}`（`internal/web/server.go` 的 `steerRun` 原样转发请求体，不用改）→ app-server `turn/steer` 透传给 agent → agent 排队时记下 → 取走时 `userMessage` 的 `data` 带上 `client_id`。被 hook 拦下的那条也带。`client_id` 可选：chat、`agent_send` 不传，照旧。

### 2.3 文案

| 地方 | 现在 | 改成 |
| --- | --- | --- |
| 运行中输入框的占位（`shell.js:207`） | 运行中：输入的话会排进 agent 的下一次请求（插话） | 补充或纠正，agent 做完这一步就会看到 |
| 待发送区标题 | （没有） | 做完这一步后发给 agent |
| 待发送区按钮 | （没有） | 立即发送（悬停：停下正在做的这一步，马上发出去） |
| 对话里插话气泡的悬停（`chat.js:364`） | 运行中插话：排进了这一次模型请求 | 运行中补充的 |
| 没送到（`composer.js:107`） | alert「插话没有送到：…」 | `#hint`：没发出去：<原因> |
| app-server 409（`internal/appserver/server.go` 的 `turnSteer`） | 没有送到：这一轮已经结束（…），结束后照常发送即可 | 这一轮刚结束，没插进去 |

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/agent/interact.go` | `Steer(text, clientID string) bool`，队列里存 `{text, clientID}`；`takeSteers` 返回这个结构；`closeSteers` 照旧只返回文字（`turn/completed` 的 `queued` 格式不变，chat 在用） |
| `internal/agent/react.go` | `:338` 起的循环：两种 `userMessage`（正常的、被 hook 拦下的）的 `data` 在 `clientID` 不为空时加 `"client_id"` |
| `main.go` | `:375` 的 `turn/steer`：参数多读一个 `client_id`，传给 `agent.Steer` |
| `internal/appserver/server.go` | `turnSteer`：input 加 `ClientID string \`json:"client_id"\``，非空时一起传给 agent；409 的文字改成第 2.3 节那句 |
| `internal/web/page/index.html` | `#composer` 里、`#attach-strip` 前面加 `<div id="steer-strip" hidden></div>` |
| `internal/web/page/composer.js` | `steer`：生成 `client_id`，先放进待发送列表并画出来，再 POST；按第 2.1 节第 6 条处理失败。画待发送区（标题、每条一行最多 3 行、“立即发送”按钮）。停止按钮：有待发送时按第 2.1 节第 5 条，放回输入框 |
| `internal/web/page/runs.js` | `item/completed` 的 steer `userMessage` 带 `client_id` 时，从这个对话的待发送列表里去掉；`exit` 时按第 2.1 节第 3、4、5 条处理待发送列表 |
| `internal/web/page/util.js` | `ui` 里加待发送列表：按对话 ID 存（`ui.steers[runId]`），切换、重新订阅对话时不丢；“立即发送”的标记也记在这里 |
| `internal/web/page/shell.js`、`internal/web/page/chat.js` | 第 2.3 节的文案 |
| `internal/web/page/app.css` | `#steer-strip` 的样式：在输入框上方，字色淡，每条前面一个 `↳`，标题和按钮在一行 |
| `README.md` | 网页端那段如有讲插话的，补一句：运行中输入的话先显示在输入框上方，送到模型时进对话，没赶上这一轮的接着作为下一条消息发；“立即发送”会停下当前这一步 |

不改：
- agent 取走插话的时机（照旧在下一次请求模型之前）。
- `turn/completed` 的 `queued` 格式、chat 的插话（chat 本来就把 `queued` 接着发）。
- 协议版本 `Protocol`（只加可选字段）。
- `internal/web/server.go`。

## 4. 这一期不做

- 撤回、编辑已经发出的插话（Codex 的 Shift+← 编辑）：插话已经在 agent 的队列里，要撤回得另加接口。
- 在别的标签页、chat 里显示待发送区。
- 改 agent 取插话的时机（比如工具跑到一半就插进去）。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过 `composer.js`、`runs.js`、`util.js`、`shell.js`、`chat.js`。
2. `grep -n '排进 agent 的下一次请求' internal/web/page/*.js` 没有输出；`grep -n 'alert("插话' internal/web/page/composer.js` 没有输出。
3. agent 的 `client_id` 透传：读 `main.go` 的 `turn/steer` 分支和 `react.go` 的循环，报告 `client_id` 从参数到 `userMessage` 的 `data` 的路径（这一条是代码走读，真实链路在第 4 条验）。

### Claude Code 在沙箱外做（真实模型，网页端）

4. 发一个要跑一会儿的任务（例如让它在工作区里 `sleep 20` 再读几个文件）。运行中输入一句话回车：输入框清空，这句话**立刻**出现在输入框上方，标题是“做完这一步后发给 agent”；`sleep` 结束、下一次请求模型时，它从待发送区消失，出现在对话里；存档里那条 `userMessage` 带着同一个 `client_id`。
5. 赶不上：在模型写最后的回答时插一句（任务是一个不调工具的长回答）。这一轮结束后，它自动作为下一条消息发出，对话里多一轮，回答针对这句话。
6. 立即发送：在 `sleep 20` 时插一句，点“立即发送”。这一轮停下，接着作为下一条消息发出。
7. 停止：在 `sleep 20` 时插一句，点停止。这一轮停下，这句话回到输入框，没有自动发出。
8. 被 hook 拦下：平台设置的 hooks 里配一个 `UserPromptSubmit` hook，只拦 prompt 里带“拦我”两个字的（读标准输入的 JSON，带这两个字就按 hooks 的约定返回拦截），插一句“拦我 xxx”。待发送区里这条在下一次请求时消失，对话里显示“（这条插话被 UserPromptSubmit hook 拦下）”，存档里没有“xxx”。验完把 hooks 改回去。
