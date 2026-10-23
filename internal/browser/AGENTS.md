# browser 的约束

- 用 chromedp，只做读取型工具，每次调用一个标签页；不伪装 User-Agent、不绕过验证码。`CHROME_PATH`、`CHROME_NO_SANDBOX` 只从环境变量读。
