package preview

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

// proxy 是一个预览会话专用的固定目标代理：这个会话的 Chrome 把所有流量都指到这里，只有授权的那个 origin 过得去。
// 放不放行只看请求里的 host:port 是否字面等于授权的主机与端口（不解析域名、不认别名）；
// 放行以后的连接一律连到 grant.dial，所以 DNS 重绑定、nip.io 这类指向本机的名字都到不了别的地址。
type proxy struct {
	grant grant
	ln    net.Listener

	mu  sync.Mutex
	bad int // 拦下的请求数：只记次数，不记路径与查询参数
}

// newProxy 在一个随机的本机端口上开始监听：只给这个会话的 Chrome 用，不开给别处。
func newProxy(g grant) (*proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &proxy{grant: g, ln: ln}
	go p.serve()
	return p, nil
}

// address 是 Chrome 的 --proxy-server 要写的地址。
func (p *proxy) address() string { return p.ln.Addr().String() }

// denied 是到目前为止拦下的请求数。
func (p *proxy) denied() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bad
}

func (p *proxy) close() { p.ln.Close() }

func (p *proxy) serve() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(conn)
	}
}

// handle 收下一条来自 Chrome 的连接：CONNECT 是 https 与 wss，其余是普通 HTTP 请求（absolute-form）。
func (p *proxy) handle(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if !p.allowed(req.Host) {
		p.deny(conn, req.Host)
		return
	}
	if req.Method == http.MethodConnect {
		p.tunnel(conn, br)
		return
	}
	p.forward(conn, br, req)
}

// tunnel 处理 CONNECT：回 200 之后这条连接就是两边的裸字节（https、wss）。
func (p *proxy) tunnel(conn net.Conn, br *bufio.Reader) {
	up, err := net.Dial("tcp", p.grant.dial)
	if err != nil {
		p.badGateway(conn)
		return
	}
	defer up.Close()
	io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	pump(conn, br, up, nil)
}

// forward 一条一条转发普通 HTTP 请求：Chrome 会复用代理连接（keep-alive），每条都要单独判断放行、
// 各自按 origin-form 写给上游。第一条之后就裸转发不行——后续请求带的是 absolute-form，
// 按路径路由的开发服务（Node、Express 这类）会把整条 URL 当成路径；换到别的 host 也没人判断了。
func (p *proxy) forward(conn net.Conn, br *bufio.Reader, req *http.Request) {
	up, err := net.Dial("tcp", p.grant.dial)
	if err != nil {
		p.badGateway(conn)
		return
	}
	defer up.Close()
	upBr := bufio.NewReader(up)
	for {
		more, err := p.relay(conn, br, up, upBr, req)
		if err != nil || !more {
			return // 上游或 Chrome 断了、某一方要关、或者已经升级成 WebSocket：这条连接到此为止
		}
		next, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		if !p.allowed(next.Host) {
			p.deny(conn, next.Host)
			return
		}
		req = next
	}
}

// relay 转发一条请求并把响应写回 Chrome；more 为 false 表示这条连接不能再用（升级成 WebSocket、或者要关）。
func (p *proxy) relay(conn net.Conn, br *bufio.Reader, up net.Conn, upBr *bufio.Reader, req *http.Request) (more bool, err error) {
	// 只去掉代理自己的头，其余原样转发：不改响应头，也不动 CORS、CSP、cookie。
	for name := range req.Header {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "Proxy-") {
			req.Header.Del(name)
		}
	}
	req.RequestURI = "" // Request.Write 要求它为空，按 origin-form 写给目标
	if err := req.Write(up); err != nil {
		return false, err
	}
	req.Body.Close()
	resp, err := http.ReadResponse(upBr, req)
	if err != nil {
		return false, err
	}
	if err := resp.Write(conn); err != nil {
		resp.Body.Close()
		return false, err
	}
	keep := !req.Close && !resp.Close
	upgraded := resp.StatusCode == http.StatusSwitchingProtocols
	resp.Body.Close()
	if upgraded {
		// 101 之后两边都是裸字节：WebSocket 的帧直接转发。
		pump(conn, br, up, upBr)
		return false, nil
	}
	return keep, nil
}

// badGateway 是连不上授权目标时的回复：开发服务没起来时 Chrome 看到的是这个。
func (p *proxy) badGateway(conn net.Conn) {
	io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
}

// pump 从这时起两边都是裸字节（CONNECT 之后、WebSocket 升级之后）：先把已经读进来的字节转过去，再双向转发。
func pump(a net.Conn, abr *bufio.Reader, b net.Conn, bbr *bufio.Reader) {
	forward := func(dst net.Conn, src *bufio.Reader) {
		if src == nil {
			return
		}
		n := src.Buffered()
		if n == 0 {
			return
		}
		rest, err := src.Peek(n)
		if err != nil {
			return
		}
		dst.Write(rest)
		src.Discard(len(rest))
	}
	forward(b, abr) // Chrome 已经发过来的（TLS 握手、WebSocket 帧）
	forward(a, bbr) // 目标已经回来的
	pipe(a, b)
}

// allowed 只看 host:port：字面等于授权的主机与端口才放行。名字大小写与端口写法都按 Chrome 发来的原样比。
func (p *proxy) allowed(hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]" // [::1] 的主机部分拆出来是 ::1
	}
	return host == p.grant.host && port == p.grant.port
}

// deny 回 403 并记一次：日志只写 host:port，请求的路径、查询参数一律不记。
func (p *proxy) deny(conn net.Conn, hostport string) {
	p.mu.Lock()
	p.bad++
	bad := p.bad
	p.mu.Unlock()
	fmt.Fprintf(os.Stderr, "预览代理拦下 %s（这条会话第 %d 次）\n", hostport, bad)
	io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
}

// pipe 双向转发两边的字节，一边读完就关掉那边的写端，另一边的读到 EOF 后自然结束。
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyHalf := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			tcp.CloseWrite()
		}
	}
	go copyHalf(b, a)
	go copyHalf(a, b)
	wg.Wait()
}
