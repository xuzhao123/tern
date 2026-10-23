# llm 的约束

- 请求一律 `stream: true` 并要 usage；拼接逻辑只有 `Stream` 一份，agent 与网页端代理共用；截断等检查在流结束后对完整回复做。
- 协议转换只在 `ConvertBody` 一处；内部消息、检查点与存档只用 chat 形式。
- 只对暂时故障（429 非额度、5xx、连接失败、断流）退避重试，最多 3 次、不占请求预算、遵守 Retry-After。
- 路由表在 `Providers`，`-provider`、`-model` 手动选。`Model.Window`、`Model.Vision` 只填核对或实测过的值，拿不准不填。新增模型协议须有对应供应商的密钥，没有就不接，也不模拟。
- 经网页端时，请求发往 `AGENT_PROXY_URL`；`LLM_API_URL` 只是方舟的地址变量。Coconut 的地址只从 `COCONUT_API_URL` 读；明文 http 只允许不带密钥的请求。
- 图片附件按内容寻址存进 `.data/[tenants/<租户>/]attachments/`（目录 0700、文件 0600）；消息、检查点、存档里只放 `attachment://` 引用，发请求时才展开。
