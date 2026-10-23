# protocol 的约束

- agent 以 `-app-server` 运行时 stdout 只走 JSON-RPC（一行一条），日志走 stderr。网页端、`chat` 和 `agent.RunChild` 是界面；界面需要的信息只读协议通知，不解析终端文本。
- 网页端启动的 agent 经 `AGENT_APP_SOCKET` 走 `.data/[tenants/<租户>/]live/` 下的 Unix socket：网页端退出时 agent 照常运行，重启后接管；agent 已经不在时，从会话记录 `.data/[tenants/<租户>/]sessions/` 补读，已写的记录不改写。
- 审批与提问（`network/requestApproval`、`tool/requestApproval`、`user/requestInput`）只能由界面上的人回答；没有界面时如实回答 unavailable。人的审批先写审计。
