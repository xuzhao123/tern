# 去掉"副本"功能

状态：已实现（2026-10-31，用户要求，Claude Code 直接实现）。

## 为什么

用户看了输入框下面的"副本"勾选框，觉得没什么用，要求整个去掉（不只藏界面入口）。项目操作里的 `initialize` 初始化动作原来只能在副本对话里跑，用户选了一起去掉。原设计见 [copy-workspace.md](copy-workspace.md)、[web-product/22-project-operations.md](web-product/22-project-operations.md)。

## 改了什么

| 位置 | 改动 |
| --- | --- |
| `internal/appserver/server.go` | 删 `makeCopy`、`dropCopy`、`gitIn`、`copyFile`、`workspace/applyCopy`；事件去掉 `copy_of`、`copy_base`；summary 不再带 `copy`，`project_dir` 就是 `workspace_dir`。`thread/start` 仍认 `copy` 参数，但只为 `copy=true` 时回 400"副本功能已去掉"：请求解码不拒绝未知字段，不留这一条的话，旧页面勾了副本会被悄悄改成在原目录里跑 |
| `internal/appserver/handle.go` | 去掉 `workspace/applyCopy` |
| `internal/appserver/diff.go` | 去掉 `copyOf`、`copyBase` 和副本基线比较，`workspace` 范围一律相对 HEAD |
| `internal/appserver/threads.go` | 删对话不再清 `.data/copies`；删掉只为它服务的 `workDirsOfOthers` |
| `internal/appserver/operations.go` | 列表回复去掉 `copy`；配置里有动作标了 `initialize` 就整份拒绝（400，写明是哪一项），不把它当普通命令在项目目录里执行 |
| `internal/web/server.go` | 去掉 `POST /runs/{id}/apply-copy` |
| `internal/web/page/` | 去掉"副本"勾选框、"应用到"按钮与它的处理、草稿里的 `copy`、列表的"副本"标记、改动面板的"副本基线"文案、项目操作里的初始化标记；执行位置写"项目目录"或"临时空间" |
| `README.md`、`internal/appserver/AGENTS.md`、`internal/web/AGENTS.md` | 去掉副本和初始化动作的说明，写明 `initialize` 不再支持 |
| `spec/web-product/` | 删 `check-copy-delivery.py` 及 `run-checks.py` 里那一步；`check-operations.py` 改为验证带 `initialize` 的配置被拒绝；`check-closeout-lifecycle.py` 去掉副本初始化那段 |

不动：`eval/coding/checks/copy-workspace/` 和编码评测题 `spec-copy-workspace`。那道题在固定的旧提交上让 tern 照 spec 实现这个功能，量的是 agent 的能力，和现在的代码里有没有这个功能无关。

## 旧数据

已有的副本对话（`~/.tern` 里目前没有）还能打开、续聊，读写的仍是 `.data/copies/` 里那个目录，改动面板相对它的 HEAD；没有"应用到"，删除对话时也不再连副本目录一起删，需要的话手动删 `.data/copies/`。

## 验收

`go build ./...`、`go vet ./internal/appserver/ ./internal/web/`、`gofmt -l`、所有页面脚本 `node --check`。临时 `TERN_HOME`、DeepSeek 真实对话：`copy=true` 回 400；项目里的对话改完文件，摘要没有 `copy`，改动面板相对 HEAD，列出 `main.go`；带 `initialize` 的 `actions.json` 回 400 并点名那一项，去掉后正常列出；`apply-copy` 路由 404；删除对话 204。页面：选了工作区后输入框下没有"副本"，从页面发送、跑完，标题菜单写"项目目录 · <目录>"，没有初始化失败。中途发现 `drafts.js` 还给 `copy` 挂监听，页面初始化失败，已修。
