# 项目级配置：工作区里的 .agents/tern.json

状态：设计（2026-10-24），交给 tern 实现（codex-parity-plan.md 5.2）。

## 1. 为什么要改

设置只有内置、平台、个人三层（`internal/appserver/settings.go` 的 `effectiveFor`）。某个仓库想默认用某个模型、关掉某些工具、加几条权限规则或者联网白名单，现在只能改全局设置，影响所有工作区。Codex 读仓库里的 `.codex/config.toml`，信任这个项目以后生效。项目级只有斜杠命令（`.agents/commands/`）。

## 2. 改什么

### 2.1 文件与能放的键

- 位置：工作区根目录的 `.agents/tern.json`，一个扁平的 JSON 对象，键和设置页的键一样，值都是字符串，例如：
  ```json
  {"provider": "deepseek", "reasoning_effort": "medium", "web_search": "off",
   "permission_rules": "deny Bash(git push *)", "net_allow": "proxy.golang.org", "max_steps": "200"}
  ```
- 只认这些键：`provider`、`model`、`reasoning_effort`、`permission_mode`、`permission_rules`、`net_allow`、`max_steps`、`context_window`、`tool_output`，以及工具开关（`toolOrder` 里的键）。
- 其余的键（`hooks`、`workspace`、`embedding`、`memory_ttl`）忽略，在设置页的“来源”里列出“项目配置里这些键不生效：…”。`hooks` 会在宿主机上跑命令，只能在平台设置里配（`internal/appserver/AGENTS.md`）。
- 值照 `valid` 校验，不合规的那一项忽略，同样列出来；文件不是合法 JSON 就整份忽略并说明。

### 2.2 合并顺序

- 内置 › 平台 › **项目** › 个人，后面的覆盖前面的。个人层是这个账号在设置页里明确选的，排在项目后面。
  - 个人层照旧只存和平台不一样的值（`settings/write` 存的是相对内置+平台的差），所以个人选的值等于平台默认时存不下来，盖不过项目。这一期不改：想在这个工作区里用别的，就在新对话的下拉里明确选（见 2.5），下拉明确选的优先于项目配置。
- `permission_rules`、`net_allow` 这两项是**追加**，不是覆盖：项目的规则加在平台规则后面，白名单取并集。
- 只在对话选了这个工作区时生效：新对话开始时（`thread/start` 算快照时）读一次，拍进快照；续聊照快照，不重读。

### 2.3 信任

- 已登记的工作区就当作信任（登记本来就是人自己做的）。
- 新对话第一次用到一份项目配置时，在这条对话的第一轮之前，界面上显示一条横幅：“这个工作区的 .agents/tern.json 改了这些设置：provider=deepseek、web_search=off…”，不拦，只告知。
- 存档的 start 事件记下 `project_config`（生效的键值），页面据此显示横幅。
  - 只记**真正进了快照**的键：被个人层盖掉的（`Source` 不是 `project`），以及被 `thread/start` 明确给的参数盖掉的（`provider`、`model`、`effort`、`permission` 给了而且和项目的值不一样），都不记。否则横幅写着 provider=deepseek，这条对话实际用的却是别的。

### 2.4 设置页

- 设置页的“来源”一栏（`effective.Source`）多一种 `project`。只在页面上选了某个工作区（新对话的工作区下拉）时，按那个工作区显示。
- 设置页的工作区列表里，有 `.agents/tern.json` 的工作区后面标“有项目配置”，悬停列出生效的键。

### 2.5 网页端新对话的下拉（验收后补）

验收时发现：新对话的供应商、推理强度、权限模式三个下拉，默认值取自不带工作区的设置（`shell.js` 的 `syncComposer` 里的 `s`，即 `ui.settings`），发送时又总是明确带上（`composer.js` 的 `start`）。所以在页面上选了有项目配置的工作区，下拉仍显示平台默认（比如 ark），也就把 ark 发了出去，项目里的 `provider` 永远不生效。改法：

- 新对话（`ui.run` 为空）的工作区下拉选中某个工作区时（换选、以及页面加载时默认选中的那个），按它取一次 `GET /settings?workspace=<名字>`，结果按工作区名字缓存在 `ui.wsSettings`。没选工作区时用 `ui.settings`。
- `syncComposer` 给新对话填默认值时，`s` 用 `ui.wsSettings[所选工作区] || ui.settings`。人明确改过的下拉（`ui.picked.provider`、`effort`、`permission`）照旧不动；`ui.picked.model` 的规则不变。
- 换工作区时，没被人改过的下拉跟着变成那个工作区的生效值。下拉旁边不加说明（横幅在第一轮之前会说）。
- 设置改了、或者 `.agents/tern.json` 改了以后：打开设置页、重新选一次工作区时重新取（不做文件监听）。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/settings.go` | 读 `.agents/tern.json`；`effectiveFor` 加项目层（只在给了工作区时）；允许的键、追加的两项；`Source` 的 `project` |
| `internal/appserver/server.go` | `threadStart` 用带项目层的设置拍快照；start 事件记 `project_config` |
| `internal/web/page/pages.js` | 设置页的来源与“有项目配置”标记 |
| `internal/web/page/chat.js` | 第一轮之前的横幅 |
| `internal/web/page/shell.js`、`composer.js` | 2.5：按所选工作区取设置，新对话的下拉默认值跟着工作区 |
| `internal/appserver/AGENTS.md` | 设置那一条补上项目层和不收的键 |
| `README.md` | 设置那一节补：工作区里的 `.agents/tern.json`，能放哪些键，合并顺序 |

不改：设置文件 `.data/settings.json` 的格式、chat、命令行 agent（只有经 app-server 开的对话读项目配置）。

## 4. 这一期不做

- 项目配置里放 MCP server、skills。
- 子目录里的配置。
- 网页上编辑项目配置（就是仓库里的一个文件，用编辑器改）。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 读代码报告：合并顺序、追加的两项、不收的键在哪里过滤。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 工作区里放 `{"provider": "deepseek", "web_search": "off", "hooks": "x", "max_steps": "abc"}`：新对话的快照里 provider 是 deepseek；第一轮之前有横幅；设置页列出“hooks、max_steps 不生效”。
4. 个人设置里 provider 是 deepseekanthropic（和平台默认不一样，存得下来）：新对话用 deepseekanthropic（个人覆盖项目），`project_config` 里没有 provider。
7. 页面上新对话选这个工作区：供应商下拉自动变成 deepseek，直接发出去的对话快照里 provider 是 deepseek；先在下拉里改成别的再发：快照用改的那个，横幅里没有 provider。换回不选工作区：下拉回到平台默认。
5. 项目 `net_allow` 是 `example.com`，平台是 `golang.org`：快照里两个都有。
6. 不选这个工作区的新对话：不受影响。
