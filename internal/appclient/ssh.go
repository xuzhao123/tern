package appclient

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xuzhao123/tern/internal/protocol"
)

// 远程模式：用系统的 ssh（读 ~/.ssh/config）连远端，跑 `tern app-server proxy`，
// 拿 ssh 的标准输入输出当协议通道，不开端口、鉴权就是 SSH。
// 走登录 shell，~/.profile 里设的 PATH 和 TERN_HOME 才生效（同 Codex：远端要装着 tern 且在登录 shell 的 PATH 上）。
//
// BatchMode=yes：不在后台卡在输密码或确认主机指纹那一步；第一次连某台机器要先在终端里手动 ssh 一次。

// proxyCommand 是远端执行的命令：proxy 自己在远端先做一次 start。
// 优先用 InstallRemote 装到 ~/.tern/bin/tern 的那份（不依赖登录 shell 的 PATH），
// 没有再用 PATH 上的 tern。两条路都走登录 shell，~/.profile 里的 TERN_HOME 才照旧生效：
// 本地 ssh 把这一整条交给远端登录用户自己的 shell，它展开 $SHELL 再交给 `$SHELL -lc`，
// 单引号里的内容原样成为登录 shell 的命令。
const proxyCommand = `exec "$SHELL" -lc 'bin="$HOME/.tern/bin/tern"; [ -x "$bin" ] || bin=tern; exec "$bin" app-server proxy'`

const (
	sshTailLines = 5           // ssh 的 stderr 只留最后几行：够看出 Permission denied、command not found
	sshExitWait  = time.Second // 握手失败后等 ssh 退出，stderr 才写得完
	sshTailBytes = 4096        // stderr 只留最后这么多字节
)

// ValidAlias 检查 -remote 的 ssh 别名：只接受具体的 Host 别名或 user@host。
// 带通配符的 Host 会匹配到别的机器，以 - 开头的会被 ssh 当成选项，都拒绝（同 Codex）。
func ValidAlias(alias string) error {
	if alias == "" {
		return errors.New("-remote 需要 ssh 别名（~/.ssh/config 里的 Host）")
	}
	if strings.HasPrefix(alias, "-") {
		return fmt.Errorf("ssh 别名不能以 - 开头：%s", alias)
	}
	for _, r := range alias {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '@' || r == '-':
		default:
			return fmt.Errorf("ssh 别名只能用字母、数字和 . _ @ -（不支持通配符）：%s", alias)
		}
	}
	return nil
}

// SSHHosts 列出 ~/.ssh/config 里具体的 Host 别名：网页端主机菜单的候选（桌面端以后也要用）。
// 只认 Host 那一行，`Host a b` 与 `Host=a` 两种写法都认：带 * ? ! 的值跳过（那是通配或排除规则，
// 不是某台具体的机器），再过一遍 ValidAlias。去重，按出现的顺序排；文件不存在就是空列表。
// 不展开 Include、不看 Match：要的是候选，不是 ssh 最终会用的配置。
func SSHHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	var hosts []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		names := fields[1:]
		if key, value, ok := strings.Cut(fields[0], "="); ok {
			if !strings.EqualFold(key, "host") {
				continue
			}
			names = append([]string{value}, names...)
		} else if !strings.EqualFold(fields[0], "host") {
			continue
		}
		for _, name := range names {
			if strings.ContainsAny(name, "*?!") || ValidAlias(name) != nil || seen[name] {
				continue
			}
			seen[name] = true
			hosts = append(hosts, name)
		}
	}
	return hosts
}

// RemoteError 是连不上远端的原因：Kind 是分类（见下），Hint 是给人看的下一步，
// Detail 是 ssh 原来的 stderr（协议对不上时是 initialize 的报错）。网页端按 Kind 决定
// 菜单里给哪个按钮：hostkey 给“确认指纹”，no-tern、protocol 给装到远端，其余再试一次。
type RemoteError struct {
	Kind   string
	Hint   string
	Detail string
}

// 连不上的分类。README 与网页端的主机菜单按这几样说话。
const (
	RemoteAuth        = "auth"        // SSH 认证失败
	RemoteHostKey     = "hostkey"     // 还没确认过主机指纹
	RemoteUnreachable = "unreachable" // 解析不了、连不上
	RemoteNoTern      = "no-tern"     // 远端找不到 tern
	RemoteProtocol    = "protocol"    // 两边的协议版本不一致
	RemoteOther       = "other"       // 其余的：只给 ssh 的原文
)

func (e *RemoteError) Error() string {
	switch {
	case e.Hint == "":
		return e.Detail
	case e.Detail == "":
		return e.Hint
	}
	return e.Hint + "\n" + e.Detail
}

// classify 按 ssh 的 stderr 与退出码认出是哪一步没成；认不出的只把原文带出去。
func classify(detail string, code int, alias string) *RemoteError {
	switch {
	case strings.Contains(detail, "Permission denied"):
		return &RemoteError{Kind: RemoteAuth, Detail: detail,
			Hint: fmt.Sprintf("SSH 认证失败：确认 ~/.ssh/config 里 %s 的 User、IdentityFile，能在终端里 `ssh %s` 不输密码登录（这里不会弹密码框）", alias, alias)}
	case strings.Contains(detail, "Host key verification failed"), strings.Contains(detail, "host key is known"):
		return &RemoteError{Kind: RemoteHostKey, Hint: "还没确认过这台机器的主机指纹", Detail: detail}
	case containsAny(detail, "Could not resolve hostname", "Connection refused", "timed out", "No route to host", "Network is unreachable"):
		return &RemoteError{Kind: RemoteUnreachable, Detail: detail,
			Hint: fmt.Sprintf("连不上 %s：检查网络，以及 ~/.ssh/config 里的 HostName、Port", alias)}
	case code == 127, strings.Contains(detail, "tern: command not found"), strings.Contains(detail, "tern: not found"):
		return &RemoteError{Kind: RemoteNoTern, Hint: "远端找不到 tern", Detail: detail}
	}
	return &RemoteError{Kind: RemoteOther, Detail: detail}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// dialSSH 起 ssh，把它的标准输入输出当协议通道。
func dialSSH(alias string) (io.ReadWriteCloser, error) {
	cmd := exec.Command("ssh", "-T", "-o", "BatchMode=yes", alias, proxyCommand)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("起 ssh（%s）：%w", alias, err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("起 ssh（%s）：%w", alias, err)
	}
	tail := &tail{}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("起 ssh（%s）：%w", alias, err)
	}
	s := &sshConn{cmd: cmd, alias: alias, in: in, out: out, tail: tail, code: -1, done: make(chan struct{})}
	// Wait 只在这里收一次；Close 不再等，只是结束进程。退出码留给分类用。
	go func() {
		if err := cmd.Wait(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				s.code = exit.ExitCode()
			}
		} else {
			s.code = 0
		}
		close(s.done)
	}()
	return s, nil
}

// sshConn 是一条经 ssh 的连接：标准输出读协议、标准输入写协议，stderr 留着报错用。
type sshConn struct {
	cmd   *exec.Cmd
	alias string
	in    io.WriteCloser
	out   io.ReadCloser
	tail  *tail
	code  int           // ssh 的退出码，-1 表示还没退出或没拿到
	done  chan struct{} // ssh 退出后关闭
}

func (s *sshConn) Read(p []byte) (int, error) { return s.out.Read(p) }

func (s *sshConn) Write(p []byte) (int, error) { return s.in.Write(p) }

// Close 结束 ssh：界面退出、连接断了都要把它收掉，不留孤儿进程。
// 关掉标准输入（远端 proxy 读到 EOF 就退出）再结束进程，管住还没退出的时候。
func (s *sshConn) Close() error {
	s.in.Close()
	select {
	case <-s.done:
	default:
		s.cmd.Process.Kill()
	}
	return nil
}

// stderrTail 等 ssh 退出（最多 d），返回 stderr 的最后几行：连不上、远端命令失败时带进报错里。
func (s *sshConn) stderrTail(d time.Duration) string {
	select {
	case <-s.done:
	case <-time.After(d):
	}
	return s.tail.lines(sshTailLines)
}

// tail 收 ssh 的 stderr，只留最后几行（一条常连着的连接上 stderr 可能一直有输出）。
type tail struct {
	mu   sync.Mutex
	last []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.last = append(t.last, p...)
	if len(t.last) > sshTailBytes {
		t.last = t.last[len(t.last)-sshTailBytes:]
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *tail) lines(n int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.last)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// dialError 给远程模式的握手失败补上原因：连不上、远端没有 tern、两边的协议版本不一致，
// 都只有 ssh 的退出码、stderr 或 initialize 的报错里看得出来。
func dialError(rwc io.ReadWriteCloser, err error) error {
	var pe *protocol.Error
	if errors.As(err, &pe) && strings.Contains(pe.Message, "协议版本不一致") {
		return &RemoteError{Kind: RemoteProtocol, Hint: "两边的协议版本不一致", Detail: pe.Message}
	}
	s, ok := rwc.(*sshConn)
	if !ok {
		return err
	}
	detail := s.stderrTail(sshExitWait)
	if detail == "" {
		detail = err.Error()
	}
	return classify(detail, s.exitCode(), s.alias)
}

// exitCode 是 ssh 的退出码；还没退出时是 -1。
func (s *sshConn) exitCode() int {
	select {
	case <-s.done:
		return s.code
	default:
		return -1
	}
}

// installCommand 把标准输入里的程序写成远端的 ~/.tern/bin/tern：先写 .new 再改名，
// 正在跑的那份照常能用，下一次连接才换成新的。
const installCommand = `mkdir -p "$HOME/.tern/bin" && cat > "$HOME/.tern/bin/tern.new" && chmod 755 "$HOME/.tern/bin/tern.new" && mv "$HOME/.tern/bin/tern.new" "$HOME/.tern/bin/tern"`

// InstallRemote 把本机正在跑的程序拷到远端：只拷同系统同架构的；装完由调用方照常连一次，
// 远端的 proxy 会按“程序不同就换”把那边的 app-server 换成新的。
func InstallRemote(alias string) error {
	if err := ValidAlias(alias); err != nil {
		return err
	}
	remoteOS, remoteArch, err := remoteUname(alias)
	if err != nil {
		return err
	}
	if remoteOS != runtime.GOOS || remoteArch != runtime.GOARCH {
		return fmt.Errorf("远端是 %s/%s，本机是 %s/%s，程序没法直接拷过去。请在远端安装 tern（在源码目录里 `go install .`），并放进登录 shell 的 PATH。",
			remoteOS, remoteArch, runtime.GOOS, runtime.GOARCH)
	}
	self, err := selfExe()
	if err != nil {
		return err
	}
	program, err := os.Open(self)
	if err != nil {
		return fmt.Errorf("读本机的程序 %s：%w", self, err)
	}
	defer program.Close()
	cmd := exec.Command("ssh", "-T", "-o", "BatchMode=yes", alias, installCommand)
	cmd.Stdin = program
	stderr := &tail{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if detail := stderr.lines(sshTailLines); detail != "" {
			return classify(detail, exitCodeOf(err), alias)
		}
		return fmt.Errorf("拷到 %s：%w", alias, err)
	}
	return nil
}

// selfExe 是正在跑的这个程序：/proc/self/exe 在 go run、程序被改名以后也读得到，没有 /proc 时退回 os.Executable。
func selfExe() (string, error) {
	if _, err := os.Stat("/proc/self/exe"); err == nil {
		return "/proc/self/exe", nil
	}
	return os.Executable()
}

// exitCodeOf 是执行失败的 ssh 的退出码，没退出时是 -1。
func exitCodeOf(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// remoteUname 取远端的系统与架构，换成 Go 的写法（Linux→linux、x86_64→amd64）。
func remoteUname(alias string) (string, string, error) {
	out, err := sshOutput(alias, "uname -s; uname -m")
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return "", "", fmt.Errorf("%s 上的 uname 输出看不懂：%s", alias, strings.TrimSpace(out))
	}
	return goOS(fields[0]), goArch(fields[1]), nil
}

func goOS(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func goArch(name string) string {
	switch strings.TrimSpace(name) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return strings.TrimSpace(name)
}

// sshOutput 跑一条远端的只读命令，收下它的标准输出；失败时按 ssh 的 stderr 分类。
func sshOutput(alias, command string) (string, error) {
	cmd := exec.Command("ssh", "-T", "-o", "BatchMode=yes", alias, command)
	stderr := &tail{}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		if detail := stderr.lines(sshTailLines); detail != "" {
			return "", classify(detail, exitCodeOf(err), alias)
		}
		return "", fmt.Errorf("ssh %s：%w", alias, err)
	}
	return string(out), nil
}

// HostKey 是一台机器的一把主机公钥：类型与 SHA256 指纹，给人核对。
type HostKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

// HostKeysInfo 是 GET /hosts/{alias}/hostkey 的回复。
type HostKeysInfo struct {
	Host string    `json:"host"`
	Port string    `json:"port"`
	Keys []HostKey `json:"keys"`
}

// HostKeys 取这台机器的主机公钥与指纹给人确认：ssh -G 拿到 ssh 最终会连的地址与端口，
// ssh-keyscan 取公钥，ssh-keygen -lf - 算 SHA256 指纹。
func HostKeys(alias string) (HostKeysInfo, error) {
	host, port, lines, err := scanHostKeys(alias)
	if err != nil {
		return HostKeysInfo{}, err
	}
	info := HostKeysInfo{Host: host, Port: port, Keys: make([]HostKey, 0, len(lines))}
	for _, line := range lines {
		key, err := keyFingerprint(line)
		if err != nil {
			return HostKeysInfo{}, err
		}
		info.Keys = append(info.Keys, key)
	}
	return info, nil
}

// TrustHostKeys 把这几条主机公钥追加进本机的 ~/.ssh/known_hosts。追加前再扫一次，
// 指纹和给人看的那次（want）不一样就不写，防止这中间被换掉。
func TrustHostKeys(alias string, want []string) error {
	_, _, lines, err := scanHostKeys(alias)
	if err != nil {
		return err
	}
	var got []string
	for _, line := range lines {
		key, err := keyFingerprint(line)
		if err != nil {
			return err
		}
		got = append(got, key.Fingerprint)
	}
	if !sameSet(got, want) {
		return errors.New("这次扫到的主机公钥和刚才看到的不一样，没有写入：请重新点一次“确认指纹”")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(home, ".ssh", "known_hosts"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(f, line); err != nil {
			f.Close()
			return err
		}
	}
	return f.Close()
}

// scanHostKeys 是 ssh -G 加 ssh-keyscan：地址、端口，以及那几个 known_hosts 格式的公钥行。
func scanHostKeys(alias string) (host, port string, lines []string, err error) {
	if err := ValidAlias(alias); err != nil {
		return "", "", nil, err
	}
	out, err := exec.Command("ssh", "-G", alias).Output()
	if err != nil {
		return "", "", nil, fmt.Errorf("ssh -G %s：%w", alias, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "hostname":
			host = fields[1]
		case "port":
			port = fields[1]
		}
	}
	if host == "" || port == "" {
		return "", "", nil, fmt.Errorf("ssh -G %s 没说连哪里", alias)
	}
	scanned, err := exec.Command("ssh-keyscan", "-T", "5", "-p", port, host).Output()
	if err != nil {
		return "", "", nil, fmt.Errorf("ssh-keyscan %s：%w", host, err)
	}
	for _, line := range strings.Split(string(scanned), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "", "", nil, fmt.Errorf("ssh-keyscan %s 没取到主机公钥", host)
	}
	return host, port, lines, nil
}

// keyFingerprint 把一条 known_hosts 格式的公钥行交给 ssh-keygen -lf -：
// 输出是“<位数> SHA256:… <主机> (ED25519)”。
func keyFingerprint(line string) (HostKey, error) {
	cmd := exec.Command("ssh-keygen", "-lf", "-")
	cmd.Stdin = strings.NewReader(line + "\n")
	out, err := cmd.Output()
	if err != nil {
		return HostKey{}, fmt.Errorf("ssh-keygen -lf -：%w", err)
	}
	fields := strings.Fields(string(out))
	key := HostKey{}
	for _, f := range fields {
		if strings.HasPrefix(f, "SHA256:") {
			key.Fingerprint = f
		}
	}
	if last := fields[len(fields)-1]; key.Fingerprint != "" && strings.HasPrefix(last, "(") && strings.HasSuffix(last, ")") {
		key.Type = strings.Trim(last, "()")
	}
	if key.Type == "" {
		// 认不出 ssh-keygen 的写法时退回公钥行里的类型：ssh-ed25519 → ED25519。
		if parts := strings.Fields(line); len(parts) >= 2 {
			key.Type = strings.ToUpper(strings.TrimPrefix(parts[1], "ssh-"))
		}
	}
	if key.Fingerprint == "" {
		return HostKey{}, fmt.Errorf("算不出 %s 的指纹", line)
	}
	return key, nil
}

// sameSet 比两份指纹：条数相同且一一对上（顺序不管）。
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// writeEnvCommand 把标准输入写成远端 TERN_HOME/.env：和 proxy 一样经登录 shell 取目录，
// 未配置时用 $HOME/.tern。先写 .new 再改名，中途断了不会留下半个文件；
// umask 与 chmod 一起保证别人读不到。
const writeEnvCommand = `exec "$SHELL" -lc 'dir="${TERN_HOME:-$HOME/.tern}"; umask 077 && mkdir -p "$dir" && cat > "$dir/.env.new" && chmod 600 "$dir/.env.new" && mv "$dir/.env.new" "$dir/.env" && printf "%s\n" "$dir/.env"'`

// WriteRemoteKeys 把几个密钥写进远端 TERN_HOME/.env：已有同名的行替换，没有的追加，写完 0600。
// 先读出远端现有内容，在本机合并好再整份传回去，值只走 ssh 的标准输入，不出现在命令行上；
// 这样不用在远端依赖 awk 之类的工具。返回写入的路径给人看。
func WriteRemoteKeys(alias string, values map[string]string) (string, error) {
	if err := ValidAlias(alias); err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", errors.New("没有要写的密钥")
	}
	// 新装的远端还没有 .env：当作空文件合并；已有文件读失败仍报错，避免覆盖原有密钥。
	current, err := sshOutput(alias, `exec "$SHELL" -lc 'dir="${TERN_HOME:-$HOME/.tern}"; [ ! -e "$dir/.env" ] || cat "$dir/.env"'`)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("ssh", "-T", "-o", "BatchMode=yes", alias, writeEnvCommand)
	cmd.Stdin = strings.NewReader(mergeEnv(current, values))
	var path strings.Builder
	cmd.Stdout = &path
	stderr := &tail{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if detail := stderr.lines(sshTailLines); detail != "" {
			return "", classify(detail, exitCodeOf(err), alias)
		}
		return "", fmt.Errorf("写 %s 的 .env：%w", alias, err)
	}
	return strings.TrimSpace(path.String()), nil
}

// mergeEnv 把 values 并进 .env 的内容：同名的行原地替换，没有的按名字排在最后。
// 其余的行（注释、别家的密钥）原样留着。
func mergeEnv(current string, values map[string]string) string {
	var out []string
	done := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(current, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		name, _, assigned := strings.Cut(trimmed, "=")
		name = strings.TrimSpace(name)
		if value, ok := values[name]; ok && assigned && !strings.HasPrefix(trimmed, "#") {
			if done[name] {
				continue // 同名的行只留一行
			}
			done[name] = true
			out = append(out, name+"="+value)
			continue
		}
		if trimmed != "" || len(out) > 0 {
			out = append(out, line)
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		if !done[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, name+"="+values[name])
	}
	return strings.Join(out, "\n") + "\n"
}
