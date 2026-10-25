package appserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/xuzhao123/tern/internal/audit"
	"github.com/xuzhao123/tern/internal/protocol"
	"github.com/xuzhao123/tern/internal/tenant"

	"github.com/google/uuid"
)

// 名字、归档与排队的后续消息放在单独的 .data/runs/<对话ID>.meta.json：
// {"name": "…", "archived": true, "queue": [{"id": "…", "text": "…", "at": "…"}]}，三项都可以没有。
// 不写进存档 .jsonl：对话结束后存档文件已经关了（run.add 在 exit 时关），改个名也不该把对话顶到列表最上面。
type runMeta struct {
	Name     string      `json:"name,omitempty"`
	Archived bool        `json:"archived,omitempty"`
	Queue    []queueItem `json:"queue,omitempty"`
}

// queueItem 是排着的一条后续消息：这一轮结束后由 app-server 接着发（见 followUp）。
// ID 由 app-server 生成，换个页面、重开界面删的还是同一项。
type queueItem struct {
	ID   string    `json:"id"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

const maxThreadName = 100 // 名字最多这么多个字（按字符数，不按字节）

func (s *Server) metaPath(id string) string {
	return filepath.Join(s.runsDir(), id+".meta.json")
}

func readMeta(path string) (runMeta, error) {
	var meta runMeta
	data, err := os.ReadFile(path)
	if err != nil {
		return meta, err
	}
	err = json.Unmarshal(data, &meta)
	return meta, err
}

// writeMeta 先写 .tmp 再 rename，权限和存档一样（0600）：读到的要么是旧内容，要么是新内容。
// 三项都没有时删掉文件，不留空壳。
func (s *Server) writeMeta(id string, meta runMeta) error {
	path := s.metaPath(id)
	if meta.Name == "" && !meta.Archived && len(meta.Queue) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.runsDir(), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// setMeta 改一项元数据：先写文件再改内存，写不进去就不改。
func (s *Server) setMeta(r *run, change func(*runMeta)) *protocol.Error {
	r.mu.Lock()
	meta := runMeta{Name: r.Name, Archived: r.Archived, Queue: slices.Clone(r.queue)}
	r.mu.Unlock()
	change(&meta)
	if err := s.writeMeta(r.ID, meta); err != nil {
		return bad(500, "无法写入对话的元数据："+err.Error())
	}
	r.mu.Lock()
	r.Name, r.Archived, r.queue = meta.Name, meta.Archived, meta.Queue
	r.mu.Unlock()
	return nil
}

// 这条对话现在的状态：agent 进程还在就是 active。四个元数据方法成功后都广播一次，各界面据此刷新列表。
func (r *run) liveStatus() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil || r.proc != nil {
		return "active"
	}
	return "idle"
}

// thread/name/set：名字去掉首尾空白、最长 100 个字；空串表示去掉名字，列表回到显示第一句话。
func (c *conn) threadNameSet(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Name     string `json:"name"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.threadToManage(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	name := strings.TrimSpace(input.Name)
	if utf8.RuneCountInString(name) > maxThreadName {
		return nil, bad(400, "名字最长 100 个字")
	}
	if err := audit.Write(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_thread_rename", Task: r.rootTask, Conversation: r.ID, Target: name, Detail: c.clientName()}); err != nil {
		return nil, bad(500, "审计日志写入失败，没有改名："+err.Error())
	}
	if ferr := c.s.setMeta(r, func(meta *runMeta) { meta.Name = name }); ferr != nil {
		return nil, ferr
	}
	c.s.status(r.ID, r.liveStatus())
	return r.summary(), nil
}

// thread/archive：归档；正在运行的也可以归档，跑完照常。
func (c *conn) threadArchive(m protocol.Message) (any, *protocol.Error) {
	return c.setArchived(m, true, "user_thread_archive", "归档")
}

// thread/unarchive：取消归档。
func (c *conn) threadUnarchive(m protocol.Message) (any, *protocol.Error) {
	return c.setArchived(m, false, "user_thread_unarchive", "取消归档")
}

func (c *conn) setArchived(m protocol.Message, archived bool, event, action string) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.threadToManage(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	if err := audit.Write(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: event, Task: r.rootTask, Conversation: r.ID, Detail: c.clientName()}); err != nil {
		return nil, bad(500, "审计日志写入失败，没有"+action+"："+err.Error())
	}
	if ferr := c.s.setMeta(r, func(meta *runMeta) { meta.Archived = archived }); ferr != nil {
		return nil, ferr
	}
	c.s.status(r.ID, r.liveStatus())
	return r.summary(), nil
}

// thread/delete：永久删除存档、检查点、快照与沙箱里的文件，见 drop 里的清单；不能撤销，也不进回收站。
func (c *conn) threadDelete(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.threadToManage(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	r.mu.Lock()
	running, turns := r.conn != nil || r.proc != nil, r.turnIDs()
	r.mu.Unlock()
	if running {
		return nil, bad(409, "这条对话还在运行，先停止再删除")
	}
	if err := audit.Write(c.ctx, audit.Record{Actor: "user", User: userName(m), Event: "user_thread_delete", Task: r.rootTask, Conversation: r.ID, Detail: c.clientName()}); err != nil {
		return nil, bad(500, "审计日志写入失败，没有删除："+err.Error())
	}
	if err := c.s.drop(r, turns); err != nil {
		return nil, bad(500, "删除失败："+err.Error())
	}
	c.s.status(r.ID, r.liveStatus())
	c.s.broadcast("thread/deleted", map[string]string{"threadId": r.ID})
	return nil, nil
}

// thread/queue/add：这一轮结束后再发，不打断现在这一步（网页端的 Tab）。文字去掉首尾空白，空的不收；
// 对话没在跑时没什么可排的，直接发就行（409）。成功回这一项，ID 由 app-server 生成。
func (c *conn) threadQueueAdd(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		Text     string `json:"text"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.threadToQueue(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return nil, bad(400, "请填写要排队的消息")
	}
	r.mu.Lock()
	running := r.conn != nil || r.proc != nil
	r.mu.Unlock()
	if !running {
		return nil, bad(409, "这条对话没在跑，直接发送就行")
	}
	item := queueItem{ID: uuid.NewString(), Text: text, At: time.Now().UTC()}
	if ferr := c.s.setMeta(r, func(meta *runMeta) { meta.Queue = append(meta.Queue, item) }); ferr != nil {
		return nil, ferr
	}
	c.s.status(r.ID, r.liveStatus())
	return item, nil
}

// thread/queue/delete：删掉排着的一项；这一项已经不在了（别的页面先删了、队列接着发过了）回 404。
func (c *conn) threadQueueDelete(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		ThreadID string `json:"threadId"`
		ID       string `json:"id"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	r, ferr := c.threadToQueue(input.ThreadID)
	if ferr != nil {
		return nil, ferr
	}
	// 找到才动：change 拿的是这一刻的队列，不用先在锁外算下标再删（中间可能被别的页面改过）。
	found := false
	if ferr := c.s.setMeta(r, func(meta *runMeta) {
		if i := slices.IndexFunc(meta.Queue, func(q queueItem) bool { return q.ID == input.ID }); i >= 0 {
			meta.Queue = slices.Delete(meta.Queue, i, i+1)
			found = true
		}
	}); ferr != nil {
		return nil, ferr
	}
	if !found {
		return nil, bad(404, "队列里没有这一项，可能已经被删掉或者发出去了")
	}
	c.s.status(r.ID, r.liveStatus())
	return nil, nil
}

const (
	maxSearchSnippet = 40 // 片段在命中处前后各留这么多字
	maxSearchHits    = 50 // 一次最多回这么多条
)

// thread/search：在所有对话的名字、每轮的问题（start、continue 的 text 与 steer 的 userMessage）
// 和最终回答（带 answer 的 turn/completed）里找子串，不区分大小写；每条对话只给最先命中的一处。
// 搜的是内存里的事件，不重读存档；评测的试次不搜，已归档的照搜。按 updated_at 从新到旧，最多 50 条。
func (c *conn) threadSearch(m protocol.Message) (any, *protocol.Error) {
	var input struct {
		Query string `json:"query"`
	}
	if ferr := decode(m, &input); ferr != nil {
		return nil, ferr
	}
	needle := foldRunes(strings.TrimSpace(input.Query))
	if len(needle) == 0 {
		return nil, bad(400, "请输入要搜索的字")
	}
	s := c.s
	s.mu.Lock()
	runs := append([]*run(nil), s.runs...)
	s.mu.Unlock()
	var hits []*threadHit
	for _, r := range runs {
		if r.Eval != "" {
			continue // 评测的试次在评测页里看
		}
		if hit := r.searchHit(needle); hit != nil {
			hits = append(hits, hit)
		}
	}
	slices.SortStableFunc(hits, func(a, b *threadHit) int { return b.updated.Compare(a.updated) })
	results := []map[string]any{}
	for _, hit := range hits[:min(len(hits), maxSearchHits)] {
		results = append(results, hit.item)
	}
	return results, nil
}

// threadHit 是一条对话的搜索结果：updated 用来排先后，item 是回给界面的那一项。
type threadHit struct {
	updated time.Time
	item    map[string]any
}

// searchHit 找这条对话里最先命中 needle 的一处：先看名字，再按事件顺序看各轮的问题、插话与最终回答。
// 名字、归档与事件都在一把锁里读完就放；没有命中回 nil。
func (r *run) searchHit(needle []rune) *threadHit {
	r.mu.Lock()
	defer r.mu.Unlock()
	where, snippet := "", ""
	name := []rune(r.Name)
	if at := findFold(name, needle); at >= 0 {
		where, snippet = "名字", excerpt(name, at, len(needle))
	}
	for i := 0; where == "" && i < len(r.events); i++ {
		text, kind := r.events[i].searchText()
		if text == "" {
			continue
		}
		runes := []rune(text)
		if at := findFold(runes, needle); at >= 0 {
			where, snippet = kind, excerpt(runes, at, len(needle))
		}
	}
	if where == "" {
		return nil
	}
	item := map[string]any{"id": r.ID, "name": r.Name, "query": r.Query, "archived": r.Archived, "where": where, "snippet": snippet}
	hit := &threadHit{item: item}
	if len(r.events) > 0 {
		hit.updated = r.events[len(r.events)-1].Time
		item["updated_at"] = hit.updated.UnixMilli()
	}
	return hit
}

// searchText 是这条事件能搜的文字：每轮的问题是 start、continue 的 text，插话是 steer 的 userMessage，
// 最终回答是带 answer 的 turn/completed。返回空表示这条事件不参与搜索。
func (e event) searchText() (text, where string) {
	switch e.Kind {
	case "start", "continue":
		return e.Text, "问题"
	case "notify":
		switch e.Method {
		case "item/completed":
			var p struct {
				Item struct {
					Type string `json:"type"`
					Data struct {
						Text  string `json:"text"`
						Steer bool   `json:"steer"`
					} `json:"data"`
				} `json:"item"`
			}
			if json.Unmarshal(e.Body, &p) == nil && p.Item.Type == "userMessage" && p.Item.Data.Steer {
				return p.Item.Data.Text, "问题"
			}
		case "turn/completed":
			var p struct {
				Answer string `json:"answer"`
			}
			if json.Unmarshal(e.Body, &p) == nil {
				return p.Answer, "回答"
			}
		}
	}
	return "", ""
}

// foldRunes 把文字按字转成小写：搜索不区分大小写，截片段时按字算，不按字节。
func foldRunes(s string) []rune {
	runes := []rune(s)
	for i, rn := range runes {
		runes[i] = unicode.ToLower(rn)
	}
	return runes
}

// findFold 在 text 里找 needle 第一次出现的位置（都按 foldRunes 转成小写），找的是字而不是字节；没有回 -1。
func findFold(text, needle []rune) int {
	for i := 0; i+len(needle) <= len(text); i++ {
		match := true
		for j, rn := range needle {
			if unicode.ToLower(text[i+j]) != rn {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// excerpt 是命中处前后各 40 个字的片段，被截掉的一头加省略号。
func excerpt(text []rune, at, n int) string {
	start, end := max(0, at-maxSearchSnippet), min(len(text), at+n+maxSearchSnippet)
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	b.WriteString(string(text[start:end]))
	if end < len(text) {
		b.WriteString("…")
	}
	return b.String()
}

// 排队只给能续聊的普通对话：评测的试次、导入的对话按 409 拒绝（和导入的对话不能续聊一致）。
func (c *conn) threadToQueue(id string) (*run, *protocol.Error) {
	r, ferr := c.s.threadOf(id)
	if ferr != nil {
		return nil, ferr
	}
	if r.Eval != "" {
		return nil, bad(409, "评测的试次不能排队")
	}
	if r.Source != "" {
		return nil, bad(409, "导入的对话只读，不能排队")
	}
	return r, nil
}

// 名字、归档与删除的操作对象：评测的试次在评测页里管理，这里一律回 409。
func (c *conn) threadToManage(id string) (*run, *protocol.Error) {
	r, ferr := c.s.threadOf(id)
	if ferr != nil {
		return nil, ferr
	}
	if r.Eval != "" {
		return nil, bad(409, "评测的试次在评测页里管理")
	}
	return r, nil
}

// drop 删掉一条对话占的文件，再把它从列表里拿掉：之后对这个 ID 的请求都照“没有这条对话”处理（404）。
// 审计（只追加的哈希链）、traces、attachments（按内容寻址，别的对话也可能在用）、progress（按工作区存）不删。
func (s *Server) drop(r *run, turns []string) error {
	// 分叉出来的对话复制了原对话前几轮的事件，共用这些轮次ID和沙箱目录：还被别的对话引用就不删。
	shared := s.turnsOfOthers(r)
	var failure error
	del := func(path string) {
		if failure != nil {
			return
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failure = err
		}
	}
	delAll := func(path string) {
		if failure == nil {
			failure = os.RemoveAll(path)
		}
	}
	for _, id := range turns {
		if shared[id] {
			continue
		}
		del(s.abs(tenant.Path("", "checkpoints", id+".json")))
		del(s.abs(tenant.Path("", "checkpoints", id+".lock")))
		delAll(s.abs(tenant.Path("", "checkpoints", id+".files")))
		delAll(s.abs(tenant.Path("", "browser", id)))
	}
	// 沙箱目录、工具输出与会话记录按对话第一轮存，所以共用第一轮的对话还在时都留着。
	if len(turns) > 0 && !shared[turns[0]] {
		delAll(s.abs(tenant.Path("", "sandbox", turns[0])))
		delAll(s.abs(tenant.Path("", "outputs", turns[0])))
		del(s.abs(tenant.Path("", "sessions", turns[0]+".jsonl")))
	}
	if failure != nil {
		return failure
	}
	// 存档最后删：前面有文件删不掉时照旧报错、对话也还在列表里，不至于只有存档不见了。
	del(filepath.Join(s.runsDir(), r.ID+".jsonl"))
	del(s.metaPath(r.ID))
	if failure != nil {
		return failure
	}
	s.mu.Lock()
	s.runs = slices.DeleteFunc(s.runs, func(other *run) bool { return other == r })
	s.mu.Unlock()
	return nil
}

// turnsOfOthers 是除这条对话以外，其余对话各轮的检查点ID。
func (s *Server) turnsOfOthers(except *run) map[string]bool {
	s.mu.Lock()
	runs := append([]*run(nil), s.runs...)
	s.mu.Unlock()
	taken := map[string]bool{}
	for _, other := range runs {
		if other == except {
			continue
		}
		other.mu.Lock()
		for _, id := range other.turnIDs() {
			taken[id] = true
		}
		other.mu.Unlock()
	}
	return taken
}
