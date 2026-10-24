package appclient

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 远程模式：用系统的 ssh（读 ~/.ssh/config）连远端，跑 `tern app-server proxy`，
// 拿 ssh 的标准输入输出当协议通道，不开端口、鉴权就是 SSH。
// 走登录 shell，~/.profile 里设的 PATH 和 TERN_HOME 才生效（同 Codex：远端要装着 tern 且在登录 shell 的 PATH 上）。
//
// BatchMode=yes：不在后台卡在输密码或确认主机指纹那一步；第一次连某台机器要先在终端里手动 ssh 一次。

// proxyCommand 是远端执行的命令：proxy 自己在远端先做一次 start。
const proxyCommand = `exec "$SHELL" -lc "tern app-server proxy"`

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
	s := &sshConn{cmd: cmd, in: in, out: out, tail: tail, done: make(chan struct{})}
	// Wait 只在这里收一次；Close 不再等，只是结束进程。
	go func() {
		cmd.Wait()
		close(s.done)
	}()
	return s, nil
}

// sshConn 是一条经 ssh 的连接：标准输出读协议、标准输入写协议，stderr 留着报错用。
type sshConn struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  io.ReadCloser
	tail *tail
	done chan struct{} // ssh 退出后关闭
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

// sshError 给远程模式的握手失败补上 ssh 的报错：连不上、远端没有 tern，都只有 stderr 里看得出来。
func sshError(rwc io.ReadWriteCloser, err error) error {
	s, ok := rwc.(*sshConn)
	if !ok {
		return err
	}
	if tail := s.stderrTail(sshExitWait); tail != "" {
		return fmt.Errorf("%w\nssh：%s", err, tail)
	}
	return err
}
