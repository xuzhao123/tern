# preview（网页预览）的约束

- 一个 app-server 一个 `Manager`，一个对话同时最多一个会话；一个会话一个独立的 Chrome 进程、一个只给它用的代理。
- **不暴露任意 CDP、任意 JavaScript。** 对外只有 `navigate`、`reload`、`back`、`forward`、`resize`、`mouse`、`key`、`text` 这几种动作（`Action`），CDP 句柄、代理端口、profile 路径都不出这个包；agent 一侧的协议（`run.handle`）不分派这些方法。
- **导航前查 scheme 与 origin。** 代理管不到非网络地址：CDP 直接 `Page.navigate` 到 `file://` 能读到本地文件。所以 `navigate` 只接受 http/https 且 origin 等于授权的那一个；`back`/`forward`/`reload` 之后与每 500 毫秒看一次地址，不在授权范围里就停在 `about:blank` 或回到 origin 的根路径，不跟过去。
- **不加 `--no-sandbox`。** Chrome 保留自己的沙箱，`CHROME_NO_SANDBOX=1` 时预览直接不可用（`Manager.Reason()`）。网络边界由代理负责：只放行字面等于授权 `host:port` 的请求，放行的连接连固定地址（`grant.dial`），不解析域名。
- **profile 私有、会话结束删除。** `--user-data-dir` 是 `os.TempDir()` 下的 0700 临时目录，不放进工作区、不放 `.data`，关会话（含失败、空转超时、app-server 退出）时删掉；下载一律拒绝。
- 帧只在内存里留最新一帧（超过协议的 4MiB 块上限就丢），不写盘、不进对话存档。
- 不改 `internal/browser`（那只是读取型工具）、不改 `internal/sandbox`、不改 `internal/protocol` 的上限。
