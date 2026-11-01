# 评测报告里的成本与耗时：逐题、和基线比

状态：已实现（2026-10-31，tern 用 DeepSeek 实现，71 次请求）。实现中 tern 指出"不对比"和"空参数回落到存的基线"冲突，按它的建议加了 `baseline=none`（见 2.4）。沙箱外验收：`eval report e20261031-142450 -baseline e20261031-022357` 逐题有请求、未命中、耗时，末尾有成本差（请求 -6.4 次、tokens -570k）；`code-calc-pow` 手算 12.7 次、19.5k、61s，与报告一致。`eval/get` 的空参数、指定 ID、`none`、不存在的 ID 四种情况经 app-server 协议核对过。网页端下拉只读了代码，没在浏览器里点。

## 1. 为什么要改

1.4 的完成标准是能回答"这次改动让每题平均多花了多少"。现在评测报告只有整体数：每试次 tokens、缓存命中占比、每答对一次的 tokens、耗时 P50（`internal/eval/summary.go` 的 `Score`）。缺三样：

1. **逐题的成本**：`CaseScore` 只有通过数。哪道题最费钱、费在请求次数多还是每次上下文大，要去翻每个试次。
2. **和基线比成本**：`Compare` 只比通过率（`MeanDiff`、回归、修复），不比 token、请求次数、耗时。
3. **网页端换基线**：评测详情页（`eval/get`）只用记录里存的 `rec.Baseline`，开评测时没选基线，事后就没法和别的记录比。

请求次数现在只在每个试次里有（`Trial.Calls`），整体和逐题都没汇总。

## 2. 改什么

### 2.1 逐题和整体都汇总请求次数、未命中 tokens、耗时

`internal/eval/summary.go`：

- `CaseScore` 加四个字段，只算计分的试次（`caseScores` 的 `keep` 过滤后的那些）：
  - `Calls float64 json:"calls_per_trial"`：平均模型请求次数（`Trial.Calls`）
  - `Tokens float64 json:"tokens_per_trial"`：平均 tokens（`Trial.Tokens`）
  - `Uncached float64 json:"uncached_per_trial"`：平均未命中缓存的 tokens（`Trial.Tokens - Trial.Cached`）
  - `Seconds float64 json:"seconds_per_trial"`：平均墙钟秒数（`Trial.Seconds`）
- `Score` 加 `Calls float64 json:"calls_per_trial"`，和现在的 `Tokens` 同样算法（总数除以 `sc.Trials`）。

用平均不用中位数：k=3 时中位数就是中间那一次，平均才能和总花费对上。整体的 `Seconds`（P50）不改。

### 2.2 和基线比成本

`Compare` 加一个 `Cost CostDiff json:"cost"`：

```go
type CostDiff struct {
	Calls    float64 `json:"calls"`    // 共同题上，每试次请求次数：这次平均 − 基线平均
	Tokens   float64 `json:"tokens"`   // 每试次 tokens 差
	Uncached float64 `json:"uncached"` // 每试次未命中缓存 tokens 差
	Seconds  float64 `json:"seconds"`  // 每试次墙钟秒数差
	Base     CaseCost `json:"base"`    // 基线在共同题上的平均，算百分比用
}
type CaseCost struct {
	Calls, Tokens, Uncached, Seconds float64 // json 名同上
}
```

算法和通过率的配对比较一致：只看两边都有计分试次的题，逐题求"这次的题内平均 − 基线的题内平均"，再对题取平均。`Base` 是基线在这些题上题内平均再对题取平均。`compare()` 里 `before` 已经是 `caseScores` 的结果，2.1 加的字段直接能用。

### 2.3 命令行报告

`internal/eval/cli.go` 的 `report`：

- 整体那行（`line`）在"每试次 N tokens"前加"每试次 N 次请求"。
- 逐题每行末尾加：`{请求} 次请求 · 未命中 {未命中/1000}k · {秒}s`（都取平均，保留整数，未命中按千取整）。
- 和基线比的那一段后面加一行：

  ```
  成本（共同题逐题平均差）：请求 +12.3 次（+14%）· tokens +1.2M（+20%）· 未命中 +15k（+9%）· 耗时 +45s（+11%）
  ```

  百分比 = 差 / `Base` 对应项；`Base` 为 0 时不写百分比。tokens ≥ 100 万写 M，否则写 k。

### 2.4 网页端评测详情

`internal/web/server.go` 第 1615 行附近 `eval/get` 的转发，把查询参数 `baseline` 一起传过去：`{"id": ..., "baseline": req.URL.Query().Get("baseline")}`。

`internal/appserver/eval.go` 的 `evalGet`：input 加 `Baseline string json:"baseline"`。三种取值：空字符串照旧用 `rec.Baseline`；`"none"` 表示明确不对比（`baseline` 为 nil，即使记录里存了基线）；其他值当评测 ID 用 `eval.Load`，读不到返回 404。返回里加 `"baseline_id"`：实际用的那份，没有就空字符串。（review 时补：tern 指出"不对比"在原写法下对存了基线的记录无效。）

`internal/web/page/eval.js` 的 `evalDetail`：

- 顶部（`p.meta` 下面那条 `hub-bar`）加一个"对比基线"下拉：选项是 `GET /evals` 列表里除自己外的记录，文字 `{id} · commit {commit} · {model}`，加一个"不对比"（选它时传 `baseline=none`）。默认选中返回的 `baseline_id`。改选后带 `?baseline=<id>` 重新取详情（`evalURL` 已有拼地址的地方就复用）。选中项放在 `ui` 上（例如 `ui.evalBaseline`），换一份评测时清空，不存 localStorage。
- 汇总表（第 172 行附近）加一列"每试次请求"。
- 逐题表（第 197 行附近）加三列：请求、未命中、耗时，取 `summary.cases` 里对应题的平均。只有计分试次时才有值，没有就 "—"。
- "与基线逐题对比"那段（第 176 行附近）加一行成本差，文字同 2.3。

### 2.5 不变的

- 打分、通过率、配对比较的算法、`Record` 存盘格式都不改；新字段都在 `Summary` 里现算，旧记录打开就有。
- 不改评测题、不改 `eval run` 的参数。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/eval/summary.go` | 2.1、2.2 |
| `internal/eval/cli.go` | 2.3 |
| `internal/appserver/eval.go` | 2.4 的 `evalGet` |
| `internal/web/server.go` | 2.4 的查询参数转发 |
| `internal/web/page/eval.js` | 2.4 的页面 |
| `README.md` | 第 281 行附近 `go run . eval` 那一行补一句：报告有逐题的请求次数、未命中 tokens、耗时，和基线比时有成本差；网页端详情页可以换基线 |

不要改：评测题、打分器、`Record` 的字段、其他页面。

## 4. 验收

### tern 自己做（沙箱里）

1. `gofmt -l .` 无输出；`go vet ./...`、`go build ./...` 通过；`node --check internal/web/page/eval.js` 通过。
2. 读代码报告：`CostDiff` 怎么算（哪些题、哪些试次）；`evalGet` 的基线取哪份、读不到怎么办；页面换基线后怎么重新取。

### Claude Code 在沙箱外做（不需要真实模型）

1. 用源码目录 `.data/eval` 里已有的两份记录：`TERN_HOME=$PWD go run . eval report <新> -baseline <旧>`，逐题有请求、未命中、耗时；末尾有成本差一行，手算一道题核对。
2. 网页端（临时 `TERN_HOME`，拷两份记录进去）打开评测详情，切换"对比基线"，成本差和逐题列随之变化；选"不对比"时对比段消失。
