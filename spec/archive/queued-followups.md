# 排队的后续消息：这一轮结束后再发，和插话分开

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。

2026-10-25 增强实现见 [队列事务与恢复](../web-product/13-message-queue.md) 和 [实现记录](../web-product/08-implementation.md)。下文保留初始版本基线；当前 Tab 为焦点导航，队列支持编辑/排序/版本冲突、服务端领取和持久暂停，网页不再先续聊再删除。

## 1. 为什么要改

运行中输入的话现在一律是插话（steer-like-codex.md）：在下一次请求模型之前塞进这一轮。有时想说的是“这一轮做完以后，接着做下一件事”，不想打断它现在的思路，现在没有办法表达，只能守着等它跑完再发。插话的待发送区也只在发出它的那个页面里。

Codex 的输入框里 Enter 是插话，Tab 是排到这一轮结束后再发（“Queued follow-up inputs”，`codex-rs/tui/src/bottom_pane/chat_composer.rs:835`）。队列在服务端（`thread/queue/add`、`update`、`delete`、`reorder`），换个客户端也看得到。

## 2. 改什么

### 2.1 app-server：每条对话一个队列

- 存在 `.meta.json`（conversation-manage.md 加的那个文件）里：`"queue": [{"id": "…", "text": "…", "at": "…"}]`，按加入的顺序。`run` 上同样留一份。
- 方法：
  - `thread/queue/add {threadId, text}`：文字去掉首尾空白后不能为空；这条对话没在跑时回 409“这条对话没在跑，直接发送就行”。成功返回这一项（`id` 由 app-server 生成）。
  - `thread/queue/delete {threadId, id}`：删一项，没有这一项回 404。
- `summary()` 加 `queue`（有才带）。队列一变就广播 `thread/status/changed`，各页面据此刷新。
- **接着发**：这一轮结束（agent 退出，`run.add` 收到 `exit`）时：
  - 队列不为空、这一轮不是人点停止结束的（`r.stopping` 为 false）、这条对话能续聊：取出第一项，像 `turn/start` 一样开始下一轮（问题就是这一项的文字；权限模式、推理强度沿用上一轮）。把 `turnStart` 里真正开始一轮的那段抽成函数，两处共用。
  - 剩下的项留在队列里，下一轮结束时再取下一项，直到取完。
  - 人点停止结束的：队列不动，不自动发。
  - 开始下一轮失败（比如模型配置坏了）：这一项放回队首，写一行日志，不重试。
- 评测的试次、导入的对话不接受排队（409）。

### 2.2 网页端

- 运行中，输入框里有字时：
  - **Enter** 照旧是插话；
  - **Tab** 是排队（斜杠菜单、`@` 菜单开着时 Tab 归菜单）；
  - 另外在停止按钮旁边加一个“排队”小按钮，悬停写“这一轮结束后再发，不打断现在这一步（Tab）”。
  - 排队成功就清空输入框；失败时文字留在输入框，`#hint` 显示原因。
- 待发送区（`#steer-strip`）下面多一节“这一轮结束后发（N）”，列出 `summary.queue` 里的每一项（每项一行，最多 3 行），每项右边一个“×”删掉它。这一节所有打开这条对话的页面都一样。
- 这条对话停下、队列还有东西时（人点了停止）：这一节标题换成“已停止，排着的 N 条没有发”，旁边一个“发第一条”按钮（按普通续聊发出去，并从队列里删掉）。
- 运行中输入框的占位文字后面补“（Tab 排到这一轮之后）”。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/threads.go` | `runMeta` 加 `Queue`；`thread/queue/add`、`thread/queue/delete` |
| `internal/appserver/server.go` | `run` 上的队列；`summary()` 加 `queue`；`exit` 时取队首开始下一轮（从 `turnStart` 抽出开始一轮的函数） |
| `internal/appserver/handle.go` | 方法表加两项 |
| `internal/web/server.go` | `POST /runs/{id}/queue {text}` → `thread/queue/add`；`DELETE /runs/{id}/queue/{qid}` → `thread/queue/delete` |
| `internal/web/page/composer.js` | Tab 与“排队”按钮；待发送区里“这一轮结束后发”一节；停下以后的“发第一条” |
| `internal/web/page/shell.js` | 占位文字；把当前对话的 `queue` 交给待发送区重画 |
| `internal/web/page/index.html` | “排队”按钮 |
| `internal/web/page/app.css` | 队列一节的样式 |
| `internal/appserver/AGENTS.md` | 补一条：排队的消息存在 `.meta.json`，一轮正常结束后由 app-server 接着发，人点停止的不发 |
| `README.md` | 网页端讲插话的那句后面补：Tab（或“排队”）是排到这一轮结束之后再发，队列所有页面都看得到 |

不改：插话（`turn/steer`）、没赶上的插话由发出页面接着发的做法、chat。

## 4. 这一期不做

- 改排队的文字、调顺序（Codex 的 `update`、`reorder`）。
- chat 里排队。
- 把没赶上的插话也转进服务端队列。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 读代码报告：`exit` 时取队首的条件（人点停止的不取）；开始下一轮失败时的处理。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 让对话跑 `sleep 20`；运行中输入“再说一句：完”按 Tab：输入框清空，待发送区出现“这一轮结束后发（1）”；这一轮结束后自动开始下一轮，问题是这句话；队列空了。
4. 另开一个页面看同一条对话：排队后两个页面的“这一轮结束后发”一样。
5. 排两条，删掉第一条：结束后只发第二条。
6. 排一条以后点停止：这一轮停下，不自动发，显示“已停止，排着的 1 条没有发”；点“发第一条”发出去。
7. 对话没在跑时调 `POST /runs/<id>/queue`：回 409。
