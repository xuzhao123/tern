package web

import (
	"bufio"
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xuzhao123/tern/internal/audit"
)

// 登录。账号放在 .data/users.json（网页端启动时读入）：
//
//	{"users": [{"name": "alice", "salt": "…", "hash": "…", "iterations": 600000}],
//	 "tenants": {"acme": {"max_running": 2, "features": ["memory", "rag", "bash"]}}}
//
// 这一版只有平台空间：账号看到的是 .data/ 下的数据。早期版本的租户账号（带 tenant 字段）与 tenants 策略留在文件里但不再使用：
// 带 tenant 的账号登录时拒绝，免得让人以为看到的是租户数据。
// 账号用 go run . web -add-user 名字 添加，密码从标准输入读，不出现在命令行和 shell 历史里。
const usersPath = ".data/users.json"

type account struct {
	Name         string `json:"name"`
	LegacyTenant string `json:"tenant,omitempty"` // 早期版本的租户账号：登录时拒绝
	Salt         string `json:"salt"`
	Hash         string `json:"hash"`
	Iterations   int    `json:"iterations"`
}

type usersFile struct {
	Users   []account       `json:"users"`
	Tenants json.RawMessage `json:"tenants,omitempty"` // 早期版本按租户的策略：不再使用，改写文件时原样保留
}

// 登录后的用户：只有名字，不带密码相关字段。
type user struct {
	Name string `json:"name"`
}

// OWASP 密码存储建议：PBKDF2-HMAC-SHA256 至少 600000 次迭代，每个账号独立的随机盐。
const iterations = 600_000

func hashPassword(password string, salt []byte, iter int) string {
	key, _ := pbkdf2.Key(sha256.New, password, salt, iter, 32)
	return hex.EncodeToString(key)
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// addUser 是 go run . web -add-user 的实现：新增或重设一个账号的密码。
func addUser(name string) error {
	if !namePattern.MatchString(name) {
		return errors.New("用户名只能是小写字母、数字和 ._-，最长32")
	}
	var file usersFile
	if data, err := os.ReadFile(usersPath); err == nil {
		if err := json.Unmarshal(data, &file); err != nil {
			return fmt.Errorf("%s 不是有效 JSON：%w", usersPath, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	password, err := readPassword()
	if err != nil {
		return err
	}
	if len(password) < 8 {
		return errors.New("密码至少 8 个字符")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	a := account{Name: name, Salt: hex.EncodeToString(salt), Hash: hashPassword(password, salt, iterations), Iterations: iterations}
	if i := slices.IndexFunc(file.Users, func(u account) bool { return u.Name == name }); i >= 0 {
		file.Users[i] = a
	} else {
		file.Users = append(file.Users, a)
	}
	data, _ := json.MarshalIndent(file, "", "  ")
	if err := os.MkdirAll(".data", 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(usersPath+".tmp", append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(usersPath+".tmp", usersPath); err != nil {
		return err
	}
	fmt.Printf("已保存账号 %s 到 %s；网页端重启后生效\n", name, usersPath)
	return nil
}

// 从终端读密码时关闭回显；从管道读时（脚本里 printf … | go run . web -add-user …）直接读一行。
func readPassword() (string, error) {
	info, _ := os.Stdin.Stat()
	terminal := info != nil && info.Mode()&os.ModeCharDevice != 0
	if terminal {
		fmt.Fprint(os.Stderr, "密码（至少 8 位，输入时不显示）：")
		stty := func(arg string) {
			cmd := exec.Command("stty", arg)
			cmd.Stdin = os.Stdin
			cmd.Run()
		}
		stty("-echo")
		defer func() { stty("echo"); fmt.Fprintln(os.Stderr) }()
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("没有读到密码")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// ---------- 会话 ----------

type session struct {
	user    user
	expires time.Time
}

type auth struct {
	mu       sync.Mutex
	accounts map[string]account
	sessions map[string]session // 键是令牌的 SHA-256：内存里没有可以直接拿去用的令牌
	failures map[string]failure
}

type failure struct {
	count int
	until time.Time
}

const (
	cookieName = "tern_session"
	sessionTTL = 12 * time.Hour
	lockAfter  = 5 // 连续失败几次后锁定这个用户名
	lockFor    = time.Minute
)

func loadAuth() (*auth, error) {
	a := &auth{accounts: map[string]account{}, sessions: map[string]session{}, failures: map[string]failure{}}
	data, err := os.ReadFile(usersPath)
	if os.IsNotExist(err) {
		return nil, errors.New("还没有账号：先运行 go run . web -add-user 名字 添加账号，再启动网页端")
	}
	if err != nil {
		return nil, err
	}
	var file usersFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s 不是有效 JSON：%w", usersPath, err)
	}
	for _, u := range file.Users {
		if !namePattern.MatchString(u.Name) {
			return nil, fmt.Errorf("%s 里的账号 %q 无效", usersPath, u.Name)
		}
		a.accounts[u.Name] = u
	}
	if len(a.accounts) == 0 {
		return nil, errors.New(usersPath + " 里没有账号")
	}
	return a, nil
}

func newToken() string {
	buf := make([]byte, 32)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// 未知用户也算一遍哈希：响应时间不泄露“这个用户名存在”。
var dummySalt = make([]byte, 16)

func (a *auth) login(name, password string) (token string, u user, err error) {
	a.mu.Lock()
	acc, known := a.accounts[name]
	f := a.failures[name]
	a.mu.Unlock()
	if time.Now().Before(f.until) {
		return "", user{}, fmt.Errorf("失败次数过多，请 %d 秒后再试", int(time.Until(f.until).Seconds())+1)
	}
	ok := false
	if known {
		salt, _ := hex.DecodeString(acc.Salt)
		ok = subtle.ConstantTimeCompare([]byte(hashPassword(password, salt, acc.Iterations)), []byte(acc.Hash)) == 1
	} else {
		hashPassword(password, dummySalt, iterations)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !ok {
		f.count++
		if f.count >= lockAfter {
			f.until, f.count = time.Now().Add(lockFor), 0
		}
		a.failures[name] = f
		return "", user{}, errors.New("用户名或密码错误")
	}
	delete(a.failures, name)
	// 早期版本按租户分的账号还在文件里：密码对也不放进来，这一版只有平台空间，登进来看到的是别的数据。
	if acc.LegacyTenant != "" {
		return "", user{}, errors.New("租户账号暂不支持")
	}
	token = newToken()
	u = user{Name: acc.Name}
	a.sessions[tokenKey(token)] = session{user: u, expires: time.Now().Add(sessionTTL)}
	return token, u, nil
}

func (a *auth) lookup(req *http.Request) *user {
	cookie, err := req.Cookie(cookieName)
	if err != nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := tokenKey(cookie.Value)
	s, ok := a.sessions[key]
	if !ok || time.Now().After(s.expires) {
		delete(a.sessions, key)
		return nil
	}
	u := s.user
	return &u
}

func (a *auth) logout(req *http.Request) {
	if cookie, err := req.Cookie(cookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, tokenKey(cookie.Value))
		a.mu.Unlock()
	}
}

type userKey struct{}

// current 取出这次请求的登录用户；经过 protect 的路由一定有。
func current(req *http.Request) *user {
	u, _ := req.Context().Value(userKey{}).(*user)
	return u
}

// 不需要登录的路径：登录页和内置 MCP（只接受本机同源请求）。
// 模型代理在 app-server 的 llm.sock 上，不在网页端，agent 不再经过网页端。
func public(path string) bool {
	return path == "/login" || path == "/mcp"
}

// protect 包住整个路由表：没登录的页面请求跳到登录页，接口返回 401；
// 会改变状态的请求另外要求同源（Cookie 设了 SameSite=Strict，这里再挡一层跨站请求）。
func (s *server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if public(req.URL.Path) {
			next.ServeHTTP(w, req)
			return
		}
		u := s.auth.lookup(req)
		if u == nil {
			if req.Method == http.MethodGet && req.URL.Path == "/" {
				http.Redirect(w, req, "/login", http.StatusSeeOther)
				return
			}
			http.Error(w, "请先登录", http.StatusUnauthorized)
			return
		}
		if req.Method != http.MethodGet && req.Method != http.MethodHead && !sameOriginRequest(req) {
			http.Error(w, "只接受网页端页面发来的请求", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), userKey{}, u)))
	})
}

func sameOriginRequest(req *http.Request) bool {
	host := req.Host
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	origin, err := url.Parse(req.Header.Get("Origin"))
	return (host == "127.0.0.1" || host == "localhost") && (req.Header.Get("Origin") == "" || (err == nil && origin.Host == req.Host))
}

func (s *server) loginPage(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(loginHTML))
		return
	}
	if !sameOriginRequest(req) {
		http.Error(w, "只接受网页端页面发来的请求", http.StatusForbidden)
		return
	}
	var input struct{ Name, Password string }
	if json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&input) != nil {
		http.Error(w, "请求必须是JSON", http.StatusBadRequest)
		return
	}
	token, u, err := s.auth.login(input.Name, input.Password)
	// 登录成败都写审计。已知账号写它自己的名字；不存在的用户名只记合法格式的名字。
	record := audit.Record{Actor: "user", Event: "login", Status: "ok", User: u.Name}
	if err != nil {
		record.Status, record.Detail = "failed", err.Error()
		s.auth.mu.Lock()
		acc, known := s.auth.accounts[input.Name]
		s.auth.mu.Unlock()
		if known {
			record.User = acc.Name
		} else if namePattern.MatchString(input.Name) {
			record.Target = input.Name
		}
	}
	audit.Log(req.Context(), record)
	if err != nil {
		time.Sleep(300 * time.Millisecond)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	writeJSON(w, u)
}

func (s *server) logoutUser(w http.ResponseWriter, req *http.Request) {
	u := current(req)
	s.auth.logout(req)
	audit.Log(req.Context(), audit.Record{Actor: "user", Event: "logout", User: u.Name})
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

// 页面启动时取当前用户：显示名字。
func (s *server) me(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, current(req))
}

const loginHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>登录 · tern</title>
<style>
:root { --bg: #f9f9f9; --panel: #fff; --text: #0d0d0d; --muted: #6b6b6b; --line: #ebebeb; }
@media (prefers-color-scheme: dark) { :root { --bg: #141414; --panel: #1f1f1f; --text: #ececec; --muted: #a3a3a3; --line: #2d2d2d; } }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: var(--bg); color: var(--text); font: 14px/1.6 ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Noto Sans SC", sans-serif; -webkit-font-smoothing: antialiased; }
form { width: 320px; padding: 32px 28px 24px; border: 1px solid var(--line); border-radius: 16px; background: var(--panel); box-shadow: 0 1px 2px rgba(0,0,0,.04), 0 8px 28px rgba(0,0,0,.06); }
.mark { display: grid; place-items: center; width: 32px; height: 32px; margin-bottom: 14px; border-radius: 8px; background: var(--text); color: var(--panel); }
h1 { margin: 0 0 20px; font-size: 18px; font-weight: 600; letter-spacing: -.01em; }
label { display: block; margin: 0 0 14px; color: var(--muted); font-size: 13px; }
input { box-sizing: border-box; width: 100%; margin-top: 6px; padding: 9px 12px; border: 1px solid var(--line); border-radius: 10px; background: var(--panel); color: var(--text); font: inherit; outline: 0; }
input:focus { border-color: var(--muted); }
button { width: 100%; margin-top: 6px; padding: 9px; border: 0; border-radius: 10px; background: var(--text); color: var(--panel); font: inherit; font-weight: 500; cursor: pointer; }
#err { min-height: 18px; margin: 10px 0 0; color: #d03b2f; font-size: 13px; }
</style></head><body>
<form id="f"><div class="mark"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 7 5 5-5 5"/><path d="M13 17h6"/></svg></div><h1>登录 tern</h1>
<label>用户名<input id="name" autocomplete="username" required autofocus></label>
<label>密码<input id="password" type="password" autocomplete="current-password" required></label>
<button>登录</button><p id="err"></p></form>
<script>
document.getElementById("f").onsubmit = async (e) => {
  e.preventDefault();
  const err = document.getElementById("err"); err.textContent = "";
  const res = await fetch("/login", { method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: document.getElementById("name").value.trim(), password: document.getElementById("password").value }) });
  if (res.ok) location.href = "/"; else err.textContent = (await res.text()).trim();
};
</script></body></html>`
