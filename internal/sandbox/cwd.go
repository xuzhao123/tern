package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// 工作目录在 bash 调用之间保留（同 Claude Code）：每次调用仍是新的 shell，环境变量、shell 变量和函数不保留，
// 只有 cd 的结果带到下一条命令。做法是把命令包一层：开头 cd 到上次的目录，结束时（EXIT trap）把 pwd 写进
// 本对话目录里的一个文件，执行完读回来。离开工作区和本对话目录的，下一条命令重置回工作区根目录并在结果里说明。
// 跨轮保留：agent 把它写进检查点的 state（Cwd、RestoreCwd），续聊、续跑时接着用；子 agent 从根目录开始。
// 后台命令从当前目录启动，但不改它；校验命令（Check）和 !命令 不受影响。

// cwdFile 是记录 pwd 的文件，在本对话目录（Dir）里；沙箱里经 /scratch 或 /workspace 看到同一个文件。
const cwdFile = ".config/tern/cwd"

var (
	cwd     string // 上次命令结束时的目录（命令看到的路径）；空表示根目录
	cwdHost bool   // cwd 是在宿主机还是沙箱里记下的：完全访问与沙箱的路径不同，换了模式就从根目录重来
)

// Cwd 返回当前工作目录（根目录时为空）和它是不是宿主机上的路径，agent 写进检查点。
func Cwd() (string, bool) {
	cwdMu.Lock()
	defer cwdMu.Unlock()
	return cwd, cwdHost
}

// RestoreCwd 在开始工作时用检查点里的目录接上；不在允许范围内的（换了模式、目录挪了）下一条命令自然从根目录开始。
func RestoreCwd(dir string, host bool) {
	cwdMu.Lock()
	defer cwdMu.Unlock()
	cwd, cwdHost = dir, host
}

// CurrentDir 是下一条 bash 命令开始时的目录（命令看到的路径），写进环境信息：新一轮开始时模型知道自己在哪。
func CurrentDir() string {
	cwdMu.Lock()
	defer cwdMu.Unlock()
	return startDir(Host)
}

// cwdRoots 返回命令看到的工作区根目录和本对话目录（没有工作区时二者相同）。
// Linux 沙箱里是挂载点；macOS 与宿主机上是真实路径（Seatbelt 下 bash 的 pwd 给的是解开符号链接后的路径）。
func cwdRoots(host bool) (root, scratch string) {
	if !host && runtime.GOOS == "linux" {
		if Project != "" {
			return "/workspace", "/scratch"
		}
		return "/workspace", "/workspace"
	}
	return realDir(hostDir()), realDir(Dir)
}

func realDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// within 判断 path 是否在 dir 里（含 dir 本身）。
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// shellQuote 把字符串放进单引号，命令里原样使用。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// startDir 是这条命令开始时的目录；换过模式或目录已不在允许范围内时为根目录。
func startDir(host bool) string {
	root, scratch := cwdRoots(host)
	if cwd == "" || cwdHost != host || !(within(cwd, root) || within(cwd, scratch)) {
		return root
	}
	return cwd
}

// withCwd 给命令加上开头的 cd；track 为真时再加 EXIT trap，把结束时的目录写进 cwdFile。
// cd 失败（目录被删了）就留在根目录，命令照常执行。
func withCwd(command string, host, track bool) string {
	root, scratch := cwdRoots(host)
	prefix := ""
	if dir := startDir(host); dir != root {
		prefix = "cd -- " + shellQuote(dir) + " 2>/dev/null || cd -- " + shellQuote(root) + "\n"
	}
	if track {
		prefix += "trap 'pwd > " + shellQuote(filepath.Join(scratch, cwdFile)) + " 2>/dev/null' EXIT\n"
	}
	return prefix + command
}

// prepareCwd 在命令执行前删掉上次留下的记录，免得命令没跑到 trap（被杀）时读到旧值。
func prepareCwd() {
	path := filepath.Join(Dir, cwdFile)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.Remove(path)
}

// recordCwd 读回命令结束时的目录，更新 cwd；返回要写进结果的目录（根目录时为空）和重置说明。
func recordCwd(host bool) (dir, note string) {
	root, scratch := cwdRoots(host)
	data, err := os.ReadFile(filepath.Join(Dir, cwdFile))
	if err != nil {
		// 没写成（命令被终止、trap 被命令自己的 EXIT trap 覆盖）：目录照旧。
		dir = startDir(host)
	} else {
		dir = strings.TrimSpace(string(data))
		if host || runtime.GOOS != "linux" {
			dir = realDir(dir)
		}
	}
	if !within(dir, root) && !within(dir, scratch) {
		note = "工作目录 " + dir + " 不在工作区里，已重置回 " + root
		dir = root
	}
	if dir == root {
		cwd, cwdHost = "", host
		return "", note
	}
	cwd, cwdHost = dir, host
	return dir, note
}
