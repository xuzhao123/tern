# skills 的约束

- 只做知识型 skill：手写 frontmatter 解析，不引入 YAML 库，不执行 skill 自带的脚本。
- 内置的放 `builtin/`，用 `//go:embed` 编进程序，不往磁盘写。
- `load_skill` 只给模型读 `references/`、`examples/` 下的 `.md`；skill 自带的 `scripts/` 模型碰不到。
