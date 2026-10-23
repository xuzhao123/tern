# web（网页端）的约束

- 必须登录：账号在 `.data/users.json`（PBKDF2），平台账号与租户账号两种；按编号访问的接口都核对租户，不属于时返回 404。模型代理凭每次启动 agent 时发的进程令牌。
- 启动 agent 时用环境变量交代运行环境：`AGENT_PROXY_URL`（模型代理地址）、`AGENT_PROXY_TOKEN`（访问代理的令牌）、`AGENT_APP_SOCKET`（协议 socket）。代理按 agent 在 `X-Agent-Upstream` 里声明的地址转发，不复制路由表。
- 设置在 `.data/settings.json`，按 内置默认 › 平台 › 个人 合并，租户策略与本机条件只会关掉工具；对话开始时把设置拍成快照写进 start 事件，续聊照快照启动。工作区只能由平台账号登记本机 git 仓库，租户须明确开通 `workspace`。
- 导入对话只接受 Codex 的 rollout 与 Claude Code 的会话 JSONL，只读、不续聊。
- 页面在 `page/`：浏览器原生 ES 模块，不打包，不引入构建工具与前端框架，按功能分文件；模块顶层只定义与挂事件，启动时的调用放 `main.js`。主页面的第三方脚本固定版本并带 SRI；模型写的可视化只在沙箱 iframe（`allow-scripts`，不给 `allow-same-origin`）里运行。`page/` 会整个打进程序，不要在里面放文档。
