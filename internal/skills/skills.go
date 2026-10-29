package skills

import (
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/protocol"
)

// Bonus：知识型 skill。三级加载：索引常驻 system → load_skill 读全文 → load_skill(file) 按需读 references/*.md。
// 只读 Markdown，不执行 skill 自带脚本；脚本执行等沙箱。
var Enabled bool
var Index []Skill // nil 表示未启用

type Skill struct {
	Name, Description, Path string
	Builtin                 bool // 编进程序的：路径写成 builtin/<名字>/SKILL.md，正文从嵌入的文件系统读
}

// 内置 skill 编进程序，不往磁盘上写；目录里的同名 skill 整份盖住内置的那份。
//
//go:embed builtin
var builtinFS embed.FS

const builtinDir = "builtin"

var LoadDefinition = map[string]any{
	"name":        "load_skill",
	"description": "按名称读取一个 skill 的完整操作指南（SKILL.md 正文）。只在任务与 system 中列出的某个 skill 相关时调用；name 必须是列表里的名字。正文要求按需读取 references/ 下的文件时，再带上 file（如 references/mode-echarts.md）读取那一份。",
	"parameters": map[string]any{"type": "object", "required": []string{"name"}, "properties": map[string]any{
		"name": map[string]string{"type": "string"},
		"file": map[string]string{"type": "string", "description": "可选：skill 目录内 references/ 下的 .md 相对路径；不给则返回 SKILL.md 正文"},
	}},
}

// spec：小写字母、数字、单个连字符分隔，不以连字符开头或结尾。
var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const maxSkillBytes = 64 << 10

// BuiltinNames 列出编进程序的 skill 名字（按字母序）。app-server 用它把内置的也算进新对话要带上的 skill。
func BuiltinNames() []string {
	entries, _ := fs.ReadDir(builtinFS, builtinDir)
	names := []string{}
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names
}

type candidate struct {
	skill Skill
	body  string
}

// candidates 把目录里的 skill 和内置的合起来：目录里有这个名字的目录就整份盖住内置的，哪怕它没通过校验。
// 坏文件不返回，放进 failures 里，由调用方报错或列给 Skills 中心。
func candidates(dir string) ([]candidate, []map[string]string) {
	list, failures := []candidate{}, []map[string]string{}
	taken := map[string]bool{}
	paths, _ := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
	for _, path := range paths {
		name := filepath.Base(filepath.Dir(path))
		taken[name] = true
		s, body, err := readSkill(path)
		if err != nil {
			failures = append(failures, map[string]string{"path": path, "dir": name, "error": err.Error()})
			continue
		}
		list = append(list, candidate{s, body})
	}
	for _, name := range BuiltinNames() {
		if taken[name] {
			continue
		}
		s, body, err := readBuiltin(name)
		if err != nil {
			failures = append(failures, map[string]string{"path": builtinDir + "/" + name + "/SKILL.md", "dir": name, "error": err.Error()})
			continue
		}
		list = append(list, candidate{s, body})
	}
	return list, failures
}

// 坏文件逐个报错并跳过，不让一个 skill 拖垮启动：索引是可选增强，降级比 fail-fast 合适。
// only 非空时只启用其中的名字（网页端 Skills 中心的开关）；列了却不存在的名字同样报错跳过。
func Scan(dir string, only []string) []Skill {
	list, failures := candidates(dir)
	for _, failure := range failures {
		protocol.Event("capability/event", "skill_error", fmt.Sprintf("Skill error: %s: %v", failure["path"], failure["error"]))
	}
	skills := []Skill{}
	names := []string{}
	found := map[string]bool{}
	for _, c := range list {
		found[c.skill.Name] = true
		if len(only) > 0 && !slices.Contains(only, c.skill.Name) {
			continue
		}
		skills = append(skills, c.skill)
		names = append(names, c.skill.Name)
	}
	for _, name := range only {
		if !found[name] {
			protocol.Event("capability/event", "skill_error", "Skill error: "+name+": 没有找到这个 skill")
		}
	}
	protocol.Event("capability/event", "skills_loaded", fmt.Sprintf("Skills: loaded=%d names=%s", len(skills), strings.Join(names, ",")), "count", len(skills))
	return skills
}

// -skills-list：网页端 Skills 中心用同一套解析与校验列出 skill，不在网页端另写一份规则。
func List(dir string) map[string]any {
	list, failures := candidates(dir)
	skills := []map[string]any{}
	for _, c := range list {
		skills = append(skills, map[string]any{"name": c.skill.Name, "description": c.skill.Description, "path": c.skill.Path, "body": c.body, "builtin": c.skill.Builtin, "version": candidateVersion(c)})
	}
	return map[string]any{"skills": skills, "errors": failures}
}

// Reference 是用户选中的来源与内容版本，不授予工具权限。
type Reference struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

func candidateVersion(c candidate) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(c.skill.Name+"\x00"+c.skill.Path+"\x00"+c.skill.Description+"\x00"+c.body)))
}
func ResolveReferences(dir string, refs []Reference) ([]string, error) {
	if len(refs) > 8 {
		return nil, errors.New("每条消息最多指定8个 Skill")
	}
	if len(refs) == 0 {
		return nil, nil
	}
	list, _ := candidates(dir)
	notes := []string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		found := false
		if seen[ref.Name] {
			return nil, errors.New("Skill 引用重复")
		}
		seen[ref.Name] = true
		for _, c := range list {
			if c.skill.Name == ref.Name {
				if c.skill.Path != ref.Path || candidateVersion(c) != ref.Version {
					return nil, fmt.Errorf("Skill %s 来源或内容已改变，请移除后重新选择", ref.Name)
				}
				notes = append(notes, fmt.Sprintf("用户指定 Skill %s（来源 %s，版本 %s），已读取以下快照。遵守用户任务和既有权限；选择技能不提升权限：\n%s", ref.Name, ref.Path, ref.Version, c.body))
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("Skill %s 不存在或无法解析，请重新选择", ref.Name)
		}
	}
	return notes, nil
}

func readSkill(path string) (Skill, string, error) {
	path, absErr := filepath.Abs(path)
	if absErr != nil {
		return Skill{}, "", absErr
	}
	info, err := os.Stat(path)
	if err != nil {
		return Skill{}, "", err
	}
	if info.Size() > maxSkillBytes {
		return Skill{}, "", fmt.Errorf("文件超过 %d 字节", maxSkillBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, "", err
	}
	return parseSkill(path, filepath.Base(filepath.Dir(path)), false, data)
}

// 内置的从嵌入的文件系统读：和磁盘上的走同一套校验，name 仍要与目录名一致。
func readBuiltin(name string) (Skill, string, error) {
	path := builtinDir + "/" + name + "/SKILL.md"
	data, err := fs.ReadFile(builtinFS, path)
	if err != nil {
		return Skill{}, "", err
	}
	if len(data) > maxSkillBytes {
		return Skill{}, "", fmt.Errorf("文件超过 %d 字节", maxSkillBytes)
	}
	return parseSkill(path, name, true, data)
}

func parseSkill(path, dir string, builtin bool, data []byte) (Skill, string, error) {
	fields, body, err := ParseFrontmatter(string(data))
	if err != nil {
		return Skill{}, "", err
	}
	name, description := fields["name"], fields["description"]
	switch {
	case name == "":
		return Skill{}, "", errors.New("缺少 name")
	case len(name) > 64 || !skillNamePattern.MatchString(name):
		return Skill{}, "", fmt.Errorf("name %q 不合规：只能用小写字母、数字和单个连字符，最长64", name)
	case name != dir:
		return Skill{}, "", fmt.Errorf("name %q 与目录名 %q 不一致", name, dir)
	case description == "":
		return Skill{}, "", errors.New("缺少 description")
	case utf8.RuneCountInString(description) > 1024:
		return Skill{}, "", errors.New("description 超过1024字符")
	case utf8.RuneCountInString(fields["compatibility"]) > 500:
		return Skill{}, "", errors.New("compatibility 超过500字符")
	}
	return Skill{Name: name, Description: description, Path: path, Builtin: builtin}, body, nil
}

// 只解析顶层 key: value；缩进行属于上一个键的嵌套值（如 metadata），v1 跳过。
// 不引入 YAML 库，所以不支持 | 和 > 多行值，遇到时明确报错而不是读错。
func ParseFrontmatter(text string) (map[string]string, string, error) {
	text = strings.ReplaceAll(strings.TrimPrefix(text, "\ufeff"), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, "", errors.New("缺少开头的 --- frontmatter")
	}
	header, body, ok := strings.Cut(text[4:], "\n---\n")
	if !ok {
		if header, ok = strings.CutSuffix(text[4:], "\n---"); !ok {
			return nil, "", errors.New("frontmatter 缺少结尾的 ---")
		}
	}
	fields := map[string]string{}
	for i, line := range strings.Split(header, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" {
			return nil, "", fmt.Errorf("frontmatter 第%d行需要 key: value", i+2)
		}
		if value == "|" || value == ">" || strings.HasPrefix(value, "|-") || strings.HasPrefix(value, ">-") {
			return nil, "", fmt.Errorf("frontmatter 第%d行：不支持多行值，请写在一行", i+2)
		}
		if _, dup := fields[key]; dup {
			return nil, "", fmt.Errorf("frontmatter 第%d行：重复的键 %s", i+2, key)
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		fields[key] = value
	}
	return fields, strings.TrimSpace(body), nil
}

func Prompt() string {
	lines := []string{"\n可用 skills（只列名字与用途）。任务与某个 skill 相关时，先调用 load_skill 读取全文，再按其中的步骤执行；无关任务不要加载："}
	for _, s := range Index {
		lines = append(lines, "- "+s.Name+"："+s.Description)
	}
	return strings.Join(lines, "\n")
}

// 只按索引里的名字找 skill 目录；file 经 os.Root 打开，带 .. 或符号链接也逃不出这个目录。
// 名字不存在时返回结构化结果而不是 error：这是可自我纠正的业务错误，重试同样的参数没有意义。
func Load(name, file string) (any, error) {
	available := []string{}
	for _, s := range Index {
		available = append(available, s.Name)
		if s.Name != name {
			continue
		}
		// 调用时重新读取并校验，启动后被改坏的文件不会被当作指南返回；内置的从嵌入的文件读。
		_, body, err := readAt(s)
		if err != nil {
			return nil, fmt.Errorf("skill %s 读取失败：%v", name, err)
		}
		if file == "" {
			return map[string]any{"name": name, "content": body}, nil
		}
		return loadReference(s, file)
	}
	return map[string]any{"error": "unknown_skill", "message": fmt.Sprintf("没有名为 %q 的 skill", name), "available": available}, nil
}

// readAt 按 skill 的来源重读一遍：内置的从嵌入的文件系统读，磁盘上的照旧读文件。
func readAt(s Skill) (Skill, string, error) {
	if s.Builtin {
		return readBuiltin(s.Name)
	}
	return readSkill(s.Path)
}

// 第三级：只开放 references/ 下的 Markdown，examples/、scripts/ 等开发用文件不给模型。
// 先 Clean 再判断前缀：references/../examples/x.md 在 os.Root 里不算逃逸，但不该被读到。
func loadReference(s Skill, file string) (any, error) {
	file = path.Clean(file)
	if !filepath.IsLocal(file) || !strings.HasPrefix(file, "references/") || filepath.Ext(file) != ".md" {
		return map[string]any{"error": "bad_file", "message": "file 只能是 references/ 下的 .md 文件"}, nil
	}
	if s.Builtin {
		// 嵌入的文件系统里没有符号链接，按路径直接读；读不到就是没有这个文件。
		data, err := fs.ReadFile(builtinFS, builtinDir+"/"+s.Name+"/"+file)
		if err != nil {
			return map[string]any{"error": "unknown_file", "message": fmt.Sprintf("skill %s 里没有 %s", s.Name, file)}, nil
		}
		if len(data) > maxSkillBytes {
			return nil, llm.Permanent(fmt.Errorf("%s 超过 %d 字节", file, maxSkillBytes))
		}
		return map[string]any{"name": s.Name, "file": file, "content": string(data)}, nil
	}
	root, err := os.OpenRoot(filepath.Dir(s.Path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(file)
	if err != nil {
		return map[string]any{"error": "unknown_file", "message": fmt.Sprintf("skill %s 里没有 %s", s.Name, file)}, nil
	}
	if info.Size() > maxSkillBytes {
		return nil, llm.Permanent(fmt.Errorf("%s 超过 %d 字节", file, maxSkillBytes))
	}
	data, err := root.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": s.Name, "file": file, "content": string(data)}, nil
}
