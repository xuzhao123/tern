# 沙箱与审批：对外三档，内部两个旋钮

状态：已实现（2026-10-26），编译与真实模型验证待做。

## 1. 为什么要改

现在 tern 有两个独立的权限概念，但它们的关系没有在界面上说清楚：

- **审批**：`permission_mode` 四档（`plan`、`ask`、`edit`、`full`），决定工具调用要不要问人。
- **边界**：`-bash` 决定有没有 bash；有 bash 就一直在沙箱里跑，没有「在宿主机上跑」这条路径。

结果是 `full` 这个名字容易让人以为「和 Codex 的完全访问一样」，实际不是：`full` 只是不再询问，命令仍在 bwrap/Seatbelt 里（`internal/sandbox/sandbox.go:157`），写不了 `$HOME`、看不到真实路径、联网仍经代理。用户开了全权之后被 `npm install`、`~/.npmrc` 这类问题卡住，得翻 `launch_linux.go` 才知道为什么。

Codex 的做法是：对外三档（请求批准 / 帮我批准 / 完全访问），内部仍是 sandbox 与 approval 两个旋钮；「完全访问」= `danger-full-access` + `never`，命令真的不在沙箱里跑。它的界面只给三个预设，像「工作区可写 + 从不询问」这种组合留在命令行。

目标：tern 采用同样的结构——**对外一个下拉三档，内部两个旋钮**，并补上真正「无沙箱」的执行路径。

## 2. 对外三档

网页端输入框旁的下拉（`id="perm"`）和设置页的「默认权限模式」都换成这三项：

| 显示 | 内部 permission | 内部 host | 行为 | 对应现在的 |
| --- | --- | --- | --- | --- |
| 请求批准 | `ask` | false | 改文件、跑命令、调 MCP 前确认；只读命令不问；联网逐个问 | `ask` |
| 帮我批准 | `edit` | false | 工作区内编辑与命令直接做；出界与联网才问 | `edit`（默认） |
| **完全访问** | `full` | **true** | 不问；命令在这台机器上直接跑，可读写任何文件、直连网络 | 新 |

- 三档都要求 `-bash`（没有 bash 就没有「命令」这一层，下拉照常显示，但不影响文件工具）。
- 「帮我批准」就是现在的 `edit`：它不做风险分类，只是「工作区内不问、越界和联网才问」。文案不承诺「检测风险」；模型审查者以后另开设计（见第 8 节）。
- 「完全访问」要二次确认，确认框写明当前主机（远程时是别名）：

  > 完全访问：可不受限制地访问互联网和这台电脑上的任何文件。只在信任这条对话时使用。

## 3. 底层的两个旋钮与高级项

内部保持两个独立的值，界面三档只是它们的预设：

| 旋钮 | 取值 | 默认 | 说明 |
| --- | --- | --- | --- |
| `permission` | `plan` / `ask` / `edit` / `full` | `edit` | 语义不变 |
| `sandbox` | `sandbox` / `host` | `sandbox` | `host` 只在有 `-bash` 时有效 |

命令行：

```bash
-bash                                   # 打开 bash（沙箱）
-bash-host                              # 命令在宿主机跑；须配合 -bash
-permission-mode=plan|ask|edit|full     # 照旧
```

网页端的高级项（设置页「权限」下的一组，不占主下拉）：

| 高级项 | permission | sandbox | 说明 |
| --- | --- | --- | --- |
| 只读计划 | `plan` | sandbox | 不能改文件；bash 里工作区只读 |
| 每步都问 | `ask` | sandbox | 同「请求批准」，主下拉已有，不重复列 |
| 无人值守（沙箱内） | `full` | sandbox | 不询问；命令仍在沙箱里，越界直接失败 |
| 命令在宿主机跑 | 任意 | `host` | 给高级用户单独组合；「完全访问」就是 `full` + `host` |

老设置兼容：

- 只有 `permission_mode`、没有 `sandbox_mode` 时按 `sandbox` 处理。老的 `full` 仍然是「无人值守（沙箱内）」，**不会自动变成完全访问**。
- 界面按 `permission` + `sandbox` 反推显示：`ask`→请求批准，`edit`→帮我批准，`full`+`host`→完全访问，其余显示为高级项。
- 项目配置 `.agents/tern.json` **不认 `sandbox_mode`**：仓库不能要求「打开就完全访问」。写了也只在设置页的「未生效」里提示。

## 4. 完全访问：宿主机执行路径

在 `internal/sandbox` 新增 `host.go`，由 `launch()`（`sandbox.go:386`）分流：

- 命令：`/bin/bash -c <command>`；`tty=true` 时按平台用 `script` 包一层，参数与沙箱里一致（Linux `script -q -f -e -c <command> /dev/null`，macOS `script -q /dev/null <command>`）。
- 工作目录：有工作区时是工作区真实路径（`sandbox.Project`），否则是 `sandbox.Dir`；不再有 `/workspace`、`/scratch` 两个视图。
- 环境变量：继承宿主机环境，但去掉 tern 自己的运行时凭据：`AGENT_PROXY_TOKEN`、`AGENT_PROXY_URL`、`AGENT_PROXY_SOCKET`、`AGENT_APP_SOCKET`。其余（包括 `HOME`、`PATH`、用户自己的密钥）照常继承——这是「完全访问」的含义。
- 网络：直连，不启动代理，不设置 `HTTP_PROXY` 一类变量。
- 资源：不加 cgroup；超时与输出截断复用 `execute()`（`sandbox.go:257`），终止用进程组。
- 后台命令：复用 `jobs.go`，`startJob()` 本来就经 `launch()`，Host 下自动走宿主机；`bash_output`、`bash_input`、`bash_kill` 不变。
- `request_network_access`：Host 下不提供（`Definitions()` 里过滤掉）。
- 联网拒绝提示（`network_denied` / `network_hint`）：Host 下为空。
- 路径提示：bash 的说明与规则文字改成真实路径版本，不再提 `/workspace`、`/scratch`、`/tmp` 每次清空。
- `sandbox.Check()`（`verify_feature`、`-check`）**永远在沙箱里**，只读挂载工作区；`Host` 不影响它。
- 子 agent：`-bash-host` 和 `-bash`、`-permission-mode` 一样进 `main.go` 的继承表，父任务在宿主机上跑，派出的子任务也在宿主机上跑（2026-10-29 用户确认）。
- 谁能开：任何登录账号都能在每轮请求、`!命令` 和个人设置里选 `host`，不像 `hooks` 那样只限平台层；网页端的账号都按可信处理（2026-10-29 用户确认保持现状）。

## 5. 协议、设置与审计

- `turn/start`、续聊请求新增可选 `sandbox` 字段（`"sandbox"` / `"host"`），不带时为 `"sandbox"`。
- 设置新增 `sandbox_mode` 键；`internal/appserver/settings.go` 的校验、默认值、合并逻辑一起处理，`projectKeys` 不加。
- 快照：start 事件记下这一轮的 `permission` 与 `sandbox`，续聊照快照启动；`summary()` 带上，界面能显示当前档位。
- agent 启动参数：`server.go` 在 Host 时拼 `-bash-host`；`-permission-mode` 照旧。
- `unavailable("bash")` 在 `sandbox_mode=host` 时不检查 bwrap / `sandbox-exec`。
- 审计：Host 下每条 `bash` 执行写一条记录（`Actor: "model"`、`Tool: "bash"`、`Detail: "host"`、参数哈希照旧）；`!命令` 记为 `Actor: "user"`。写不进审计就不执行。
- 提示词：`sandbox.Rules()` 在 Host 下追加「当前没有沙箱：命令直接在这台机器上跑，能读写任何文件、能直接联网；路径是真实路径」；`permission.Describe()` 的 `full` 补一句「命令是否在沙箱里由 sandbox 决定」。

## 6. 界面

- `index.html` 的 `perm` 下拉只放三项；「完全访问」带警示样式（`data-warn="1"`，`layout.js` 里按值加红色）。
- 选中「完全访问」时 `composer.js` 弹一次 `confirm`，内容带主机名；取消则回到上一档。
- 设置页（`pages.js:229`）主下拉三项，下面加「高级」一组：只读计划、无人值守（沙箱内）、命令在宿主机跑（`sandbox_mode`）。
- 老对话恢复档位（`shell.js:271`）：按上面的反推规则显示；没有 `sandbox` 字段的按 `sandbox`。
- 远程主机：确认框与档位标签带主机名（`ui.host`）。

## 7. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/sandbox/sandbox.go` | `Host` 变量；`Run`/`Shell` 的分派与说明；`Definitions()` 在 Host 下去掉 `request_network_access`；`Rules()` 的真实路径文案 |
| `internal/sandbox/host.go`（新） | `launchHost()`：argv、环境过滤、工作目录、tty；与 `launch()` 共用启动/终止路径 |
| `internal/sandbox/launch_linux.go`、`launch_darwin.go` | `launch()` 里的 Host 分支；平台相关的 `script` 参数复用现有函数 |
| `internal/sandbox/jobs.go` | Host 下 `script` 的探测与 tty 说明 |
| `main.go` | 新增 `-bash-host`（默认 false，须配合 `-bash`）；设置 `sandbox.Host`；子 agent 继承 `-bash-host` |
| `internal/appserver/settings.go` | `sandbox_mode` 默认值、校验、合并；`projectKeys` 不加；`unavailable("bash")` 在 Host 下跳过 bwrap 检查 |
| `internal/appserver/server.go` | 启动 agent 拼 `-bash-host`；快照与 `summary()` 带 `sandbox` |
| `internal/appserver/threads.go` | 续聊、分叉、副本照快照传 `sandbox` |
| `internal/web/page/index.html` | `perm` 下拉三项与警示样式 |
| `internal/web/page/layout.js` | 按档位加警示样式、显示主机 |
| `internal/web/page/composer.js` | 读档位、完全访问的二次确认、请求里带 `sandbox` |
| `internal/web/page/shell.js` | 载入对话时按 `permission` + `sandbox` 恢复档位 |
| `internal/web/page/pages.js` | 设置页三档 + 高级项 |
| `internal/web/page/style.css` | 警示色 |
| `README.md` | 参数表加 `-bash-host`；功能表补一句「完全访问在宿主机跑」 |
| `spec/README.md` | 索引状态与描述 |

不改：`permission` 的四档与规则判断；`toolspec.go` 与 `dispatch.go`；沙箱内的挂载、seccomp、cgroup、网络代理；`Check()` 的只读语义。

## 8. 这一期不做

- **模型审查者**（Codex 的 auto-review / guardian）。本期「帮我批准」就是 `edit`，只按边界判断，不判断命令风险。要做审查者时单独开设计：在 `permission.Decide` 返回 `ask` 的位置接一次小模型调用，带断路器与审计，三档的对外文案不变。
- 模型失败后申请出沙箱（Codex 的 escalation）。出沙箱只能由人选择档位。
- 命令级豁免（Claude Code 的 `excludedCommands`）。
- 放宽沙箱内的挂载、环境变量、网络端口（另开 spec）。
- 项目配置设置 `sandbox_mode`（明确禁止）。
- Windows、桌面端。

## 9. 验收

### tern 自己做（不需要真实模型）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过。
2. 改了页面脚本后 `node --check internal/web/page/*.js` 通过。
3. 读代码报告：Host 分支的 argv、环境过滤、终止路径；`Check()` 不受 Host 影响。

### 真实模型（临时 clone，`TERN_HOME` 指向临时目录）

4. `-bash -bash-host -permission-mode=full`：让模型写 `$HOME/tern-host-test`，文件出现在真实 `$HOME`；`curl -sS https://example.com` 能通。
5. 同一条写 `$HOME` 的命令，在 `-permission-mode=full`（不带 `-bash-host`）下失败（只读文件系统 / Operation not permitted）。
6. `-permission-mode=ask -bash-host`：命令在宿主机跑，但每条都要人批准。
7. Host 下后台命令：`background=true` 起 `sleep 30`，`bash_kill` 能停掉，`bash_output` 有退出码。
8. `verify_feature` / `-check` 在 Host 下仍进沙箱：校验命令写工作区应失败。
9. 审计 `.data/audit/audit.jsonl` 里有 Host 执行的记录（`Detail: "host"`），`!命令` 记为 `user`。

### 网页端（常驻账号 `qa`）

10. 主下拉只有三项；三档分别落成 `permission=ask`、`edit`、`full`+`host`。
11. 选「完全访问」弹确认框，带主机名；取消后档位回退。
12. 远主机上选「完全访问」：确认框显示别名；执行发生在远端（在远端 `$HOME` 建文件能看到）。
13. 老对话（只有 `permission_mode=full`）打开时显示「无人值守（沙箱内）」，命令仍被沙箱限制；保存后仍是 `sandbox`。
14. 设置页把「命令在宿主机跑」打开、主下拉选「请求批准」，下一轮启动参数里有 `-bash-host` 与 `-permission-mode=ask`。

## 10. 风险与回滚

- **风险**：完全访问下，默认开启的 `open_page`/`ds_fetch` 让「网页注入 → 任意命令」没有兜底。因此默认不选、二次确认、红色警示、审计记录；设置页与 README 写明含义。
- **兼容**：`-permission-mode=full` 与老设置的语义不变；旧客户端不带 `sandbox` 字段一律按沙箱内。
- **回滚**：去掉界面「完全访问」一项、`sandbox_mode` 按默认 `"sandbox"` 处理即可；`-bash-host` 默认 false，留着也不影响现有行为。
