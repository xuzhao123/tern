# 改过文件后重跑同一条命令不算死循环；进度文件里写错的校验命令能改

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型验收）。

## 1. 为什么要改

**7.2**：循环检测是“最近 6 个动作里同一工具、同样参数出现 3 次就熔断”（`internal/agent/approval.go:224`，在 `react.go:416` 执行之前计数）。“改文件 → 跑测试 → 改文件 → 跑测试 → 改文件 → 跑测试”里同一条测试命令出现 3 次，就被当成死循环拦下了。2026-10-24 实现 app-server 第 3 块时，tern 在 149 步被拦下，活其实已经干完，只差最后的验证。文件改过以后再跑同一条命令，是在看新的结果，不是原地打转。

**7.5**：进度文件的工具 `progress` 只有 `read`、`init`、`note`（`internal/agent/progress.go:73`），`init` 只能一次。初始化时写错的校验命令再也改不了，那一项永远“未通过”。2026-10-23 第二期第 2 块有一条校验命令有笔误；2026-10-24 实现工作区选择框时，校验命令写成了 `/tmp/browse-check.sh`，验证者的沙箱里 `/tmp` 是空的，那一项一直判不过。

## 2. 改什么

### 2.1 成功改过文件就清空动作窗口

- 一批工具结果回来以后（`react.go:439` 调 `loop.observe` 的地方），这一批里只要有 `edit_file` 或 `write_file` 成功（`Status` 是 ok），就把 `loop.Recent` 清空。工作区变了，之前的动作不再和之后的比。
- 只看成功的：同一个失败的编辑原样重试 3 次，照旧算死循环。
- 按调用顺序：同一批里先编辑、后执行的那几个动作已经在执行前计过数了，清空之后从下一批开始重新数。
- 续跑和分叉回放时重算状态的 `replayProgress`（`approval.go:279`）照同样的规则：回放到一批里有成功的 `edit_file`、`write_file` 结果，就清空 `p.Recent`。
- “无进展”检测（`staleHint`、`staleLimit`，看结果有没有新信息）不变。

### 2.2 `progress` 加 `fix_check`

- `action=fix_check`，参数 `id`、`check`（新的校验命令）、`reason`（为什么原来的不对，必填）。
- 只能改还没通过的项（`Passing` 为 false）。已经通过的拒绝：“F3 已经通过验证，校验命令不能再改”。
- 改了以后：
  - 这一项的 `Check` 换成新的，`Verified` 清空，`Passing` 保持 false，要重新 `verify_feature`；
  - 自动追加一条交接说明（`Notes`）：“改了 F3 的校验命令：<旧命令> → <新命令>。原因：<reason>”，人和下一个会话都看得到。
- 新命令和 `init` 时一样，必须非空。改动写进 harness 自己留的权威副本（`mirrorPath`），和其他写进度的动作一样。
- 工具说明补上 `fix_check`：“初始化时写错的校验命令（路径不对、笔误、依赖了沙箱里没有的东西）用它改；只能改没通过的，要写原因，改动会记进交接说明。”`progressRules` 里加一句：校验命令跑不起来、明显是命令本身写错时，用 `fix_check` 改，不要绕开验证。
- `verify_feature` 的验证者照旧只看描述、命令和输出：把校验命令改成 `true` 这种没意义的，验证者对照描述照样判不过。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/agent/react.go` | `:439` 附近：一批结果里有成功的 `edit_file`、`write_file` 时清空 `loop.Recent` |
| `internal/agent/approval.go` | `replayProgress` 同样的规则；`actionWindow` 上面的注释补一句为什么改过文件就清空 |
| `internal/agent/progress.go` | `progress` 的 `action` 枚举加 `fix_check`，参数加 `id`、`reason`（`check` 已有，作为顶层参数也收）；实现 2.2；工具说明和 `progressRules` 补那几句 |

不改：熔断的阈值（6 个里 3 次）、无进展检测、`verify_feature` 的流程和验证者的提示词、进度文件的位置。

## 4. 这一期不做

- 用 bash 改了文件（`sed -i`、`gofmt -w`）也清空窗口：要比较工作区前后的状态，代价大。
- 删除或新增功能项。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过。
2. 读代码报告：清空窗口的条件（只看成功的编辑），以及 `replayProgress` 和实时检测一致的地方。
3. `fix_check`：在仓库里建临时目录（比如 `tmp-progresscheck/`，验完删掉）放一个小程序，直接调 `progress` 工具的处理函数（`agent` 包里导出的入口，或者经 `agent` 的工具分发），对一个 `/scratch` 下的临时工作区：`init` 两项 → `fix_check` 没通过的那项成功、`Notes` 里多了一条、`Verified` 清空 → `fix_check` 不带 `reason` 报错 → 把一项手动标成通过后再 `fix_check` 报“已经通过验证”。如果入口没有导出、写不了临时程序，就读代码报告这几种情况的分支。

### Claude Code 在沙箱外做（真实模型）

4. 7.2：在一个临时 Go 仓库里让模型做一件要“改文件 → `go build` → 再改 → 再 `go build`”至少三轮的事（比如逐个修三处编译错误，每修一处跑一次 `go build ./...`）。这一轮不因 `repeated_action` 结束，存档里没有“疑似死循环”。
5. 7.5：给临时工作区放一份进度文件，其中一项的校验命令故意写错（比如引用一个不存在的脚本），让模型用 `progress(action=read)` 接手并把这一项做完。模型用 `fix_check` 改了命令并写了原因，交接说明里有这条记录，之后 `verify_feature` 通过。
