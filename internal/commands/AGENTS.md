# commands 的约束

- 斜杠命令只是提示词模板，不执行命令。
- 内置的放 `builtin/`，用 `//go:embed` 编进程序，不往磁盘写。
