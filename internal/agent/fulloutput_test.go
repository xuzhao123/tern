package agent

import "testing"

func TestFullOutput(t *testing.T) {
	cases := map[string]bool{
		`{"id":"c1","tool":"load_skill","status":"ok","result":{"content":"..."}}`: true,
		`{"id":"c2","tool":"read_file","status":"ok","result":"..."}`:              false,
		`不是 JSON`: false,
	}
	for content, want := range cases {
		if got := fullOutput(content); got != want {
			t.Errorf("fullOutput(%s) = %v，期望 %v", content, got, want)
		}
	}
}
