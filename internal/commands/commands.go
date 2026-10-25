// Package commands 是自定义斜杠命令：用户输入 /名字参数，harness 把对应的 Markdown 模板展开成本轮的提示词。
// 和 skill 的区别在于谁来触发：skill 由模型看索引后决定加载，斜杠命令由用户明确输入；内容都只是文字，不执行任何东西。
//
//	内置级  internal/commands/builtin/<名字>.md（编进程序，随程序发布）
//	个人级  commands/<名字>.md（harness 目录，所有工作区都能用）
//	项目级  <工作区>/.agents/commands/<名字>.md（随仓库共享）
//
// 同名时后面的整份覆盖前面的：项目级 › 个人级 › 内置级。
//
// 文件可以有 frontmatter（description、argument-hint），正文里 $ARGUMENTS 换成全部参数，$1…$9 换成按空格切开的第几个参数；
// 正文没有引用参数而用户给了参数时，参数追加在末尾（和 Claude Code 一致）。
package commands

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/xuzhao123/tern/internal/skills"
)

// 内置命令编进程序：改内置的就是改源码、重新编译，不往磁盘上写。
//
//go:embed builtin
var builtinFS embed.FS

// builtinDir 是嵌入文件系统里内置命令的目录，内置命令的 path 写成 builtin/<名字>.md。
const builtinDir = "builtin"

type Command struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argument_hint,omitempty"`
	Scope        string `json:"scope"` // builtin、user、project
	path         string
}

// 只有 /名字 后面是空白或结尾才算命令，所以 /home/dev/x 这样的路径不会被当成命令。
var invocation = regexp.MustCompile(`^/([a-z0-9][a-z0-9_-]*)(?:\s+([\s\S]*))?$`)

// IsInvocation 判断输入是不是 /名字 形式的命令调用：chat 用它决定把这一行当命令还是当一轮发给 agent。
func IsInvocation(input string) bool {
	return invocation.MatchString(strings.TrimSpace(input))
}

// builtinPaths 列出编进程序的内置命令文件。
func builtinPaths() []string {
	entries, _ := fs.ReadDir(builtinFS, builtinDir)
	paths := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			paths = append(paths, builtinDir+"/"+e.Name())
		}
	}
	return paths
}

// List 列出可用的命令：内置的、个人级、项目级，同名的后面覆盖前面的；workspace 为空时没有项目级。坏文件跳过，不影响其他命令。
func List(harness, workspace string) []Command {
	files := [][2]string{}
	for _, path := range builtinPaths() {
		files = append(files, [2]string{path, "builtin"})
	}
	dirs := [][2]string{{filepath.Join(harness, "commands"), "user"}}
	if workspace != "" {
		dirs = append(dirs, [2]string{filepath.Join(workspace, ".agents", "commands"), "project"})
	}
	for _, d := range dirs {
		paths, _ := filepath.Glob(filepath.Join(d[0], "*.md"))
		for _, path := range paths {
			files = append(files, [2]string{path, d[1]})
		}
	}
	found := map[string]Command{}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f[0]), ".md")
		if !invocation.MatchString("/" + name) {
			continue
		}
		c, _, err := read(f[0])
		if err != nil {
			continue
		}
		c.Name, c.Scope = name, f[1]
		found[name] = c
	}
	out := []Command{}
	for _, c := range found {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Command) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// read 读一个命令文件：内置的 path 以 builtin/ 开头，从嵌入的文件系统读；其余按磁盘路径读。
func read(path string) (Command, string, error) {
	var data []byte
	var err error
	if strings.HasPrefix(path, builtinDir+"/") {
		data, err = fs.ReadFile(builtinFS, path)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return Command{}, "", err
	}
	if len(data) > 32<<10 {
		return Command{}, "", errors.New("命令文件超过 32KB")
	}
	text := string(data)
	c := Command{path: path}
	if strings.HasPrefix(strings.TrimPrefix(text, "\ufeff"), "---") {
		fields, body, err := skills.ParseFrontmatter(text)
		if err != nil {
			return Command{}, "", err
		}
		c.Description, c.ArgumentHint, text = fields["description"], fields["argument-hint"], body
	}
	return c, strings.TrimSpace(text), nil
}

// Expand 处理用户输入：不是 /名字 开头的原样返回（name 为空）；是命令就返回展开后的提示词。
// 命令不存在时报错，不把“/拼错的名字”当普通问题发给模型。
func Expand(input, harness, workspace string) (prompt, name string, err error) {
	m := invocation.FindStringSubmatch(strings.TrimSpace(input))
	if m == nil {
		return input, "", nil
	}
	name, args := m[1], strings.TrimSpace(m[2])
	var command *Command
	all := List(harness, workspace)
	for i := range all {
		if all[i].Name == name {
			command = &all[i]
		}
	}
	if command == nil {
		names := []string{}
		for _, c := range all {
			names = append(names, "/"+c.Name)
		}
		return "", name, fmt.Errorf("没有 /%s 这个命令；可用的命令：%s", name, strings.Join(names, "、"))
	}
	_, body, err := read(command.path)
	if err != nil {
		return "", name, fmt.Errorf("命令 /%s 读取失败：%w", name, err)
	}
	fields := strings.Fields(args)
	used := strings.Contains(body, "$ARGUMENTS")
	body = strings.ReplaceAll(body, "$ARGUMENTS", args)
	for i := 9; i >= 1; i-- {
		placeholder := fmt.Sprintf("$%d", i)
		value := ""
		if i <= len(fields) {
			value = fields[i-1]
		}
		used = used || strings.Contains(body, placeholder)
		body = strings.ReplaceAll(body, placeholder, value)
	}
	if !used && args != "" {
		body += "\n\nARGUMENTS: " + args
	}
	return body, name, nil
}
