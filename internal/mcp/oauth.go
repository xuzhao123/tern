package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mark3labs/mcp-go/client/transport"
)

// 远程 MCP server 的 OAuth 令牌：TERN_HOME 下的 .data/mcp-tokens.json（0600），
// 按 server 的地址存 {"<url>": {"client_id": …, "client_secret": …, "token": {…}}}。
// app-server 登录时写、退出登录时删；agent 连接时读，过期的由 mcp-go 用 refresh token 换新再写回。
// 两边都跑在 TERN_HOME 下，用同一个相对路径，所以同一时刻可能有几个进程读写同一份文件：
// 读改写都拿 <文件>.lock 的 flock，写成先写 .tmp 再改名。
const tokensFile = ".data/mcp-tokens.json"

// AuthRequiredMessage 是远程 server 要求授权时连接失败的开头：agent 跳过这个 server 并打印它，
// 网页端的“测试连接”据此认出这一项要登录。
const AuthRequiredMessage = "MCP 需要登录"

// tokenEntry 是一个 server 在令牌文件里的一项：OAuth 客户端身份（动态注册到的，或者用户自己填的）与令牌。
type tokenEntry struct {
	ClientID     string           `json:"client_id,omitempty"`
	ClientSecret string           `json:"client_secret,omitempty"`
	Token        *transport.Token `json:"token,omitempty"`
}

// withTokens 拿锁、读出整个令牌文件交给 change；change 说要写（第一个返回值）才写回。
// 文件不存在当空表；文件坏了直接报错，不拿空表覆盖别人的令牌。
func withTokens(change func(map[string]tokenEntry) (bool, error)) error {
	if err := os.MkdirAll(filepath.Dir(tokensFile), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(tokensFile+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close() // 关闭文件即释放 flock
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	entries := map[string]tokenEntry{}
	data, err := os.ReadFile(tokensFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &entries); err != nil {
			return fmt.Errorf("%s 不是有效JSON，请手动检查后再运行：%w", tokensFile, err)
		}
	}
	write, err := change(entries)
	if err != nil || !write {
		return err
	}
	if data, err = json.MarshalIndent(entries, "", "  "); err != nil {
		return err
	}
	if err := os.WriteFile(tokensFile+".tmp", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tokensFile+".tmp", tokensFile)
}

// entry 是这个地址在令牌文件里的一项；没有时 ok 是 false。
func entry(url string) (e tokenEntry, ok bool, err error) {
	err = withTokens(func(entries map[string]tokenEntry) (bool, error) {
		e, ok = entries[url]
		return false, nil
	})
	return e, ok, err
}

// updateEntry 改这个地址的一项，没有就新建。
func updateEntry(url string, change func(*tokenEntry)) error {
	return withTokens(func(entries map[string]tokenEntry) (bool, error) {
		e := entries[url]
		change(&e)
		entries[url] = e
		return true, nil
	})
}

// HasToken 报告令牌文件里有没有这个地址的访问令牌：agent 据此决定用不用 OAuth 客户端连接，
// MCP 中心据此显示“已登录”。
func HasToken(url string) bool {
	e, ok, err := entry(url)
	return err == nil && ok && e.Token != nil && e.Token.AccessToken != ""
}

// SetClientCredentials 存下 OAuth 客户端身份：动态注册到的，或者用户按 server 的要求自己填的。
// clientID 为空时什么也不做（输入框留空就是沿用已经存下的）。
func SetClientCredentials(url, clientID, clientSecret string) error {
	if strings.TrimSpace(clientID) == "" {
		return nil
	}
	return updateEntry(url, func(e *tokenEntry) { e.ClientID, e.ClientSecret = clientID, clientSecret })
}

// DeleteToken 删掉这个地址的一整项（客户端身份与令牌），退出登录用。
func DeleteToken(url string) error {
	return withTokens(func(entries map[string]tokenEntry) (bool, error) {
		if _, ok := entries[url]; !ok {
			return false, nil
		}
		delete(entries, url)
		return true, nil
	})
}

// fileTokenStore 是 transport.TokenStore 的文件实现：mcp-go 取令牌、刷新后写回都经过它，
// 所以刷新出来的新令牌其他进程（下一轮对话、另一个 agent）也拿得到。
type fileTokenStore struct{ url string }

func (s fileTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e, ok, err := entry(s.url)
	if err != nil {
		return nil, err
	}
	if !ok || e.Token == nil || e.Token.AccessToken == "" {
		return nil, transport.ErrNoToken
	}
	return e.Token, nil
}

func (s fileTokenStore) SaveToken(ctx context.Context, token *transport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return updateEntry(s.url, func(e *tokenEntry) { e.Token = token })
}

// oauthConfig 是这个 server 的 OAuth 配置：客户端身份从令牌文件读，令牌也存回同一个文件，PKCE 打开。
func oauthConfig(url, redirect string) (transport.OAuthConfig, error) {
	e, _, err := entry(url)
	if err != nil {
		return transport.OAuthConfig{}, err
	}
	return transport.OAuthConfig{ClientID: e.ClientID, ClientSecret: e.ClientSecret, RedirectURI: redirect,
		TokenStore: fileTokenStore{url: url}, PKCEEnabled: true}, nil
}

// NewOAuthHandler 建这个 server 的 OAuthHandler：app-server 的登录流程用它注册客户端、给出授权地址、换令牌。
// 独立的 handler 要自己设 base URL（mcp-go 连 server 时才设），元数据从 server 的 /.well-known 上发现。
func NewOAuthHandler(url, redirect string) (*transport.OAuthHandler, error) {
	config, err := oauthConfig(url, redirect)
	if err != nil {
		return nil, err
	}
	handler := transport.NewOAuthHandler(config)
	handler.SetBaseURL(url)
	return handler, nil
}

// PKCE 与 state 用 mcp-go 的实现：verifier 换令牌时用，challenge 进授权地址，state 让回调认得出是哪一次登录。
func GenerateCodeVerifier() (string, error) { return transport.GenerateCodeVerifier() }

func GenerateCodeChallenge(verifier string) string { return transport.GenerateCodeChallenge(verifier) }

func GenerateState() (string, error) { return transport.GenerateState() }
