# 去掉 calculator 和 get_current_datetime，日期放进环境信息

状态：已实现（2026-10-24，tern 实现；第 5 节验收全部通过，第 6–10 条由 Claude Code 用真实模型跑）。review 时补了 `internal/appserver/server.go:61` 的注释、`weekday` 看过程的打分器（`outcome`）。

## 1. 为什么要改

`calculator`、`get_current_datetime` 是 learning-agent 早期的示例工具，现在每个对话都默认带着，排在工具清单最前面（`main.go:639`、`internal/tools/tools.go:18`）。2026-10-24 用户问 Codex 默认带哪些工具，觉得这两个可以去掉。

对照本机 Codex 源码（`~/workspace/codex`，提交 `970111f6cb`，2026-10-23）：
- 默认的工具是 `exec_command`/`write_stdin`、`apply_patch`、`view_image`、子 agent 那几个，加上配了才有的 `web_search`、MCP（`codex-rs/core/src/tools/spec_plan.rs` 的 `add_core_tool_sources`，默认值在 `codex-rs/features/src/lib.rs`）。
- 没有计算器。要算准就在沙箱里跑命令。
- `current_time` 默认不注册：要 `CurrentTimeReminder`（`UnderDevelopment`，默认关），或者模型自己声明有 `clock`。
- 当前日期和时区写在环境上下文里（`codex-rs/core/src/context/world_state/environment.rs` 的 `current_date`、`timezone`）。

tern 去掉这两个工具以后有一个缺口：环境信息（含日期）只在选了工作区时才发（`internal/agent/react.go:261`），日期还是 UTC。没选工作区的对话就不知道今天几号了。所以照 Codex，日期一律放进环境信息。

## 2. 改什么

### 2.1 去掉两个工具

- agent 的工具清单从空开始，只放按开关追加的那些。`internal/tools` 整个包删掉。
- 网页端内置的计算器 MCP（`/mcp`、`-mcp-serve`）保留，它是演示和测试 MCP 用的。它现在借用 `tools.Calculate` 和 `tools.Definitions[0]` 的描述，把这两样搬进 `internal/mcp`。
- 设置里的两个开关（`calculator`、`get_current_datetime`）去掉。

### 2.2 日期一律放进环境信息

- 没选工作区的对话也发环境信息，只有三项：日期、系统、权限。选了工作区的照旧，多出工作区、bash 路径、git 那几项。
- 日期写成 Asia/Shanghai 的日期加星期，例如 `2026-10-25 星期日（Asia/Shanghai）`。时区和长期记忆（`internal/memory/memory.go:62`）、评测打分（`internal/eval/grade.go:113`）用的一致，原来的 `get_current_datetime` 用的也是它。
- 不放几点几分：环境信息在对话开头发一份，之后哪项变了才追加。如果写上时间，续聊时每一轮都会变，每一轮都要多一条提醒。Codex 也只放日期。要准确的时间，就用 bash 跑 `date`。

### 2.3 老数据照常能用

- 老对话的快照、老检查点的启动参数里可能有 `-disallowed-tools calculator,get_current_datetime`。agent 遇到这两个名字直接忽略，因为工具已经没了，没什么可去掉的。不认识的其他名字照旧报错。
- 老的 `.data/settings.json` 里可能有 `"calculator": "off"`。合并设置时本来就只看 `builtin` 里有的键（`internal/appserver/settings.go` 的 `effectiveFor`），不用迁移。

### 2.4 评测集

- 删掉“计算”组的 6 题：`calc-sqrt`、`calc-div`、`calc-year`、`calc-floor`、`calc-circle`、`calc-speed`。
- 删掉 `minutes-left`：它要精确到分钟，环境信息里没有时间，题目本身又没开 bash。
- `weekday` 留下，改成考环境信息里的日期：`{"type": "tool_used", "tool": "get_current_datetime"}` 换成 `{"type": "outcome"}`（评测集要求每题至少一项看结果、一项看过程的打分器，`outcome` 默认要求正常答完；review 时发现补上），`reference` 改成“以环境信息里的日期（Asia/Shanghai）为准”。`weekday` 的期望值由 `observedTime` 算，新试次里没有 `get_current_datetime` 的结果，它会退回用试次开始时间（`internal/eval/grade.go:113`），不用改。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/tools/tools.go` | 删掉，`internal/tools` 目录一起删 |
| `internal/mcp/calculator.go` | 新建：从 `tools.go` 搬来 `Calculate`、`evaluate` 和它们用到的辅助代码，改成包内的 `calculate`；`calculatorDescription` 常量是原来 `calculator` 的 description 原文 |
| `internal/mcp/mcp.go` | MCP server 的 calculator 改用包内的 `calculate` 和 `calculatorDescription`，不再 import `internal/tools`；`:160` 注释里“（本地也有 calculator）”去掉，只说加 `mcp_` 前缀是为了不和本地工具重名 |
| `internal/agent/dispatch.go` | 最后的 `return tools.Run(...)` 换成 `return nil, llm.Permanent(fmt.Errorf("未知工具：%s", call.Function.Name))`（和原来 `tools.Run` 的 default 一样）；`:22` 注释里“内置工具交给 tools.Run”去掉 |
| `internal/agent/toolspec.go` | 删掉 `calculator`、`get_current_datetime` 两行 |
| `main.go` | `llm.Tools` 从空开始（`:639` 那行 `kept(tools.Definitions)` 去掉，后面按开关追加的不变；`kept` 还要用在浏览器工具上）；`-disallowed-tools` 只认 `browser.Definitions` 里的名字，`calculator`、`get_current_datetime` 两个旧名字跳过（注释写原因：老对话的快照和检查点里还会带）；flag 说明和 `:632` 的报错去掉这两个名字；`:624` 的注释同步改 |
| `internal/workspace/context.go` | `EnvironmentFields`：`Dir` 为空时只返回日期、系统、权限三项；日期改成 Asia/Shanghai（`time.FixedZone("Asia/Shanghai", 8*60*60)`）的 `2006-01-02` 加中文星期，后面写“（Asia/Shanghai）”；文件开头的注释同步改（没选工作区也发环境信息） |
| `internal/agent/react.go` | `:261` 的条件 `workspace.Dir != "" && resume == nil` 改成 `resume == nil`；`environment` 上面的注释如有“工作区”前提一并改 |
| `internal/memory/memory.go` | `:61` 注释“与 get_current_datetime 使用同一时区”改成“与环境信息里的日期使用同一时区” |
| `internal/appserver/settings.go` | `toolOrder`、`singleTools`、`builtin` 去掉这两项 |
| `internal/appserver/server.go` | `:61` `Disallowed` 字段注释“关掉的单个工具（内置工具、web_search、open_page）”改成只说 `web_search`、`open_page`，老存档里可能还有已经去掉的 `calculator`、`get_current_datetime`（review 时补） |
| `internal/web/page/shell.js` | `TOOL_INFO` 去掉两项；`runTools` 返回值去掉 `calculator`、`get_current_datetime` |
| `internal/web/page/chat.js` | `welcome` 里依赖这两个工具的那条示例问题（`:279`）删掉 |
| `internal/web/page/pages.js` | `:280`、`:301` 两处“工具是 agent 的 calculator”“它把 agent 的 calculator …”改成不提 agent，例如“工具是一个计算器 calculator（四则运算、括号、sqrt、floor）” |
| `eval/cases.jsonl` | 删 7 题、改 `weekday`，见第 2.4 节 |
| `README.md` | `:109` “普通任务只启用 calculator、get_current_datetime；…”改成：默认没有常驻工具，检索、记忆、MCP、Skills、子 agent、浏览器、DeepSeek 搜索、Bash、编码工具按需开启；日期（Asia/Shanghai）在环境信息里。`-disallowed-tools` 的说明只剩 `web_search`、`open_page`；`:124` “工具扩展”一行去掉 `[tools](internal/tools/)` 和“本地工具”。其余提到计算器 MCP 的地方（`:63`、`:186`）不改 |

不改：
- `internal/eval/grade.go`：`observedTime` 读老评测记录里的 `get_current_datetime` 结果，回放老记录还用得上。
- `minutes_left` 这种 grader：代码留着，只是暂时没有题目用它。
- `eval/coding-cases.jsonl`：`code-calc-pow` 用的是历史提交 `ee91d23` 的仓库。
- `internal/web/server.go:194` 启动时那行“远程 MCP …（内置 calculator …）”：MCP 计算器还在。
- `-mcp-serve` 的用法、协议版本 `Protocol`、`spec/` 下的文件。

## 4. 这一期不做

- 环境信息里放几点几分。
- 时区可配置（照旧写死 Asia/Shanghai，和记忆、评测一致）。
- 删 `minutes_left` grader 的代码。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过 `shell.js`、`chat.js`、`pages.js`。
2. `test ! -e internal/tools`；`grep -rn --include=*.go -E 'calculator|get_current_datetime' .` 只剩这几处：`internal/mcp/`（MCP 计算器）、`main.go`（`-mcp-serve` 和 `-disallowed-tools` 的兼容）、`internal/eval/grade.go`、`internal/web/server.go:194`、`internal/memory/memory.go` 里没有（注释已改）。
3. MCP 计算器照常工作：
   ```sh
   T=/scratch/tools-check && mkdir -p $T && go build -o $T/tern . && \
   (printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"calculator","arguments":{"expression":"floor(sqrt(1234*5678))"}}}'; sleep 1) | HOME=$T/home $T/tern -mcp-serve
   ```
   预期：`id` 为 2 的回复里有 `2647`。协议版本不被接受时换成 mcp-go 支持的版本，报告用的是哪个。
4. `-disallowed-tools` 不认识的名字照旧报错：`HOME=$T/home ARK_API_KEY=x $T/tern -disallowed-tools nosuch -question hi; echo "exit=$?"`。预期：`exit=1`，报错里列出的只有 `web_search`、`open_page`。
5. 评测集：`python3 -c` 逐行解析 `eval/cases.jsonl`，没有 `calc-` 开头的 id、没有 `minutes-left`；`weekday` 的 graders 是 `weekday` 和 `outcome`。

### Claude Code 在沙箱外做（真实模型）

6. 没选工作区：`-provider deepseek -question '今天几号，星期几？'`。预期：请求里没有 `tools` 字段；第一条提醒是 `<environment>`，里面有日期、系统、权限三项；回答和当天 Asia/Shanghai 的日期、星期一致。
7. 选了工作区：`-workspace <某个 git 仓库> -provider deepseek -question '今天几号？'`。预期：环境信息里有工作区、git 分支，日期是 Asia/Shanghai 的格式。
8. 老参数兼容：`-disallowed-tools calculator,get_current_datetime -provider deepseek -question '你好'` 正常回答，不报错。
9. 网页端：设置页没有“计算器”“当前时间”两个开关；新对话的请求里没有这两个工具；MCP 中心里 `tern-web` 测试连接能列出 `calculator`。
10. 评测：`tern eval -case weekday -k 1` 通过。
