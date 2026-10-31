"""校验 /review 的范围：模板与 spec 第 2 节给的全文一致，README 的 /review 一行同步。"""
import re, sys

spec = open("spec/review-scope.md", encoding="utf-8").read()
block = re.search(r"`review\.md` 改成：\s*```markdown\n(.*?)\n```", spec, re.S)
if not block:
    sys.exit("spec/review-scope.md 里找不到第 2 节的模板全文（校验脚本需要更新）")
want = block.group(1).strip()
got = open("internal/commands/builtin/review.md", encoding="utf-8").read().strip()
problems = []
if got != want:
    problems.append("internal/commands/builtin/review.md 应与 spec 第 2 节给的全文一字不差")
readme = open("README.md", encoding="utf-8").read()
row = next((l for l in readme.splitlines() if l.startswith("| `/review`")), "")
if "/review main" not in row or "/review <提交>" not in row:
    problems.append(f"README 内置命令表的 /review 一行应写明 /review main 与 /review <提交>：{row}")
if problems:
    sys.exit("\n".join(problems))
print("ok")
