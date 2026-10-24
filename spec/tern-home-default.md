# 数据目录默认就是 ~/.tern

状态：已实现（2026-10-24，tern 实现；第 5 节验收全部通过，第 7、8 条由 Claude Code 用真实模型跑）。review 时改过第 3 节 README 清单的第 3、5 项（两段的位置、“下文”的说法）。合并后发现沙箱里的 Go 工具链跟着 `TERN_HOME` 走，见第 7 节，已由 tern 修好（2026-10-24，第 7 节的验收通过）。合并后又发现登记工作区的相对路径和 `~` 不能用，见第 8 节，已由 tern 修好（2026-10-24，第 8 节的验收通过）。

## 1. 为什么要改

2026-10-24 用户问“像命令行一样用 `tern xx` 怎么用”，答案绕在数据目录上：没设 `TERN_HOME` 时，在源码根目录里运行用的是仓库本身，在别处用的是 `~/.tern`（`main.go:172`）。同一个装好的 `tern`，在仓库里和在别处敲，是两份数据、两个 app-server。用户接着问能不能像 Codex 一样把东西放在 `~/.codex` 那样的固定位置，再问能不能去掉这个设计。

“源码根目录就用当前目录”是 app-server-design.md 第 4 节为了“原来的用法不变”加的（第 14 节第 2 条）。现在 tern 能装成命令行用了，这条特例反而让人搞不清数据在哪。

同一天交给 tern 实现这份设计时又碰到一次：仓库在 Lima 的 virtiofs 挂载上（宿主机是 macOS），上一个 app-server 没正常退出，留下的 `control.sock`、`llm.sock` 在虚拟机里 stat 不了，也删不掉（`Operation not supported`），新的 app-server 起不来（`bind: operation not supported`），只能把 `.data/app-server` 整个改名。`~/.tern` 在虚拟机自己的磁盘上（ext4），没有这个问题。

Codex 的做法：默认 `~/.codex`，环境变量 `CODEX_HOME` 可以换。tern 照这个来。

## 2. 改什么

**规则**：设了 `TERN_HOME` 就用它（相对路径按启动时的工作目录解释，同现在）；没设就用 `~/.tern`。在哪个目录启动都一样，源码根目录不再特殊。

其余照旧：`main` 最前面算出它、建好（0700）、写回环境变量、chdir 过去；`sandbox-init` 在这之前分流，不碰它；agent 的 `-workspace`、`-image`、`-hooks` 按启动时的工作目录转成绝对路径。

环境变量不去掉，有三处要靠它换目录：

- 临时 clone 里的验证要和用户的数据分开：以前靠“clone 是源码根目录”自动分开，以后显式设 `TERN_HOME=<clone>` 或一个临时目录。
- `-dev` 每轮用 `go run .` 启动 agent，agent 的工作目录就是 `TERN_HOME`，所以它必须是源码根目录：`TERN_HOME=$PWD go run . app-server -dev`。这个检查不变（`internal/appserver/appserver.go:72`），报错里写上这条命令。
- 同一台机器上要第二份数据（比如 `ssh localhost` 当远端）。

**已定的取舍**：

- 在仓库里开发时，`go run . web`、`go run . chat` 用的也是 `~/.tern`，按“最后启动的界面说了算”会把后台的 app-server 换成开发版。以前仓库的 `.data/` 就是日常数据，开发版本来就跑在日常数据上，这不算退步，不另加规则。
- 仓库里提交的 `skills/`、`commands/`、`hooks/`、`eval/`、`queue/`、`retrieval/corpus.md` 照旧相对 `TERN_HOME` 找，不改成按当前目录找。要用仓库里的这几份，就 `TERN_HOME=$PWD`，或者拷到 `~/.tern`。
- 不写自动迁移：旧数据怎么搬写在 README 里，由人做一次（第 4 节）。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `main.go` | `setupHome` 去掉 `case home == "" && tenant.SourceRoot(dir)` 这一支，没设就是 `~/.tern`。函数上面的注释同步改：去掉“没设时，在 tern 源码根目录里就用当前目录（原来的用法）”，写成“没设时用 ~/.tern，在哪个目录启动都一样（同 Codex 的 `CODEX_HOME`）” |
| `internal/tenant/tenant.go` | `SourceRoot` 的注释改成只给 `-dev` 用：`-dev` 用 `go run .` 启动 agent，`TERN_HOME` 得是源码根目录。函数本身不改 |
| `internal/appserver/appserver.go` | `-dev` 的报错加上用法，例如：`-dev 要用 go run . 启动 agent，TERN_HOME 须是 tern 源码根目录（在源码根目录里用 TERN_HOME=$PWD）：<目录>`；上面那行注释不用改 |
| `README.md` | 见下面的清单 |
| `AGENTS.md` | “数据与密钥”第一条的括号改成“（没设时是 `~/.tern`）”；“验证”里“会改仓库内容的验证在临时 clone 里做”后面补一句：运行 clone 编出的程序时把 `TERN_HOME` 设成 clone 或临时目录，不碰 `~/.tern` |

README 要改的地方：

1. “快速开始”第一段（现在第 11 行）：数据目录是 `~/.tern`（不存在就建，0700），`TERN_HOME` 可以换成别的目录；`.env`、`.data/`、`skills/`、`commands/`、`hooks/`、`retrieval/corpus.md`、`eval/cases.jsonl`、`queue/tasks.jsonl` 都相对它找；在哪个目录运行都一样，源码根目录也是。删掉“在 tern 源码根目录里就用当前目录，原来的用法不变”和判断源码根目录的那句。
2. 紧跟的代码块：`.env` 放到 `~/.tern`：
   ```sh
   mkdir -p ~/.tern && cp .env.example ~/.tern/.env && chmod 600 ~/.tern/.env
   # 在 ~/.tern/.env 中填写自己的 ARK_API_KEY
   go run . -question '计算 1231+23123'
   ```
3. 在第 1 项那段后面（“agent 自己的路径参数…”那段之前）加两小段，别插在第 2 项的代码块和“不带 `-question` 时从终端交互输入”之间：
   - **开发 tern 本身**：在源码目录里 `go run .` 用的也是 `~/.tern`，所以后台的 app-server 会换成开发版（规则见下文）。要用仓库里提交的评测集、示例任务、skills、hooks，或者用 `-dev`，就把 `TERN_HOME` 指到源码根目录，例如 `TERN_HOME=$PWD go run . eval`。这是另一份数据、另一个 app-server，对话、设置、账号和 `~/.tern` 不通。
   - **从旧版升级**：以前在源码根目录里不设 `TERN_HOME`，数据在仓库的 `.data/`。想接着用：先在源码根目录里 `TERN_HOME=$PWD go run . app-server stop`，停掉旧的 app-server；再把 `.env` 和 `.data/` 拷到 `~/.tern`（`.data/gocache` 是编译缓存，`.data/selfuse` 是临时 clone，不用拷）。或者一直 `export TERN_HOME=<源码目录>`。
4. “网页端支持新对话…”那段（现在第 33 行）里 `-dev` 的说法：改成 `TERN_HOME=$PWD go run . app-server -dev`、`TERN_HOME=$PWD go run . app-server start -dev`，“要求 `TERN_HOME` 就是 tern 源码根目录”保留。
5. “需要可执行文件时”（现在第 72–77 行）改成装到 PATH 上：
   ```sh
   go install .          # 装到 $(go env GOPATH)/bin/tern；这个目录在 PATH 上，就能在任何目录用 tern
   tern -question '计算 1231+23123'
   ```
   再加一句：文档里的 `go run .` 都可以换成 `tern`；改了代码要重新 `go install .`。（这段在 README 中间，前后都有 `go run .`，不写“下文”。）
6. “其他入口”表：`app-server [-dev]` 一行的“要求 `TERN_HOME` 是 tern 源码根目录”后面补“（`TERN_HOME=$PWD`）”；`queue`、`eval` 两行补一句：用仓库里的 `queue/tasks.jsonl`、`eval/*.jsonl` 要 `TERN_HOME=$PWD`。

不改：

- `internal/appclient`（start、stop、proxy、ssh）。远端的 `proxy` 走登录 shell，工作目录是家目录，本来就不在源码根目录里，行为不变。
- `internal/sandbox`。`-bash-project-ro` 挂的还是 `TERN_HOME`（main 已经 chdir 过去），只是它现在默认是 `~/.tern`。沙箱里 `HOME` 是 `/scratch`，沙箱里不设 `TERN_HOME` 运行 tern 会用 `/scratch/.tern`，不用处理。
- `internal/hooks`（`AGENT_HARNESS_DIR` 还是 `TERN_HOME`）、`internal/eval`、`internal/queue`、`internal/retrieval`、`internal/skills`、`internal/commands` 的相对路径。
- 协议版本 `Protocol`、app-server 的 `initialize` 回复。
- `spec/` 下的文件。
- `.claude/skills/tern-implement/SKILL.md`，由 Claude Code 改。

## 4. 这一期不做

- 自动迁移：不检测仓库里的旧 `.data/`，不提示也不搬。
- 全局个人指令 `~/.tern/AGENTS.md`（对应 Codex 的 `~/.codex/AGENTS.md`），另写一份设计。
- 让 `-dev` 能用在任意 `TERN_HOME` 上（比如记下源码目录、在那里 `go run`）。
- `eval/cases.jsonl`、`retrieval/corpus.md` 不存在时更友好的提示；现在照旧报“打不开文件”。
- 改名 `TERN_HOME`，或者加 `-home` 这样的参数。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

在 clone 根目录里先编一份：`T=/scratch/home-check && rm -rf $T && mkdir -p $T && go build -o $T/tern .`。沙箱里 `/tmp` 每次调用都是空的，所以放 `/scratch`，后面每条命令开头都先 `T=/scratch/home-check`。app-server 是后台进程，一条命令结束时沙箱会把它一起收掉，所以 start、检查、stop 要写在同一条命令里。`HOME=$T/home` 让 `~/.tern` 落在临时目录里。

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过。
2. 源码根目录里不设 `TERN_HOME`，用的是 `~/.tern`：
   `env -u TERN_HOME HOME=$T/home $T/tern app-server start && test -S $T/home/.tern/.data/app-server/control.sock && stat -c %a $T/home/.tern && test ! -e .data/app-server && env -u TERN_HOME HOME=$T/home $T/tern app-server stop`
   预期：start 打一行 JSON，权限是 `700`，clone 里没有 `.data/app-server`，stop 正常退出。
3. 相对路径的 `TERN_HOME` 按启动目录解释：
   `cd $T && TERN_HOME=rel ./tern app-server start && test -S $T/rel/.data/app-server/control.sock && TERN_HOME=rel ./tern app-server stop`
4. 源码根目录里不设 `TERN_HOME` 跑 `-dev` 报错：
   `env -u TERN_HOME HOME=$T/home $T/tern app-server -dev; echo "exit=$?"`
   预期：`exit=1`，报错里有 `TERN_HOME=$PWD`。
5. `TERN_HOME=$PWD` 时 `-dev` 能启动：
   `TERN_HOME=$PWD $T/tern app-server start -dev && TERN_HOME=$PWD $T/tern app-server stop`
6. `grep -n SourceRoot main.go` 没有输出；`grep -n '源码根目录里就用当前目录' README.md AGENTS.md` 没有输出。

### Claude Code 在沙箱外做（真实模型）

`T` 换成沙箱外的一个临时目录（`mktemp -d`），程序用 clone 重新编一份。

7. 在 clone 根目录里，`touch $T/marker` 后跑 `env -u TERN_HOME HOME=$T/home DEEPSEEK_API_KEY=… $T/tern -provider deepseek -question '计算 1231+23123'`（`$T/home/.tern` 里没有 `.env`，只导出这一个密钥）。预期：回答里有 24354；`find $T/home/.tern/.data -type f -newer $T/marker` 有文件；`find .data -type f -newer $T/marker` 没有输出（第 5 条留下的 `.data/app-server` 不算新文件）。
8. 在别的目录（`cd $T`）跑同一条命令，结果一样，数据落在同一个 `$T/home/.tern`。

## 7. 合并后发现：沙箱里的 Go 工具链（2026-10-24）

数据目录改成 `~/.tern` 以后，交给 tern 下一份 spec 时，预热缓存就失败了：`go: go.mod requires go >= 1.25.5 (running go 1.25.3; GOTOOLCHAIN=local)`。

原因：沙箱挂哪份 Go，由 `internal/sandbox/sandbox.go:51` 的 `go env GOROOT GOMODCACHE` 决定，它在进程的当前目录里运行，也就是 `TERN_HOME`。以前 `TERN_HOME` 是 tern 仓库，`go.mod` 写着 1.25.5，`GOTOOLCHAIN=auto` 让它切到模块缓存里的 1.25.5；现在是 `~/.tern`，没有 `go.mod`，拿到的是本机装的 1.25.3（`/home/dev/.local/go`）。所以以前也只是碰巧对：别的工作区的 `go.mod` 要求的版本，从来没被照顾到。

**改法**：`goEnv` 在工作区目录里运行 `go env`（`cmd.Dir = Project`），这样拿到的是工作区 `go.mod` 要求的工具链，和在那个仓库里手动敲 `go build` 一样。在工作区里失败时（比如要的工具链下载不了），退回现在的做法、在当前目录里再跑一次，沙箱里至少有一份 Go，报错会说清楚版本不够。

| 文件 | 改动 |
| --- | --- |
| `internal/sandbox/sandbox.go` | `goEnv` 先 `cmd.Dir = Project` 跑 `go env GOROOT GOMODCACHE`，失败或输出不是两行时，不设 `Dir` 再跑一次；注释写上为什么在工作区里跑。`sync.OnceValue` 不变：一个 agent 进程只有一个工作区 |

验收：
- tern 做：`gofmt -l .` 没有输出，`go vet ./...`、`go build ./...` 通过。
- Claude Code 在沙箱外做（沙箱里起不了嵌套的沙箱）：用 clone 编的程序，在家目录里、不设 `GOTOOLCHAIN`，
  1. `-workspace <tern 的 clone> -bash -check 'go version'` 输出 go1.25.5；
  2. `-workspace <一个没有 go.mod 的临时 git 仓库> -bash -check 'go version'` 输出本机的 go1.25.3。

## 8. 合并后发现：登记工作区的路径（2026-10-24）

用户在网页端设置页添加工作区，加不上。复现（8090，qa 账号，`POST /settings/workspaces`）：

| 填的 | 结果 |
| --- | --- |
| `.` | 400 不是 git 仓库（没有 .git）：/home/dev/.tern |
| `~/workspace/tern` | 400 目录不存在：/home/dev/.tern/~/workspace/tern |
| `workspace/tern` | 400 目录不存在：/home/dev/.tern/workspace/tern |

原因：`workspaceAdd` 把相对路径接在 `TERN_HOME` 后面（`internal/appserver/settings.go:432`），输入框的提示还写着“相对 agent 目录（如 . 表示本仓库）”（`internal/web/page/pages.js:136`）。以前 `TERN_HOME` 是 tern 仓库，填 `.` 正好是它；改成 `~/.tern` 以后，相对路径就没有意义了。`~` 从来没有展开过：它是 shell 的写法，这里不经过 shell。

**改法**：
- `~` 或 `~/…` 开头：换成 app-server 所在机器上的家目录（`os.UserHomeDir()`）。远程模式下就是远端的家目录，和设置页改的本来就是远端的设置一致。
- 其他相对路径：不再接在 `TERN_HOME` 后面，返回 400：“请填绝对路径，可以用 ~ 开头，例如 ~/workspace/tern”。
- 输入框的提示改成：“仓库目录的绝对路径，可以用 ~ 开头，例如 ~/workspace/tern”。

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/settings.go` | `workspaceAdd`：先处理 `~`（只认 `~` 和 `~/` 开头，`~别人` 当相对路径拒绝），再要求绝对路径，否则返回上面那句 400；`:432` 的注释改成为什么不收相对路径（app-server 没有“当前目录”可言） |
| `internal/web/page/pages.js` | `:136` 输入框的 `placeholder` 改成上面那句 |

不改：登记以后存的是展开后的绝对路径（`workspaceDir.Path`），对话里选工作区、chat 的 `-workspace 名字` 照旧按名字找。

验收：
- tern 做：`gofmt -l .` 没有输出，`go vet ./...`、`go build ./...` 通过，`node --check internal/web/page/pages.js` 通过。
- Claude Code 在沙箱外做（网页端，临时 `HOME`）：在临时家目录下建一个 git 仓库 `repo`；填 `~/repo` 能登记，存下来的是绝对路径；填 `.`、`repo` 返回 400 和上面那句提示；填一个不存在的 `~/nosuch` 返回“目录不存在”。
