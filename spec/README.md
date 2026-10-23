# 设计文档

新功能先在这里写设计，确认后交给 tern 实现（分工见 [AGENTS.md](../AGENTS.md)，交给 tern 的步骤见 [CLAUDE.md](../CLAUDE.md)）。一份设计一个 Markdown 文件，结构参照已有的几份：为什么要改、改什么、按文件的改动（含不要改的）、这一期不做什么、验收。

| 文档 | 内容 | 状态 |
| --- | --- | --- |
| [remote-agent-design.md](remote-agent-design.md)（配图页面 [remote-agent.html](remote-agent.html)） | 远程 agent：网页上的对话交给用户本机的 tern 执行；tern 的几种界面与命名 | 待确认：第 12 节的四个问题。前置改动“macOS 上能编译”已完成 |
| [bash-timeout-and-polling.md](bash-timeout-and-polling.md) | bash 单条命令时限 2 分钟；轮询后台任务不算重复动作 | 已实现（2026-10-23） |
| [sandbox-memory-and-defaults.md](sandbox-memory-and-defaults.md) | 沙箱内存 2G 与 OOM 说明；命令行默认值和网页端一致；代理地址改用 `AGENT_PROXY_URL` | 已实现（2026-10-23） |

## 还没写设计的待办

- `edit_file` 匹配失败时给出文件里最接近的原文。2026-10-23 实现第一份 spec 时，tern 有 4 次编辑因为 gofmt 对齐过的空白没对上而失败，每次都要多花几轮去看原文。
- 只读模块缓存引起的 `go: writing stat cache … read-only file system` 提示：不影响结果，但模型每次都要解释一遍。
- tern 的沙箱里连不到模型接口，需要真实模型的验收只能在沙箱外由人或 Claude Code 跑。
- 知识库资料 `retrieval/corpus.md` 讲的还是 learning-agent，里面仍写“观测台”。
- `.env` 里验证账号的变量名还叫 `OBSERVER_TEST_USER`、`OBSERVER_TEST_PASSWORD`；改名要同时改用户本机的 `.env`。
