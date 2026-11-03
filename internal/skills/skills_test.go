package skills

import (
	"io/fs"
	"path"
	"regexp"
	"testing"
)

// 内置 skill 正文里写到的 references/、examples/ 路径都要能经 load_skill 读到：迁 skill 时漏拷、改名都会在这里暴露。
var skillFilePattern = regexp.MustCompile(`(?:references|examples)/[A-Za-z0-9_./-]+\.md`)

func TestBuiltinReferencesLoad(t *testing.T) {
	Index = Scan(t.TempDir(), nil)
	defer func() { Index = nil }()
	for _, name := range BuiltinNames() {
		root := builtinDir + "/" + name
		fs.WalkDir(builtinFS, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || path.Ext(p) != ".md" {
				return err
			}
			data, _ := fs.ReadFile(builtinFS, p)
			for _, file := range skillFilePattern.FindAllString(string(data), -1) {
				result, err := Load(name, file)
				if err != nil {
					t.Errorf("%s 引用 %s：%v", p, file, err)
					continue
				}
				if m := result.(map[string]any); m["error"] != nil {
					t.Errorf("%s 引用 %s：%v", p, file, m["message"])
				}
			}
			return nil
		})
	}
}
