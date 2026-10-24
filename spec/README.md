# 设计文档

新功能先在这里写设计，确认后交给 tern 实现（分工见 [AGENTS.md](../AGENTS.md)，交给 tern 的步骤见 [.claude/skills/tern-implement](../.claude/skills/tern-implement/SKILL.md)）。一份设计一个 Markdown 文件，结构参照已有的几份：为什么要改、改什么、按文件的改动（含不要改的）、这一期不做什么、验收。只有用户要求时才另配 HTML 页面，两者内容一致，有出入以 Markdown 为准。

| 文档 | 内容 | 状态 |
| --- | --- | --- |
| [app-server-design.md](app-server-design.md) | 照 Codex 的 SSH 远程：常驻的 `tern app-server` 管对话，网页端、终端、以后的桌面端经 Unix socket 或 SSH 连它；`TERN_HOME` | 第一步（`TERN_HOME`、`-agent-server`）已实现（2026-10-23）；第一期（app-server、网页端与 chat 改成客户端、去掉租户账号、SSH 远程与重连）已实现（2026-10-24）。第二期另写在 app-server-phase2-design.md，已实现（2026-10-23） |
| [app-server-phase2-design.md](app-server-phase2-design.md) | app-server 第二期：`thread/fork` 与 chat 的 `/fork`、网页端的“分叉”；`thread/rewind` 只退文件或只退对话，网页端的撤销可选三种；一个网页端同时连几台主机（读 `~/.ssh/config`，记住连过的，列表按主机分组） | 已实现（2026-10-23） |
| [remote-agent-design.md](remote-agent-design.md)（配图页面 [remote-agent.html](remote-agent.html)） | 远程 agent：网页上的对话交给用户本机的 tern 执行；tern 的几种界面与命名 | 搁置（2026-10-23），改按 app-server-design.md 做。前置改动“macOS 上能编译”已完成 |
| [remote-control-research.html](remote-control-research.html) | 调研：Claude Code 的 Remote Control、Codex 的远程连接与 app-server，和 tern 远程 agent 的两种方案对比，以及要先对齐的产品问题 | 调研（2026-10-23），不是设计 |
| [bash-timeout-and-polling.md](bash-timeout-and-polling.md) | bash 单条命令时限 2 分钟；轮询后台任务不算重复动作 | 已实现（2026-10-23） |
| [sandbox-memory-and-defaults.md](sandbox-memory-and-defaults.md) | 沙箱内存 2G 与 OOM 说明；命令行默认值和网页端一致；代理地址改用 `AGENT_PROXY_URL` | 已实现（2026-10-23） |

## 还没写设计的待办

- `edit_file` 匹配失败时给出文件里最接近的原文。2026-10-23 实现第一份 spec 时，tern 有 4 次编辑因为 gofmt 对齐过的空白没对上而失败，每次都要多花几轮去看原文。
- 只读模块缓存引起的 `go: writing stat cache … read-only file system` 提示：不影响结果，但模型每次都要解释一遍。
- tern 的沙箱里连不到模型接口，需要真实模型的验收只能在沙箱外由人或 Claude Code 跑。
- 知识库资料 `retrieval/corpus.md` 讲的还是 learning-agent，里面仍写“观测台”。
- 重复动作检测会误判“改测试 → 重编 → 再跑同一条命令”：命令文本相同，中间改过文件也算重复。2026-10-24 实现 app-server 第 3 块时，tern 在 149 步被拦下（6 步里同一条 bash 出现 3 次），工作已做完、只差最后的验证。
- `.env` 里验证账号的变量名还叫 `OBSERVER_TEST_USER`、`OBSERVER_TEST_PASSWORD`；改名要同时改用户本机的 `.env`。
- tern 在沙箱里用无头 Chrome 测页面时，Chrome 自己的后台流量（update.googleapis.com、safebrowsing.googleapis.com 等十来个域名）每个都弹一次联网审批。2026-10-23 实现 app-server 第二期时两块都碰到，只能逐个拒绝。
- tern 的进度文件里写错的校验命令改不了：第二期第 2 块的一条校验命令有笔误（要求代码里出现一个不可能有的字面量），进度文件只能由工具改，tern 自己修不了，那一项一直是“未通过”。
