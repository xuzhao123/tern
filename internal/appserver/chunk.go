package appserver

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/xuzhao123/tern/internal/protocol"
)

// 二进制内容分块传：一条回复或通知就是一行，不能超过 protocol.Conn 一行 16MB 的上限，而附图 base64 之后会超。
// 所以附件、沙箱文件、浏览器画面、导入的会话文件按块传，每块原始数据不超过 4MB（base64 放在 data 字段）。
// 不改 protocol 包。

// MaxChunk 是一块原始数据的上限；界面按它切，读回来时也按它取。
const MaxChunk = 4 << 20

// part 是一块：第一块不带 uploadId、offset 为 0，回复里带回新的 uploadId，之后每块带上它；
// offset 必须等于已经收到的字节数。带 done: true 的那块才算收完，其余参数（文件名等）也在那块里。
type part struct {
	UploadID string `json:"uploadId"`
	Offset   int64  `json:"offset"`
	Data     []byte `json:"data"`
	Done     bool   `json:"done"`
}

// receive 收一块上传的数据：不是最后一块时返回 uploadId（回给界面接着传），最后一块返回全部数据。
// 收到一半的数据放在内存里，归这个连接所有，连接断开就丢掉。
func (c *conn) receive(m protocol.Message, limit int) (data []byte, uploadID string, done bool, err *protocol.Error) {
	var p struct{ part }
	if ferr := decode(m, &p); ferr != nil {
		return nil, "", false, ferr
	}
	// 一块原始数据不能超过 4MB：界面按这个数切，超过说明不是按协议在传（base64 之后会顶到一行 16MB 的上限）。
	if len(p.Data) > MaxChunk {
		return nil, "", false, bad(400, "一块原始数据不能超过 4194304 字节")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.UploadID == "" {
		if p.Offset != 0 {
			return nil, "", false, bad(400, "offset 必须等于已经收到的字节数")
		}
		if len(p.Data) > limit {
			return nil, "", false, bad(413, tooLarge(limit))
		}
		if p.Done {
			return p.Data, "", true, nil
		}
		id := newUploadID()
		c.uploads[id] = p.Data
		return nil, id, false, nil
	}
	buffered, ok := c.uploads[p.UploadID]
	if !ok {
		return nil, "", false, bad(400, "找不到这次上传："+p.UploadID)
	}
	if p.Offset != int64(len(buffered)) {
		return nil, "", false, bad(400, "offset 必须等于已经收到的字节数")
	}
	if len(buffered)+len(p.Data) > limit {
		delete(c.uploads, p.UploadID) // 超上限就丢掉
		return nil, "", false, bad(413, tooLarge(limit))
	}
	buffered = append(buffered, p.Data...)
	if !p.Done {
		c.uploads[p.UploadID] = buffered
		return nil, p.UploadID, false, nil
	}
	delete(c.uploads, p.UploadID)
	return buffered, "", true, nil
}

// tooLarge 是超过上限时的报错，沿用原来各接口的说法。
func tooLarge(limit int) string {
	switch limit {
	case 128 << 20:
		return "文件不能超过 128 MB"
	case 10 << 20:
		return "文件不能超过 10 MB"
	}
	return "图片不能超过 20 MB"
}

// chunk 从 data 里取 offset 起的一块，length 不超过 4MB；界面按 size 接着取，直到取完。
func chunk(data []byte, offset, length int64) ([]byte, *protocol.Error) {
	if length <= 0 || length > MaxChunk {
		return nil, bad(400, "length 取 1 到 4194304")
	}
	if offset < 0 || offset > int64(len(data)) {
		return nil, bad(400, "offset 超出内容范围")
	}
	return data[offset:min(int64(len(data)), offset+length)], nil
}

// newUploadID 给一次分块上传取个编号：随机，同一个连接里不重。
func newUploadID() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}
