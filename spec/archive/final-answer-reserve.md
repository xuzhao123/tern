# 最后一次请求留给最终回答；75% 的预算提醒改成逐项核对

状态：已实现（2026-10-31，tern 用 Coconut flash 实现，243 次请求；review 时补了三处只认 `purpose == "main"` 的地方）。沙箱外 `-max-steps 3` 在 Responses（Coconut flash）、chat（`deepseek`）、Messages（`deepseekanthropic`）三种协议上都通过：第 3 次请求打出 `Final request: tools disabled`，结局 `no_tool_calls`，最终回答 2.5–2.8k 字，检查点有 `forced_final: true`。用户随后把被测模型改回 DeepSeek 官方 API，整题评测改为和同一代码的 DeepSeek 基线比，结果见 [harness-next-phase.md](../harness-next-phase.md) 进度。

## 1. 为什么要改

被测主力模型换成 Coconut 的 deepseek flash（`coconut-deepseek-v4-1-flash-max`）后，编码评测基线（`e20261031-022357`，10 题 k=3）的 pass@1 是 47%，DeepSeek 直连是 83%。15 个失败试次里有 14 个用满了请求上限（`max_steps`），其中 7 个的代码校验其实已经通过，只是没停下来，到上限时连最终回答都没有。[请求预算提醒](request-budget-reminder.md)照常发了 43 条，对 flash 没有作用。

回放实验：`spec-thread-goal` 的 w-2 在第 90 轮已经做完自己那部分验收，第 92 轮起越界去起服务、开无头 Chrome。保留前 91 轮，在第 92 轮插入不同的话，每种 3 次：

| 插入的话 | 通过 | 说明 |
| --- | --- | --- |
| A "继续。"（对照） | 0/3 | 都用满 120 次 |
| B 预算提醒的原话（只做交给你的部分……做完就给最终回答） | 0/3 | 1 次自己停了，但多跑的几轮把代码改坏了，校验没过 |
| C 直接说"你那部分验收已经做完，沙箱外的不是你的，立刻给最终回答，不要再调用任何工具" | 3/3 | 都在第 92 轮收尾 |
| D 不给具体事实：先对照任务列出交给你的验收项和状态，都做完了就给最终回答、不要再调用工具 | 2/3 | 两次通过的分别到第 111、119 次请求才停 |

结论：

1. 对 flash 来说，说原则不管用；直接说事实并要求"不再调用工具"才管用。harness 一般不知道"做没做完"这个事实，所以不能只靠提醒。
2. 用满上限时没有最终回答，是最不可接受的结局：做对的也记失败，人也看不到它做了什么。harness 能保证这件事不发生：**最后一次请求不让调用工具**。和模型无关，对所有供应商都有效。
3. D 比 B 好（2/3 对 0/3），75% 那档的提醒改用 D 的写法。n 很小，以整题评测结果为准。

## 2. 改什么

### 2.1 最后一次请求只能给最终回答

设了 `max_steps`（不是 `llm.Unlimited`）时，主循环里如果**这次主请求是最后一次**（发出前 `client.Calls == maxSteps-1`）：

1. 先追加一条 `llm.Reminder`：

   ```
   [最后一次请求] 这是 {上限} 次模型请求里的最后一次，这次不能再调用工具。现在给最终回答：改了什么、做了哪些验收、结果如何，没做完或没验收的逐项列出来。
   ```

2. 这次请求带上工具定义，但禁止调用（`tool_choice` 为 none）。摘要请求现在就是这么做的（`internal/llm/llm.go` 里 `purpose == "compact"` 时设 `tool_choice: none`，`messages.go`、`responses.go` 已经会转换）。不要改成不带 tools：Anthropic Messages 协议的历史里有 `tool_use`、`tool_result` 时必须带 tools。
3. 模型照常返回文字回答，这一轮按正常结束处理，结局是现在的 `no_tool_calls`，有最终回答。终端日志打一行 `Final request: tools disabled calls={n} max={上限}`；退出记录（检查点、`turn/completed` 的结果）里加 `forced_final: true`，让评测和人能看出这次是被逼着收尾的。
4. 如果这一轮先要压缩、压缩用掉了最后一次请求，照现在的做法停在 `max_steps`，不额外处理。
   已知缺口（实现时 tern 指出）：判断在 `Next` 之前做，`Next` 里的 `Prepare` 如果在倒数第二次时压缩，主请求就成了最后一次但没被判出来，仍可调用工具，之后停在 `max_steps`。要补得把判断和提醒挪进 `Context.Next` 的 `Prepare` 之后，改动更大；压缩正好落在倒数第二次很少见，这一期不做，评测里看到再说。
5. 子 agent 走同一个 `Run`，用自己的上限，自然适用。

**需要改的地方**：主请求在 `internal/agent/context_manager.go` 的 `client.Call(ctx, c.View, c.withTools, "main")`。`callModel` 里 `tool_choice: none` 现在只认 `purpose == "compact"`。做法是加一个只在这一次用的 purpose（比如 `"final"`），`callModel` 里 `compact` 和 `final` 都设 `tool_choice: none`；或者给 `Call` 加一个参数。哪种和现有代码更贴近由实现者选，读代码报告里说明。用量统计、日志里的 `purpose=` 照新值写。

`llm.go` 里 `!withTools && len(ToolCalls) > 0` 会报"意外返回工具调用"。设了 `tool_choice: none` 时，如果模型仍然返回工具调用（个别兼容接口不遵守），也按同样的方式处理：这一轮报错停下，结局照现在的规则。不要为它另外重试。

### 2.2 75% 那档的预算提醒改成逐项核对

`internal/agent/react.go` 里预算提醒的文字按档位区分：50% 不变；75% 改成：

```
[收尾检查] 已用 {已用} / {上限} 次模型请求（含摘要）。先对照任务原文，列出交给你自己做的验收项，逐项写它现在的状态（做了、结果是什么）。写明由别人做的验收不算你的，即使这里做得到也不要做。如果交给你的都已经做完，现在就给最终回答（改了什么、你的验收结果、没做的项），不要再调用任何工具；还有没做完的，只做那几项。
```

日志行照旧是 `Budget reminder: level=75 …`。档位逻辑、`State.Budget` 都不变。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/agent/react.go` | 2.1：判断最后一次、追加提醒、打日志、退出记录里的 `forced_final`；2.2：75% 的文字 |
| `internal/agent/context_manager.go` | 2.1：最后一次主请求禁止调用工具 |
| `internal/llm/llm.go` | 2.1：新 purpose（或新参数）也设 `tool_choice: none` |
| `internal/llm/messages.go`、`internal/llm/responses.go` | 只有现在的转换认不出新的 purpose 时才改；能直接复用就不动 |
| `internal/eval/cli.go`（第 186 行附近的编码题汇总）、需要时 `internal/eval/grade.go` 读字段 | 编码题汇总里加一项"被逼收尾 N 次"（读 `forced_final`），和现在的"没正常结束但校验通过"并列。通过率的算法不改 |
| 其他按 `purpose == "main"` 识别主请求的地方（review 时补）：`internal/eval/run.go` 第 624 行附近数轮次、`internal/appserver/eval.go` 第 402 行附近取主对话快照、`internal/mcpserver/mcpserver.go` 第 272 行附近数请求次数 | `"final"` 也是主请求，三处都按主请求算 |
| `internal/agent/AGENTS.md` | 有列 harness 提醒的地方就补一行；没有就不加 |
| `README.md` | 参数表里两处 `-max-steps`（第 173、189 行附近）各补一句：设了上限时，最后一次请求不能调用工具，用来给最终回答 |

不要改：系统提示词、默认的 `max_steps`、评测题、网页端。

## 4. 这一期不做

- 不做"检测到越界行为（起服务、开浏览器）就提醒"。行为判断容易误伤正当的自测，见 [harness-next-phase.md](../harness-next-phase.md) 10-29 的"注意"。
- 不按 token 设预算。
- 网页端不显示 `forced_final`，只进日志、检查点和评测汇总。

## 5. 验收

### tern 自己做（沙箱里，不需要真实模型）

1. `gofmt -l .` 无输出；`go vet ./...`、`go build ./...` 通过。
2. 读代码报告：上限 120 时，第几次请求前发 75% 的收尾检查、第几次请求禁止调用工具（应该是第 91 次、第 120 次）；最后一次碰上压缩时怎么办；`tool_choice: none` 在 chat、Responses、Messages 三种协议下分别怎么发出去。

### Claude Code 在沙箱外做（真实模型）

1. `-max-steps 3`，给一个至少要读 5 个文件的问题：第 3 次请求的日志有 `Final request: tools disabled`，最终回答不为空，结局 `no_tool_calls`，检查点里有 `forced_final`。三种协议各跑一次，确认 `tool_choice: none` 被接受：Responses 用 Coconut flash（`-provider coconut`），chat 用 `-provider deepseek`，Messages 用 `-provider deepseekanthropic`。
2. 编码评测（Coconut flash，10 题 k=3）和基线 `e20261031-022357` 比：用满上限（`max_steps`）结局降到 0 或接近 0；pass@1 高于 47%。逐题列出"被逼收尾"的次数。通用评测（评审豆包）和 `e20261031-055901` 比，不能变差。

## 6. 已确认

1. 被逼收尾的试次，校验通过就算通过（用户 2026-10-31 同意按建议做）。按现在的打分规则，结局是 `no_tool_calls` 且校验通过就算：它确实给了最终回答，人能拿到结果。汇总里单独列出被逼收尾的次数，免得通过率的提高被误读成模型变强了。
