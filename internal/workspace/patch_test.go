package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func patchArgs(s string) string {
	b, _ := json.Marshal(map[string]string{"input": s})
	return string(b)
}

// 一份补丁里新建、修改（多段、末尾段、宽松匹配）、删除、改名一起做；对不上时一个文件都不动；撤销全部还原。
func TestApplyPatch(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) { os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644) }
	write("a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	write("gone.txt", "old\n")
	write("mv.txt", "x\ny\n")
	SnapshotDir, SnapshotTask, changes = filepath.Join(t.TempDir(), "snap"), "t", nil
	defer func() { SnapshotDir, SnapshotTask, changes, seen = "", "", nil, map[string]string{} }()
	if err := Open(dir); err != nil {
		t.Fatal(err)
	}
	update := "*** Begin Patch\n*** Update File: a.go\n@@\n-\treturn 1\n+\treturn 10\n*** End Patch"
	if _, err := Run(nil, "apply_patch", patchArgs(update)); err == nil || !strings.Contains(err.Error(), "read_file") {
		t.Fatalf("没读过就改应被拒绝：%v", err)
	}
	for _, f := range []string{"a.go", "gone.txt", "mv.txt"} {
		if _, err := Run(nil, "read_file", `{"path":"`+f+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	bad := "*** Begin Patch\n*** Add File: new/n.txt\n+hello\n*** Update File: a.go\n@@\n-\treturn 99\n+\treturn 3\n*** End Patch"
	if _, err := Run(nil, "apply_patch", patchArgs(bad)); err == nil {
		t.Fatal("第二个文件对不上时应报错")
	}
	if _, err := os.Stat(filepath.Join(dir, "new/n.txt")); err == nil {
		t.Fatal("补丁失败时不应新建任何文件")
	}
	good := "*** Begin Patch\n*** Add File: new/n.txt\n+hello\n*** Update File: a.go\n@@ func A() int {\n-\treturn 1\n+\treturn 11\n@@ func B() int {\n-  return 2\n+\treturn 22\n }\n*** End of File\n*** Delete File: gone.txt\n*** Update File: mv.txt\n*** Move to: moved/mv2.txt\n x\n-y\n+z\n*** End Patch"
	result, err := Run(nil, "apply_patch", patchArgs(good))
	if err != nil {
		t.Fatal(err)
	}
	if m := result.(map[string]any); len(m["files"].([]map[string]any)) != 5 || !strings.Contains(m["matched"].(string), "第 2 段") {
		t.Errorf("结果：%v", m)
	}
	read := func(name string) string { b, _ := os.ReadFile(filepath.Join(dir, name)); return string(b) }
	if got := read("a.go"); got != "package a\n\nfunc A() int {\n\treturn 11\n}\n\nfunc B() int {\n\treturn 22\n}\n" {
		t.Errorf("a.go = %q", got)
	}
	if read("moved/mv2.txt") != "x\nz\n" || read("new/n.txt") != "hello\n" {
		t.Errorf("改名或新建不对")
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); err == nil {
		t.Error("gone.txt 应已删除")
	}
	plan, err := PlanRewind([]string{SnapshotDir})
	if err != nil || len(plan.Conflicts) > 0 {
		t.Fatalf("撤销计划：%+v %v", plan, err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if read("gone.txt") != "old\n" || read("mv.txt") != "x\ny\n" || !strings.Contains(read("a.go"), "return 1\n") {
		t.Error("撤销后应还原")
	}
	for _, f := range []string{"moved/mv2.txt", "new/n.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("撤销后 %s 应删除", f)
		}
	}
}

func TestParsePatchErrors(t *testing.T) {
	for _, input := range []string{
		"*** Update File: a\n@@\n-x\n+y",
		"*** Begin Patch\n*** End Patch",
		"*** Begin Patch\n*** Add File: a\nno plus\n*** End Patch",
		"*** Begin Patch\n*** Update File: a\n@@\n?x\n*** End Patch",
		"*** Begin Patch\nhello\n*** End Patch",
	} {
		if _, err := parsePatch(input); err == nil {
			t.Errorf("应报错：%q", input)
		}
	}
}

// 一个文件一份补丁，返回应用后的内容和错误。
func patched(t *testing.T, content, body string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte(content), 0o644)
	seen = map[string]string{}
	if err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(nil, "read_file", `{"path":"f.txt"}`); err != nil {
		t.Fatal(err)
	}
	_, err := Run(nil, "apply_patch", patchArgs("*** Begin Patch\n*** Update File: f.txt\n"+body+"\n*** End Patch"))
	data, _ := os.ReadFile(path)
	return string(data), err
}

func TestPatchKeepsCRLF(t *testing.T) {
	got, err := patched(t, "a\r\nb\r\nc\r\n", "@@\n a\n-b\n+B\n+B2\n c")
	if err != nil || got != "a\r\nB\r\nB2\r\nc\r\n" {
		t.Fatalf("%q %v", got, err)
	}
	// 混用换行的文件：没碰到的行原样保留。
	got, err = patched(t, "a\r\nb\nc\n", "@@\n-b\n+B")
	if err != nil || got != "a\r\nB\nc\n" {
		t.Fatalf("混用：%q %v", got, err)
	}
}

func TestPatchInsertAfterHeader(t *testing.T) {
	got, err := patched(t, "func a() {\n}\n\nfunc b() {\n}\n", "@@ func a() {\n+\t// a")
	if err != nil || got != "func a() {\n\t// a\n}\n\nfunc b() {\n}\n" {
		t.Fatalf("%q %v", got, err)
	}
	// 没有定位行的只加不删仍然加在文件末尾。
	got, err = patched(t, "x\n", "@@\n+y")
	if err != nil || got != "x\ny\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestPatchRejectsAmbiguous(t *testing.T) {
	const content = "x\nkeep\nx\nkeep\n"
	got, err := patched(t, content, "@@\n x\n-keep\n+CHANGED")
	if err == nil || !strings.Contains(err.Error(), "不止一处") || got != content {
		t.Fatalf("不唯一应被拒绝：%q %v", got, err)
	}
	// 有定位行：取它后面的第一处。
	got, err = patched(t, "a:\nv\nb:\nv\n", "@@ b:\n-v\n+V")
	if err != nil || got != "a:\nv\nb:\nV\n" {
		t.Fatalf("定位行：%q %v", got, err)
	}
	// 精确匹配唯一时，只差空白的另一处不算重复。
	got, err = patched(t, "\tv\n  v\n", "@@\n-\tv\n+w")
	if err != nil || got != "w\n  v\n" {
		t.Fatalf("精确唯一：%q %v", got, err)
	}
}

// 文件结尾的状态不变：没有换行的还是没有，末尾的空行还在。
func TestPatchKeepsFileEnd(t *testing.T) {
	got, err := patched(t, "a\nb", "@@\n-a\n+A")
	if err != nil || got != "A\nb" {
		t.Fatalf("没有结尾换行：%q %v", got, err)
	}
	got, err = patched(t, "a\nb\n\n\n", "@@\n-a\n+A")
	if err != nil || got != "A\nb\n\n\n" {
		t.Fatalf("末尾空行：%q %v", got, err)
	}
	got, err = patched(t, "a\r\nb", "@@\n-a\n+A")
	if err != nil || got != "A\r\nb" {
		t.Fatalf("CRLF 没有结尾换行：%q %v", got, err)
	}
}

// 补丁自己的路径互相冲突时一个文件也不写。
func TestPatchPathConflict(t *testing.T) {
	dir := t.TempDir()
	if err := Open(dir); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"*** Add File: a\n+1\n*** Add File: a/b\n+2", "*** Add File: a/b\n+2\n*** Add File: a\n+1"} {
		_, err := Run(nil, "apply_patch", patchArgs("*** Begin Patch\n"+body+"\n*** End Patch"))
		if err == nil || !strings.Contains(err.Error(), "冲突") {
			t.Fatalf("应报冲突：%v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "a")); err == nil {
			t.Fatal("冲突时不应写任何文件")
		}
	}
}

// 改名保留权限；删除和改名撤销后，原文件的权限也回来。
func TestPatchKeepsMode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "del.sh"), []byte("#!/bin/sh\n"), 0o750)
	SnapshotDir, SnapshotTask, changes = filepath.Join(t.TempDir(), "snap"), "t", nil
	defer func() { SnapshotDir, SnapshotTask, changes, seen = "", "", nil, map[string]string{} }()
	if err := Open(dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"run.sh", "del.sh"} {
		if _, err := Run(nil, "read_file", `{"path":"`+f+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	patch := "*** Begin Patch\n*** Update File: run.sh\n*** Move to: bin/run2.sh\n*** Delete File: del.sh\n*** End Patch"
	if _, err := Run(nil, "apply_patch", patchArgs(patch)); err != nil {
		t.Fatal(err)
	}
	mode := func(name string) os.FileMode {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	if got := mode("bin/run2.sh"); got != 0o755 {
		t.Errorf("改名后权限 %o", got)
	}
	plan, err := PlanRewind([]string{SnapshotDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if got := mode("run.sh"); got != 0o755 {
		t.Errorf("撤销后 run.sh 权限 %o", got)
	}
	if got := mode("del.sh"); got != 0o750 {
		t.Errorf("撤销后 del.sh 权限 %o", got)
	}
}
