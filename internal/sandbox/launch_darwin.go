//go:build darwin

package sandbox

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/telemetry"
)

// macOS 上的沙箱：/usr/bin/sandbox-exec 加一段 Seatbelt 策略（seatbelt.sbpl）代替 bwrap。没有挂载命名空间，
// 命令看到的是真实路径；也没有 cgroup 与 seccomp，内存、CPU、进程数不限制，时长照旧（超时杀进程组）。
// 共用的部分（参数解析、输出截断、后台命令、规则文字）在 sandbox.go；Linux 的启动在 launch_linux.go。

// sandboxExec 是 macOS 自带的 Seatbelt 沙箱启动程序。
const sandboxExec = "/usr/bin/sandbox-exec"

// processView 写进 bash 的工具描述：没有 PID namespace，命令看得到别的进程（只是不能给它发信号）。
const processView = ""

// serviceNote 写进 bash 的工具描述（见 sandbox.go 的 Definitions）：macOS 上没有自己的网络，本机回环也不放开，
// 命令连不到宿主机上的服务，同一条命令里起的也连不上。
const serviceNote = "macOS 上连不上本机的服务，包括同一条命令里起的；整个过程超过时限，就把这一整条放到后台。"

// serviceNoteShort 写进后台命令的说明（见 jobs.go 的 startJob）。
const serviceNoteShort = "macOS 上连不上本机的服务，包括同一条命令里起的。"

// launcher 是启动沙箱的程序名，只用在启动失败的说明里。
const launcher = "sandbox-exec"

// signalNote 为空：macOS 上没有 cgroup，命令被信号结束多半是自己或被用户停掉的，没有内存上限的说明可加。
var signalNote string

//go:embed seatbelt.sbpl
var seatbeltPolicy string

// 策略里这行是工作区可写规则的位置：工作区可写时换成下面的两条，只读（plan 模式、校验命令）时整行去掉。
const writeWorkspaceMark = "; WRITE-WORKSPACE\n"

const writeWorkspaceRules = `(allow file-write* (subpath (param "WORKSPACE")))
(deny file-write* (subpath (param "WORKSPACE_GIT")))
`

// ruleText 是 Rules 里平台相关的那一段：工作目录、能写哪里、资源限制。
func ruleText() string {
	scratch, _ := filepath.Abs(Dir)
	limits := "没有内存、CPU 与进程数限制，单条命令最多 " + Timeout.String() + "（到时被终止，返回终止前的输出）。"
	rule := "\n可以用 bash 在 macOS 沙箱（Seatbelt）里执行命令：工作目录 " + scratch + "（整个对话内保留文件，包括之前几轮写的和用户上传的），每次调用是新的 shell；只能写这个目录、" + os.TempDir() + " 与设备文件；" + limits
	if Project != "" {
		access := "可写"
		if !ProjectWrite {
			access = "只读（plan 模式）"
		}
		project, _ := filepath.Abs(Project)
		rule = "\n可以用 bash 在 macOS 沙箱（Seatbelt）里执行命令：" + project + " 就是对话所选的工作区（" + access + "，和文件工具看到的是同一份代码；.git 只读，.env 与 .data 看不到）；本对话的临时目录是 " + scratch + "（整个对话内保留，用户上传的文件在这里）。每次调用是新的 shell；只能写这两处、" + os.TempDir() + " 与设备文件；" + limits + "有宿主机的 Go 工具链（模块缓存与构建缓存是空的）。改工作区的源码用 edit_file、write_file（有快照可以撤销，会跑编辑 hook），bash 用来构建、测试和查看；bash 改的文件没有快照，撤销时不会还原。"
	}
	if front().Node {
		rule += "改了网页或脚本，交付前在沙箱里实际检查一次：`node --check 文件.js` 查语法。"
	}
	return rule
}

// bashIntro 是 bash 工具描述的开头：没有挂载，工作区就是它的真实路径，本对话的目录就是临时目录。
func bashIntro() string {
	scratch, _ := filepath.Abs(Dir)
	bash := "在隔离的 macOS 沙箱（Seatbelt）里用 bash 执行一条命令，返回退出码和输出。工作目录 " + scratch + " 在整个对话内保留文件（之前几轮写的、用户上传的都在）；"
	if Project != "" {
		project, _ := filepath.Abs(Project)
		bash = "在隔离的 macOS 沙箱（Seatbelt）里用 bash 执行一条命令，返回退出码和输出。" + project + " 是对话所选的工作区（和文件工具看到的是同一份代码；bash 里的路径就是它，不是 /workspace）；" + scratch + " 是本对话的临时目录（整个对话内保留，用户上传的文件在这里）；"
	}
	return bash
}

// tmpNote 接着 bashIntro 说临时目录：macOS 上是宿主机的临时目录（$TMPDIR），不是每次新建的。
var tmpNote = "临时目录是 " + os.TempDir() + "（宿主机的，不保证是空的）。"

// realPath 把路径里的符号链接解开：Seatbelt 按真实路径匹配（macOS 上 /tmp 是 /private/tmp、
// TMPDIR 在 /var/folders 下也是 /private/var），参数要写解开后的路径才拦得住、也放得开。
func realPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// sandboxCommand 拼出 macOS 上的一次启动：策略经 -p 传，路径经 -D 传，命令是 /bin/bash -c。
// sandbox-exec 不动环境变量，所以环境在这里设：照 Linux 清空，只留 PATH、HOME（本对话的沙箱目录）、
// LANG、TMPDIR 与代理，密钥不带进去。
func sandboxCommand(ctx context.Context, workspace, command string, tty, writable bool) (*launchSpec, error) {
	if _, err := os.Stat(sandboxExec); err != nil {
		return nil, llm.Permanent(errors.New("没有 " + sandboxExec + "，这台机器不能用 Seatbelt 沙箱"))
	}
	policy := strings.Replace(seatbeltPolicy, writeWorkspaceMark, "", 1)
	if Project != "" && writable {
		policy = strings.Replace(seatbeltPolicy, writeWorkspaceMark, writeWorkspaceRules, 1)
	}
	home := os.Getenv("TERN_HOME")
	if home == "" {
		home, _ = os.Getwd()
	}
	data, envFile := filepath.Join(home, ".data"), filepath.Join(home, ".env")
	projectData, projectEnv := data, envFile
	target, git := workspace, filepath.Join(workspace, ".git")
	if Project != "" {
		project, _ := filepath.Abs(Project)
		target, git = project, filepath.Join(project, ".git")
		projectData, projectEnv = filepath.Join(project, ".data"), filepath.Join(project, ".env")
	}
	// 没有工作区时项目那两个参数与运行数据指向同一处：-D 每项都要有值，重复拒绝一次没有影响。
	params := [][2]string{
		{"TERN_DATA", realPath(data)}, {"TERN_ENV", realPath(envFile)},
		{"PROJECT_DATA", realPath(projectData)}, {"PROJECT_ENV", realPath(projectEnv)},
		{"SCRATCH", realPath(workspace)}, {"TMP", realPath(os.TempDir())},
		{"WORKSPACE", realPath(target)}, {"WORKSPACE_GIT", realPath(git)},
		// Seatbelt 的网络地址只接受 localhost 或 *，HTTP_PROXY 仍使用实际监听的 127.0.0.1。
		{"PROXY", strings.Replace(proxyEndpoint(), "127.0.0.1:", "localhost:", 1)},
	}
	spec := &launchSpec{argv: []string{sandboxExec, "-p", policy}, dir: realPath(target)}
	for _, p := range params {
		spec.argv = append(spec.argv, "-D", p[0]+"="+p[1])
	}
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	}
	proxy := "http://" + proxyEndpoint()
	spec.env = []string{
		"PATH=" + path, "HOME=" + workspace, "TMPDIR=" + os.TempDir(),
		"HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy, "http_proxy=" + proxy, "https_proxy=" + proxy, "NO_PROXY=" + "localhost,127.0.0.1",
	}
	if lang := os.Getenv("LANG"); lang != "" {
		spec.env = append(spec.env, "LANG="+lang)
	}
	spec.env = append(spec.env, telemetry.Env(ctx)...)
	if tty {
		// BSD 的 script 把命令当参数（没有 -c）：命令原样交给 /bin/bash -c，写法和前台一致。
		spec.argv = append(spec.argv, "script", "-q", "/dev/null", "/bin/bash", "-c", command)
	} else {
		spec.argv = append(spec.argv, "/bin/bash", "-c", command)
	}
	return spec, nil
}

// kill 把一次执行整个结束：macOS 上没有 cgroup，杀整个进程组（sandbox-exec 与它起的命令在同一组）。
func kill(cmd *exec.Cmd, unit string) {
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// cleanup 没有要从宿主机清掉的临时内容：临时文件都在进程自己的临时目录里。
func cleanup() {}
