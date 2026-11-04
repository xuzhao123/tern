package permission

import (
	"encoding/json"
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
