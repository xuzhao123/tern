# 网页端选推理强度

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。实现时 app-server 重启把这一轮打断过一次，tern 续做时核对过已经完整。

## 1. 为什么要改

推理强度只能在命令行用 `-reasoning-effort` 定（`main.go:256`，取值 `minimal`、`low`、`medium`、`high`，默认 `high`）。网页端和 chat 发起的对话一律是默认值。续聊时，app-server 沿用上一轮检查点里的值（`internal/appserver/server.go:421`，`history.Effort`）。简单问题也按 `high` 跑，又慢又贵；难题想拉高也没有入口。

Codex 的 `/model` 里模型和推理强度一起选，之后各轮生效。

## 2. 改什么

- **设置**：`builtin`（`internal/appserver/settings.go`）加 `reasoning_effort`，默认 `high`，取值同上，按平台 › 个人合并（和 `permission_mode` 一样）。设置页“默认权限模式”下面加一行“默认推理强度”。
- **每轮可以换**：和权限模式一样（`turnStart` 的 `Permission` 注释）。
  - `thread/start` 和 `turn/start` 的 input 都加 `effort`（空就用设置里的默认值；续聊时空就沿用这条对话上一轮的）。
  - 取值不对回 400：“effort 取 minimal、low、medium 或 high”。
- **记进存档**：start、continue 事件（`event` 结构）加 `Effort string json:"reasoning_effort,omitempty"`。`snapshotArgs` 按它加 `-reasoning-effort`，代替 `:421` 那段从 `history.Effort` 取的写法；旧存档没有这个字段时，照旧用 `history.Effort`。
- **网页端**：
  - 输入框那一行，在权限模式下拉（`#perm`）旁边加 `#effort` 下拉：“推理：高 / 中 / 低 / 最低”（对应 high、medium、low、minimal），悬停写“推理强度：越高越仔细，也越慢、越贵；每轮可以换”。
  - 新对话默认取设置里的值；进行中的对话默认取它上一轮的值（从存档里最后一个 start/continue 事件的 `reasoning_effort` 读）；人改过就保持人的选择，直到发送（和 `ui.picked.permission` 同样的写法）。
  - 发新对话、续聊时把它放进请求体的 `effort`。
  - 导入的对话不显示这个下拉（和 `#perm` 一样）。
- **chat**：`-effort` 参数不加（命令行界面暂缓）；chat 发起的对话用设置里的默认值。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/settings.go` | `builtin` 加 `reasoning_effort`；`valid` 校验取值；设置页的数据里带上 |
| `internal/appserver/server.go` | `event` 加 `Effort`；`threadStart`、`turnStart` 的 input 加 `effort` 并校验；写进 start/continue 事件；`snapshotArgs` 加 `-reasoning-effort`；`:421` 只在事件里没有时才用 `history.Effort` |
| `internal/web/page/index.html` | `#perm` 旁边加 `<select id="effort">` |
| `internal/web/page/shell.js` | `syncComposer` 里决定 `#effort` 的值与显示（照 `#perm` 的写法）；`TOOL_INFO` 不动 |
| `internal/web/page/composer.js` | 发送时带上 `effort`；`#effort` 的 `onchange` 记 `ui.picked.effort` |
| `internal/web/page/runs.js` | 从 start/continue 事件读 `reasoning_effort`，记在 run 上（`r.effort`） |
| `internal/web/page/pages.js` | 设置页加“默认推理强度” |
| `README.md` | 网页端那段补一句：输入框旁边能选推理强度，每轮可以换，默认在设置里 |

不改：agent 的 `-reasoning-effort` 参数、各家协议里推理强度的写法（`internal/llm`）、记忆召回用的 `MemoryEffort`。

## 4. 这一期不做

- 按模型隐藏不支持推理强度的选项（不支持的供应商会忽略这个字段）。
- chat 的 `/model`。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 读代码报告：新对话、续聊两种情况下 `-reasoning-effort` 从哪来；旧存档没有字段时的退路。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 页面上选“低”发一个新对话：这一轮的请求体里 `reasoning_effort` 是 `low`，存档 start 事件里 `reasoning_effort` 是 `low`。
4. 同一条对话续聊时选“高”：这一轮的请求是 `high`；再续聊不动下拉，沿用 `high`。
5. 设置页把默认改成“中”：新对话的下拉默认是“中”。
6. 请求体里 `effort` 填一个不认识的值：回 400。
