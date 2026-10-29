package appserver

import (
	"encoding/json"
	"strings"

	"github.com/xuzhao123/tern/internal/protocol"
)

// 页边界按事件序号固定；只复制这一页，不在搜索文字时持有运行锁。
func (c *conn) threadFind(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Query    string `json:"query"`
		After    int    `json:"after"`
		Through  int    `json:"through"`
	}
	if err := decode(m, &input); err != nil {
		return nil, err
	}
	needle := foldRunes(strings.TrimSpace(input.Query))
	if len(needle) == 0 || len(needle) > 200 || input.After < 0 || input.Through < 0 || input.Through > 0 && input.After > input.Through {
		return nil, bad(400, "请输入1–200字的关键词和有效分页边界")
	}
	r, ferr := c.s.threadOf(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.mu.Lock()
	through := input.Through
	last := 0
	if len(r.events) > 0 {
		last = r.events[len(r.events)-1].Seq
	}
	if through == 0 {
		through = last
	}
	if through > last || input.After > through {
		r.mu.Unlock()
		return nil, bad(409, "任务历史已变化，请重新查找")
	}
	page := make([]event, 0, 1000)
	for _, e := range r.events {
		if e.Seq > input.After && e.Seq <= through {
			page = append(page, e)
		}
		if len(page) == 1000 {
			break
		}
	}
	r.mu.Unlock()
	hits := []map[string]any{}
	next := input.After
	for _, e := range page {
		if c.ctx.Err() != nil {
			return nil, bad(408, "查找已取消")
		}
		next = e.Seq
		text, where := e.searchText()
		toolID := ""
		if e.Kind == "notify" && e.Method == "item/completed" {
			var body struct {
				Item struct {
					ID   string `json:"id"`
					Type string `json:"type"`
					Data struct {
						Observation json.RawMessage `json:"observation"`
					} `json:"data"`
				} `json:"item"`
			}
			if json.Unmarshal(e.Body, &body) == nil && body.Item.Type == "toolCall" {
				text, where, toolID = string(body.Item.Data.Observation), "工具结果", body.Item.ID
			}
		}
		if e.Kind == "shell" {
			var body struct {
				Output string `json:"output"`
			}
			if json.Unmarshal(e.Body, &body) == nil {
				text, where = e.Text+"\n"+body.Output, "命令"
			}
		}
		runes := []rune(text)
		if at := findFold(runes, needle); at >= 0 {
			hits = append(hits, map[string]any{"seq": e.Seq, "call": e.Call, "sub": e.Sub, "task": e.Task, "tool_id": toolID, "where": where, "snippet": excerpt(runes, at, len(needle))})
		}
		if len(hits) == 50 {
			break
		}
	}
	if next >= through || len(page) == 0 {
		next = 0
	}
	return map[string]any{"hits": hits, "through": through, "next": next}, nil
}
