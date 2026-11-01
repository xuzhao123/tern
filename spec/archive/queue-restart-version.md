# 队列已暂停时，app-server 重启不应再改队列版本

状态：已实现并验收（2026-10-31，tern 用 DeepSeek 实现）。`run-checks.py --only queue` 两步都通过；整套网页验收之前一次是 31/32（只差这一项），没有用新程序整套重跑。

## 1. 为什么要改

`check-queue.py --mode finish` 失败：停下一条有排队消息的对话，记下 `queue_version`，重启 app-server，版本变了（复现：磁盘上 8，起一次 app-server 变 9，再重启变 10；队列内容和顺序不变）。

原因：`internal/appserver/server.go` 第 853 行附近，上一轮没有正常结束、队列里有东西、不在停止中时，把队列标成暂停、写 `QueueError`，并 `QueueVersion++`。app-server 每次启动都会对这条对话再走一遍，队列早就是暂停的，版本还是每次加一。网页端拿着旧版本去发送或调整顺序，就会无故 409。[13-message-queue.md](../web-product/13-message-queue.md) 要求重启前后队列和版本不变。

## 2. 改什么

在上面那段里，只有**队列还没有因为这个原因暂停**时才改：读 `r.queuePaused`（和别的字段一起在 `r.mu` 里读），已经是暂停的就什么都不写（不改 `QueueError`、不加版本、不广播）。第一次发现时的行为不变。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/server.go` | 第 2 节 |

不要改：别的地方的 `QueueVersion++`、队列的其他规则、页面。

## 4. 验收

### tern 自己做（沙箱里）

1. `gofmt -l .` 无输出；`go vet ./...`、`go build ./...` 通过。
2. 读代码报告：这段现在什么时候改元数据，已经暂停时走哪条路。

### Claude Code 在沙箱外做

`python3 spec/web-product/run-checks.py --binary <新程序> --only queue` 两步都通过。
