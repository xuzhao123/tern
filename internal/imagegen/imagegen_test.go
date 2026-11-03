package imagegen

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// fake 按宽高画一张纯色 PNG；prompt 里有“失败”的那项返回错误。
type fake struct{}

func (fake) Name() string { return "fake" }
func (fake) Generate(_ context.Context, req Request) ([]byte, error) {
	if strings.Contains(req.Prompt, "失败") {
		return nil, errors.New("后端拒绝")
	}
	img := image.NewRGBA(image.Rect(0, 0, req.Width, req.Height))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(0, 0, color.Black)
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	return buf.Bytes(), err
}

func TestRun(t *testing.T) {
	t.Chdir(t.TempDir()) // 附件目录相对工作目录（tenant.Here）
	Active = fake{}
	defer func() { Active = nil }()

	out, err := Run(context.Background(), `{"request_list":[{"prompt":"横向结构图","width":512,"height":384},{"prompt":"这项会失败","width":512,"height":512},{"prompt":"方形关系图","width":256,"height":256}]}`)
	if err != nil {
		t.Fatal(err)
	}
	result := out.(Result)
	if len(result.Images) != 2 || len(result.Failed) != 1 || result.Failed[0].Index != 2 {
		t.Fatalf("结果不对：%+v", result)
	}
	if img := result.Images[0]; !strings.HasPrefix(img.Ref, "attachment://") || img.Path != "image_gen #1" || img.Width != 512 || img.Height != 384 {
		t.Fatalf("第一张图不对：%+v", img)
	}
	if result.Images[1].Path != "image_gen #3" {
		t.Fatalf("成功项的序号不对：%+v", result.Images[1])
	}

	if _, err := Run(context.Background(), `{"request_list":[{"prompt":"都失败","width":512,"height":512}]}`); err == nil {
		t.Fatal("全部失败时应返回错误")
	}
	for _, bad := range []string{`{"request_list":[]}`, `{"request_list":[{"prompt":"","width":512,"height":512}]}`, `{"request_list":[{"prompt":"x","width":100,"height":512}]}`} {
		if _, err := Run(context.Background(), bad); err == nil {
			t.Errorf("应拒绝：%s", bad)
		}
	}
}

func TestRunWithoutBackend(t *testing.T) {
	Active = nil
	if _, err := Run(context.Background(), `{"request_list":[{"prompt":"x","width":512,"height":512}]}`); err == nil {
		t.Fatal("没有后端时应返回错误")
	}
}
