# 浏览目录来登记工作区，在新对话里直接添加

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。2026-10-25 已补真实 OpenSSH 链路的远端目录浏览验证，环境范围见 [收尾记录](closeout-20261025.md)。验收时发现“登记后再取消，下拉退回登记前的值”，已修。

## 1. 为什么要改

2026-10-24 用户在设置页添加工作区，试了几次都加不上（`~`、`.`、相对路径都不行，tern-home-default.md 第 8 节已修）。即便修好了，现在的做法也只有设置页里一个输入框（`internal/web/page/pages.js:135`）：要记得完整路径，敲错只有一句报错。没登记过工作区时，新对话的工作区下拉整个是藏起来的（`internal/web/page/shell.js` 的 `ws.hidden = !r && !names.length`），人根本不知道有这个选项。

Codex 的界面靠 app-server 的 `fs/readDirectory`、`fs/getMetadata` 浏览所连机器上的目录来选项目（`codex-rs/app-server-protocol/src`），远端也一样；打开一个文件夹就是一个项目，不用先去设置。

## 2. 改什么

### 2.1 app-server：`workspace/browse`

列出 app-server 所在机器上某个目录下的子目录，给界面做选择框用。
- 参数 `{path}`：空串表示家目录；`~`、`~/…` 照 `workspace/add` 的规则展开；其他相对路径返回 400（同一句“请填绝对路径，可以用 ~ 开头，例如 ~/workspace/tern”）。
- 返回：
  ```json
  {"path": "/home/dev/workspace", "parent": "/home/dev", "home": "/home/dev",
   "git": false, "registered": "",
   "entries": [{"name": "tern", "path": "/home/dev/workspace/tern", "git": true, "registered": "tern"}],
   "truncated": false}
  ```
  - `path` 是 Clean 过的绝对路径；在根目录时 `parent` 为空串。
  - `git` 表示这个目录下有 `.git`。
  - `registered` 是已经登记成哪个工作区（名字），没登记是空串。和 `workspace/add` 的重复检查一样，按路径比较。
- `entries` 只列目录（指向目录的符号链接也算），不列文件；名字以 `.` 开头的不列；按名字排序；最多 500 个，多了截断，`truncated` 为 true。
- 出错：不存在或不是目录返回 400“目录不存在：<路径>”；没有读权限返回 400“没有权限读这个目录：<路径>”。
- 只读：不建目录、不改设置。

和 `workspace/add` 一样，能连上 app-server 的就是这台机器的主人（`internal/appserver/AGENTS.md`）。这里只给出目录名，不给文件内容，`workspace/add` 本来就收任意路径，所以不另加限制。

### 2.2 网页端：选目录的对话框

- 路由 `GET /workspaces/browse?path=…` 转成 `workspace/browse`（查询参数同名，照 `internal/web/AGENTS.md` 的写法）。
- `index.html` 加一个 `<dialog id="dir-dialog">`，照撤销对话框（`#rewind-dialog`，`shell.js:380` 的 `showModal`）的写法：
  - 顶上一个路径输入框（可以直接改，回车跳过去，支持 `~`）和“上一级”按钮。
  - 中间是子目录列表，点一下进去；`git` 为真的后面标“git 仓库”，已登记的标“已登记：<名字>”。截断时最后一行写“只显示前 500 个”。
  - 底下：“登记这个目录”按钮和“取消”。当前目录是 git 仓库、又没登记过时按钮才能点；不是 git 仓库时按钮灰掉，悬停写“不是 git 仓库：先在这个目录里 git init”；已登记时写“已登记：<名字>”。
  - 点“登记这个目录”就 `POST /settings/workspaces {path}`。成功后关掉对话框，把登记好的工作区（返回的 `name`）交给打开它的地方；失败把服务端的文字显示在对话框里，不关。
  - 标题写“选工作区目录 · <主机>”，主机用顶栏那个名字。远端主机的页面带 `/h/<别名>/` 前缀，`api()` 会补上，浏览的就是远端的目录，不用另外处理（2.5）。
  - 第一次打开从家目录开始；之后从上次停留的目录开始（只记在这个页面里）。
- 设置页（`pages.js:135`）：输入框旁边加“浏览…”按钮，打开这个对话框，登记成功后照现在一样刷新设置页。手敲路径的输入框保留。
- 新对话的工作区下拉（`shell.js` 里生成 `$("workspace")` 那一段）：
  - 编码工具可用时一直显示，没登记过也显示（只有“无工作区”和下面这一项）。
  - 最后一项是“＋ 添加工作区…”。选了它就打开对话框；登记成功后刷新设置（`loadSettings()`），选中新的工作区，记作人选过（`ui.picked.workspace = true`）；取消就退回原来选的那一项。
  - 进行中的对话照旧锁定，不显示这一项。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/settings.go` | 加 `workspaceBrowse`（2.1）。`~` 的展开和“必须绝对路径”的判断从 `workspaceAdd` 里抽成一个小函数，两处共用 |
| `internal/appserver/handle.go` | 方法表加 `"workspace/browse": (*conn).workspaceBrowse` |
| `internal/web/server.go` | 加 `mux.HandleFunc("GET /workspaces/browse", s.browseWorkspaces)`，转成 `workspace/browse`，参数 `path` 取查询参数 |
| `internal/web/page/index.html` | 加 `<dialog id="dir-dialog">` 的骨架 |
| `internal/web/page/pages.js` | 设置页的“浏览…”按钮；选目录对话框的逻辑放在这里，导出一个 `pickWorkspace()`（返回登记好的名字，取消返回空），给 `shell.js` 用 |
| `internal/web/page/shell.js` | 工作区下拉按 2.2 改：一直显示、加“＋ 添加工作区…” |
| `internal/web/page/app.css` | 对话框里的目录列表、标记的样式，和撤销对话框一致 |
| `README.md` | 网页端讲工作区的地方补一句：设置页和新对话的工作区下拉里都能浏览目录来登记，远端主机浏览的是远端的目录 |

不改：
- `workspace/add` 的规则（必须是 git 仓库、按路径去重、名字取目录名）。
- `.data/settings.json` 的格式、协议版本 `Protocol`。
- chat 的 `-workspace 名字`。

## 4. 这一期不做

- 不是 git 仓库的目录也能登记、“git init”按钮（计划 1.6）。
- 候选与最近用过的目录（1.3）、对话按工作区分组（1.4）、工作区改名（1.5）。
- 在对话框里新建目录。
- 显示隐藏目录的开关。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过 `pages.js`、`shell.js`。
2. 协议层（编一份程序，`HOME` 指到 `/scratch` 下的临时目录，在家目录下建 `ws/a`（git init）、`ws/b`（普通目录）、`ws/.hidden`，起 app-server，直接连 control.sock 发请求，同一条命令里收尾）：
   - `workspace/browse {path: ""}` 返回家目录；`{path: "~/ws"}` 的 `entries` 是 `a`（`git` 真）和 `b`，没有 `.hidden`；
   - 先 `workspace/add {path: "~/ws/a"}`，再浏览 `~/ws`，`a` 的 `registered` 是它的名字；
   - `{path: "ws"}` 返回 400 和“请填绝对路径…”；`{path: "~/nosuch"}` 返回 400“目录不存在…”。

### Claude Code 在沙箱外做（无头 Chrome 驱动真实页面，临时 `HOME`）

3. 没登记过工作区时，新对话的工作区下拉显示出来，有“＋ 添加工作区…”；选它打开对话框，从家目录点进一个 git 仓库，“登记这个目录”能点，点了以后下拉里选中了新的工作区。
4. 对话框里进到一个普通目录，按钮灰掉、悬停是“不是 git 仓库…”；取消后下拉退回原来的选项。
5. 设置页点“浏览…”登记一个仓库，设置页的工作区列表里出现它。
6. 远端：网页端连一台远端主机（指向本机的 ssh 别名，远端用另一个 `HOME`），在 `/h/<别名>/` 的页面里打开对话框，看到的是远端的家目录。2026-10-24 本机 `~/.ssh/config` 里没有这样的别名，不为验收去改用户的 SSH 配置：没有就跳过这一条，在结果里写明；远端走的是同一个 `api()` 前缀和同一个协议方法。
