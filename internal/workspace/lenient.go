package workspace

import (
	"errors"
	"fmt"
	"strings"
)

// 模型给的 old_string 常常和文件只差空白：gofmt 对齐过的结构体字段、注释、行尾多出来的空格。
// 精确匹配不到时按行逐级放宽再找（和 Codex 的 apply_patch 一样），只差空白就照改，并在结果里注明用了哪一条规则，
// 模型就知道回头看一眼缩进；真对不上时给出文件里最接近的一段原文，可以照着抄，不必先 read_file。
//
// lenientLevels 是放宽的各条规则，按顺序试，哪一级先有匹配就停在哪一级。
var lenientLevels = []struct {
	rule string
	norm func(string) string
}{
	{"忽略行尾空白", func(line string) string { return strings.TrimRight(line, " \t") }},
	{"忽略首尾空白", strings.TrimSpace},
	// gofmt 对齐结构体字段、注释时最常见的就是这种：只差行内空白的多少。
	{"忽略行内空白的多少", func(line string) string { return strings.Join(strings.Fields(line), " ") }},
}

type lenientHit struct {
	rule  string
	from  int   // 这段第一行的行首
	to    int   // 这段最后一行的行尾（不含换行）
	line  int   // 第一处的行号
	count int   // 这一级匹配到几处
	lines []int // 各处的行号，最多 10 个，报错时用
}

// linesOf 把文本切成行，去掉结尾的空行：末尾的换行是行与行之间的分隔，不算单独一行（和 read_file 的行号一致）。
func linesOf(s string) []string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// blank 判断 old_string 是不是只有一行、而且去掉空白以后什么都没有：这种不放宽，
// 对不上多半是模型把缩进当成了原文，给它最接近的原文更有用。
func blank(s string) bool {
	return len(linesOf(s)) <= 1 && strings.TrimSpace(s) == ""
}

// lenientFind 在 text 里按行找 oldString，一级一级放宽空白。返回第一级有匹配的那一处（可能有多处，由调用方报错）。
func lenientFind(text, oldString string) *lenientHit {
	file := linesOf(text)
	old := linesOf(oldString)
	if len(old) == 0 || len(old) > len(file) {
		return nil
	}
	starts := make([]int, len(file)) // 每行的行首在 text 里的字节偏移
	for i, off := 0, 0; i < len(file); i++ {
		starts[i] = off
		off += len(file[i]) + 1
	}
	for _, level := range lenientLevels {
		normFile := make([]string, len(file))
		for i, line := range file {
			normFile[i] = level.norm(line)
		}
		want := make([]string, len(old))
		for i, line := range old {
			want[i] = level.norm(line)
		}
		hit := &lenientHit{rule: level.rule}
		for i := 0; i+len(old) <= len(file); i++ {
			match := true
			for j := range want {
				if normFile[i+j] != want[j] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			if hit.count == 0 {
				hit.line = i + 1
				hit.from = starts[i]
				hit.to = starts[i+len(old)-1] + len(file[i+len(old)-1])
			}
			hit.count++
			if len(hit.lines) < 10 {
				hit.lines = append(hit.lines, i+1)
			}
		}
		if hit.count > 0 {
			return hit
		}
	}
	return nil
}

// closest 找文件里和 oldString 最像的一段：行数相同的窗口里，按第 3 级规整后逐行相等的行数最多的，同分取最靠前的。
// 一行都对不上时，退一步找 oldString 第一个非空行出现在文件哪一行，从那一行开始。找不到时 lines 为 0。
func closest(text, oldString string) (start, lines, same int) {
	file := linesOf(text)
	old := linesOf(oldString)
	if len(old) == 0 || len(file) == 0 {
		return 0, 0, 0
	}
	norm := lenientLevels[len(lenientLevels)-1].norm
	normFile := make([]string, len(file))
	for i, line := range file {
		normFile[i] = norm(line)
	}
	if len(old) <= len(file) {
		want := make([]string, len(old))
		for i, line := range old {
			want[i] = norm(line)
		}
		best, bestStart := -1, 0
		for i := 0; i+len(old) <= len(file); i++ {
			n := 0
			for j := range want {
				if normFile[i+j] == want[j] {
					n++
				}
			}
			if n > best {
				best, bestStart = n, i
			}
		}
		if best > 0 {
			return bestStart, len(old), best
		}
	}
	for _, line := range old {
		key := norm(line)
		if key == "" {
			continue
		}
		for i := range file {
			if !strings.Contains(normFile[i], key) {
				continue
			}
			lines = min(len(old), len(file)-i)
			same = 0
			for j := 0; j < lines; j++ {
				if normFile[i+j] == norm(old[j]) {
					same++
				}
			}
			return i, lines, same
		}
	}
	return 0, 0, 0
}

// notFound 在精确匹配和放宽匹配都没找到时给出错误：附上文件里最接近的一段原文（格式和 read_file 一样），
// 模型照抄这段再试一次，比先 read_file 少一轮。
func notFound(path, text, oldString string) error {
	start, n, same := closest(text, oldString)
	base := fmt.Sprintf("old_string 在 %s 里没有找到；检查空格、缩进与换行是否和 read_file 输出一致，并且不要带行号前缀", path)
	if n == 0 {
		return errors.New(base + "。先用 read_file 看一下这个文件现在的内容")
	}
	file := linesOf(text)
	var b strings.Builder
	span := fmt.Sprintf("第 %d–%d 行", start+1, start+n)
	if n == 1 {
		span = fmt.Sprintf("第 %d 行", start+1)
	}
	fmt.Fprintf(&b, "%s。文件里最接近的是%s（%d 行里有 %d 行对得上）：\n", base, span, n, same)
	for i := start; i < start+n && i < start+12; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, file[i])
	}
	b.WriteString("照这段原文改 old_string 再试。")
	return errors.New(b.String())
}
