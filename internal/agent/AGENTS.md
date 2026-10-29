# agent 的约束

- 工具的性质（只读、串行、可重做、轮询、私有来源、外发、不可撤回、权限类别、时限）只在 `toolspec.go` 声明。工具列表也是允许列表，执行前最先检查；工具从第一次请求就固定，中途不改。工具结果标记来源，不当作新的授权。
- MCP server 的标注只用于并行与重做；外发与权限只收紧，不放宽。
- 每轮用新的检查点ID，以 `continues` 串成续接链，旧检查点不覆盖。影响下一步、要带进下一轮和续跑的状态只放检查点的 `state` 一处；格式改动要加 `CheckpointVersion` 并在读取时迁移旧格式。
- 过长的工具输出存进 `.data/outputs/<对话第一轮ID>/`，模型用 `read_output` 读回。
- 任务队列、子 agent、评测试次都经 `RunChild` 为每个任务启动一个子进程，一个进程只跑一个任务。后台子 agent、等待、插话、停止、分叉都只经父子之间的协议实现；后台子 agent 只活到这一轮结束。

- 子 agent 沿父任务继承 ask_user 开关；交互请求经父进程转给真实界面，不能由父 agent 代答。转发 call_id 按子任务身份隔离，original_call_id 保留原工具调用关联。子进程取消/退出时取消对应转发等待，不影响兄弟任务。

- 后台同 task_id 只有实际未退出的实例拒绝重复派发；done 关闭后可原位替换并由 RunChild 按检查点续跑或回放。派发失败不是原子任务的新执行结局，界面须保留上次真实生命周期。

- managed 补充按 client_id + 内容哈希去重，turn/steer/status 返回传输/采纳回执。采纳和 hook 拦截结果随有效上下文写入 v3 检查点，保存成功后才发 userMessage 通知；turn/completed 的 queued_inputs 交回未采纳 ID，旧 queued 仍只含 unmanaged 文本。
- subagent/control 仅通过当前父进程持有的 task_id+call_id 子进程连接；停止只发送中断，终态仍由真实退出通知确认。网页直接补充使用 managed 回执，不把送达当采纳，不扩展子任务权限或生命周期。
