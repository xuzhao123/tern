package appserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/protocol"
	"github.com/xuzhao123/tern/internal/tenant"
)

// 附图：界面把原图存进附件目录（按内容寻址，agent 写入的是同一个目录），
// 之后的消息、检查点与对话存档里只放 attachment:// 引用。缩放交给 agent 做，界面显示原图。
//
// 图是用户的私有数据：目录 0700、文件 0600（llm.StoreAttachment）；
// 上传与别的会改变状态的操作一样先写审计，写不进审计就不保存。
// 原图最大 20MB，base64 后超过一行 16MB 的上限，所以按块传（见 chunk.go）。

// attachmentDir 是附件目录 .data/attachments。
func (s *Server) attachmentDir() string {
	return filepath.Join(s.dir, tenant.Path("", "attachments"))
}

// attachment/upload：分块收图片，最后一块带 done 时处理。名字只给模型看的来源和界面说明用，
// 存下来的名字由内容决定，所以同名不同图、不同名同图都不会打架。
func (c *conn) attachmentUpload(m protocol.Message) (any, *protocol.Error) {
	data, id, done, ferr := c.receive(m, llm.MaxImageBytes)
	if ferr != nil {
		return nil, ferr
	}
	if !done {
		return map[string]string{"uploadId": id}, nil
	}
	if llm.ImageMIME(data) == "" {
		return nil, bad(400, "只支持 PNG、JPEG、GIF、WebP")
	}
	var input struct {
		Name string `json:"name"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	name := attachmentName(input.Name)
	record := audit.Record{Actor: "user", User: userName(m), Event: "user_attach", Target: name, Detail: c.clientName()}
	record.Args(string(data))
	if err := audit.Write(c.ctx, record); err != nil {
		return nil, bad(500, "审计日志写入失败，没有保存这张图："+err.Error())
	}
	ref, err := llm.StoreAttachment(c.s.attachmentDir(), data)
	if err != nil {
		return nil, bad(500, "这张图存不下来："+err.Error())
	}
	return map[string]any{"ref": ref, "name": name}, nil
}

// attachment/read：引用不合法、文件不存在、内容不是图片都是 404。mime 按文件头认，界面禁止嗅探。
func (c *conn) attachmentRead(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Name   string `json:"name"`
		Offset int64  `json:"offset"`
		Length int64  `json:"length"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	path, err := llm.AttachmentPath(c.s.attachmentDir(), "attachment://"+input.Name)
	if err != nil {
		return nil, bad(404, "找不到这张图")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, bad(404, "找不到这张图")
	}
	mime := llm.ImageMIME(data)
	if mime == "" {
		return nil, bad(404, "找不到这张图")
	}
	part, ferr := chunk(data, input.Offset, input.Length)
	if ferr != nil {
		return nil, ferr
	}
	return map[string]any{"data": part, "size": len(data), "mime": mime}, nil
}

// attachmentName 把上传时给的文件名收成一层、无控制字符、最长 128 字节；空的就是 image。
func attachmentName(raw string) string {
	name := strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, strings.TrimSpace(raw))
	// 只要最后一层：浏览器给的是文件名，传路径也只会留下名字本身。
	if name = filepath.Base(name); name == "." || name == ".." || name == string(filepath.Separator) || !utf8.ValidString(name) {
		name = ""
	}
	if len(name) > 128 {
		name = strings.ToValidUTF8(name[:128], "")
	}
	if name == "" {
		return "image"
	}
	return name
}

// attachRef 是消息里的一张图：字段名与 agent turn/start 的 images 一致。
type attachRef struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
}

// checkImages 校验一条消息带的图：最多 llm.MaxImages 张，每个引用都要是附件目录里的文件。
func (s *Server) checkImages(images []attachRef) *protocol.Error {
	if len(images) > llm.MaxImages {
		return bad(400, fmt.Sprintf("每条消息最多 %d 张图", llm.MaxImages))
	}
	for _, img := range images {
		path, err := llm.AttachmentPath(s.attachmentDir(), img.Ref)
		if err != nil {
			return bad(400, "不是有效的附件引用："+img.Ref)
		}
		if _, err := os.Stat(path); err != nil {
			return bad(400, "找不到这张图，请重新上传："+img.Ref)
		}
	}
	return nil
}

// visionModel 回答“这家供应商的这个模型能不能看图”，规则与 agent 完全一致（llm.Config.Vision）。
// model 为空表示用这家供应商的默认值（.env 或路由表决定），与 agent 不传 -model 时一样。
func visionModel(provider, model string) (string, bool) {
	config, _ := llm.LoadConfig(provider) // 没有密钥时仍会解析出模型名
	if model != "" {
		config.Model = model
	}
	return config.Model, config.Vision()
}

// archiveImages 只在存档时把请求体里的图片 data URL 换成 attachment:// 引用：同一张图每次请求都带，
// 内联进按请求存的存档会成倍膨胀。转发给上游的仍是原请求体，一个字节都不改（Responses 上游只多一步换协议）。
//
// 没有图片的请求原样返回（纯文字请求的存档与之前逐字节一致）；解析或存储任何一步不顺就保留原来的
// data URL：存档宁可大一点，也要如实反映模型当时看到了什么。
func (s *Server) archiveImages(body []byte) []byte {
	if !bytes.Contains(body, []byte(`"data:image/`)) {
		return body
	}
	var request map[string]json.RawMessage
	var messages []map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil || json.Unmarshal(request["messages"], &messages) != nil {
		return body
	}
	dir := s.attachmentDir()
	changed := false
	for _, message := range messages {
		var parts []map[string]json.RawMessage
		// content 是字符串的消息（纯文字、工具结果文字）不用看。
		if json.Unmarshal(message["content"], &parts) != nil {
			continue
		}
		dirty := false
		for _, part := range parts {
			var kind string
			var image struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(part["type"], &kind) != nil || kind != "image_url" ||
				json.Unmarshal(part["image_url"], &image) != nil || !strings.HasPrefix(image.URL, "data:") {
				continue
			}
			ref, err := llm.StoreDataURL(dir, image.URL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "存档：有一张图没有换成引用（%v），按原样记录\n", err)
				continue
			}
			part["image_url"], _ = json.Marshal(map[string]string{"url": ref})
			dirty = true
		}
		if !dirty {
			continue
		}
		if data, err := json.Marshal(parts); err == nil {
			message["content"], changed = data, true
		}
	}
	if !changed {
		return body
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return body
	}
	request["messages"] = data
	if data, err = json.Marshal(request); err != nil {
		return body
	}
	return data
}
