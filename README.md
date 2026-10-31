# tern

一个用 Go 实现的 agent harness：命令行 agent、网页端、终端客户端、任务队列、评测与归因，都在同一个程序里。由 learning-agent 演变而来，保留了它的全部功能与提交历史，去掉了只为学习而设的实验入口和课程文档。

名字取自北极燕鸥（Arctic tern）：每年迁徙几万公里，对应长任务、接力与续跑。

`用户问题 → 组织上下文 → 请求模型 → tool_calls → 执行工具 → 结果回填 → 下一轮或结束`

网页侧栏顶部的活动图标可按主机、项目查看待处理、运行中和完成未读任务，并直接处理审批或提问。已读标记与通知偏好按账号保存在当前浏览器；标为已读不会批准请求，关闭全部网页后不保证收到系统通知。

## 快速开始

需要 Go 1.25.5 或以上版本。数据目录默认是 `~/.tern`（不存在就自动创建，0700），环境变量 `TERN_HOME` 可以换成别的目录。`.env`、`.data/`、`skills/`、`commands/`、`hooks/`、`retrieval/corpus.md`、`eval/cases.jsonl`、`queue/tasks.jsonl` 都相对它找；在哪个目录运行都一样，源码根目录也是。文档里没写绝对路径的文件路径都相对于它。`~/.tern/AGENTS.md` 是个人指令：所有对话都会带上，排在项目的 `AGENTS.md` 前面；评测不读。

**开发 tern 本身**：在源码目录里 `go run .` 用的也是 `~/.tern`，所以后台的 app-server 会换成开发版（规则见下文）。要用仓库里提交的评测集、示例任务、hooks，或者用 `-dev`，就把 `TERN_HOME` 指到源码根目录，例如 `TERN_HOME=$PWD go run . eval`。这是另一份数据、另一个 app-server，对话、设置、账号和 `~/.tern` 不通。

**从旧版升级**：以前在源码根目录里不设 `TERN_HOME`，数据在仓库的 `.data/`。想接着用：先在源码根目录里 `TERN_HOME=$PWD go run . app-server stop`，停掉旧的 app-server；再把 `.env` 和 `.data/` 拷到 `~/.tern`（`.data/gocache` 是编译缓存，`.data/selfuse` 是临时 clone，不用拷）。或者一直 `export TERN_HOME=<源码目录>`。

agent 自己的路径参数 `-workspace`、`-image`、`-hooks` 里的相对路径按启动时的工作目录解释；子命令（`web`、`chat`、`queue`、`eval`…）的参数按 `TERN_HOME` 解释。

`.env` 放到 `~/.tern`：

```sh
mkdir -p ~/.tern && cp .env.example ~/.tern/.env && chmod 600 ~/.tern/.env
# 在 ~/.tern/.env 中填写自己的 ARK_API_KEY
go run . -question '计算 1231+23123'
```

不带 `-question` 时从终端交互输入。启动网页端前先添加登录账号（密码从标准输入读）：

```sh
go run . web -add-user admin     # 添加登录账号
go run . web
# 打开 http://127.0.0.1:8090 登录
go run . web -remote devbox       # 或者连远端：ssh 别名见 ~/.ssh/config
go run . web -remote devbox -remote devbox2   # 一次连几台；启动后也能在主机菜单里连
```

这一版只有平台空间：登录后看到的就是 `.data/` 下的数据。早期 `users.json` 里带 `tenant` 的账号登录时会被拒绝，提示“租户账号暂不支持”。

网页端支持新对话、续聊、停止，以及每个模型 Step 的完整输入输出。子智能体、文件、改动和浏览器过程统一在右侧工作面板中以标签切换，主对话留在左边；按任务恢复阅读位置，可明确选择全宽或恢复已关闭标签。项目文件只读预览与任务产物分开；回答中的文件链接可定位行号，文件引用先加入草稿，发送后才交给 agent。运行中输入的话先显示在输入框上方，送到模型时进对话，没赶上这一轮的接着作为下一条消息发；“停止并发送未采纳内容”会正常停下当前轮再只发送未采纳内容，点停止则保留到暂停队列。补充由服务端持久接收，刷新或多开网页不会重复发送；采纳未知时先核对记录。运行时点“排队”是排到这一轮结束之后再发（Tab 用于键盘焦点导航），队列存在 app-server 里，所有打开这条对话的页面都看得到；排着的时候待发送区列出“这一轮结束后发（N）”，支持编辑、删除和上下移动；冲突会保留编辑草稿。停下以后队列保持暂停，“发第一条”只发送一项，“继续全部”才恢复后续自动发送。重启后保留暂停状态，发送结果未知时先查询回执。输入框左下为附件和权限，右下的模型菜单统一选择供应商、具体模型与推理强度；已有任务沿用实际模型，推理强度（高、中、低、最低）每轮都可以换，默认值在设置页里。对话可以改名、归档、删除（左侧列表每行右边的“⋯”）：已归档的默认收在列表最后的“已归档（N）”里；删除会删掉这条对话的存档、各轮的检查点与快照、浏览器画面，以及按第一轮存的沙箱目录、工具输出与会话记录，不能撤销。对话还可以定一个目标：在顶栏任务标题菜单里点“＋ 定个目标”，已有目标同时显示在输入框上方并可点击修改，留空就是清除；改了以后下一轮开始时告诉模型一次，正在跑的这一轮不打断。左侧列表标出在等你审批、等你回答的对话（标题前的小标签）和上次看过以后又有新结果的对话（行尾的小圆点；“看过”只记在这个浏览器里），有在等的对话时标签页标题前面加条数；设置页的“通知”能打开浏览器通知，页面不在前台时对话跑完、停在等你审批或回答就提醒。列表上面的搜索框能搜对话的名字、问题和回答（不区分大小写，已归档的也搜，结果按最近活动排，片段里命中的字加粗），点一条打开那条对话，清空搜索框或按 Esc 回到原来的列表；搜索框旁边的工作区筛选只显示某个工作区（或“无工作区”）的对话，选择记在这个浏览器里。右侧“改动”面板可以选择工作目录、未暂存、已暂存或相对指定分支共同祖先的提交差异；默认工作目录包含各轮与人自己改的、未跟踪的文件。输入框上方的摘要可打开并刷新工作目录改动。行号或“文件反馈”可先保存意见，再核对版本并加入输入草稿，实际发送后才交给 agent。每轮末尾列出这一轮改过的文件，点“看改动”直接展开。顶栏的“后台命令（N）”看模型这一轮用后台方式跑的命令（`bash` 带 `background=true`）：列表里有命令、状态（在跑 / 已结束，退出码 N / 已停止：原因）和跑了多久，在跑的可以“看输出”（每 2 秒刷新，只看不消费，模型自己 `bash_output` 照样读到）和“停止”（先确认，写审计 `user_job_kill`）；这一轮结束后列表还在，没有按钮。输入框里敲 `@` 搜工作区的文件（`git ls-files` 的结果，按路径子序列匹配），选中插入路径。对话停下时，输入框里 `!命令` 在这条对话的沙箱里跑一条命令（和模型的 bash 同一个沙箱，工作区按这条对话的权限模式可写或只读），输出作为一张卡片留在对话里，下一轮模型也看得到；运行中发 `!` 会提示等这一轮结束。运行中按 Esc 和点停止一样（待发送区里有话时是“立即发送”：停下这一轮，这些话作为下一条消息发出去）。每轮回答下面的“复制”把这一轮的 Markdown 原文写进剪贴板；顶栏的“导出”把整条对话存成一个 Markdown 文件（每轮的问题、插话、用到的工具次数与回答，不含工具的参数与结果），在页面里拼，不经服务端。顶栏的“旁支提问”（对话停下时才有）从最后一轮临时分出去问一句，问答不进原对话；旁支在已归档里，前面标“旁支”，打开时最上面一条横幅可以返回原对话。网页端是 app-server 的客户端：它自己启动时先做一次 `app-server start`，之后所有对话、设置、文件都在 app-server 里，网页端退出不影响正在跑的对话。网页端没有 `-dev` 了，要从源码跑 agent 就 `TERN_HOME=$PWD go run . app-server -dev` 或 `TERN_HOME=$PWD go run . app-server start -dev`（要求 `TERN_HOME` 就是 tern 源码根目录，否则报错退出）。
输入草稿按当前账号、主机和任务保存于本浏览器；切换任务或刷新后恢复，多标签页冲突时可保留或合并。普通发送和续聊带请求 ID，网络结果未知时保留草稿并查询服务端回执，不能直接重复发送；事件流重连按已收到的序号补齐。草稿不是跨设备同步，首次初始化或浏览器存储异常时会明确提示。

项目树按执行主机和目录身份组织任务，项目改名后历史任务仍归属该项目；筛选也按该身份匹配。同名但不同目录不会合并，副本任务关联原项目并保留实际副本目录。项目行可新建任务、改名或移除登记（不删目录和历史）；任务菜单的置顶按登录账号保存。顶栏显示实际执行主机、目录与原目录/隔离副本，已有任务的目录固定。

网页侧栏以任务为主，设置与“工具与更多”位于底部；Skills、MCP、记忆、评测、审计和导入仍可从这里打开。“搜索与操作”（Cmd/Ctrl+Shift+P）可以查找操作、任务全文、当前任务历史与项目文件。底部“外观”可选浅色、深色或跟随系统。子任务总览按任务身份汇总，区分后台启动与真正完成，详情展示上下文来源和结果；缺少终止记录时显示待确认。完整产品改造进度见 [web-product 实现记录](spec/web-product/08-implementation.md)。

起 agent 用哪个程序：app-server 起 agent 用 `/proc/self/exe`（没有 `/proc` 时用 `os.Executable()`），也就是正在跑的那个程序本身。**改了代码以后重新起界面就行**：界面（网页端、chat、`app-server start`）启动时发现后台的 app-server 是另一个程序（build 不同），并且比自己先启动，就把它换成自己的程序。正在跑的对话不打断：agent 是独立进程，由新的 app-server 接管，这一轮仍用旧程序跑完，下一轮用新的。换了会打一行“后台的 app-server 换成了这次启动的程序（build … → build …）”。

- build 是可执行文件内容的 sha256 前 12 位：同一份源码编两次结果相同，不会白换。
- 从源码更新网页端：先停掉原来的 web，再在源码目录执行 `go run . web`；不需要另外停止 app-server。同版本复用后台也能继续运行 Linux 沙箱命令：即使 Go 清理了旧的临时可执行文件，沙箱仍从运行中的程序读取。已启动的旧版 agent 不会被替换，这轮结束后再用新版继续。
- 最后启动的界面说了算：已经开着的旧界面重连时不会换回去。所以先开着旧网页端、再 `go run . chat`，网页端也就用上了新的 app-server（协议版本 `Protocol` 没变时）。
- 有评测在跑时不换（试次不能接管），打一行原因；评测跑完后再起界面就换。
- 新程序改了 `Protocol`：旧的 app-server 握手时拒绝，照样换掉；还开着的旧界面会一直“重连中”，要重启。
- 改了 agent 和 app-server 之间的协议（`session/attach`、模型代理）的那一次，先等正在跑的轮次结束再起新界面，否则接管不上。
- `-dev` 照旧：每轮用 `go run .` 起 agent，不换界面也能用上改过的 agent 代码。程序相同时不会因为 `-dev` 换模式；换程序时旧的是 `-dev` 就保留。

### 分叉与撤销

每轮问题下面有“撤销”“编辑”和“分叉”。三种都要求这一轮有检查点、对话已经结束、不是导入的对话或评测的试次；“撤销”还要求对话有工作区（撤销改的是工作区里的文件），“编辑”和“分叉”不要求。

- **分叉**：从这一轮之后另起一段对话，前面各轮带过去，原对话不变、文件不动。新对话在列表里带“分叉”标记，第 n 轮后面有一条横幅链回原对话；`thread/fork` 只复制存档里的事件（`seq` 不改），最后追加一条 `fork` 事件，新对话的摘要里 `fork_of` 是原对话的 ID。
- **撤销**：对话框里选三种退法——文件和对话一起退（默认）、只退文件、只退对话；换一种就按这个 `mode` 重新预览，有冲突只挡前两种。只退一边之后记录和磁盘上的文件对不上，下一轮的 `turn/start` 用 `notes` 告诉模型这些文件还在（或已经恢复）。
- **编辑**：每一轮的问题下面还有“编辑”，对话框里是这一轮原来的问题，改完点“撤销并重新发”：先按选的退法撤销这一轮及之后，再用改过的问题接着问（权限模式、推理强度取输入框旁边当前的选择，不带附图）。没有工作区的对话只有“只退对话”能选；撤销成功、这句没发出去时，内容回到输入框，输入框下面写“没发出去：原因”。
- 限制：分支和原对话共用工作区与 bash 沙箱，所以分叉时会把原对话之后各轮改过的文件列进提醒；分叉时带过来的轮次只能在原对话里撤销（`409`）；分支跑过一轮以后，原对话不能再撤销分叉点之前（含）的轮次，agent 报“之后有 2 个分支”；分支还没跑过、原对话先撤销到了分叉点之前，分支续聊回 `409`“接着的那一轮已经撤销”。chat 的 `/fork <n>`、`/rewind <n> [both|files|chat]` 与网页端是同一套。

### 远程（SSH）

`go run . web -remote devbox`、`go run . chat -remote devbox`：界面在本机，对话、agent、代码、模型密钥都在远端。本机用系统的 ssh 连远端，`ssh -T -o BatchMode=yes devbox <命令>`，<命令> 是 `exec "$SHELL" -lc 'bin="$HOME/.tern/bin/tern"; [ -x "$bin" ] || bin=tern; exec "$bin" app-server proxy'`，用它的标准输入输出说协议：不开端口、不需要公网，鉴权就是 SSH，也不需要在本机放模型密钥。登录 shell 里优先用 `~/.tern/bin/tern`（网页里“装到远端”装的就是那里），没有才用 PATH 上的 `tern`。

远端的准备：装好 tern（协议版本和本机一致，对不上时 `initialize` 会报出两边的版本；远端重新安装以后，下一次连接时远端的 `proxy` 会把那边的 app-server 换成新装的，规则同上），tern 在登录 shell 的 PATH 上或者 `~/.tern/bin/tern` 里（proxy 走登录 shell，`~/.profile` 里设的 PATH 和 `TERN_HOME` 才生效），`TERN_HOME` 里有 `.env`（模型密钥），工作区在远端的设置页里登记（网页端的设置页改的就是远端的设置）。

第一次连某台机器要在终端里手动 `ssh devbox` 一次（确认主机指纹、登录），之后的连接带 `BatchMode=yes`，不会在后台卡在输密码或确认指纹那一步。下面这几样也可以在网页的主机菜单里补（只在网页端，chat 没有）：

- 远端没装 tern（菜单写“远端找不到 tern”），或者已连上的那台程序不是本机的：菜单里有“装到远端”“用本机的 tern 更新远端”，把本机正在跑的程序拷到远端的 `~/.tern/bin/tern`。只拷同系统同架构的，不同会提示在远端安装（在源码目录里 `go install .`）；装完自动重连，远端的 proxy 会把那边的 app-server 换成新的，正在跑的对话不会被打断。
- 第一次连这台机器（菜单写“还没确认过这台机器的主机指纹”）：点“确认指纹”，列出 `ssh-keyscan` 取到的公钥类型与指纹，和你在那台机器上看到的一致再确认，就写进本机的 `~/.ssh/known_hosts`（写入前再扫一次核对，中途换了就不写）。
- 远端没有配模型密钥（菜单写“远端没有配模型密钥”）：点“从本机复制密钥”，勾上本机 `.env` 里有值的几个（`ARK_API_KEY`、`DEEPSEEK_API_KEY`…），确认后经 ssh 的标准输入写进远端 `TERN_HOME` 的 `.env`（已有同名的行替换、没有的追加，权限 0600；那台机器上能读你文件的人都看得到），新起的对话就生效。
- 连不上的原因会写明是哪一步：SSH 认证失败（`auth`）、还没确认主机指纹（`hostkey`）、解析不了或连不上（`unreachable`）、远端找不到 tern（`no-tern`）、两边的协议版本不一致（`protocol`），认不出的只给 ssh 的原文（`other`）。网页端 `POST /hosts` 连不上回 502 与 JSON `{error, kind, hint, detail}`。

`-remote` 只认 `~/.ssh/config` 里具体的 Host 别名或 `user@host`：带通配符的 `Host` 会匹配到别的机器，以 `-` 开头的会被 ssh 当成选项，都拒绝。
远程模式下网页端内置的计算器 MCP（`/mcp`）在本机的网页端进程里，远端的 agent 连不到，这一期不处理：要用就在远端另起一个网页端。

连接断了（ssh 断了、远端的 app-server 重启了）界面自己重连，本机模式和远程模式一样：按 1、2、4…秒退避，最多 30 秒；本机模式每次先 `app-server start`，远程模式重新起 ssh。重连期间那台的请求立刻失败，网页端回 503“正在重连 devbox”，顶栏和左侧列表显示“devbox · 重连中”（别的照常）；正在看的事件流不会断，连上后按每条订阅最后收到的序号重新补齐，既不重复也不缺。启动时第一次就连不上直接报错（带上 ssh 的报错），不进入重连。

### 几台主机

网页端可以同时连几台主机，每台一条连接；页面一次操作一台（当前主机）：新对话、设置、Skills/MCP、评测都是当前主机的。

- 启动：不带 `-remote` 连本机；`-remote` 可以写多次，写了几台就只连这几台、不连本机，任何一台连不上就报错退出。
- 主机菜单（点顶栏的主机与状态）：连着的各台点一下整页跳过去，每台后面有“断开”（只剩一台时没有），断开就关掉连接、也从记住的列表里删掉（命令行连的那几台也能断开）；连不上的（`state` 是 `failed`）显示那次 ssh 的报错，点它就是再连一次；下面是候选（`~/.ssh/config` 里具体的 Host 别名，本机没连时还有“本机”）和一个输入框，可以手填别名或 `user@host`。
- 记住的主机：页面上连过的记在 `.data/web-hosts.json`（`{"hosts": ["devbox", ""]}`，空串表示本机；权限 0600，所有账号共用、不分个人）。下次启动先连命令行定的那几台，再在后台一台台连记住的：连不上不挡启动，那一台记成“连不上”，等菜单里再试。`-remote` 指定的只管这一次，不写进文件。
- 地址：本机的页面与接口不变（`/`、`/runs`、`/?run=…`，旧链接照样能开）；远端主机的页面与数据接口带 `/h/<别名>` 前缀，如 `/h/devbox/runs`、`/h/devbox/?run=…`。没连上的主机回 404“没有连上主机 devbox”，记住了但这次没连上的回 502（文字是那次的报错）；只带了 `-remote` 时本机没连，`GET /` 跳到第一台。
- 左侧对话列表按主机分组：当前主机在最上面，每组标题写“主机 · 状态”；点别的主机的对话整页跳到那台的地址。观测页的“加入对比”只在当前主机这一组。
- 断开当前主机时，正在看的事件流随之结束，页面跳到剩下的第一台。
- 工作区：设置页的“浏览…”和新对话工作区下拉的最后一项“＋ 添加工作区…”都逐层浏览目录，把选中的目录登记成工作区；远端主机的页面浏览的是远端的目录，登记进远端的设置。对话框打开时在目录列表上面列出“可能要找的仓库”（已登记工作区的上一级和家目录下的 `workspace`、`code`、`src`、`projects`、`repos`、`dev` 里还没登记的 git 仓库，按目录修改时间排，最多 20 个），点一下跳到那个目录；设置页在“浏览…”下面列前 5 个，每个后面一个“登记”。工作区可以改名（设置页每个工作区后面的“改名”）：只改登记的名字，平台层与个人层里的“默认工作区”跟着改，已有对话照旧显示原来的名字、续聊用的还是那个目录。不是 git 仓库的目录也能登记（对话框里“git init 后登记”，或者确认后“不用 git，直接登记”）：代价是撤销只能退回 `edit_file`、`write_file` 改过的文件，bash 改的退不回来，改动面板也看不了；设置页把这样的工作区标成“不是 git 仓库”。新对话时还可以勾上工作区旁边的“副本”：在 `.data/copies/<工作区名>-<对话ID 前 8 位>` 里开对话（原目录的提交与没提交的改动都带过去，先提交成基线），原目录一点不碰；列表里副本对话前面标“副本”，停下以后任务标题菜单给“应用到 <工作区名>”，点了把副本里的改动写进原目录的工作区（不提交；对不上时回冲突，把 git 的报错显示出来），改动面板看的是副本相对基线的改动。删除副本对话时副本目录一起删掉。

装到 PATH 上：

```sh
go install .          # 装到 $(go env GOPATH)/bin/tern；这个目录在 PATH 上，就能在任何目录用 tern
tern -question '计算 1231+23123'
```

文档里的 `go run .` 都可以换成 `tern`；改了代码要重新 `go install .`。

## 模型与工具配置

配置读取 `TERN_HOME` 下的 `.env`，同名环境变量优先。密钥只写入本地私有配置，保持文件权限为 0600；`.env` 已忽略提交。

| 模式 | 配置项 | 默认值 |
| --- | --- | --- |
| 方舟聊天模型 | `ARK_API_KEY`（兼容 `LLM_API_KEY`）、`LLM_API_URL`、`LLM_MODEL` | `-provider ark`；`https://ark.cn-beijing.volces.com/api/coding/v3/chat/completions`；`doubao-seed-2.1-pro` |
| DeepSeek 聊天模型 | `DEEPSEEK_API_KEY`、`DEEPSEEK_API_URL`、`DEEPSEEK_MODEL` | `-provider deepseek`；`https://api.deepseek.com/chat/completions`；`deepseek-flash` |
| DeepSeek（Anthropic 接口） | 与 DeepSeek 共用 `DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`；`DEEPSEEK_ANTHROPIC_URL` 可改地址 | `-provider deepseekanthropic`；`https://api.deepseek.com/anthropic/v1/messages`；`deepseek-flash`；Anthropic Messages 协议 |
| Coconut 聊天模型 | `COCONUT_API_URL`（必填，指向本机 Codex 兼容服务的 `.../v1/responses`）、`COCONUT_MODEL`；不要密钥 | `-provider coconut`；没有默认地址；`coconut-gpt-6-1-sol-max`；走 Responses 协议 |
| 线上向量模型 | 与方舟共用 `ARK_API_KEY` | `-embedding ark` |
| 本地向量模型 | 不需要 embedding API 密钥；首次使用会下载模型 | `-embedding local`；聊天请求仍使用选定的线上模型 |
| 浏览器 | 本机安装 Chrome/Chromium；`CHROME_PATH` 可指定可执行文件 | `-browser`；仅环境变量读取浏览器配置 |
| DeepSeek 搜索 | 与 DeepSeek 聊天模型共用 `DEEPSEEK_API_KEY`；Search/Fetch 按成功请求计费 | `-ds-search`；与选用哪家聊天模型无关 |
| Bash 沙箱 | Linux 用 bubblewrap（cgroup 资源限制依赖 systemd 用户会话），macOS 用 `sandbox-exec`（Seatbelt，不限制内存与 CPU）；后台命令可以用 `bash_input` 写输入，`tty=true` 开伪终端（需要 `script`）。`-bash-host` 是「完全访问」：命令直接在宿主机上跑，没有沙箱 | `-bash` |
| 网页端登录 | 账号在 `.data/users.json`，用 `go run . web -add-user` 添加 | 没有账号时网页端拒绝启动 |
| 调用链追踪 | 无需配置，span 写入 `.data/traces/`；`OTEL_EXPORTER_OTLP_ENDPOINT` 另发给 Jaeger 等 OTLP 平台；`OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true` 采集提示词与工具内容 | 默认只记元数据 |

豆包的地址有两套，不能混用：默认的 `.../api/coding/v3` 是方舟 Coding Plan 订阅入口，模型名用套餐里的 Model Name（如 `doubao-seed-2.1-pro`、`doubao-seed-2.1-lite`、`kimi-k3`、`glm-5.3`）或 `ark-code-latest`（跟随控制台“开通管理”的模型选择，切换后 3–5 分钟生效）；`.../api/v3` 是按量付费入口，模型名带日期版本号（如 `doubao-seed-2-1-pro-260628`）。方舟文档说明 `.../api/v3` 不消耗套餐额度、照按量计费，所以地址不能混用；模型名按文档用 Model Name——套餐入口实测也接受带日期版本号的旧名字，但文档没有列出，是否计入套餐以控制台用量为准。`ark-code-latest` 的响应里 `model` 是 `auto`（实测），看不出实际是哪个模型回答的，评测与归因要固定模型时选具体名字。`.env` 里的 `LLM_API_URL`、`LLM_MODEL` 会覆盖默认值，以前填过老地址老模型的要一起改掉或删掉这两行；启动输出里的 `Model: provider=ark model=... upstream=...` 可以核对实际用的地址与模型。套餐额度按 5 小时、周、月分档，用完返回 429（`hour / week / month allocated quota exceeded`），不是地址配错。

方舟套餐里的模型可以换：命令行用 `-model kimi-k3`（`.env` 或环境变量里的 `LLM_MODEL` 改的是不加 `-model` 时的默认值）；网页端分两级选：一级是供应商与接口（豆包、DeepSeek、DeepSeek 的 Anthropic 接口、Coconut），二级才是具体模型，只有一个模型的供应商不显示第二级。设置页的“默认值”里同样是这两级（`provider`、`model`，`model` 空表示用供应商默认值），新对话在输入框不选就用它；页面另选以页面为准。清单在路由表 `llm.Provider.Models`，经 `GET /models` 给页面，14 个名字 2026-10-20 逐个真实调用过（含一次工具调用）。app-server 以 `-model` 传给 agent，它和其他启动参数一起写进检查点，所以一条对话固定一个模型：续聊、中断续跑、归因回放和子 agent 都用同一个。上下文窗口默认按模型（取路由表里这个模型核对过的输入上限），在设置里填了值就以设置里的为准，所以换窗口更小的模型时要检查这一项。

默认使用原生 Tool Calling：工具定义在请求的 `tools` 字段，模型返回 `tool_calls`，回答不限制文本格式。默认没有常驻工具；检索、记忆、MCP、Skills、子 agent、浏览器、DeepSeek 搜索、Bash、编码工具按需开启，日期（Asia/Shanghai）在环境信息里。`-disallowed-tools` 去掉其中单个工具（逗号分隔，只有浏览器的 web_search、open_page；名字与 Claude Code 的 `--disallowed-tools` 一致，但只认工具名，按调用拦截用 `-deny` 规则）；网页端在设置里逐个开关，默认关 web_search，搜索用 ds_search，打开网页用 open_page。

推理强度默认 `high`。API 输出上限默认省略，显式设置 `-max-output-tokens` 才发送；本地上下文规划仍保留输出空间，省略 API 参数不代表无限输出。向量检索直接在 agent 进程内执行。

设置分四层：内置默认 › 平台 › 项目 › 个人，后面的覆盖前面的。项目层是工作区根目录的 `.agents/tern.json`，一个扁平的 JSON 对象，键和设置页一样、值都是字符串，例如：

```json
{"provider": "deepseek", "reasoning_effort": "medium", "web_search": "off",
 "permission_rules": "deny Bash(git push *)", "net_allow": "proxy.golang.org", "max_steps": "200"}
```

能放 `provider`、`model`、`reasoning_effort`、`permission_mode`、`permission_rules`、`net_allow`、`max_steps`、`context_window`、`tool_output` 和工具开关（`rag`、`memory`、`skills`、`mcp`、`subagents`、`ask_user`、`bash`、`web_search`、`open_page`、`ds_search`、`coding`）。`permission_rules`、`net_allow` 是追加不是覆盖：项目的规则接在平台规则后面，白名单取并集。`hooks` 会在宿主机上跑命令，只能在平台设置里配；`workspace`、`embedding`、`memory_ttl` 也不收。不生效的键、值不合规的那一项、不是合法 JSON 的整份文件都不生效，设置页的“来源”里会说明；工作区列表里给这些工作区标“有项目配置”，悬停列出生效的键。项目配置只在对话选了这个工作区时生效：新对话开始时读一次、拍进快照，续聊照快照不重读；已登记的工作区就当作信任，第一轮之前页面顶部一条横幅说明它改了哪些设置，不拦。chat 与命令行 agent 不读项目配置。

## 代码结构

一个 Go 模块、一个入口 `main.go`，代码按功能放在 `internal/` 下。

| 部分 | 文件或模块 | 关注点 |
| --- | --- | --- |
| 入口 | [main.go](main.go) | 参数、工具开关、agent / web / app-server / queue / metrics / eval / audit 的入口 |
| 模型协议 | [internal/llm](internal/llm/) | messages、tools、tool_calls、请求预算与用量 |
| 执行循环 | [internal/agent/react.go](internal/agent/react.go)、[工具分发](internal/agent/dispatch.go)、[确认与循环检测](internal/agent/approval.go) | 结果回填、并发、分类重试与终止；高风险确认、私有数据标记、结果日志与无进展检测 |
| 上下文与恢复 | [上下文管理](internal/agent/context_manager.go)、[检查点](internal/agent/checkpoint.go) | Transcript/View、压缩、写前日志、续跑 |
| 知识与记忆 | [retrieval](internal/retrieval/)、[memory](internal/memory/) | 向量检索、规则写入、两路召回与重排 |
| 工具扩展 | [mcp](internal/mcp/)、[skills](internal/skills/) | 协议服务、按需加载知识 |
| 多任务执行 | [queue](internal/queue/)、[子进程](internal/agent/child.go)、[子 agent](internal/agent/subagent.go) | worker、任务 ID、独立上下文与权限继承 |
| 编码工具 | [workspace](internal/workspace/)、[permission](internal/permission/) | 读、找、搜、改；`os.Root` 路径边界、读后才写的版本核对、输出预算；项目指令、环境信息与系统提醒；权限规则与模式 |
| 扩展点 | [hooks](internal/hooks/)、[commands](internal/commands/) | 生命周期 hooks（配置与脚本在 [hooks/](hooks/)）；斜杠命令模板（内置的 [builtin](internal/commands/builtin/)、`TERN_HOME` 的 `commands/` 与工作区的 `.agents/commands/`，同名时后面的覆盖前面的） |
| 外部执行 | [browser](internal/browser/)、[sandbox](internal/sandbox/)、[netguard](internal/netguard/) | 页面读取、进程隔离、网络与资源边界、出口地址判断 |
| 常驻服务 | [appserver](internal/appserver/)、[appclient](internal/appclient/) | 控制 socket 上的协议方法、对话与设置等数据、模型代理、生命周期（`initialize`、`server/shutdown`）；界面一侧的 `start`、`stop`、`proxy` 与 `Call`、`Subscribe` |
| 网页端 | [web](internal/web/)（页面在 [page](internal/web/page/)，原生 ES 模块）、[protocol](internal/protocol/)、[telemetry](internal/telemetry/)、[metrics](internal/metrics/) | 登录、页面、把 HTTP 接口翻成协议方法；agent 与界面的结构化协议、OpenTelemetry span 与导出、核心指标 |
| 评测与归因 | [eval](internal/eval/)、[评测集](eval/cases.jsonl)、[网页端评测页](internal/web/page/eval.js) | 题目与打分器、多次尝试与 pass^k、评审模型、消融与基线对比、归因卡片与回放验证 |
| 审计 | [audit](internal/audit/) | 只追加的审计记录、哈希链与校验；agent 与 app-server 共同写入 |
| 多租户与登录 | [tenant](internal/tenant/)、[网页端登录](internal/web/auth.go) | 按租户决定数据路径（`queue` 与 agent 的 `-tenant` 仍在用）；网页端这一版只有平台账号：账号、密码哈希与会话 |

一个 agent 进程运行一个任务，功能开关由入口设置。app-server、任务队列和子 agent 用独立子进程执行任务；检索等工具直接在该 agent 进程内调用。

## 参数速查

| 常用参数 | 默认值 | 作用 |
| --- | --- | --- |
| `-question` | 交互输入 | 本轮任务 |
| `-image` | 空 | 随问题附带的图片（PNG、JPEG、GIF、WebP），可写多次，每条消息最多 8 张；网页端输入框可附图 |
| `-provider` | ark | 选择方舟或 DeepSeek |
| `-model` | 空 | 这家下面的具体模型（如 `kimi-k3`）；空时用 `LLM_MODEL` / `DEEPSEEK_MODEL` 或路由表默认值；网页端是输入框旁的第二个下拉 |
| `-max-steps` | 0 | 模型请求预算，含摘要与辅助调用；0 表示不限（靠重复动作/无进展熔断与 Ctrl-C 收住）；队列与评测任务没给预算时补 10。设了上限时，最后一次请求不能调用工具，用来给最终回答 |
| `-parallel` | 4 | 独立工具并发上限 |
| `-rag` / `-memory` | false | 按需启用知识检索或长期记忆 |
| `-resume` | 空 | 按检查点 ID 续跑，配置沿用检查点 |

<details>
<summary>展开全部 agent 参数</summary>

| 参数 | 默认值 | 用途 |
| --- | --- | --- |
| `-question` | 交互输入 | 交给模型的任务 |
| `-image` | 空 | 图片输入：随问题附带的图片路径，可写多次。按文件头只收 PNG、JPEG、GIF、WebP；长边超过 2048 或 32×32 块超过 2500 时等比缩小；按内容寻址存进 `.data/[tenants/<租户>/]attachments/`，消息与检查点里只放 `attachment://` 引用；处理不了的图以提醒说明，不让这一轮失败；每条消息最多 8 张；所选模型不能看图时发请求前换成说明文字。能看图的模型在有工作区或 bash 时另有 `view_image`，看工作区或沙箱里的图片 |
| `-history-stdin` | false | 从stdin接收上次上下文JSON，配合-question续聊（命令行用；网页端改经 `turn/start` 传入） |
| `-continues` | 空 | 续聊时上一轮的检查点ID：本轮用新ID并记下续接关系，子 agent 可按 task_id 引用之前各轮的子任务；网页端自动处理 |
| `-agent-server` | false | 以结构化协议与界面通信（JSON-RPC，一行一条）：stdout 只走协议，日志改走 stderr；app-server 与父 agent 启动子进程时使用。app-server 另给环境变量 `AGENT_APP_SOCKET`，协议与日志改走这个 Unix socket，app-server 重启后能接管 |
| `-parallel` | 4 | 工具执行并发，1到16 |
| `-max-steps` | 0 | 模型请求总预算，包含最终回答与上下文摘要；0 表示不限。设了上限时，最后一次请求不能调用工具，用来给最终回答 |
| `-retries` | 2 | 工具额外重试次数，0到5 |
| `-context-window` | 0 | 总窗口；0 表示按模型取路由表里核对过的输入上限（取不到时 262144），显式给值时才用给的值 |
| `-max-output-tokens` | 0 | 0省略API输出上限；大于0才传max_completion_tokens（含推理） |
| `-reasoning-reserve` | 1024 | 输入容量额外安全余量 |
| `-tool-output-tokens` | 2000 | 工具结果进入View时的粗估上限；超过时完整内容存进 `.data/outputs/`，视图里放预览，模型用 `read_output` 读回 |
| `-keep-groups` | 2 | 最近组超20%配额或挤占摘要空间时逐步减少，可到0 |
| `-reasoning-effort` | high | 所有请求统一的推理强度：minimal、low、medium、high |
| `-rag` | false | 增加 search_docs，项目知识回答先检索并引用来源 |
| `-embedding` | ark | 向量模型：ark线上方舟，local本地纯Go MiniLM；search_docs 与长期记忆共用 |
| `-min-score` | 0.40 | 检索相似度阈值，范围−1到1 |
| `-search-docs` | 空 | 只执行检索，不调用聊天模型；local无需密钥，ark使用同一ARK_API_KEY |
| `-k` | 3 | 配合 search-docs，最多返回1–6条候选 |
| `-memory` | false | 跨进程长期记忆：开头经背景补充、两路召回、RRF、重排后调入，结尾写入原文与来源，增加 remember_memory、search_memory 与 forget_memory |
| `-memory-ttl` | 0 | 本轮“记住”写入的有效期，如 `1m`；0 不过期 |
| `-memory-limit` | 200 | 记忆总量上限，超出时淘汰重要性×新近度最低的一条 |
| `-memory-forget` | 空 | 只删除指定编号的记忆后退出，如 `M3`；不做容量淘汰，不需要密钥 |
| `-memory-search` | 空 | 只执行一次记忆检索，stdout 输出各阶段 JSON；配合 `-memory-mode` 对比 |
| `-memory-mode` | rerank | memory-search 的方式：keyword（旧关键词）、vector、bm25、hybrid（RRF）、rerank（完整流程） |
| `-memory-context-calls` | 4 | 本次运行最多几次背景生成；0 表示全部按原文检索 |
| `-memory-tokens` | 800 | 注入或返回的记忆估算 token 预算，另有最多5条的上限 |
| `-mcp-server` | 空 | MCP server：`http(s)://…` 地址连远程 server（Streamable HTTP），否则当作子进程命令（stdio，按空格切分，不经过shell）；可重复给多个；配合 `-question` 时把它们的工具注册为 `mcp_*`，连不上的 server 打印 `MCP error` 后跳过；要求 OAuth 的远程 MCP 先在网页端的 MCP 中心点“登录”（令牌存在 `.data/mcp-tokens.json`），没登录时这个 server 跳过并提示去登录；server 提供 resources 时另加两个工具 `list_mcp_resources`、`read_mcp_resource`；`-mcp-list`/`-mcp-raw` 只接受一个 |
| `-mcp-list` | false | 只用 SDK 连接 `-mcp-server`，打印协议版本、capabilities 与工具列表，不请求模型 |
| `-mcp-raw` | 空 | 不用 SDK，手写 JSON-RPC：`legacy`（initialize 握手）或 `modern`（server/discover + 每请求 `_meta`），逐行打印收发 |
| `-mcp-call` / `-mcp-args` | 空 / `{}` | 配合 `-mcp-list` 或 `-mcp-raw` 再调用一次指定工具 |
| `-mcp-serve` | false | 作为 MCP server（stdio）运行，暴露 calculator；须单独使用 |
| `-mcp-http` | 空 | 配合 `-mcp-serve`，改用 Streamable HTTP 在该地址提供 `/mcp`（远程 MCP），如 `127.0.0.1:8091` |
| `-skills` | false | 扫描内置的与 `skills/*/SKILL.md`，system 中放索引，增加 load_skill |
| `-skill` | 空 | 配合 `-skills`，只启用这个名字的 skill（可重复）；不给则启用全部 |
| `-skills-list` | false | 只扫描内置的与 `skills/`，stdout 输出每个 skill 的元数据、正文与校验错误（JSON），不请求模型；须单独使用 |
| `-timeout` | 0 | 整次运行的时限，如 `2m`；0 不限，仍可 Ctrl+C |
| `-tool-timeout` | 30s | 其他工具单次尝试的时限；到时记为结果未知，只读工具才重试（bash 用 `-bash-timeout`） |
| `-bash-timeout` | 2m | bash 单条命令的时限，大于 0、不超过 `10m`；到时沙箱终止整条命令，返回终止前的输出；写在 bash 的工具描述里 |
| `-task-id` | 随机 UUID | 检查点ID（字母、数字和 `._-`）；已存在时拒绝启动，提示改用 `-resume` |
| `-resume` | 空 | 从该ID的检查点续跑，配置取自检查点；须单独使用；已完成的直接返回存档答案 |
| `-subagents` | false | 增加 spawn_agent：把独立子任务交给全新上下文的子 agent（子进程），每次运行最多4个 |
| `-tenant` | 空 | 多租户：本任务所属的租户，数据都在 `.data/tenants/<租户>/` 下；带 `tenant` 的队列任务自动加上（app-server 启动 agent 时不加，这一版只有平台空间）；可与 `-resume` 一起用 |
| `-private-input` | false | 任务输入已含私有数据，外发类工具执行前要确认；父 agent 派出子任务时自动加上，一般不手动使用 |
| `-subagent-steps` | 6 | 每个子 agent 的模型请求预算，1到20 |
| `-provider` | ark | 模型路由：`ark`（豆包，方舟 Coding Plan）、`deepseek`、`deepseekanthropic`（DeepSeek 的 Anthropic 接口）或 `coconut`（本机 Responses 服务）；子 agent 继承，续跑沿用 |
| `-model` | 空 | 这家供应商下的具体模型，优先于 `LLM_MODEL` / `DEEPSEEK_MODEL`；写进检查点的启动参数，子 agent 继承，续跑与回放沿用 |
| `-bash` | false | 增加 bash 与 request_network_access，命令在沙箱里执行（Linux 用 bubblewrap，macOS 用 sandbox-exec）；工作目录按对话保留，续聊各轮共用，网页端可上传、下载 |
| `-bash-host` | false | 配合 `-bash`：命令直接在宿主机上跑（完全访问，没有沙箱）。网页端的权限下拉选「完全访问」时带这个参数；也可以单独与任意 `-permission-mode` 组合。校验命令（`-check`、`verify_feature`）仍进沙箱 |
| `-workspace` | 空 | 增加 read_file、glob、grep、edit_file、write_file，只在这个仓库目录里读写；子 agent 继承。配合 `-bash` 时 bash 的 `/workspace` 就是这个仓库（`.git` 只读，`.env`、`.data` 看不到），本对话目录改挂 `/scratch` |
| `-personal-instructions` | true | 读 `TERN_HOME` 下的 `AGENTS.md`（默认 `~/.tern/AGENTS.md`）作为个人指令放进系统提示词，排在项目指令前面，没选工作区也带上；`TERN_HOME` 就是工作区根目录时这个文件已作为项目指令读了，不再读一遍。评测传 `false`，结果才可比；子 agent 继承 |
| `-permission-mode` | edit | plan（只读计划）、ask（改动都问）、edit（工作区内编辑与沙箱 bash 直接执行）、full（不问，含高风险确认，由程序放行并写审计；deny、ask 规则照常）。命令是否在沙箱里由 `-bash-host` 决定，full 不自动关沙箱 |
| `-allow` / `-ask` / `-deny` | 空 | 权限规则，可重复，如 `-allow "Bash(go build *)"`、`-deny "Bash(git push *)"`、`-ask "Edit(internal/agent/**)"`；顺序 deny › ask › allow；子 agent 继承 |
| `-hooks` | 空 | 生命周期 hooks 配置（Claude Code 的 hooks 格式），JSON 原文或文件路径，如 `-hooks hooks/hooks.json`；命令在宿主机上运行，只由用户提供；子 agent 继承。问题以 `/名字` 开头时按内置的、`TERN_HOME` 的 `commands/`、工作区的 `.agents/commands/` 模板展开（同名时后面的覆盖前面的） |
| `-bash-project-ro` | false | 配合 `-bash`：项目只读挂到 `/project`（`.env`、`.data`、`.git`、`bin` 除外） |
| `-net-allow` | 空 | 配合 `-bash`：沙箱可经代理访问的域名（含子域名，可重复），由用户批准后添加；网页端由设置里的“沙箱联网白名单”（`net_allow`）和审批生成。不在白名单的域名，有界面时代理当场请人批准 |
| `-browser` | false | 增加 web_search 与 open_page，用无头 Chrome 查资料；环境变量 `CHROME_PATH` 指定浏览器，系统不支持 Chrome 沙箱时设 `CHROME_NO_SANDBOX=1` |
| `-ds-search` | false | 增加 ds_search 与 ds_fetch，调用 DeepSeek 的 Search/Fetch API（服务端执行，没有画面）；子 agent 继承。 |
| `-tool-format` / `-coding-prompt` / `-todo` / `-clear-old-results` | json / default / false / false | 消融开关，默认值就是现在的做法：工具结果发给模型时用纯文本、编码提示词的 minimal 或 extended 写法、上下文内的 todo 清单、续聊时清理之前各轮的工具结果 |
| `-check` | 空 | 不请求模型，只在工作区只读挂载的沙箱里跑这条命令，最后一行输出 JSON 的退出码与输出（编码评测的校验命令用它），须配合 `-workspace` 与 `-bash` |
| `-shell` | 空 | 不请求模型，只在沙箱里跑这一条命令，最后一行输出 JSON 的退出码与输出：工作区按 `-permission-mode` 决定能不能写，沙箱目录由环境变量 `AGENT_SHELL_DIR` 指定（网页端的 `!命令` 用它，app-server 填这条对话的沙箱目录），须配合 `-bash` |
| `-ask-user` | false | 增加 ask_user，模型可以在运行中向人提问（经协议的 `user/requestInput`）；只在界面上有人能当场回答时打开，终端客户端与网页端自动加上，子 agent 沿父任务开关继承并将问题转给同一网页 |

</details>

## 内置的斜杠命令与 skill

程序自带几个斜杠命令和 skill，装好就能用，不用往 `TERN_HOME` 里拷：

| 名字 | 类型 | 做什么 |
| --- | --- | --- |
| `/review` | 命令 | 评审改动：默认是还没提交的改动，`/review main` 对比 main 分支，`/review <提交>` 评审那个提交；按严重程度列出问题，不改文件 |
| `/init` | 命令 | 看一遍工作区，生成或更新根目录的 `AGENTS.md`（项目是什么、构建与测试命令、约定） |
| `code-review` | skill | 按“先构建、再看逻辑”的固定顺序评审 Go 代码；模型判断任务相关时用 load_skill 加载 |

命令与 skill 都分三层，同名时后面的整份覆盖前面的：

| | 命令 | skill |
| --- | --- | --- |
| 内置 | 编在程序里（[internal/commands/builtin/](internal/commands/builtin/)、[internal/skills/builtin/](internal/skills/builtin/)） | 同左 |
| 个人 | `TERN_HOME/commands/` | `TERN_HOME/skills/` |
| 项目 | `<工作区>/.agents/commands/` | 没有 |

不想要内置的那份，就在个人或项目里写一份同名的覆盖它（整份替换）。skill 在 Skills 中心按名字开关，内置的删不掉、只能关掉；命令没有开关。

## 其他入口

| 命令 | 作用 |
| --- | --- |
| `go run . chat [-remote 别名] [-continue 对话ID] [-provider …] [-model …] [-permission-mode …] [-workspace 名字]` | 终端客户端，app-server 的界面：对话、agent 进程、模型请求都在 app-server 里（带 `-remote` 时在远端），网页端开的对话也能接着聊；过程流式显示，运行中输入的话作为插话；断线时打一行“连接断开，重连中…”，连上后打“已重连”，这一轮接着往下走；`/help` 列出轮次、压缩、撤销（`/rewind <轮>`，可以只退文件、只退对话）、从某一轮之后分叉（`/fork <轮>`）、切权限与批准计划、`/resume <对话ID>` 等命令；不再接受 agent 参数（工具与权限来自 app-server 的设置）；agent 的终端日志写在 `.data/chat.log` |
| `go run . web [-addr …] [-remote 别名 …]` | 网页端：网页界面，需要先用 `-add-user` 添加账号。它是 app-server 的客户端：不带 `-remote` 时连本机 app-server，启动时先做一次 `app-server start`；带 `-remote` 时经 ssh 连远端，可以写多次，写了几台就只连这几台。可以同时连几台主机（见下面“几台主机”）：顶栏显示当前主机与连接状态（重连中、连不上换成醒目的样式），点开是主机菜单；远端主机的地址带 `/h/<别名>` 前缀。启动时打印连上的各台 app-server 的主机、`TERN_HOME`、版本与 build（本机的 app-server 被换掉时先打一行）；`GET /connection` 推所有主机 `{hosts: [{alias, host, remote, state, runs, error}]}`，`GET`、`POST`、`DELETE /hosts` 是菜单用的候选、连接与断开；重连期间那台的请求回 503“正在重连 <host>” |
| `go run . app-server [-dev]` | 常驻的 app-server：前台运行，一个 `TERN_HOME` 一个，在 `.data/app-server/control.sock` 上等界面连进来（一行一条 JSON-RPC 2.0，`initialize` 握手），模型代理在同目录的 `llm.sock` 上；日志写 stderr，Ctrl-C 或 SIGTERM 与 `server/shutdown` 一样收尾：删掉两个 socket 与 pid 文件后退出。`-dev`：每次对话用 `go run .` 启动 agent，改完 agent 代码不必重启 app-server，要求 `TERN_HOME` 是 tern 源码根目录（`TERN_HOME=$PWD`） |
| `go run . app-server start [-dev]` | 幂等启动：控制 socket 能答 `initialize`、是同一个程序（或者比这个进程晚启动）就直接返回；是另一个程序、又比这个进程先启动，就让它退出，换成这个程序（见上面“改了代码以后重新起界面就行”，有评测在跑时不换）；没在运行就以新会话（setsid）在后台起一个，把 `-dev` 传给它，输出写 `.data/app-server/app-server.log`，等它能握手再返回（最多 30 秒）；子进程提前退出就不再等，报错并带上日志的最后几行。同一个 `TERN_HOME` 里同时启动的排队（`.data/app-server/start.lock`）。输出一行 JSON：socket、pid、version、build，换了或者没换的原因在 `notice` |
| `go run . app-server stop` | 发 `server/shutdown`，按 pid 文件等进程真的不在才返回（socket 与 pid 文件都已删掉）；没有在运行的 app-server 时提示“没有在运行的 app-server”，正常退出 |
| `go run . app-server proxy` | 经 SSH 时由界面调用：先做一次 start，再把标准输入输出与控制 socket 原样转发，任何一边断开就退出；stdout 只走协议 |
| `go run . mcp-server [-remote 别名]` | 把 tern 当 MCP server（stdio）：名字 `tern`，版本是程序的版本。别的 agent 客户端（Claude Code、Codex）把一件事交给 tern 做，tern 经 app-server 开对话（和网页端、chat 一样，所以对话进列表、审批在网页端答），等这一轮结束再返回 `{thread_id, outcome, answer}`。三个工具：`tern_start`（`prompt`，可选 `workspace`、`provider`、`model`、`permission`）、`tern_reply`（`thread_id`、`prompt`，可选 `permission`）、`tern_threads`（`limit`，默认 20）。等的时候每 10 秒发一次进度通知（第几次模型请求；在等审批时写“去网页端答复”），调用方取消这次调用就发 `turn/interrupt` 停这一轮。`-remote` 经 ssh 连远端的 app-server（同 `tern web -remote`）。怎么配进客户端见下面“把 tern 当 MCP server” |
| `go run . queue` | 任务队列：按 `queue/tasks.jsonl` 逐个执行，每个任务一个 agent 子进程，同一个 id 只执行一次；用仓库里的 `queue/tasks.jsonl` 要 `TERN_HOME=$PWD` |
| `go run . metrics [-since 168h] [-prefix ID前缀] [-tenant 租户] [-json]` | 统计完成率、耗时、成本等核心指标，只读 `.data/` |
| `go run . eval` / `go run . eval -cases eval/coding-cases.jsonl` | 评测：通用评测集（每题 k 次、DeepSeek 评审，需要 `DEEPSEEK_API_KEY`）或编码任务评测（每个试次一份新的仓库拷贝，校验命令在沙箱里只读运行）；`eval triage`、`eval replay` 做失败归因与回放；网页端左侧“评测与归因”是同一功能的页面；用仓库里的 `eval/*.jsonl` 要 `TERN_HOME=$PWD` |
| `go run . audit [-tenant 租户] [-task ID] [-verify]` | 列出并校验审计日志（只追加、哈希链）；网页端左侧“审计日志”是只读页面 |
| `go run . rewind [-tenant 租户] [-dry-run] [-mode both\|files\|chat] <轮次ID>` | 撤销这一轮及之后各轮用编码工具改过的文件，对话退回上一轮；`-mode files` 只退文件、`chat` 只退对话；网页端每轮问题下的“撤销”对话框（三种选法）和 chat 的 `/rewind <轮> [both\|files\|chat]` 是同一功能 |

## 把 tern 当 MCP server

`tern mcp-server` 是 stdio 上的 MCP server，让别的 agent 客户端把一件事交给 tern 做：开一条对话、等这一轮结束、把最终回答交回去。它连的是本机的 app-server（带 `-remote 别名` 时经 ssh 连远端），所以用之前网页端（或 `app-server start`）那边不用特意准备，`tern mcp-server` 自己会先做一次 start；工作区要先在网页端登记，用的时候只填名字。一轮可能跑很多分钟：客户端要支持长时间的工具调用，中途收到的是进度通知；遇到联网或工具审批时，去网页端答复（MCP 这一侧答不了审批）。

- Claude Code：`claude mcp add tern -- tern mcp-server`（远端：`claude mcp add tern -- tern mcp-server -remote devbox`）。
- Codex：`~/.codex/config.toml` 里

  ```toml
  [mcp_servers.tern]
  command = "tern"
  args = ["mcp-server"]
  ```

- 其他客户端：MCP 配置里 command 写 `tern`，args 写 `["mcp-server"]`。

这和另外两样 MCP 不是一回事：`-mcp-serve` 是 tern 作为 MCP 客户端时顺带提供的计算器 server（`internal/mcp`），网页端的 `/mcp` 是给远端 agent 用的内置 server；`tern mcp-server` 反过来，把 tern 自己给别的客户端当工具用。

## 网页任务工作流

右侧工作面板用「+」打开计划、持续 Goal、项目文件、改动、交付修改、项目操作、任务产物和子任务。任务产物里以点开头的隐藏项（`.config` 一类）默认收起，列表末尾可以展开。标签、阅读位置与草稿按账号、执行主机和任务保存在本浏览器；换设备不会自动同步。搜索面板支持任务全文、当前任务历史和项目文件快开。输入 `$` 可显式选择已启用 Skill，发送时核对来源与内容版本；不可用时保留草稿并报错。

点输入区用量圆环可查看运行实例报告的上下文和累计用量，也可准备「下一条消息发送前压缩」。未发送时不调用模型。计划需明确批准后才开始实施；持续 Goal 需单独填写目标、完成标准和预算并点击开始，普通对话不会自动变成持续任务。Goal 的暂停、修改、恢复和结束保留历史；完成需要真实工具证据，预算耗尽或证据不足时不会自行扩预算或宣称完成。Web 启动的主 agent 使用内部 `-task-controls` 开启这些工具，子任务不继承。

「交付修改」显示真实仓库状态，可按文件暂存/取消暂存、预览全部暂存文件后本地提交。提交不包含未暂存修改、不推送；已配置签名/可执行 Git 钩子时请使用原 Git 提交流程。操作结果未知时先查原请求，避免重复副作用。项目和副本目录始终在确认界面显示。

Linux 执行主机可在项目 `.agents/actions.json` 配置常用操作（配置本身不会自动执行）：

```json
{"version":1,"actions":[
  {"name":"检查","command":"go vet ./... && go build ./...","platforms":["linux"]},
  {"name":"开发服务","command":"python3 -m http.server 18080 --bind 127.0.0.1","background":true,"ports":[18080],"platforms":["linux"]},
  {"name":"初始化副本","command":"npm ci","initialize":true,"platforms":["linux"]}
]}
```

点击操作后先检查命令与目录，再明确运行。单次命令最多5分钟，长期服务最多24小时，关闭标签或模型一轮结束不会停止服务；后端重启不自动重跑，需核对实例。初始化仅允许隔离副本；失败可能保留部分修改。项目操作始终在Linux沙箱内，网络仍按现有白名单；日志需用户明确加入草稿。内部服务 worker 的 `-service-log` 开启实时日志，`-service-ports` 仅导出指定沙箱 loopback 端口到私有Unix socket，不公开宿主TCP端口；不要把这些标志当作普通agent参数。

任务产物与项目文件支持静态 Markdown/HTML、图片和 PDF 预览/版本批注。静态 HTML 禁止脚本、表单和外部资源；动态网页交互预览仍受隔离浏览器运行环境门槛限制，详见 [网页预览方案](spec/web-product/19-web-preview.md)，当前浏览器过程页只展示已有运行证据。

完全访问决定命令的权限与执行位置，Bash工具开关仍单独生效。旧任务若创建时没有启用命令，输入区会显示原因；点“为本任务启用命令”可在不新建任务、不改变权限的情况下，为后续执行启用shell。`!命令`使用输入区当前选择的权限与执行位置；旧客户端不传时沿用上一轮。
