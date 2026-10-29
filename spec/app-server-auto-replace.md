# app-server 跟着最新启动的程序走

状态：已实现（2026-10-23，用户要求由 Claude Code 直接实现，验收见第 6 节）。

## 1. 为什么要改

app-server 常驻（照 Codex 的 daemon），界面启动时只看控制 socket 能不能握手：能就用，不管后台跑的是哪个程序。改了代码以后重新 `go run . web`，网页端是新的，app-server 和它起的 agent 还是旧的；要记得先 `tern app-server stop`，或者一开始就用 `-dev`。2026-10-23 用户问“怎么用现在的 tern”，答案就绕在 stop、start、`-dev` 上。

Codex 的 daemon 跑的是单独装的一份程序，有后台自动更新；终端连上时比版本，不一致只提示一行，用 `/daemon` 手动重启（`codex-rs/app-server-daemon/README.md`、`codex-rs/tui/src/update_versions.rs`）。它只提示、不自动换，是因为对话在 app-server 进程里跑，重启会打断正在跑的轮次（`shutdownGraceSeconds`，默认等 60 秒）。

tern 的 agent 是独立进程，app-server 退出时照常跑，新的 app-server 起来后接管（`internal/appserver/live.go` 的 `adopt`）。所以换 app-server 不打断正在跑的轮次，可以直接换，不必像 Codex 那样只提示。tern 也不需要 Codex 的托管安装和自动更新：只有一个用户，从源码编译。

## 2. 改什么

**规则：最后启动的界面说了算。** 界面（网页端、chat、`app-server start`、`app-server proxy`）启动时连上 app-server，同时满足下面三条就把它换成自己的程序：

1. 程序不同：两边的 build 不一样。
2. app-server 比这个界面先启动。界面自己起的、或者别的更晚启动的界面换过的，都比它晚，不换。
3. 没有评测在跑。评测的试次是 app-server 的普通子进程，不能接管，换掉就丢了。

条件 2 防止两个不同版本的界面来回换：换完以后，app-server 的启动时间晚于所有已经开着的界面，它们重连时不会再换回去。开着的旧网页端照常用新的 app-server，只要协议版本 `Protocol` 没变。

**build**：可执行文件内容的 sha256 前 12 位，进程里算一次。现在的版本号是“提交号+dirty”，同一个提交上两次没提交的改动分不出来，所以不用它做判断，只照旧用于显示。Go 的链接是确定的：同一份源码 `go run` 两次，build 相同，不会白换。

**换的过程**（`appclient.Start`）：

1. 整个 start 拿 `.data/app-server/start.lock` 的文件锁，同一个 `TERN_HOME` 里同时启动的界面排队，不会一起去换。
2. 连上并握手，`initialize` 的回复里有 `build`、`started`、`dev`、`evals`。
3. 按上面的规则判断：
   - 要换：对这条连接发 `server/shutdown`，按 pid 等进程退出；再用自己的程序起一个新的。旧的是 `-dev` 就保留 `-dev`。
   - 不换：照旧直接返回。
   - 只因为有评测在跑而没换：返回一行提示。
4. 握手被拒（app-server 回了协议错误，多半是协议版本不一致）：这时拿不到 `started`，改用 pid 文件的修改时间（app-server 启动时写的）。比界面早就发 SIGTERM（和 `server/shutdown` 是同一套收尾），等它退出再起新的；比界面晚就照旧报错。

**提示**：`Info` 加 `build` 和 `notice`。`notice` 是给人看的一行，换了就写“后台的 app-server 换成了这次启动的程序（build 旧 → build 新），正在跑的对话由新的接管”，因为评测没换就写原因。chat 打在开头，网页端打在启动输出里，`app-server start` 的 JSON 里带着，`proxy` 写到 stderr（stdout 是协议通道）。

**远程**：本机界面不替远端换。远端的 `tern app-server proxy` 每次 ssh 连接都是新进程，自己按同一规则换。所以在远端重新安装 tern 以后，下一次连接就会换成新的。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/handle.go` | `ServerInfo` 加 `Build`、`Started`（time.Time）、`Dev`、`Evals`；`info()` 改成 `(s *Server) info()`，填上这四项；加 `Build()`：读 `/proc/self/exe`（没有时 `os.Executable()`）算 sha256，取前 12 位，`sync.Once` 只算一次，读不到写 `unknown` |
| `internal/appserver/appserver.go` | `Server` 加 `started time.Time`，`open` 里记下 |
| `internal/appserver/eval.go` | 加 `runningEvals()`：`busy` 不为空的评测数 |
| `internal/appclient/client.go` | 包变量 `startedAt`（本进程启动时间）；`Start` 加锁和换的逻辑；`Info` 加 `Build`、`Notice`；`stopPID`：等 pid 退出（`Stop` 和换共用）；`Proxy` 把 `notice` 写 stderr |
| `internal/chat/chat.go`、`internal/web/server.go` | 打出 `notice`；网页端启动时那一行加上 build |
| `README.md`、`internal/appserver/AGENTS.md` | 改“改了代码要先 stop”那段，写上规则和限制 |

不改：协议版本 `Protocol`（`initialize` 的回复只加字段）、agent 的协议、`adopt`、`-dev` 的含义。

## 4. 限制

- 被接管的 agent 还是旧程序，这一轮跑完为止，下一轮才用新程序。
- 新 app-server 和旧 agent 之间的协议（`session/attach`、序号补发、模型代理的令牌）要兼容。改了这层协议的那一次，先等正在跑的轮次结束再启动新界面。否则接管失败，agent 脱离 10 分钟后自己中断、写好检查点，之后可以续跑。
- 新程序改了 `Protocol` 时，还开着的旧界面会一直“重连中”，要重启它们。
- app-server 正在启动 agent 的那一瞬间被换（`launch` 起了进程、还没发出 `turn/start`），这一轮会卡到 agent 自己 10 分钟后退出。原来手动 `app-server stop` 也有这个窗口。

## 5. 这一期不做

- Codex 的托管安装、自动更新、`--no-daemon`/嵌入式。
- 网页端页面上的提示（只在启动输出里打）。
- 本机界面替远端换程序。

## 6. 验收

在临时 clone 里做（`TERN_HOME` 是 clone，不碰用户的 `.data/`）：用同一份代码编两个 build 不同的程序 A、B（B 加 `-buildvcs=false`）。

1. 只用 A：`A app-server start` 两次，第二次不换；`A app-server stop` 正常。
2. `A app-server start` 后跑一轮要几十秒的对话；中途 `B app-server start`：JSON 的 `notice` 写换了；pid 变了；这一轮跑完，存档里有“app-server 重启：重新接管正在运行的 agent”。
3. 接着再 `B app-server start`：build 相同，不换。
4. 一个早于 B 启动、还开着的 A 界面（chat）重连后不把 app-server 换回 A。
5. 旧版本（这次改动之前的提交，`initialize` 不带 `build`）起的 app-server：新界面启动时换掉它。

## Linux 沙箱与 web 重启（2026-10-25 修正）

`go run . web` 退出后，Go 可能删除临时可执行文件，但常驻 app-server 和已启动 agent 仍在运行。再次运行同版本 web 会复用后台；不能要求用户额外 stop/start app-server 才能执行 bash。

Linux 沙箱应从 `/proc/self/exe` 打开正在运行的程序，通过文件描述符交给 bwrap 的 `--ro-bind-data`，以只读、可执行权限挂到 `/.sandbox/agent`。不再用 `os.Executable()` 返回的可能已删除的路径作为挂载来源。文件描述符在子进程启动后或构造失败时关闭，保留现有 seccomp 与 Chrome 包装脚本的 fd 位置。前台、后台和 PTY 共用这条启动路径。

按文件改动：`internal/sandbox/launch_linux.go` 的程序来源、fd 生命周期与 bwrap 参数；根 README 的源码启动说明。无需改变自动替换判断、模型、工作区权限、数据格式或 macOS 启动方式。

验收：隔离运行目录中，先复现旧版程序路径删除后 bash 挂载失败；修复后验证删掉当前程序仍可执行前台/后台/PTY 并写文件，检查沙箱内程序只读、退出码传递与 fd 回收。实际 `go run . web` 重启后，同版本复用后台仍正常；换成新 build 后自动替换后台，原对话可继续通过真实模型写入文件。不在用户日常数据目录执行此验收。

本次隔离验收通过：旧实现删除程序后稳定复现 bwrap 路径不存在；修复后程序存在/删除、有/无 Chrome 包装时，前台、后台、PTY 和只读挂载检查通过，连续执行的 fd 数保持 8 → 8。实际启动旧 web、停止并删除其程序，再运行修复后的 `go run . web`，后台自动换成新 build，原对话通过真实模型写入文件；再次停止 web 并重新运行同版本，复用程序已删除的同一个后台进程，原对话再次通过真实模型写文件，HTTP 下载内容和 `!` 命令读取均正确。Linux vet/build、Darwin arm64 build/vet 与 diff 检查通过。
