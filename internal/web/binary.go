package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/xuzhao123/tern/internal/appserver"
)

// 二进制内容分块传：附图、沙箱文件、浏览器画面、导入的会话文件都按 appserver.MaxChunk（4MB）一块，
// 因为协议一行最多 16MB，整份传在 base64 之后会超。上限（附图 20MB、文件面板 10MB、导入 128MB）由 app-server 判。

// chunkHead 是分块下载第一块的回复：大小，以及原来放在响应头里的信息（类型、文件名、修改时间）。
type chunkHead struct {
	Data     []byte `json:"data"`
	Size     int64  `json:"size"`
	Mime     string `json:"mime"`
	Name     string `json:"name"`
	Modified int64  `json:"modified"`
}

// upload 把请求体按块发给一个分块上传的方法，最后一块带 done 和其余参数（文件名等），把最后一块的回复写进响应体。
// 边读边发，不把整个文件读进内存；超过上限时 app-server 回 413，这里原样带回页面。
func (s *server) upload(w http.ResponseWriter, req *http.Request, method string, extra map[string]any) {
	buf := make([]byte, appserver.MaxChunk)
	var uploadID string
	var offset int64
	for {
		n, err := io.ReadFull(req.Body, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			http.Error(w, "请求体读不出来："+err.Error(), http.StatusBadRequest)
			return
		}
		done := err == io.EOF || err == io.ErrUnexpectedEOF
		params := map[string]any{"data": buf[:n], "offset": offset, "uploadId": uploadID}
		if done {
			params["done"] = true
			for key, value := range extra {
				params[key] = value
			}
		}
		result, ferr := s.call(req, method, params)
		if ferr != nil {
			fail(w, ferr)
			return
		}
		if done {
			writeJSON(w, result)
			return
		}
		// 不是最后一块：回复里带回的 uploadId 带上，接着传。
		var part struct {
			UploadID string `json:"uploadId"`
		}
		if json.Unmarshal(result, &part) != nil || part.UploadID == "" {
			http.Error(w, "app-server 没有给出 uploadId", http.StatusBadGateway)
			return
		}
		uploadID, offset = part.UploadID, offset+int64(n)
	}
}

// download 分块取回一份二进制内容并写进响应体：先取第一块，用 head 设好响应头，再按 size 接着取到取完。
func (s *server) download(w http.ResponseWriter, req *http.Request, method string, extra map[string]any, head func(chunkHead)) {
	next := func(offset int64) (chunkHead, error) {
		params := map[string]any{"offset": offset, "length": appserver.MaxChunk}
		for key, value := range extra {
			params[key] = value
		}
		var out chunkHead
		return out, s.callInto(req, method, params, &out)
	}
	first, err := next(0)
	if err != nil {
		fail(w, err)
		return
	}
	head(first)
	w.WriteHeader(http.StatusOK)
	written := int64(len(first.Data))
	w.Write(first.Data)
	// 响应头已经发出去了，后面某一块出错只能就此断开：页面拿到的内容不完整，和原来读到一半出错一样。
	for written < first.Size {
		part, err := next(written)
		if err != nil || len(part.Data) == 0 {
			return
		}
		w.Write(part.Data)
		written += int64(len(part.Data))
	}
}

// RFC 5987 的 filename*：中文文件名下载后保持原名；字母数字和 -._~ 以外的字节都转成 %XX。
func urlPathEscape(name string) string {
	var b strings.Builder
	for _, c := range []byte(name) {
		if c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
