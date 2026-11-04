# workspace 的约束

- 有工作区时根目录 AGENTS.md（没有时 CLAUDE.md）进系统提示词；子目录的指令在读写那个目录的文件时以提醒追加。环境信息与运行中的变化以 `<system-reminder>` 用户消息追加在末尾，不改写已发出的内容；提醒不是用户原话。
- 文件经 `os.Root` 访问；改之前核对“上次读到的内容”，对不上就拒绝。编辑工具不能写 `.git`。
- 在沙箱外对工作区跑 git 一律用 `Git`（私有 git 目录，见 `git.go`）：仓库配置模型能影响，直接跑 git 会把 fsmonitor、clean filter 变成宿主机上的命令。
- apply_patch 先整体算出结果，有一处对不上就一个文件都不改；改、删已有文件同样要先读过。glob、grep 跳过 .gitignore 忽略的文件（git 判断）。
- edit_file、write_file、apply_patch 写入前存快照，存不下就不写。撤销是人的动作（命令行或网页端按钮），先写审计；最后一次写入后又被改过的，整次不做。
