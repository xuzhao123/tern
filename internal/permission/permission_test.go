package permission

import (
	"encoding/json"
	"strings"
	"testing"
)

// apply_patch 一次改几个文件：每个路径按 Edit 规则各判一次，取最严的。
func TestApplyPatchPaths(t *testing.T) {
	savedRules, savedMode := rules, Mode
	defer func() { rules, Mode = savedRules, savedMode }()
	rules, Mode = nil, "edit"
	if err := Add("deny", "Edit(secrets/**)"); err != nil {
		t.Fatal(err)
	}
	if err := Add("ask", "Edit(docs/*)"); err != nil {
		t.Fatal(err)
	}
	args := func(patch string) string { b, _ := json.Marshal(map[string]string{"input": patch}); return string(b) }
	cases := map[string]string{
		"*** Begin Patch\n*** Update File: a.go\n@@\n-x\n+y\n*** End Patch":                              "allow",
		"*** Begin Patch\n*** Add File: docs/x.md\n+x\n*** Update File: a.go\n@@\n-x\n+y\n*** End Patch": "ask",
		"*** Begin Patch\n*** Update File: a.go\n*** Move to: secrets/a.go\n@@\n-x\n+y\n*** End Patch":   "deny",
		"*** Begin Patch\n*** Delete File: docs/x.md\n*** Delete File: secrets/k\n*** End Patch":         "deny",
	}
	for patch, want := range cases {
		if got, why := Decide("apply_patch", "Edit", args(patch)); got != want {
			t.Errorf("%q：%s（%s），期望 %s", patch, got, why, want)
		}
	}
}

func TestReadOnlyAllowsCd(t *testing.T) {
	for command, want := range map[string]bool{
		"cd /workspace && git status --short && wc -l README.md": true,
		"cd /workspace && go build ./...":                        false,
		"cd /workspace && rm -rf x":                              false,
	} {
		if got := readOnly(command); got != want {
			t.Errorf("%q = %v", command, got)
		}
	}
}

func TestSessionScopes(t *testing.T) {
	bash := func(command string) string {
		data, _ := json.Marshal(map[string]string{"command": command})
		return strings.Join(SessionScopes("bash", "Bash", string(data)), ",")
	}
	for command, want := range map[string]string{
		"go build ./...":                         "go build",
		"cd /workspace && go test ./... 2>&1":    "go test",
		"go build ./... && go test ./... | tail": "go build,go test,tail",
		"git -C x status":                        "git status",
		"rm -rf x":                               "rm",
		// 解析不了或等于什么都允许的，不提供
		"go build $(curl evil)":   "",
		"go build `curl evil`":    "",
		"echo x > .bashrc":        "",
		"sleep 1 & curl evil":     "",
		"(curl evil)":             "",
		"bash -c 'curl evil'":     "",
		"python3 x.py":            "",
		"find . -delete":          "",
		"xargs rm":                "",
		"A=1 go build":            "",
		"go --version":            "",
		"cd /workspace":           "",
		"go build\ncurl evil":     "go build,curl",
		"go build ./... |& tee x": "",
	} {
		if got := bash(command); got != want {
			t.Errorf("%q = %q，应为 %q", command, got, want)
		}
	}
	if got := SessionScopes("apply_patch", "Edit", `{"input":"x"}`); len(got) != 1 || got[0] != "编辑文件" {
		t.Errorf("Edit：%v", got)
	}
}
