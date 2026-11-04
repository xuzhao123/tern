# 完整支持 visualization skill

状态：第（一）（二）块已实现（2026-11-01，Claude Code 直接实现，未提交）；2026-11-03 按用户要求，visualization 不再内置，挪到个人 skill 目录 `~/.tern/skills/visualization/`（见下面“位置变更”）；第（三）块只做了壳子：`internal/imagegen`（后端接口 `Backend`、`Active` 为 nil 时不提供工具、参数校验、并发生成、存附件、编号）、agent 主循环给多张图编号、网页端显示工具结果缩略图与回答里的 `![说明](tern-image:N)`；后端、工具开关、`netguard` 与审计等用户接后端时再做。第（四）块未做。

额外修复（实现时用真实模型发现）：`load_skill` 的结果原来也受 `-tool-output-tokens`（默认 2000）限制，超过就存成文件、视图里只放预览，模型多半不会再 `read_output`，所以 visualization 的 SKILL.md（6401 字符）和大部分 references 实际只读到首尾。现在 `internal/agent/context_manager.go` 的 `fullOutput` 让 `load_skill` 的结果整份进视图（skill 的每个文件加载时已限制在 64KB 以内）。对所有 skill 生效。

位置变更（2026-11-03，用户要求）：visualization 与同批迁来的 book-writer、human-signal、product-manager 一起从 `internal/skills/builtin/` 挪到 `~/.tern/skills/`，成为个人 skill，不再用 `//go:embed` 编进程序，内置的只剩 `code-review`。`-skills-list` 已核对四个都从 `~/.tern/skills/` 读到、无报错。随之改了：`internal/skills/skills_test.go` 删掉依赖内置 visualization 的两个测试（`examples/` 放行与 `scripts/`、`..` 拦截的路径检查；`validate_skill.py` 校验），只留“内置 skill 正文引用的文件都能读到”；README 内置表去掉这四行；`internal/skills/AGENTS.md` 不再提脚本在 `go test` 里跑。`loadReference` 放行 `examples/` 的逻辑不变，个人 skill 同样适用。下文第（一）块和验收第 1 条里的内置路径与 `go test` 校验按这次变更读。换机器时要自己拷 `~/.tern/skills/`。

实现与本设计的出入：`![说明](tern-image:N)` 在回答里直接用同源的附件地址显示（页面有登录态），不转 data URL；只有沙箱 iframe 里的 `tern-image:N` 才换成 data URL。visualization 的运行环境段落比第三节更短，因为 skill 自带校验有加载预算（`generated_illustration` 分支 38000 字节，现在 37982），再长就超。

## 一、为什么要改

上一轮把 learning-agent 的 `visualization` 迁成了内置 skill（`internal/skills/builtin/visualization/`），但只带了 `SKILL.md` 和 `references/`，删掉了对 `examples/`、`schemas/`、`scripts/` 的引用。原因有两个：tern 读不到这些文件；它们服务的两种模式 tern 也不支持。用户要求完整迁移，所以改成让 tern 支持这个 skill 的全部能力，skill 正文尽量不删。

skill 有五种主模式。对照 tern 现在的能力：

| skill 需要的能力 | 用在哪 | tern 现状（已在代码里核对） |
| --- | --- | --- |
| 回复里渲染 ` ```echarts ` 和 ` ```html type="renderer" ` | `echarts`、`html_svg`，以及另外两种模式的输出 | **已有**：`internal/web/page/md.js` 的 `vizSlot`/`claimViz`，沙箱 iframe（只给 `allow-scripts`） |
| 事实核验搜索（skill 里叫 `general_search`） | 所有模式 | **已有**，只是名字不同：`ds_search`、`ds_fetch`（`internal/websearch`）和 `web_search`、`open_page`（`internal/browser`） |
| 模型能看到用户的原图 | `static_image_overlay` | **已有**：网页端附图、`view_image`（`internal/agent/view_image.go`），只在 Vision 模型上提供（`internal/llm/protocol.go` 的模型表） |
| renderer 里能显示用户原图，skill 要求“真实 HTTPS URL” | `static_image_overlay` | **没有**：附件走 `GET /attachments/{name}`，要登录 cookie（`SameSite=Strict`，`internal/web/auth.go:356`）；沙箱 iframe 是空源，带不上 cookie；本机网页端也不一定是 HTTPS |
| 图片生成工具 `image_gen`（1–6 张，每张 prompt/width/height） | `generated_illustration` | **没有** |
| 回复里显示生成的图片 | `generated_illustration` | **没有**：`md.js` 不渲染 Markdown 图片语法 |
| 图片搜索 `image_search` | `generated_illustration` 校准外形，用户要真实图片时 | **没有** |
| `load_skill` 能读 `examples/*.md` | 原图标注、生成式配图卡住时参考 | **不能**：`loadReference` 只放行 `references/` 下的 `.md`（`internal/skills/skills.go`） |
| `scripts/validate_skill.py` | 开发时校验结构、路由和加载预算 | 没跟过来。对原 skill 实测能跑，结果 `PASS`，只依赖 Python 标准库 |

## 二、改什么

分四块，可以分批交给 tern 实现，每一块单独验收。

### （一）skill 全量迁入，load_skill 放开 `examples/`

- 把 learning-agent 的 `skills/visualization/` 整个目录复制到 `internal/skills/builtin/visualization/`（2026-11-03 起改放 `~/.tern/skills/visualization/`），包括 `examples/`、`schemas/`、`scripts/`、`agents/`，覆盖上一轮删减过的版本。
- `SKILL.md` 只改“本项目运行环境”那一段（第 46 行），按本设计说明 tern 的实际能力（见第三节），其余正文和 references 原样保留。上一轮删掉的 `examples/`、`schemas/`、`scripts/` 引用全部恢复。
- `internal/skills/skills.go` 的 `loadReference`：放行的前缀从只有 `references/` 改成 `references/` 和 `examples/`，仍然只收 `.md`、仍然先 `path.Clean`。`schemas/`、`scripts/`、`agents/` 照旧不给模型读。`LoadDefinition` 里 `file` 参数的说明同步改。
- 新增 `internal/skills/skills_test.go`，测两件事：
  1. 每个内置 skill 正文里出现的 `references/…​.md`、`examples/…​.md` 路径，都能用 `Load` 读到；
  2. 机器上有 `python3` 时，运行 `python3 -I builtin/visualization/scripts/validate_skill.py`，要求输出 `"status": "PASS"`；没有 `python3` 时 `t.Skip`。（2026-11-03 visualization 挪出内置后删掉；要校验时手动在 `~/.tern/skills/visualization/` 里跑。）
- `internal/skills/AGENTS.md` 里“不执行 skill 自带的脚本”这条不变：脚本只在测试里跑，模型碰不到。

### （二）原图标注：renderer 里引用附图

skill 要求 renderer 里的 `<img>` 用“真实 HTTPS URL”，这样沙箱 iframe 才能直接加载。tern 里不给模型 URL，改成让模型按图片编号引用，网页端负责把图换进去：

- **写法**：模型在 renderer 块里写 `<img src="tern-image:1">`，数字就是模型看到的 `[Image #1]` 的编号。编号在整个对话里唯一：`internal/agent/react.go:516` 按 `LastImageNumber+1` 分配，用户附图和 `view_image` 读的图共用一套编号。多图任务的 `images:[…]` 同样写 `tern-image:N`。
- **网页端**（`internal/web/page/md.js`、`chat.js`）：
  - 渲染 renderer 块前，扫出代码里所有的 `tern-image:N`；
  - 在当前对话已加载的图片里按 label 找到 ref（`react.go:300` 发给界面的图片带了 `ref` 和 `label`），用父页面的登录态 `fetch(refURL(ref))` 取到字节，转成 `data:` URL，替换进代码，然后再设 `srcdoc`；
  - 找不到编号或取图失败时，替换成一张写着“图片 N 不可用”的占位 SVG，不让 iframe 去请求外部地址；
  - iframe 的 `sandbox` 不变，仍然只有 `allow-scripts`。图片字节由父页面塞进去，iframe 拿不到登录态，也碰不到别的接口；只认当前对话自己的图片编号。
- **缓存**：`claimViz` 现在按“类型＋代码”找已有的 iframe，替换 data URL 之后，这个 key 仍然用替换前的原始代码，否则同一个回答每次重渲染都会重建 iframe。

### （三）生成式知识配图：新增 `image_gen` 工具

- **新工具** `image_gen`，参数照 skill 的契约（`references/generated-tool-contracts.md`）：`request_list` 有 1–6 项，每项 `prompt`、`width`、`height`。可选的 `model_version` 先不提供，skill 写明了“schema 里没有的字段不提交”。
- **后端**：调用图片生成 API，后端选型见第五节问题 1。拿到结果后下载图片字节，走 `llm.PrepareImage` 按内容寻址存进附件目录（和用户附图、`view_image` 同一套），按对话编号分配 `[Image #N]`。
- **工具结果**：每张图返回 `label`、`ref`、尺寸、成功或失败原因。部分失败只报失败的那几项，skill 规定“只重试失败项”。当前模型能看图（Vision）时，把生成的图像 `view_image` 一样随工具结果发给模型，满足 skill 要求的“生成后检查可见文字、构图”；不能看图时在结果里写明“未做视觉检查”。
- **回复里显示**：模型在回复正文里写 `![解释性图解](tern-image:N)`。`md.js` 给模型回答加上图片渲染，但只认 `tern-image:N`：取图方式和第（二）块相同，显示成普通 `<img>`，点开看大图。外部 URL 的 Markdown 图片仍不渲染，避免模型回答去请求任意外部地址。
- **开关**：和 `ds_search` 一样做成工具开关 `image_gen`，网页端设置里可以关，默认开（skill 只在路由选中 `generated_illustration` 时才调它）。`-disallowed-tools` 也认这个名字。README 的工具开关列表同步更新。
- **权限与审计**：调用会产生费用，算有外部副作用的工具。权限模式和 `ds_search` 处理方式一样：联网类调用要过 `netguard`，写审计记录。

### （四）图片搜索 `image_search`

做法见第五节问题 2。这一块不做时，skill 本身已有降级规则：工具不存在就从文字描述提炼特征，用户要真实图片时说明做不到。

## 三、`SKILL.md` 运行环境段落改成什么

替换第 46 行“本项目运行环境”整段，大意如下（实现时按最终做了哪几块调整）：

> **本项目运行环境**：回复显示在 tern 网页端（命令行里这些块按原文显示，不渲染），按 `Device platform` 为电脑端处理。`echarts` 块渲染在宽度随消息、高度 420px 的 iframe 中，renderer 块渲染在自动撑高的沙箱 iframe 中，两者都可从 jsDelivr 加载库。事实核验用 `ds_search`、`ds_fetch`（没有时用 `web_search`、`open_page`）。原图在本环境里没有 HTTPS 地址：凡是规范要求“真实 HTTPS URL”的地方，一律写 `tern-image:N`（N 是 `[Image #N]` 的编号），网页端会把图换进去，坐标照常用 0–999 相对坐标。生成式配图用 `image_gen`，结果在正文里写 `![说明](tern-image:N)`；没有 `image_gen` 时 `generated_illustration` 改用 `html_svg` 并标明“示意”。（`image_search` 的说明按问题 2 的结论写。）

## 四、不要改的

- 沙箱 iframe 的权限（`sandbox="allow-scripts"`，不加 `allow-same-origin`）。
- `/attachments/{name}` 的鉴权和 cookie 策略：不为了 iframe 放开匿名访问，也不发带签名的临时 URL。
- skill 的 references 正文：环境差异只写在 `SKILL.md` 的运行环境段落，不逐个文件改。
- 不执行 skill 自带脚本：模型碰不到 `scripts/`。（`validate_skill.py` 原先在 `go test` 里跑，visualization 挪出内置后改为手动跑。）

## 五、需要确认的问题

1. **`image_gen` 用哪个后端？** 现在 tern 的豆包走方舟 Coding Plan 入口。`.env.example` 写明 Coding Plan 和按量付费的 `/api/v3` 入口“计费不同、地址不能混用”。方舟的图片生成（Seedream 系列）是否在 Coding Plan 里、用哪个模型名、单张费用多少，代码里查不到，需要你确认。选项：
   - 方舟按量付费入口加 Seedream，另配一个 `IMAGE_API_URL`、`IMAGE_MODEL`，密钥沿用 `ARK_API_KEY` 还是另配，也需要你定；
   - 别的图片 API；
   - 这一期不做生成，只做（一）（二）。
2. **`image_search` 怎么做？** 选项：
   - 用现有浏览器（`internal/browser`）打开必应图片搜索，抽取图片地址，下载后存成附件返回 `tern-image:N`。不要新的密钥，但页面结构一变就会坏；
   - 接一个图片搜索 API，需要你选服务并提供密钥；
   - 这一期不做，靠 skill 自带的降级。
3. **生成的图要不要同时落到工作区？** 现在的设计只存进附件目录，在对话里显示。如果希望像 artifact 那样能在工作面板里打开、下载，要再加一步写入工作区，需要你定是否要、写到哪个目录。
4. **`image_gen` 默认开还是关？** 设计里写的是默认开，因为 skill 只在需要时才调用。它会产生费用，你也可以选默认关。

## 六、这一期不做

- 命令行（chat、终端）渲染图表和图片：仍按原文显示代码块。
- 移动端适配：skill 的移动端分支照常保留，tern 仍按电脑端处理。
- 除 visualization 之外，不让别的 skill 读 `examples/` 以外的新目录。

## 七、验收

1. `go test ./internal/skills/` 通过；在 `~/.tern/skills/visualization/` 里手动跑 `python3 -I scripts/validate_skill.py` 输出 `PASS`；`-skills-list` 列出的 visualization 正文与 learning-agent 原版的差异只有运行环境那一段。
2. 用真实模型（豆包，Vision）在网页端附一张图，问“图里标出所有的红色按钮”：回答里的 renderer 块显示原图和标注；浏览器开发者工具里看到 iframe 没有发出对 `/attachments/` 或外部地址的请求。
3. 同一对话里写 `tern-image:99`（不存在的编号）时显示“图片不可用”占位，不报错、不白屏。
4. （做了第三块时）问“画一张解释光合作用的知识配图”：调用 `image_gen`，回答正文里显示生成的图；把 `image_gen` 关掉再问一次，回答降级成 HTML/SVG 示意图，并标明“示意”。
5. 已有的 echarts、renderer 回答渲染不变，重渲染时 iframe 不重建（交互状态不丢）。
