package appserver

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/xuzhao123/tern/internal/protocol"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

type requestReceipt struct {
	State    string          `json:"state"`
	Hash     string          `json:"hash"`
	Response json.RawMessage `json:"response,omitempty"`
	Error    *protocol.Error `json:"error,omitempty"`
}

func (s *Server) receiptPath(user, id string) string {
	hash := sha256.Sum256([]byte(user + "\x00" + id))
	return filepath.Join(s.dir, ".data", "requests", fmt.Sprintf("%x.json", hash))
}

func writeReceipt(path string, receipt requestReceipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func readReceipt(path string) (requestReceipt, error) {
	var receipt requestReceipt
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &receipt)
	}
	return receipt, err
}

// 回执认领与写入持锁，真正执行不持全局锁；其他任务仍能并行开始。
func (c *conn) requestOnce(m protocol.Message, call func(*conn, protocol.Message) (any, *protocol.Error)) (any, *protocol.Error) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(m.Params, &params); err != nil {
		return nil, bad(400, "请求必须是 JSON 对象")
	}
	var id string
	if raw, ok := params["request_id"]; ok {
		if err := json.Unmarshal(raw, &id); err != nil || !requestIDPattern.MatchString(id) {
			return nil, bad(400, "request_id 格式不正确")
		}
	} else {
		return call(c, m)
	}
	canonical, err := json.Marshal(params)
	if err != nil {
		return nil, bad(400, err.Error())
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(append([]byte(m.Method+"\x00"), canonical...)))
	path := c.s.receiptPath(userName(m), id)
	c.s.requestMu.Lock()
	previous, err := readReceipt(path)
	if err == nil {
		c.s.requestMu.Unlock()
		if previous.Hash != hash {
			return nil, bad(409, "request_id 已用于不同的请求")
		}
		if previous.State == "accepted" {
			return previous.Response, nil
		}
		if previous.State == "rejected" {
			return nil, previous.Error
		}
		return nil, bad(409, "请求已经接收，正在确认结果；请查询回执")
	}
	if !errors.Is(err, os.ErrNotExist) {
		c.s.requestMu.Unlock()
		return nil, bad(500, "无法读取发送回执")
	}
	receipt := requestReceipt{State: "pending", Hash: hash}
	err = writeReceipt(path, receipt)
	c.s.requestMu.Unlock()
	if err != nil {
		return nil, bad(500, "无法保存发送回执，没有执行请求")
	}
	result, ferr := call(c, m)
	if ferr != nil {
		receipt.State, receipt.Error = "rejected", ferr
	} else {
		receipt.State = "accepted"
		receipt.Response, err = json.Marshal(result)
		if err != nil {
			return nil, bad(500, "请求已执行，但无法编码回执，请查询状态")
		}
	}
	c.s.requestMu.Lock()
	err = writeReceipt(path, receipt)
	c.s.requestMu.Unlock()
	if err != nil {
		return nil, bad(500, "请求已处理，但回执保存失败，请查询状态")
	}
	return result, ferr
}

func (c *conn) requestStatus(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		RequestID string `json:"request_id"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	if !requestIDPattern.MatchString(input.RequestID) {
		return nil, bad(400, "request_id 格式不正确")
	}
	user := userName(m)
	c.s.requestMu.Lock()
	receipt, err := readReceipt(c.s.receiptPath(user, input.RequestID))
	c.s.requestMu.Unlock()
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{"state": "not_found"}, nil
	}
	if err != nil {
		return nil, bad(500, "无法读取发送回执")
	}
	if receipt.State == "pending" {
		c.s.mu.Lock()
		runs := append([]*run(nil), c.s.runs...)
		c.s.mu.Unlock()
		for _, r := range runs {
			r.mu.Lock()
			for _, e := range r.events {
				if e.Kind == "shell" && e.RequestID == input.RequestID && e.RequestUser == user {
					receipt.State = "accepted"
					receipt.Response = append(json.RawMessage(nil), e.Body...)
					break
				}
			}
			r.mu.Unlock()
			if receipt.State == "accepted" {
				return receipt, nil
			}
		}
	}
	if receipt.State == "pending" {
		c.s.mu.Lock()
		runs := append([]*run(nil), c.s.runs...)
		c.s.mu.Unlock()
		for _, r := range runs {
			r.mu.Lock()
			found := false
			queueID := ""
			var queued *queueItem
			managed := false
			for _, item := range r.managedSteers {
				if item.ID == input.RequestID && item.User == user {
					managed = true
					break
				}
			}
			items := append([]queueItem(nil), r.queue...)
			if r.queueClaim != nil {
				items = append(items, *r.queueClaim)
			}
			for _, item := range items {
				if item.RequestID == input.RequestID && item.User == user {
					copy := item
					queued = &copy
					break
				}
			}
			for _, e := range r.events {
				if e.RequestID == input.RequestID && e.RequestUser == user {
					found = true
					queueID = e.QueueID
					break
				}
			}
			r.mu.Unlock()
			if managed {
				receipt.State = "accepted"
				receipt.Response, _ = json.Marshal(map[string]any{"id": input.RequestID, "managed": true, "state": "received"})
				break
			}
			if queued != nil {
				receipt.State = "accepted"
				receipt.Response, _ = json.Marshal(queued)
				break
			}
			if found {
				receipt.State = "accepted"
				if queueID != "" {
					receipt.Response, _ = json.Marshal(map[string]any{"id": r.ID, "queue_id": queueID})
				} else {
					receipt.Response, _ = json.Marshal(r.summary())
				}
				break
			}
		}
	}
	return receipt, nil
}
