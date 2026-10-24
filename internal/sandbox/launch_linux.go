//go:build linux

package sandbox

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/telemetry"
)

// Linux 上的沙箱：每次调用起一个全新的沙箱，里面跑一次 /bin/bash -c：
//
//	systemd-run --user --scope   cgroup：内存、CPU、进程数上限
//	  └ bwrap                    namespace：只读系统目录、可写 /workspace、断网、看不到家目录与项目
//	      └ seccomp              禁止 ptrace、mount、bpf 等危险系统调用
//	          └ sandbox-init     沙箱里的第一个程序：转发代理端口，再运行命令
//	              └ /bin/bash -c "<command>"
//
// 和 Codex、Claude Code 在 Linux 上的路线一致（bubblewrap + seccomp），不需要 Docker。
// 共用的部分（参数解析、输出截断、后台命令、规则文字）在 sandbox.go；macOS 的启动在 launch_darwin.go。

// processView 写进 bash 的工具描述：沙箱有自己的 PID 与网络 namespace，别的命令的进程与端口都看不到。
const processView = "，只看得到自己的进程和网络"

// launcher 是启动沙箱的程序名，只用在启动失败的说明里。
const launcher = "bubblewrap"

// goEnv 是宿主机上 Go 工具链与模块缓存的位置：有工作区时只读挂进沙箱，命令能离线 go build / go test。
// 在工作区里跑 go env：GOTOOLCHAIN=auto 时它按工作区的 go.mod 选工具链，沙箱里挂的就是在
// 那个仓库里手动敲 go build 会用的那份。工作区里跑不成（比如要求的工具链下载不了）就在
// 进程的当前目录里再跑一次，沙箱里至少有一份 Go，版本不够时由 go 自己报错。
// 用 sync.OnceValue 只算一次：一个 agent 进程只有一个工作区。
var goEnv = sync.OnceValue(func() [2]string {
	if env, ok := goEnvIn(Project); ok || Project == "" {
		return env
	}
	env, _ := goEnvIn("")
	return env
})

func goEnvIn(dir string) ([2]string, bool) {
	cmd := exec.Command("go", "env", "GOROOT", "GOMODCACHE")
	cmd.Dir = dir // 空表示当前目录
	out, err := cmd.Output()
	if err != nil {
		return [2]string{}, false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return [2]string{}, false
	}
	return [2]string{lines[0], lines[1]}, true
}

// 资源上限：一次命令最多用这么多；时长由 -bash-timeout 控制（见 Timeout）。
// 内存给 2G：沙箱里冷缓存编译整个 Go 模块时 512M 会被内核 OOM 杀掉。机器 7.7G，前台加后台最多同时 5 个沙箱，这是上限，不是预留。
// 进程数按线程算：无头 Chrome 加载一个页面就要 100 个左右（64 时建不了线程直接崩），256 仍能挡住 fork 炸弹。
const (
	memoryMax = "2G"
	tasksMax  = "256"
	cpuQuota  = "100%"
	tmpSize   = "67108864" // /tmp 是内存盘，最多 64MB
)

// signalNote 是命令被 SIGKILL/SIGTERM 结束时的说明：前台、后台都没有别的杀法，
// 多半是 systemd 按 cgroup 的内存上限结束了整个 scope。
var signalNote = "进程被系统结束：多半是超出了内存上限（" + memoryMax + "）。Go 项目可以分开编译各个包，其他程序减少并行"

// ruleText 是 Rules 里平台相关的那一段：工作目录、能写哪里、资源限制，以及沙箱里能用哪些前端工具。
func ruleText() string {
	limits := "内存、进程数有限，单条命令最多 " + Timeout.String() + "（到时被终止，返回终止前的输出）。"
	rule := "\n可以用 bash 在 Linux 沙箱里执行命令：工作目录 /workspace（整个对话内保留文件，包括之前几轮写的和用户上传的），每次调用是新的 shell；只能写 /workspace 和 /tmp（/tmp 每次调用都是空的）；" + limits
	if Project != "" {
		access := "可写"
		if !ProjectWrite {
			access = "只读（plan 模式）"
		}
		rule = "\n可以用 bash 在 Linux 沙箱里执行命令：/workspace 就是对话所选的工作区（" + access + "，和文件工具看到的是同一份代码；.git 只读，.env 与 .data 看不到）；/scratch 是本对话的临时目录（整个对话内保留，用户上传的文件在这里）。每次调用是新的 shell；只能写这两处和 /tmp（/tmp 每次调用都是空的，要留到下一次调用的放 /scratch）；" + limits + "有 Go 工具链，离线编译（模块缓存只读，GOPROXY=off）。改工作区的源码用 edit_file、write_file（有快照可以撤销，会跑编辑 hook），bash 用来构建、测试和查看；bash 改的文件没有快照，撤销时不会还原。"
	}
	if ProjectRO {
		rule += "项目目录只读挂在 /project。"
	}
	if t := front(); t.Node || t.Chrome != "" {
		rule += "改了网页或脚本，交付前在沙箱里实际检查一次："
		if t.Node {
			rule += "`node --check 文件.js` 查语法；"
		}
		if t.Chrome != "" {
			rule += "`" + chromeName() + " --headless --no-sandbox --disable-gpu --user-data-dir=/tmp/chrome --dump-dom file:///路径.html` 输出脚本运行之后的 DOM（加 --enable-logging=stderr 能看到脚本报错），可以写一个小测试页加载改动的代码、模拟点击或悬浮后打印结果（Chrome 启动时访问 Google 的域名被拒是正常的，不影响结果，不用为此申请联网）；这个命令已经关掉了 Chrome 自己的后台联网；自己写脚本启动 Chrome 时也用它，别直接调 /opt/chrome 下的程序。Google 的域名在这个 Chrome 里一律连不上；真要用就用 curl（会照常申请联网）。"
		}
	}
	return rule
}

// bashIntro 是 bash 工具描述的开头：工作区挂在 /workspace，本对话的目录挂在 /scratch。
func bashIntro() string {
	bash := "在隔离的 Linux 沙箱里用 bash 执行一条命令，返回退出码和输出。工作目录 /workspace 在整个对话内保留文件（之前几轮写的、用户上传的都在）；"
	if Project != "" {
		bash = "在隔离的 Linux 沙箱里用 bash 执行一条命令，返回退出码和输出。/workspace 是对话所选的工作区（和文件工具看到的是同一份代码）；/scratch 是本对话的临时目录（整个对话内保留，用户上传的文件在这里）；"
	}
	return bash
}

// tmpNote 接着 bashIntro 说临时目录：Linux 上是每次新建的内存盘。
const tmpNote = "/tmp 每次都是空的。"

// 沙箱里的 chrome 命令是 /opt/chrome-bin 下的包装脚本（名字和真正的程序一样，PATH 最前面）：
// Chrome 自己的后台联网（组件更新、安全浏览、优化提示、自动填充、同步）不是任务需要的，
// 但沙箱的代理会把每个域名都当成一次联网审批推给界面，所以固定关掉。
func chromeName() string { return filepath.Base(front().Chrome) }

// chromeReal 是包装脚本 exec 的真正的 Chrome：挂在 /opt/chrome 下时是挂进去的那一份，
// 装在 /usr 下时是宿主机上的绝对路径（/usr 在沙箱里本来就看得到）。
func chromeReal() string {
	if t := front(); t.ChromeDir != "" {
		return "/opt/chrome/" + t.Chrome
	}
	return front().Chrome
}

// chromePAC 是给沙箱里的 Chrome 用的 PAC：光有关后台联网的参数不够，Chrome 155 加载本地页面时仍会去连
// accounts.google.com、update.googleapis.com、www.google.com、www.gstatic.com（试过
// --disable-extensions、--disable-field-trial-config、--host-resolver-rules 都拦不住）。
// 这里把 Google 家的域名指向一个不通的端口：失败就走不到沙箱的代理，也就不会弹联网审批；
// 别的域名照旧交给沙箱的代理，照旧问人。base64 内嵌在 data: URL 里，省得处理引号和转义。
func chromePAC() string {
	pac := `function FindProxyForURL(url, host) {
  if (/(^|\.)(google\.com|googleapis\.com|gstatic\.com|gvt1\.com)$/.test(host)) return "PROXY 127.0.0.1:9";
  return "PROXY 127.0.0.1:` + proxyPort + `";
}
`
	return base64.StdEncoding.EncodeToString([]byte(pac))
}

// chromeWrapper 生成沙箱里 chrome 命令的包装脚本。PAC 那个参数整个用单引号括起来：里面的 `;` 在 sh 里是命令分隔符，
// 不括住的话 Chrome 只收到 --proxy-pac-url=data:application/x-javascript-config，后面的 base64 被当成另一条命令，
// PAC 等于没生效。base64 只有 A-Z a-z 0-9 + / =，放进单引号没有转义问题。
func chromeWrapper() string {
	return `#!/bin/sh
# tern 的沙箱：Chrome 的后台联网（更新、安全浏览、优化提示等）不是任务需要的，固定关掉。
exec ` + chromeReal() + ` --disable-background-networking --disable-component-update \
  --disable-sync --disable-default-apps --disable-domain-reliability \
  --disable-client-side-phishing-detection --no-first-run --no-default-browser-check \
  --metrics-recording-only --safebrowsing-disable-auto-update \
  --disable-features=OptimizationHints,OptimizationGuideModelDownloading,AutofillServerCommunication,Translate,MediaRouter,NetworkTimeServiceQuerying \
  '--proxy-pac-url=data:application/x-javascript-config;base64,` + chromePAC() + `' \
  "$@"
`
}

var (
	etcOnce  sync.Once
	etcDir   string
	cgroupOK = sync.OnceValue(func() bool { return systemdRun() == nil })
	// systemd ≥254 的 systemd-run 会先把命令行参数里的 ${...} 换成它自己的环境变量：沙箱里 `x=hi; echo ${x}`
	// 会打印空，还输出“Referenced but unset environment variable…”。旧版 systemd 没有这个替换，也不认这个参数，
	// 所以带参数探测一次：认就加，不认就不加（旧版本来也不替换）。
	noExpandEnv = sync.OnceValue(func() bool { return systemdRun("--expand-environment=no") == nil })
)

// systemdRun 用 systemd-run 跑一个 true：探测它能不能用，以及认不认 extra 里的参数。
func systemdRun(extra ...string) error {
	args := append([]string{"--user", "--scope", "--quiet"}, extra...)
	return exec.Command("systemd-run", append(args, "true")...).Run()
}

// sandboxCommand 拼出 Linux 上的一次启动：bwrap 的参数、seccomp 程序与 chrome 包装脚本那两个文件描述符，
// 有 systemd 用户会话时再放进一个有名字的 scope（cgroup）。
func sandboxCommand(ctx context.Context, workspace, command string, tty, writable bool) (*launchSpec, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, llm.Permanent(err)
	}
	// seccomp 程序通过文件描述符交给 bwrap（--seccomp 3）。bwrap 要到 exec 之后才读它，
	// 所以临时文件在启动之后才删（见 launchSpec.done）。
	filter, err := os.CreateTemp("", "tern-seccomp-")
	if err != nil {
		return nil, llm.Permanent(err)
	}
	if _, err := filter.Write(seccompProgram()); err != nil {
		filter.Close()
		os.Remove(filter.Name())
		return nil, llm.Permanent(err)
	}
	filter.Seek(0, 0)
	spec := &launchSpec{extra: []*os.File{filter}, done: func() { filter.Close(); os.Remove(filter.Name()) }}

	// chrome 的包装脚本经管道交给 bwrap 的 --ro-bind-data：它是 ExtraFiles 的第二项，也就是 fd 4
	// （第一项是 fd 3 的 seccomp 程序）。脚本几百字节，放得进管道缓冲，写完就关掉写端。
	if front().Chrome != "" {
		wrapper, pw, err := os.Pipe()
		if err != nil {
			spec.done()
			return nil, llm.Permanent(err)
		}
		_, err = io.WriteString(pw, chromeWrapper())
		pw.Close()
		if err != nil {
			wrapper.Close()
			spec.done()
			return nil, llm.Permanent(err)
		}
		spec.extra = append(spec.extra, wrapper)
	}

	// 和 Claude Code 给 Bash 子进程设 TRACEPARENT 一样：沙箱里的程序愿意的话，可以把自己的 span 接到这次执行下面。
	spec.argv = bwrapArgs(ctx, workspace, self, command, tty, writable)
	if cgroupOK() {
		// 每次执行一个有名字的 scope（一个 cgroup）：超时时按 cgroup 整体杀掉。
		// 不能只杀进程组：bwrap 的 --new-session 让沙箱里的进程进了新的会话，进程组信号打不到它们。
		spec.unit = fmt.Sprintf("tern-sandbox-%d-%d", os.Getpid(), time.Now().UnixNano())
		opts := []string{"--user", "--scope", "--quiet", "--collect", "--unit", spec.unit}
		if noExpandEnv() {
			// 不让 systemd 替换命令行里的 ${...}：命令要原样交给沙箱里的 shell，否则模型写的 `${x}`、`${PIPESTATUS[0]}` 都变成空。
			opts = append(opts, "--expand-environment=no")
		}
		opts = append(opts, "-p", "MemoryMax="+memoryMax, "-p", "MemorySwapMax=0", "-p", "TasksMax="+tasksMax, "-p", "CPUQuota="+cpuQuota, "--")
		spec.argv = append(append([]string{"systemd-run"}, opts...), spec.argv...)
	} else {
		fmt.Println("Sandbox: cgroup 不可用（没有 systemd 用户会话），本次不限制内存、CPU 与进程数")
	}
	return spec, nil
}

// kill 把一次执行整个结束：有 cgroup 时按 cgroup 杀（bwrap 的 --new-session 让沙箱里的进程离开了进程组），再杀进程组。
func kill(cmd *exec.Cmd, unit string) {
	if unit != "" {
		exec.Command("systemctl", "--user", "kill", "--signal=SIGKILL", unit+".scope").Run()
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// cleanup 删掉为沙箱准备、退出时要清掉的临时内容（/etc 的最小版本）。
func cleanup() {
	if etcDir != "" {
		os.RemoveAll(etcDir)
	}
}

func bwrapArgs(ctx context.Context, workspace, self, command string, tty, writable bool) []string {
	args := []string{"bwrap",
		// 新的用户、PID、网络、IPC、UTS、cgroup namespace；禁止沙箱里再建 user namespace；去掉全部特权。
		"--unshare-all", "--unshare-user", "--disable-userns", "--cap-drop", "ALL",
		"--die-with-parent", "--new-session", "--hostname", "sandbox",
		// 系统目录只读。/bin、/lib 在这台机器上是指向 /usr 的符号链接。
		"--ro-bind", "/usr", "/usr", "--symlink", "usr/bin", "/bin", "--symlink", "usr/sbin", "/sbin", "--symlink", "usr/lib", "/lib",
		"--proc", "/proc", "--dev", "/dev", "--size", tmpSize, "--tmpfs", "/tmp",
		"--dir", "/etc",
	}
	if _, err := os.Lstat("/lib64"); err == nil {
		args = append(args, "--symlink", "usr/lib64", "/lib64")
	}
	// /etc 只挂运行程序必需的几项；passwd、group、hosts 用生成的最小版本，不暴露本机用户列表。
	for _, path := range []string{"/etc/ld.so.cache", "/etc/alternatives", "/etc/ssl", "/etc/ca-certificates", "/etc/localtime", "/etc/fonts"} {
		if _, err := os.Stat(path); err == nil {
			args = append(args, "--ro-bind", path, path)
		}
	}
	etc := minimalEtc()
	for _, name := range []string{"passwd", "group", "hosts"} {
		args = append(args, "--ro-bind", filepath.Join(etc, name), "/etc/"+name)
	}
	home, path := "/workspace", "/usr/local/bin:/usr/bin:/bin"
	if t := front(); t.NodeDir != "" {
		args, path = append(args, "--ro-bind", t.NodeDir, "/opt/node"), "/opt/node/bin:"+path
	}
	if t := front(); t.ChromeDir != "" {
		args, path = append(args, "--ro-bind", t.ChromeDir, "/opt/chrome"), "/opt/chrome:"+path
	}
	if t := front(); t.Chrome != "" {
		// 包装脚本和真正的程序同名的那个文件，放在 PATH 最前面：命令照旧写 chrome 的名字，就带上关掉后台联网的参数。
		args = append(args, "--perms", "0755", "--ro-bind-data", "4", "/opt/chrome-bin/"+chromeName())
		path = "/opt/chrome-bin:" + path
	}
	var extraEnv []string
	if Project != "" {
		mode := "--ro-bind"
		if writable {
			mode = "--bind"
		}
		args = append(args, mode, Project, "/workspace")
		// 版本库历史只读：命令能看 diff、log，不能提交、改写历史；密钥和运行数据换成空的。
		if info, err := os.Stat(filepath.Join(Project, ".git")); err == nil && info.IsDir() {
			args = append(args, "--ro-bind", filepath.Join(Project, ".git"), "/workspace/.git")
		}
		for _, hidden := range []string{".data", ".cache"} {
			if info, err := os.Stat(filepath.Join(Project, hidden)); err == nil && info.IsDir() {
				args = append(args, "--tmpfs", "/workspace/"+hidden)
			}
		}
		if _, err := os.Stat(filepath.Join(Project, ".env")); err == nil {
			args = append(args, "--ro-bind", "/dev/null", "/workspace/.env")
		}
		args = append(args, "--bind", workspace, "/scratch", "--chdir", "/workspace")
		home = "/scratch"
		if g := goEnv(); g[0] != "" && GoCache != "" && os.MkdirAll(filepath.Join(GoCache, "tmp"), 0o700) == nil {
			// 模块缓存按 try 挂：新机器上还没下载过任何模块（或换了 HOME）时这个目录不存在，
			// --ro-bind 会让整个沙箱起不来；没有依赖的项目照常编译，要用模块的报错说找不到。
			args = append(args, "--ro-bind", g[0], "/opt/go", "--ro-bind-try", g[1], "/opt/gomod", "--bind", GoCache, "/opt/gocache")
			path = "/opt/go/bin:" + path
			extraEnv = []string{"GOROOT=/opt/go", "GOMODCACHE=/opt/gomod", "GOCACHE=/opt/gocache",
				// 沙箱只有 1 个 CPU（CPUQuota=100%），并行编译只会多占内存，一次编一个包。
				"GOTMPDIR=/opt/gocache/tmp", "GOPROXY=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off", "GOFLAGS=-mod=readonly -p=1"}
		}
	} else {
		args = append(args, "--bind", workspace, "/workspace", "--chdir", "/workspace")
	}
	if ProjectRO {
		if project, err := os.Getwd(); err == nil {
			args = append(args, "--ro-bind", project, "/project")
			// 后挂载的会盖住先挂载的：密钥、运行数据和 git 历史换成空的。
			for _, hidden := range []string{".data", ".git", ".cache", "bin"} {
				if info, err := os.Stat(filepath.Join(project, hidden)); err == nil && info.IsDir() {
					args = append(args, "--tmpfs", "/project/"+hidden)
				}
			}
			if _, err := os.Stat(filepath.Join(project, ".env")); err == nil {
				args = append(args, "--ro-bind", "/dev/null", "/project/.env")
			}
		}
	}
	proxy := "http://" + proxyEndpoint()
	args = append(args,
		"--ro-bind", self, "/.sandbox/agent", "--ro-bind", proxyDir, "/.sandbox/net",
		// 挂载都设好之后把根目录改成只读（/workspace、/tmp 是单独的挂载，仍可写）；
		// 让 sandbox-init 当 1 号进程，/proc/1/cmdline 就不会暴露 bwrap 的命令行和宿主机上的路径。
		"--remount-ro", "/", "--as-pid-1",
		// 环境变量全部清空：ARK_API_KEY 之类的密钥不会带进沙箱。
		"--clearenv", "--setenv", "PATH", path, "--setenv", "HOME", home, "--setenv", "LANG", "C.UTF-8", "--setenv", "TMPDIR", "/tmp",
		"--setenv", "HTTP_PROXY", proxy, "--setenv", "HTTPS_PROXY", proxy, "--setenv", "http_proxy", proxy, "--setenv", "https_proxy", proxy, "--setenv", "NO_PROXY", "localhost,127.0.0.1",
	)
	if tty {
		// script 用 $SHELL 跑命令（不设就是 sh），设成 bash：命令的写法和前台一致（前台是 /bin/bash -c）。
		args = append(args, "--setenv", "SHELL", "/bin/bash")
	}
	for _, pair := range append(extraEnv, telemetry.Env(ctx)...) {
		name, value, _ := strings.Cut(pair, "=")
		args = append(args, "--setenv", name, value)
	}
	args = append(args, "--seccomp", "3", "/.sandbox/agent", "sandbox-init")
	if tty {
		// 命令放进 script，由它开一个伪终端：命令以为自己在终端里。-e 让退出码是命令的（不然总是 0）；
		// -f 每条输出立刻交出来。script 用 $SHELL 跑命令（不设就是 sh），上面设成 bash，命令的写法和前台一致。
		// 命令原样作为一个参数传，不经 shell 再转一层。
		args = append(args, "script", "-q", "-f", "-e", "-c", command, "/dev/null")
	} else {
		args = append(args, "/bin/bash", "-c", command)
	}
	return args
}

func minimalEtc() string {
	etcOnce.Do(func() {
		dir, err := os.MkdirTemp("", "tern-sandbox-etc-")
		if err != nil {
			return
		}
		uid, gid := os.Getuid(), os.Getgid()
		os.WriteFile(filepath.Join(dir, "passwd"), fmt.Appendf(nil, "sandbox:x:%d:%d:sandbox:/workspace:/bin/bash\n", uid, gid), 0o644)
		os.WriteFile(filepath.Join(dir, "group"), fmt.Appendf(nil, "sandbox:x:%d:\nnogroup:x:65534:\n", gid), 0o644)
		os.WriteFile(filepath.Join(dir, "hosts"), []byte("127.0.0.1 localhost sandbox\n"), 0o644)
		etcDir = dir
	})
	return etcDir
}
