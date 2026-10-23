# eval 的约束

- 题目在 `eval/cases.jsonl` 与 `eval/coding-cases.jsonl`：固定题目、参考答案与打分器，不写死模型回答或工具顺序；运行记录、人工归因与回放放 `.data/eval/`。
- 先用确定性检查，开放题由与被测模型不同的供应商评审；结局与用量以检查点为准。编码题每个试次一份新的仓库拷贝，校验命令只在工作区只读挂载的沙箱里运行。
- 回放验证从检查点分叉（`agent.ForkCheckpoint`、`agent.ForkSnapshot`），不改原检查点。
