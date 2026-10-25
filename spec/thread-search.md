# 搜索对话

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。

## 1. 为什么要改

对话多了以后，想找“上次让它查 Lima 那条”只能在左侧列表里一条条翻，列表标题只是第一句话。Codex 的 app-server 有 `thread/search`、`thread/searchOccurrences`。

## 2. 改什么

### 2.1 app-server：`thread/search`

- 参数 `{query}`：去掉首尾空白，至少 1 个字；不区分大小写，按子串匹配。
- 搜的范围：所有对话（评测的试次除外，已归档的也搜），每条看：
  - 名字（conversation-manage.md 的 `name`）；
  - 每一轮的问题（start、continue 事件的 `text`），以及运行中的插话（steer 的 `userMessage`）；
  - 每一轮的最终回答（带 `answer` 的 `turn/completed` 通知）。
- 每条对话只给最先命中的一处：`{"id", "name", "query", "archived", "updated_at", "where": "名字|问题|回答", "snippet": "…命中处前后各 40 个字…"}`。
- 按 `updated_at` 从新到旧，最多 50 条；结果里不带全文。
- 用内存里的事件（`r.events`），不重读存档；各条对话的锁照常拿、拿完就放。

### 2.2 网页端

- 左侧列表上面加一个搜索框（`#run-search`，占位“搜索对话：名字、问题、回答”）。
- 敲字 250 毫秒去抖后请求；连了几台主机时每台都搜（和 `loadRuns` 一样按主机分组显示），只显示最后一次请求的结果。
- 有搜索词时，列表换成结果：每条显示名字（没有就第一句话），下面一行淡色的“问题/回答：…片段…”，片段里命中的那几个字加粗（用 `textContent` 分段拼，不用 HTML）。已归档的标“已归档”。点一下打开那条对话（别的主机的整页跳过去，照 `runRow`）。
- 没有结果写“没有找到”。清空搜索框、或者按 Esc，回到原来的列表。
- 搜索框只在对话页（不是评测、观测页）显示。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appserver/threads.go` | `thread/search` |
| `internal/appserver/handle.go` | 方法表加一项 |
| `internal/web/server.go` | `GET /runs/search?q=` → `thread/search`（`q` 对应 `query`）；注意放在 `GET /runs/{id}…` 一类路由不会把 `search` 当成对话 ID 的位置 |
| `internal/web/page/index.html` | `#run-search` |
| `internal/web/page/shell.js` | 搜索框的逻辑和结果列表 |
| `internal/web/page/app.css` | 搜索框、结果的样式 |
| `README.md` | 网页端那段补一句：列表上面能搜对话的名字、问题和回答 |

不改：`thread/list`、对话列表原来的画法。

## 4. 这一期不做

- 分词、拼音、模糊搜索；只做子串。
- 搜工具调用的参数和结果。
- 列出一条对话里的所有命中处（`searchOccurrences`）。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 读代码报告：搜的字段、只给最先命中的一处、排序和上限；路由不和 `/runs/{id}` 冲突的写法。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 建三条对话：问题里有“海燕”的、回答里有“海燕”的、改名成“海燕笔记”的，再建一条无关的。搜“海燕”：三条都在，无关的不在，`where` 分别是问题、回答、名字，片段里“海燕”加粗。
4. 归档其中一条，再搜：还在，标着“已归档”。
5. 清空搜索框：回到原来的列表。
6. 搜一个不存在的词：显示“没有找到”。
