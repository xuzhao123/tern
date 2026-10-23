# CLAUDE.md

先读 [AGENTS.md](AGENTS.md)。这份只写给 Claude Code。

- 只做设计：在 `spec/` 写 Markdown（结构和索引见 `spec/README.md`），不改代码。用户明确要求时才例外，只限那一次。
- 设计里点名的函数、路径、参数，先在代码里核对存在；拿不准的列成“需要确认的问题”，不替用户拍板。
- 用户要你让 tern 实现某份设计、或 review tern 的改动时，用技能 `tern-implement`。
