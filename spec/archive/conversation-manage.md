# 对话改名、归档、删除

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。

## 1. 为什么要改

网页端左侧的对话列表只增不减：没有改名、归档、删除（`internal/web/server.go:150` 起的路由里都没有），标题就是第一句话（`internal/web/page/shell.js` 的 `runRow` 显示 `r.query`）。用久了列表全是“hello”“执行一下pwd”这种，找不到要的那条。

Codex 的 app-server 有 `thread/name/set`、`thread/archive`、`thread/unarchive`、`thread/delete`（`codex-rs/app-server-protocol/src`），TUI 里是 `/rename`、`/archive`、`/delete`。

## 2. 改什么

### 2.1 名字与归档：放在单独的元数据文件里

- 每条对话可以有一个 `.data/runs/<对话ID>.meta.json`：`{"name": "…", "archived": true}`，两项都可以没有。
- 先写 `.tmp` 再 rename，权限和存档一样。
- 不写进存档 `<对话ID>.jsonl`，原因有两个：对话结束后存档文件已经关了（`run.add` 在 `exit` 时 `r.file.Close()`）；列表按最后一个事件的时间排序（`run.updated`），改个名不该把对话顶到最上面。
- `loadRuns` 读存档时一并读它；`run` 加 `Name string`、`Archived bool`；`summary()` 加 `name`、`archived` 两个字段（空就不带）。
- 分叉出来的对话不继承原对话的名字和归档状态。

### 2.2 协议方法

| 方法 | 参数 | 做什么 |
| --- | --- | --- |
| `thread/name/set` | `{threadId, name}` | 名字去掉首尾空白，最长 100 个字；空串表示去掉名字，回到显示第一句话 |
| `thread/archive` | `{threadId}` | 归档；正在运行的也可以归档，跑完照常 |
| `thread/unarchive` | `{threadId}` | 取消归档 |
| `thread/delete` | `{threadId}` | 永久删除，见 2.3 |

- 四个方法成功后都广播 `thread/status/changed`（`status` 照这条对话现在的状态），各界面据此刷新列表；删除另外广播 `thread/deleted {threadId}`。
- 都写审计：`Actor: "user"`，`Event` 分别是 `user_thread_rename`、`user_thread_archive`、`user_thread_unarchive`、`user_thread_delete`，`Conversation` 是对话 ID，改名把新名字放进 `Target`。审计写不进去就不做（和审批一样，`internal/appserver/AGENTS.md`）。
- 评测的试次（`r.Eval` 不为空）四个方法都回 409：“评测的试次在评测页里管理”。

### 2.3 删除删哪些

- 正在运行的（`r.conn` 不为空，或者 agent 进程还在）回 409：“这条对话还在运行，先停止再删除”。
- 删掉：
  - 存档 `.data/runs/<对话ID>.jsonl` 和 `.meta.json`；
  - 这条对话各轮的文件（轮次 ID 用现成的 `run.turnIDs()`）：`.data/checkpoints/<轮次ID>.json`、`.lock`、`.files/`（撤销用的快照，`internal/agent/rewind.go:33`），`.data/browser/<轮次ID>/`；
  - 按对话第一轮 ID 存的：`.data/sandbox/<第一轮ID>/`、`.data/outputs/<第一轮ID>/`、`.data/sessions/<第一轮ID>.jsonl`（沙箱和 outputs 见 `main.go:866`–`893`，sessions 见 `internal/agent/journal.go:40`）。
- 分叉出来的对话复制了原对话前几轮的事件，共用这些轮次 ID 和沙箱目录（`internal/appserver/AGENTS.md` 讲分叉的那条）。所以每个 ID 先看还有没有别的对话（`s.runs` 里除它以外的）的 `turnIDs()` 里有它，有就不删那个 ID 的文件。
- 不删：`.data/audit/`（只追加的哈希链）、`.data/traces/`、`.data/attachments/`（按内容寻址，别的对话可能也在用）、`.data/progress/`（按工作区存，几条对话共用）。
- 从 `s.runs` 里拿掉；之后对这个 ID 的请求照“没有这条对话”处理。
- 删除不能撤销，也不进回收站。

### 2.4 网页端

- 路由：
  - `PUT /runs/{id}/name {name}` → `thread/name/set`
  - `POST /runs/{id}/archive` → `thread/archive`
  - `DELETE /runs/{id}/archive` → `thread/unarchive`
  - `DELETE /runs/{id}` → `thread/delete`
- 对话列表（`shell.js` 的 `runRow`）：
  - 有名字时显示名字，悬停时还是第一句话和对话 ID。
  - 每行右边一个 “⋯” 按钮（悬停那一行才显示），点开是菜单：改名、归档（已归档的是“取消归档”）、删除。
  - 改名用 `prompt()` 弹框，默认值是现在的名字或第一句话。
  - 删除用 `confirm()`：“永久删除这条对话？存档、检查点和沙箱里的文件都会删掉，不能撤销。”
- 已归档的默认不在列表里；列表最下面一行“已归档（N）”，点开或收起，展开后已归档的显示在下面，样式淡一点。
- 正在看的对话被删了（不管是在这个页面删的，还是收到 `thread/deleted`），回到新对话页。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/server.go` | `run` 加 `Name`、`Archived`；`loadRuns` 读 `.meta.json`；`summary()` 加两个字段；四个方法的实现（也可以放进新文件 `internal/appserver/threads.go`）；删除按 2.3 |
| `internal/appserver/handle.go` | 方法表加四个方法 |
| `internal/web/server.go` | 四条路由 |
| `internal/web/page/shell.js` | `runRow` 显示名字、“⋯”菜单；`groupRows` 分出已归档；收到删除时离开当前对话 |
| `internal/web/page/runs.js` 或收通知的地方 | 处理 `thread/deleted`（如果网页端的列表刷新走的是 `/connection` 的 `runs` 计数，就让删除也让计数加一，不另开一路） |
| `internal/web/page/app.css` | “⋯”按钮和菜单、已归档一组的样式 |
| `internal/appserver/AGENTS.md` | 加一条：名字和归档放 `.meta.json`，不进存档；删除时被分叉对话引用的轮次文件不删 |
| `README.md` | 网页端那段补一句：对话可以改名、归档、删除；删除会删掉存档、检查点和沙箱里的文件 |

不改：存档格式（`.jsonl` 里不加新的事件种类）、协议版本 `Protocol`（只加方法）、chat。

## 4. 这一期不做

- 搜索对话（计划 4.2）、分组文件夹。
- 批量操作。
- chat 里的 `/rename`、`/archive`、`/delete`（命令行界面暂缓）。
- 删除进回收站。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 协议层：编一份程序，`HOME` 指到 `/scratch` 下的临时目录，起 app-server。用 `thread/import` 导入一份很小的 Codex rollout（或者自己写一个最小的存档 `.jsonl` 放进 `.data/runs/`，再重启 app-server），当作一条已结束的对话。同一条命令里依次：
   - `thread/name/set` 改名 → `thread/list` 里这条的 `name` 对了；重启 app-server 后还在；
   - `thread/archive` → `archived` 为真；`thread/unarchive` → 没有了；
   - `thread/delete` → `thread/list` 里没有了，`.data/runs/` 下两个文件都删了；
   - 审计里有这四条记录。
3. 读代码报告：删除时“被别的对话引用的轮次不删”是怎么判断的。

### Claude Code 在沙箱外做（真实模型、无头 Chrome 驱动真实页面，临时 `HOME`）

4. 跑一条用 bash 的对话，再从第 1 轮之后分叉一条。删掉原对话：原对话的存档、只属于它的检查点删了；分叉那条还能打开、还能续聊，共用的沙箱目录还在。
5. 页面：改名后列表显示新名字，刷新页面还在；归档后从列表消失，“已归档（1）”展开能看到，取消归档回来；删除弹确认，确认后从列表消失；正在看的那条被删，回到新对话页。
6. 正在运行的对话点删除，回 409，页面显示“这条对话还在运行，先停止再删除”。
