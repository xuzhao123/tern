package workspace

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/xuzhao123/tern/internal/llm"
)

// apply_patch：一次改多个文件（新建、删除、改名、多处修改），格式同 Codex 的 apply_patch。
// 先把整份补丁解析、逐个文件算出结果，全部对得上才动手；有一处对不上就一个文件也不改。
// 和 edit_file 一样：改、删已有文件之前要先 read_file 读过、且之后没被改过；写入前存快照，撤销这一轮时一起退。

var PatchDefinition = map[string]any{
	"name": "apply_patch",
	"description": "用一份补丁一次改多个文件：新建、删除、改名、在多处修改。格式：\n" +
		"*** Begin Patch\n" +
		"*** Add File: 路径\n+新文件的每一行都以 + 开头\n" +
		"*** Delete File: 路径\n" +
		"*** Update File: 路径\n*** Move to: 新路径（可选，改名）\n" +
		"@@ 可选：一行用来定位的原文（如函数签名）\n 不变的上下文行以空格开头\n-删掉的行\n+加上的行\n" +
		"*** End Patch\n" +
		"每处修改前后各带约 3 行原样的上下文，在文件里要能唯一定位，定位不唯一会被拒绝（写了 @@ 定位行时取它后面的第一处，只有 + 行时加在定位行后面）；一个文件可以有多个 @@ 段，按在文件里的先后顺序写。改文件末尾时在那一段后加一行 *** End of File。" +
		"路径相对工作区根目录。修改、删除已有文件之前要先 read_file 读过（之后被改过会被拒绝）；新建的文件不能已经存在。" +
		"整份补丁要么全部生效、要么一个文件都不改。只改一个文件的一两处时用 edit_file 更简单。",
	"parameters": object(map[string]any{
		"input": map[string]string{"type": "string", "description": "完整的补丁，从 *** Begin Patch 到 *** End Patch"},
	}, "input"),
}

type patchOp struct {
	kind   string // add、delete、update
	path   string
	moveTo string
	add    []string     // add：新文件的行
	chunks []patchChunk // update
}

type patchChunk struct {
	header   string // @@ 后面的定位行，可以为空
	old, new []string
	eof      bool // 这一段在文件末尾
	plus     int  // 加了几行、删了几行（不算上下文），报给模型和界面
	minus    int
}

func parsePatch(input string) ([]patchOp, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(input), "\r\n", "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, errors.New("补丁要以 *** Begin Patch 开头、*** End Patch 结尾")
	}
	lines = lines[1 : len(lines)-1]
	var ops []patchOp
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			op := patchOp{kind: "add", path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))}
			for i++; i < len(lines) && !strings.HasPrefix(lines[i], "*** "); i++ {
				if !strings.HasPrefix(lines[i], "+") {
					return nil, fmt.Errorf("新建 %s 时每一行都要以 + 开头：%q", op.path, lines[i])
				}
				op.add = append(op.add, lines[i][1:])
			}
			ops = append(ops, op)
		case strings.HasPrefix(line, "*** Delete File: "):
			ops = append(ops, patchOp{kind: "delete", path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))})
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			op := patchOp{kind: "update", path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))}
			i++
			if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to: ") {
				op.moveTo = strings.TrimSpace(strings.TrimPrefix(lines[i], "*** Move to: "))
				i++
			}
			var chunk *patchChunk
			for ; i < len(lines); i++ {
				l := lines[i]
				if l == "*** End of File" {
					if chunk == nil {
						return nil, fmt.Errorf("%s：*** End of File 前面没有修改", op.path)
					}
					chunk.eof = true
					continue
				}
				if strings.HasPrefix(l, "*** ") {
					break
				}
				if l == "@@" || strings.HasPrefix(l, "@@ ") {
					op.chunks = append(op.chunks, patchChunk{header: strings.TrimSpace(strings.TrimPrefix(l, "@@"))})
					chunk = &op.chunks[len(op.chunks)-1]
					continue
				}
				if chunk == nil {
					// 第一段可以不写 @@（Codex 也这样接受）。
					op.chunks = append(op.chunks, patchChunk{})
					chunk = &op.chunks[0]
				}
				switch {
				case l == "": // 空的上下文行：有的模型把行首的空格吞掉了
					chunk.old, chunk.new = append(chunk.old, ""), append(chunk.new, "")
				case l[0] == ' ':
					chunk.old, chunk.new = append(chunk.old, l[1:]), append(chunk.new, l[1:])
				case l[0] == '-':
					chunk.old, chunk.minus = append(chunk.old, l[1:]), chunk.minus+1
				case l[0] == '+':
					chunk.new, chunk.plus = append(chunk.new, l[1:]), chunk.plus+1
				default:
					return nil, fmt.Errorf("%s：修改段里的每一行要以空格、- 或 + 开头：%q", op.path, l)
				}
			}
			if len(op.chunks) == 0 && op.moveTo == "" {
				return nil, fmt.Errorf("%s：没有要改的内容", op.path)
			}
			ops = append(ops, op)
		case strings.TrimSpace(line) == "":
			i++
		default:
			return nil, fmt.Errorf("看不懂这一行（应该是 *** Add File、*** Delete File 或 *** Update File）：%q", line)
		}
	}
	if len(ops) == 0 {
		return nil, errors.New("补丁里没有任何文件")
	}
	return ops, nil
}

// seekLines 从 start 起找 want 在 file 里的位置：先精确，再按 lenientLevels 逐级放宽空白。eof 时只认文件末尾。
// 返回位置、用了哪条放宽规则（精确时为空）和同一规则下的下一处（没有时为 -1）；找不到时位置为 -1。
func seekLines(file, want []string, start int, eof bool) (at int, rule string, next int) {
	if len(want) == 0 {
		return len(file), "", -1
	}
	norms := append([]struct {
		rule string
		norm func(string) string
	}{{"", func(s string) string { return s }}}, lenientLevels...)
	for _, level := range norms {
		match := func(i int) bool {
			for j := range want {
				if level.norm(file[i+j]) != level.norm(want[j]) {
					return false
				}
			}
			return true
		}
		if eof {
			if i := len(file) - len(want); i >= 0 && match(i) {
				return i, level.rule, -1
			}
			continue
		}
		for i := max(start, 0); i+len(want) <= len(file); i++ {
			if !match(i) {
				continue
			}
			for j := i + 1; j+len(want) <= len(file); j++ {
				if match(j) {
					return i, level.rule, j
				}
			}
			return i, level.rule, -1
		}
	}
	return -1, "", -1
}

// applyChunks 把各段修改按顺序用到文件的行上。
func applyChunks(path string, text string, chunks []patchChunk) (string, []string, error) {
	// 补丁里的行不带 \r：整份是 CRLF 的文件按 LF 匹配，写回时还原，不把换行符全改掉。
	crlf := strings.Contains(text, "\r\n") && strings.Count(text, "\r\n") == strings.Count(text, "\n")
	if crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	// 只去掉结尾换行留下的那个空元素：文件末尾的空行和“最后一行没有换行”都原样保留。
	file := strings.Split(text, "\n")
	endsLine := file[len(file)-1] == ""
	if endsLine {
		file = file[:len(file)-1]
	}
	var loose []string
	pos := 0
	for n, c := range chunks {
		if c.header != "" {
			at, _, _ := seekLines(file, []string{c.header}, pos, false)
			if at < 0 {
				return "", nil, fmt.Errorf("%s 第 %d 段：找不到 @@ 后面的定位行 %q", path, n+1, c.header)
			}
			pos = at + 1
			if len(c.old) > 0 && strings.TrimSpace(c.old[0]) == strings.TrimSpace(c.header) {
				pos = at // 定位行本身也写进了上下文
			}
		}
		old, repl := c.old, c.new
		at, rule, next := pos, "", -1 // 有定位行、只加不删：加在定位行后面
		if len(old) > 0 || c.header == "" || c.eof {
			at, rule, next = seekLines(file, old, pos, c.eof)
		}
		// 模型常在末尾多带一个空的上下文行（文件结尾的换行）：去掉再找一次。
		if at < 0 && len(old) > 0 && old[len(old)-1] == "" {
			old = old[:len(old)-1]
			if len(repl) > 0 && repl[len(repl)-1] == "" {
				repl = repl[:len(repl)-1]
			}
			at, rule, next = seekLines(file, old, pos, c.eof)
		}
		if at < 0 {
			want := strings.Join(c.old, "\n")
			return "", nil, fmt.Errorf("%s 第 %d 段：要替换的原文在文件里找不到（从第 %d 行往后找的）。\n%v", path, n+1, pos+1, notFound(path, text, want))
		}
		// 没有定位行时原文要唯一：改错地方还报成功，比报错重试糟得多。有定位行时取它后面的第一处。
		if next >= 0 && c.header == "" {
			return "", nil, fmt.Errorf("%s 第 %d 段：要替换的原文出现了不止一处（这一段应用时的第 %d、%d 行）；多带几行上下文，或用 @@ 写一行定位", path, n+1, at+1, next+1)
		}
		if rule != "" {
			loose = append(loose, fmt.Sprintf("%s 第 %d 段按%s匹配", path, n+1, rule))
		}
		file = append(file[:at], append(append([]string{}, repl...), file[at+len(old):]...)...)
		pos = at + len(repl)
	}
	out := strings.Join(file, "\n")
	if len(file) > 0 && endsLine {
		out += "\n"
	}
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, loose, nil
}

type patchWrite struct {
	path    string
	before  []byte      // nil：原来不存在
	after   []byte      // nil：删除
	mode    fs.FileMode // 新文件的权限；0 表示默认（改名时沿用原文件的，可执行位不丢）
	added   int
	removed int
}

func applyPatch(input string) (any, error) {
	ops, err := parsePatch(input)
	if err != nil {
		return nil, llm.Permanent(err)
	}
	mu.Lock()
	defer mu.Unlock()
	// 第一遍：解析路径、读当前内容、核对、算出新内容。任何一处出错都不写。
	var writes []*patchWrite
	touched := map[string]bool{}
	var loose []string
	target := func(p string) (string, error) {
		path, err := clean(p)
		if err != nil {
			return "", err
		}
		if err := guarded(path); err != nil {
			return "", err
		}
		if touched[path] {
			return "", llm.Permanent(fmt.Errorf("%s 在补丁里出现了不止一次；同一个文件的修改写在一个 *** Update File 里", path))
		}
		touched[path] = true
		return path, nil
	}
	existing := func(path string) ([]byte, error) {
		data, err := read(path)
		if err != nil {
			return nil, err
		}
		return data, fresh(path, data)
	}
	for _, op := range ops {
		path, err := target(op.path)
		if err != nil {
			return nil, err
		}
		switch op.kind {
		case "add":
			if _, err := root.Stat(path); err == nil {
				return nil, llm.Permanent(fmt.Errorf("%s 已经存在，不能用 Add File 新建；要改它用 Update File", path))
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, llm.Permanent(fmt.Errorf("无法访问 %s：%v", path, err))
			}
			content := strings.Join(op.add, "\n")
			if len(op.add) > 0 {
				content += "\n"
			}
			writes = append(writes, &patchWrite{path: path, after: []byte(content), added: len(op.add)})
		case "delete":
			data, err := existing(path)
			if err != nil {
				return nil, err
			}
			writes = append(writes, &patchWrite{path: path, before: data, removed: len(linesOf(string(data)))})
		case "update":
			data, err := existing(path)
			if err != nil {
				return nil, err
			}
			text := string(data)
			updated := text
			if len(op.chunks) > 0 {
				var l []string
				if updated, l, err = applyChunks(path, text, op.chunks); err != nil {
					return nil, llm.Permanent(err)
				}
				loose = append(loose, l...)
			}
			added, removed := 0, 0
			for _, c := range op.chunks {
				added, removed = added+c.plus, removed+c.minus
			}
			if op.moveTo == "" {
				writes = append(writes, &patchWrite{path: path, before: data, after: []byte(updated), added: added, removed: removed})
				continue
			}
			dest, err := target(op.moveTo)
			if err != nil {
				return nil, err
			}
			if _, err := root.Stat(dest); err == nil {
				return nil, llm.Permanent(fmt.Errorf("改名的目标 %s 已经存在", dest))
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, llm.Permanent(fmt.Errorf("无法访问 %s：%v", dest, err))
			}
			info, err := root.Stat(path)
			if err != nil {
				return nil, llm.Permanent(fmt.Errorf("无法访问 %s：%v", path, err))
			}
			writes = append(writes, &patchWrite{path: path, before: data},
				&patchWrite{path: dest, after: []byte(updated), added: added, removed: removed, mode: info.Mode().Perm()})
		}
	}
	// 补丁自己新建的路径互相冲突（a 和 a/b）磁盘上还查不出来，写到一半才失败，要在动手前拦下。
	for _, a := range writes {
		for _, b := range writes {
			if a.after != nil && b.after != nil && strings.HasPrefix(b.path, a.path+"/") {
				return nil, llm.Permanent(fmt.Errorf("%s 和 %s 冲突：一个文件不能同时是另一个文件的上级目录", a.path, b.path))
			}
		}
	}
	// 第二遍：先把快照都存下来（存不下就一个都不改），再依次写入。
	for _, w := range writes {
		if err := snapshot(w.path, w.before, w.before != nil); err != nil {
			return nil, fmt.Errorf("写入前的快照没存下来，补丁没有应用：%w", err)
		}
	}
	files := []map[string]any{}
	for _, w := range writes {
		action := "updated"
		switch {
		case w.after == nil:
			if err := root.Remove(w.path); err != nil {
				return nil, fmt.Errorf("删除 %s 失败（前面的文件已经改了，见 files）：%w", w.path, err)
			}
			removed(w.path)
			delete(seen, w.path)
			action = "deleted"
		default:
			if w.before == nil {
				action = "added"
				if dir := filepath.Dir(w.path); dir != "." {
					if err := root.MkdirAll(dir, 0o755); err != nil {
						return nil, fmt.Errorf("无法创建目录 %s：%w", dir, err)
					}
				}
			}
			if err := root.WriteFile(w.path, w.after, cmp.Or(w.mode, 0o644)); err != nil {
				return nil, fmt.Errorf("写入 %s 失败（前面的文件已经改了，见 files）：%w", w.path, err)
			}
			written(w.path, w.after)
			seen[w.path] = hash(w.after)
		}
		note(w.path)
		files = append(files, map[string]any{"path": w.path, "action": action, "added": w.added, "removed": w.removed})
	}
	result := map[string]any{"files": files}
	if len(loose) > 0 {
		result["matched"] = strings.Join(loose, "；") + "：回头看一眼缩进对不对"
	}
	return result, nil
}
