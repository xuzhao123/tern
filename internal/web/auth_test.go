package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func request(token string) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	return req
}

// 重启（重新 loadAuth）后登录还在；登出、改密码、删账号、过期后都作废；文件里没有明文令牌。
func TestSessionsSurviveRestart(t *testing.T) {
	t.Chdir(t.TempDir())
	os.Mkdir(".data", 0o700)
	salt := "00"
	write := func(hash string) {
		os.WriteFile(usersPath, []byte(`{"users":[{"name":"qa","salt":"`+salt+`","hash":"`+hash+`","iterations":1}]}`), 0o600)
	}
	write(hashPassword("pw", []byte{0}, 1))
	load := func() *auth {
		t.Helper()
		a, err := loadAuth()
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := load()
	token, _, err := a.login("qa", "pw")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sessionsPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("会话文件：%v %v", info, err)
	}
	if data, _ := os.ReadFile(sessionsPath); strings.Contains(string(data), token) {
		t.Fatal("文件里不能有明文令牌")
	}
	a = load()
	if u := a.lookup(request(token)); u == nil || u.Name != "qa" {
		t.Fatalf("重启后应仍然登录：%v", u)
	}
	// 过期
	key := tokenKey(token)
	s := a.sessions[key]
	s.expires = time.Now().Add(-time.Minute)
	a.sessions[key] = s
	if a.lookup(request(token)) != nil {
		t.Fatal("过期应作废")
	}
	// 登出后重启
	token, _, _ = a.login("qa", "pw")
	a.logout(request(token))
	if load().lookup(request(token)) != nil {
		t.Fatal("登出后重启不应恢复")
	}
	// 改密码后重启
	a = load()
	token, _, _ = a.login("qa", "pw")
	write(hashPassword("new", []byte{0}, 1))
	if load().lookup(request(token)) != nil {
		t.Fatal("改密码后应作废")
	}
	// 账号没了
	a = load()
	token, _, _ = a.login("qa", "new")
	os.WriteFile(usersPath, []byte(`{"users":[{"name":"other","salt":"00","hash":"x","iterations":1}]}`), 0o600)
	if load().lookup(request(token)) != nil {
		t.Fatal("账号删除后应作废")
	}
}
