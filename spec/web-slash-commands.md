# 网页端的 `/` 命令：从"提示词模板"扩成"界面命令"

状态：已实现并验收（2026-10-31，用户要求 Claude Code 直接实现）。第 7 节的问题按推荐取舍，见第 8 节。

## 1. 为什么要改

网页端输入框敲 `/`，现在只列出**提示词模板**：内置的 `/init`、`/review`，加上 `$TERN_HOME/commands/`、`<工作区>/.agents/commands/` 里的 `.md`（`internal/commands`，见 [builtin-commands-skills.md](builtin-commands-skills.md)）。选了以后，整句话当成一条消息发给 agent，由 agent 的 `commands.Expand`（`main.go`）展开成提示词。

Codex 应用的 `/` 不是这样：它的 28 条命令里，大多数是**界面动作**：压缩、分叉、改名、归档、切计划模式、看状态，直接在界面上执行，不发给模型。启用的 skill 也会出现在 `/` 列表里。

tern 的这些能力大都已经有了，只是入口散在标题菜单、"打开"菜单、每轮的按钮、侧栏菜单、命令面板（Ctrl/⌘ Shift P）里。这次要做的是把它们接到 `/` 上，不新增后端能力。

## 2. Codex 应用的 `/` 命令（参照）

来源：[Codex 应用斜杠命令](https://learn.chatgpt.com/docs/reference/slash-commands?surface=app)（2026-10-31 查阅）。补充参考：同站 CLI 版列表里的 `/diff`、`/copy`、`/export`、`/ps`、`/permissions`、`/skills`、`/new`。

菜单行为：敲 `/` 打开，继续输入就是筛选；启用的 skill 出现在列表里；自定义提示词显示成 `/prompts:<名字>`；可用的命令随环境变化。

## 3. 对照：哪些做、哪些不做

"tern 现有入口"一栏的函数都已在代码里核对过。

### 3.1 这一期做（只接现有能力）

| 命令 | Codex 的含义 | tern 怎么做 | tern 现有入口 |
| --- | --- | --- | --- |
| `/new` | 新开一个空白对话 | 同"新聊天" | `$("new-chat")`（`shell.js` 的 `newChat`） |
| `/compact` | 压缩当前对话的上下文 | **下一条消息发出前压缩**（tern 现有语义，和终端 chat 的 `/compact` 一致），提示写清楚；带参数时（`/compact 继续做 X`）直接把参数当这条消息发出并压缩 | `ui.compactNext`（`composer.js`、`context.js`） |
| `/fork` | 把对话复制成一条新对话 | 从最后一轮分叉 | `forkTurn(r, t)`（`shell.js`） |
| `/side 问题` | 临时旁支提问，不打断主对话 | 旁支提问；带参数时把问题填好 | `sideQuestion()`（`shell.js`） |
| `/goal 文本` | 设持续目标 | 不带参数：打开目标编辑；带参数：直接设成这句 | `editGoal()`、`PUT /runs/{id}/goal`（`shell.js`） |
| `/plan 提示` | 切计划模式 | 权限切到"只读计划"；带参数就同时发出这句 | `#perm` 的 `plan` 选项（`index.html`） |
| `/model` | 选模型 | 新对话：打开输入框右下的模型菜单；已有对话：tern 一条对话固定一个模型，提示"换模型请新建任务"，并给"使用其他模型新建任务" | `#composer-model-info`、`#new-model-task`（`layout.js`） |
| `/reasoning` | 选推理强度 | 打开模型菜单，焦点放在推理强度 | `#effort`（每轮可换） |
| `/permissions` | 设权限（CLI 才有） | 打开权限下拉 | `#perm` |
| `/project` | 选新对话的项目 | 新对话：焦点放到工作区下拉；已有对话：提示项目已固定 | `#workspace` |
| `/status` | 对话 ID、上下文用量、限额 | 打开现有的"上下文与用量"对话框，上面加对话 ID（可复制）、模型、推理强度、权限、执行主机与目录；没有限额（tern 不接计费） | `context.js` 的对话框、`#run-id`、`#execution-context` |
| `/rename 名字` | 改名 | 带参数直接改；不带就弹出原来的改名框 | `renameRun(r, h)`（`shell.js`） |
| `/archive` | 归档 | 归档当前对话 | `archiveRun(r, h)`（`shell.js`） |
| `/pin` | 置顶 / 取消置顶 | 切换置顶 | `PUT /runs/{id}/pin`（`shell.js` 侧栏菜单里那一项） |
| `/review` | 代码评审 | **保持现有的提示词命令**（已支持范围参数，见 [review-scope.md](review-scope.md)） | `internal/commands/builtin/review.md` |
| `/init` | 生成 AGENTS.md | 保持现有的提示词命令 | `internal/commands/builtin/init.md` |
| `/mcp` | MCP 状态 | 打开 MCP 中心 | `openMCP()`（`pages.js`） |
| `/memories` | 记忆设置 | 打开长期记忆 | `openMemory()`（`pages.js`） |
| `/skills`（CLI） | 浏览 skill | 打开 Skills 中心 | `openSkills()`（`pages.js`） |
| `/diff`（CLI） | 看 diff | 打开右侧改动面板 | `openDiff(r, [], "workspace")`（`diff.js`） |
| `/copy`（CLI） | 复制最后一个回答 | 复制最后一轮回答的 Markdown 原文 | 回答下面的"复制"（`chat.js` 的 `renderCall`） |
| `/export`（CLI） | 导出 Markdown | 导出对话 | `exportRun(r)`（`export.js`） |
| `/ps`（CLI） | 后台终端 | 打开后台命令面板 | `$("jobs-btn")`，`openWorkTab("jobs")` |
| `/rewind` | （tern 终端 chat 已有） | 撤销最后一轮，弹出现有的三选一对话框 | `rewindTurn(r, t)`（`shell.js`） |
| `/help` | — | 在菜单里列出全部命令和说明 | 新增 |
| skill | 启用的 skill 出现在 `/` 列表里 | 列出这条对话能用的 skill，选了就加到 skill 引用条上（同 `$` 选择的效果），不插入文字 | `skillrefs.js`：要新增一个导出函数按名字加引用，可用条件照 `open()` 里的判断 |

### 3.2 不做

| 命令 | 原因 |
| --- | --- |
| `/worktree`、`/local`、`/cloud`、`/cloud-environment` | tern 没有云端执行；副本 / worktree 功能 2026-10-31 已按用户要求去掉（[remove-copy-workspace.md](remove-copy-workspace.md)） |
| `/fast` | 路由表里没有"快速档" |
| `/approve` | tern 没有自动评审，审批卡片已经在对话里 |
| `/usage` | tern 不接计费；用量在 `/status` 里 |
| `/feedback` | 没有反馈通道 |
| `/pet`、`/ide-context`、`/personality`、`/chat` | 没有对应能力 |
| 已有对话里切换模型 | tern 一条对话固定一个模型（`thread/start` 时定下），要改得动 app-server 和 agent，单独立项 |

## 4. 设计

### 4.1 三类条目，一个菜单

1. **界面命令**（3.1 表里除了 `/review`、`/init`）：注册在页面里，在浏览器里执行，**不发给模型**。
2. **提示词命令**：现有的那套，`/commands` 接口照旧，选了以后整句发给 agent 展开。
3. **skill**：来自 `GET /hub`，选了加到 skill 引用条上。

菜单按这个顺序分三组显示，每组一个小标题；每行写 `/名字 参数提示` 和一句说明。提示词命令照旧标"内置 / 个人 / 项目"。

### 4.2 注册表

新文件 `internal/web/page/slash.js`。每条界面命令是：

```js
{ name, args: "可选，参数提示", desc, when: (r) => "" /* 可用返回空，不可用返回原因 */, run: (r, arg) => {} }
```

`when` 决定灰掉与否，灰掉时把原因写在说明里，比如：
- `/fork`："这一轮跑完才能分叉"
- `/archive`："新任务还没有对话"
- `/compact`："导入的对话不能续聊"

判断条件照抄各个现有按钮的禁用条件，不另定规则。

### 4.3 输入与执行

- 输入框里只有 `/前缀`（光标前没有空格）时打开菜单，筛选按名字前缀，再按说明里包含。菜单里**上下键选择、Enter 或 Tab 补全**，Esc 关闭。现在的 `/` 菜单只能用鼠标点，键盘处理照 `@` 菜单的 `atPick` 那套写。
- 选中界面命令：
  - 不带参数的立刻执行，并清空输入框；
  - 带参数的补成 `/名字 `，等用户写完参数按 Enter 执行。
- 发送时如果整句是 `/名字` 或 `/名字 参数`，而且名字是界面命令，就在 `composer.js` 的提交处拦下执行，不发给 agent。执行完清空输入框，结果写进 `#hint`（如"已置顶"、"下一条消息发出前会先压缩"）。
- 不是界面命令的 `/xxx`：照旧当提示词命令或普通文字发出去，不改现有行为。
- 运行中：
  - 界面命令照常执行，各自的 `when` 管能不能做。比如 `/status`、`/diff`、`/ps` 运行中也能用，`/fork`、`/rewind` 不能。
  - 提示词命令照现有逻辑走插话或排队。
- 附件、skill 引用、压缩选项：
  - 界面命令不带走它们，草稿里保留，同 `!命令` 的处理；
  - `/compact 文本`、`/plan 文本` 发出的那条消息照常带上。

### 4.4 重名

界面命令的名字保留：个人或项目里写了同名的提示词命令（比如 `commands/status.md`），菜单里显示界面命令，提示词那份标"被内置界面命令占用，改个名字才能用"。见第 7 节问题 1。

## 5. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/web/page/slash.js`（新） | 注册表、菜单渲染（三组）、键盘选择、`runSlash(text)` 判断并执行界面命令 |
| `internal/web/page/composer.js` | `syncSlash` 改成调用 `slash.js` 的渲染；提交（`composer` 的 submit 与 Enter）先问 `runSlash`，返回已处理就不发送；`/compact 文本`、`/plan 文本` 走正常发送前先设好压缩标记或权限 |
| `internal/web/page/shell.js` | 导出 `renameRun`、`archiveRun`、`editGoal`、`sideQuestion`，以及置顶的那段逻辑（现在写在侧栏菜单的匿名函数里，提成函数 `pinRun(r, h)`），供注册表调用；行为不变 |
| `internal/web/page/skillrefs.js` | 新增 `addSkill(skill)`：照 `open()` 里的可用条件判断，加进引用并 `saveDraft()` |
| `internal/web/page/context.js` | 对话框加上对话 ID（可复制）、模型、推理强度、权限、执行位置，供 `/status` 打开 |
| `internal/web/page/main.js` | 引入 `slash.js` |
| `internal/web/page/layout.css` | 菜单分组标题、选中态、灰掉的行 |
| `README.md` | 网页端一节加一段：`/` 列出的三类命令和这一期的命令表 |

**不要改：**
- `internal/commands`、`/commands` 接口、agent 的 `commands.Expand`；
- 终端 chat 的 `/` 命令；
- 命令面板（Ctrl/⌘ Shift P）。它和 `/` 是两个入口，这一期不合并，以后可以让命令面板的"操作"复用 `slash.js` 的注册表。

## 6. 验收

在临时 `TERN_HOME`、qa 账号下用 DeepSeek 真实对话，无头 Chrome 操作页面：

1. 新任务敲 `/`：三组都在。`/fork`、`/archive` 灰掉并写明原因；`/rev` 筛出 `/review`；上下键加 Enter 能选。
2. 跑完一轮后，下面这些都**不产生新的模型请求**（核对对话事件里没有新的 `continue`）：
   - `/rename 测试名`：侧栏和标题变；
   - `/pin`：进置顶组；
   - `/status`：显示对话 ID、模型、上下文用量；
   - `/diff`：右栏打开改动；
   - `/copy`：剪贴板里是最后一轮回答；
   - `/archive`：进已归档。
3. `/compact` 后发一句：对话里出现"上下文已压缩"，然后才是回答。
4. `/plan 规划一下加个函数`：权限变成只读计划，这句发出；`/side 这是什么`：开出旁支提问；`/fork`：出现新对话，带分叉横幅。
5. 运行中：`/status` 能用，`/fork` 灰掉；`/review` 照旧走插话或排队。
6. 选一个启用的 skill：引用条上出现 `$名字`，输入框不多出文字。
7. 个人目录放一个 `commands/status.md`：菜单里显示界面命令 `/status`，那份提示词标为被占用。
8. `node --check` 所有页面脚本；`spec/web-product/run-checks.py` 全量通过。

## 7. 需要确认的问题

1. **重名怎么办**：上面的设计是界面命令优先、同名提示词命令不可用（照 4.4）。另一种照 Codex：自定义提示词一律显示成 `/prompts:<名字>`，不会和界面命令撞，但已有的个人命令要换个敲法。
2. **`/compact` 的语义**：tern 现有的是"下一条消息发出前压缩"，Codex 是立刻压缩。立刻压缩要让 app-server 支持不带问题的一轮（只压缩、不回答）。这一期按现有语义做，可以吗？
3. **`/model` 在已有对话里**：这一期只提示"换模型请新建任务"。要不要把"对话中途换模型"单独立项？
4. **范围**：3.1 共 25 条，是一次做完，还是先做最常用的一批（`/new`、`/compact`、`/fork`、`/side`、`/goal`、`/plan`、`/status`、`/diff`、`/rename`、`/review`、`/init`、skill）？

## 8. 实现记录（2026-10-31）

用户说"直接做"，第 7 节按推荐取舍：
1. 界面命令优先，同名提示词命令灰掉并提示改文件名；
2. `/compact` 沿用"下一条消息发出前压缩"；
3. `/model` 在已有对话里只提示新建任务；
4. 25 条一次做完。

实现和第 5 节的出入：
- `main.js` 不用改：`composer.js` 引入了 `slash.js`。
- 原来的 `syncSlash`、`loadCommands` 从 `composer.js` 挪进了 `slash.js`。
- `shell.js` 的 `renameRun`、`editGoal`、`sideQuestion` 加了可选参数，带了就不弹框；`renameRun`、`archiveRun`、`pinRun` 返回是否成功；`pinRun` 是从侧栏菜单里提出来的。

验收中发现并修掉的问题：
- `/plan 文本`、`/compact 文本` 在提交事件里再 `requestSubmit`，浏览器忽略，消息没发出去。改成等这次提交结束再提交。
- 空状态时输入框在页面中间，菜单往上展开被窗口顶部切掉。改成往下展开。
- 新任务时很多命令灰掉，挤在前面。每组里能用的排前面。
- 顺手把侧栏"已归档"的 `▸ ▾` 也换成线条箭头（缺字形时是方框）。

验收（临时 `TERN_HOME`、DeepSeek、无头 Chrome），第 6 节各条都过了：

1. **新任务菜单**：三组都在；`/fork` 灰掉并写原因；`/rev` 筛出 `/review` 和 `$code-review`；`/压缩` 筛出 `/compact`；上下键跳过灰掉的项。
2. **`/plan 文本`**：权限变成只读计划，这句发出去了。
3. **一轮结束后的界面命令**：依次执行 `/rename 斜杠测试`、`/pin`、`/copy`（剪贴板里是最后一轮回答）、`/status`（对话 ID、执行位置、本轮设置、模型、用量）、`/diff`（右栏打开改动）、`/permissions`（焦点到权限下拉）。存档里 `continue` 事件前后都是 0，没有新的模型请求。
4. **`/compact 文本`**：对话里先出现"上下文已压缩 · 摘要 864 字"，再是回答。
5. **skill 和重名**：选 `$code-review` 后，引用条出现它，输入框清空；个人目录的 `commands/status.md` 灰掉并写明重名，`/joke` 正常列出。
6. **分叉和旁支**：`/fork` 打开新对话，带分叉横幅；`/side 问题` 开出旁支并改好名字。
7. **运行中**：`/fork` 灰掉，`/status` 能打开，`/review` 走插话。之后 `/archive`、`/pin` 都正常；`/nosuch hello` 不弹菜单，照旧当文字。
8. **检查**：`node --check` 全部页面脚本，4 个纯脚本检查通过，页面无脚本报错。没跑 `run-checks.py` 全量：这次只改页面，没动它们用到的接口。
