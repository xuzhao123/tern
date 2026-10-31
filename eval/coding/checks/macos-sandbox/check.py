"""校验 macOS 沙箱：拆出 launch_linux.go / launch_darwin.go，Seatbelt 策略 seatbelt.sbpl 用 go:embed 编进去，
sandbox-exec -p 策略 -D 参数启动；策略拒绝默认、挡住运行数据与密钥、网络只经代理；设置里 macOS 认 sandbox-exec。
macOS 上的真实运行要 Mac，这里只能交叉编译和读代码。"""
import os, re, sys

problems = []
def read(path):
    return open(path, encoding="utf-8").read() if os.path.exists(path) else None

for path in ("internal/sandbox/launch_linux.go", "internal/sandbox/launch_darwin.go", "internal/sandbox/seatbelt.sbpl"):
    if read(path) is None:
        problems.append(f"应有 {path}")
darwin = read("internal/sandbox/launch_darwin.go") or ""
if darwin:
    if "/usr/bin/sandbox-exec" not in darwin:
        problems.append("launch_darwin.go 应用 /usr/bin/sandbox-exec 启动")
    if not re.search(r"//go:embed\s+seatbelt\.sbpl", darwin):
        problems.append("seatbelt.sbpl 应用 //go:embed 编进程序（launch_darwin.go）")
    if '"-p"' not in darwin or '"-D"' not in darwin:
        problems.append("策略应经 -p 传、路径经 -D 传")
linux = read("internal/sandbox/launch_linux.go") or ""
if linux and "bwrap" not in linux:
    problems.append("bwrap 的启动应搬进 launch_linux.go")
policy = read("internal/sandbox/seatbelt.sbpl") or ""
if policy:
    if "(deny default)" not in policy:
        problems.append("策略应以 (deny default) 为默认")
    for param in ("TERN_DATA", "TERN_ENV", "PROJECT_DATA", "PROJECT_ENV"):
        if not re.search(r'\(deny file-read\*[^\n]*' + param, policy):
            problems.append(f"策略应拒绝读 {param}")
    if not re.search(r'\(allow file-write\*[^\n]*SCRATCH', policy):
        problems.append("策略应允许写本对话的沙箱目录（SCRATCH）")
    if not re.search(r'network-outbound[^\n]*\(param "PROXY"\)', policy):
        problems.append("策略的外网应只经联网代理（PROXY 参数）")
settings = read("internal/appserver/settings.go") or ""
if "sandbox-exec" not in settings:
    problems.append("internal/appserver/settings.go 判断 bash 可不可用时应认 macOS 的 /usr/bin/sandbox-exec")
agents = read("internal/sandbox/AGENTS.md") or ""
if "Seatbelt" not in agents and "sandbox-exec" not in agents:
    problems.append("internal/sandbox/AGENTS.md 应补 macOS 用 Seatbelt")
if problems:
    sys.exit("\n".join(problems))
print("ok")
