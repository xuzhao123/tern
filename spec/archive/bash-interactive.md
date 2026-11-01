# 交互式命令：给后台命令写输入，可选伪终端

状态：已实现并完成收尾验证（2026-10-25），范围与证据见 [本批收尾记录](closeout-20261025.md)。

## 1. 为什么要改

`bash` 一次跑一条命令，后台命令（`background=true`，`internal/sandbox/jobs.go`）只能读输出、停掉，不能往里写输入。要回答提示的程序（`npm init`、交互式安装、`python -i`、需要输入密码以外确认的脚本）只能想办法绕开，比如 `yes | …`。

Codex 的 `exec_command` 起一个 PTY 会话，`write_stdin` 往里写（`codex-rs/core/src/tools/handlers/unified_exec.rs`）。

## 2. 改什么

- `bash` 的 `background=true` 加可选参数 `tty`（布尔，默认 false）：
  - false：标准输入是一个管道，命令能从 stdin 读（现在是 `/dev/null`）。
  - true：在沙箱里用 `script -qfc <命令> /dev/null` 包一层，给命令一个伪终端（util-linux 的 `script`，沙箱里的 `/usr/bin` 本来就看得到）；没有 `script` 时报错“沙箱里没有 script，不能开伪终端”。
- 新工具 `bash_input {job_id, text, close}`：
  - 往这个后台命令的标准输入写 `text`（原样写，要回车就自己带 `\n`）；`close=true` 时写完关掉 stdin（发 EOF）。
  - 返回写之后 1 秒内的新输出（和 `bash_output` 的格式一样），省一次 `bash_output`。
  - 命令已经结束回“这个后台命令已经结束”；最多一次写 64KB。
- 工具说明补：要回答提示的程序用 `background=true`（需要终端的加 `tty=true`），再用 `bash_input` 输入；前台命令的标准输入还是 `/dev/null`。
- `toolspec.go` 里 `bash_input` 和 `bash_kill` 一样标成有副作用、按顺序执行（`Serial`），权限规则按 `Bash(...)` 一类处理。
- macOS 的沙箱（macos-sandbox.md）上，`script` 的参数不一样（`script -q /dev/null <命令>`），按平台拼。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/sandbox/jobs.go` | 后台命令留着 stdin 的写端；`tty` 时用 `script` 包；`bash_input` 的实现 |
| `internal/sandbox/sandbox.go` | `bash` 的参数定义加 `tty`；`bash_input` 的定义与分派；说明文字 |
| `internal/agent/toolspec.go` | `bash_input` 的特性 |
| `internal/agent/dispatch.go` | 分派 `bash_input`（和 `bash_output`、`bash_kill` 一样交给 sandbox） |
| `README.md` | 工具配置里 Bash 那一行补：后台命令可以用 `bash_input` 写输入，`tty=true` 开伪终端 |

不改：前台命令（stdin 照旧 `/dev/null`）、后台命令的上限、只活到这一轮结束的规则。

## 4. 这一期不做

- 改终端的行列数（Codex 的 `command/exec/resize`）。
- 人从网页上给后台命令写输入（background-jobs-panel.md 只能看和停）。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过。
2. 读代码报告：stdin 写端的生命周期（这一轮结束时关掉）；`tty` 时的命令行。

### Claude Code 在沙箱外做（真实模型）

3. 让模型在后台跑 `read -p "名字？" n; echo "你好 $n"`，再用 `bash_input` 输入“tern\n”：输出里有“你好 tern”。
4. `tty=true` 跑 `python3 -i -q`，输入 `1+1\n`，再 `close=true`：输出里有 `2`，命令结束。
5. 给已经结束的后台命令 `bash_input`：报“已经结束”。
