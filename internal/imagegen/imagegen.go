// Package imagegen 是 image_gen 工具的壳子：参数校验、并发调用后端、把生成的图存成附件。
// 真正调用哪家图片生成 API 由 Backend 决定；Active 为 nil（还没接后端）时 main 不提供这个工具，
// visualization skill 按正文的降级规则改用 HTML/SVG 示意图。设计见 spec/archive/visualization-full.md 第二节（三）。
package imagegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/xuzhao123/tern/internal/llm"
)

// Request 是 request_list 里的一项，字段照 visualization skill 的契约（references/generated-tool-contracts.md）。
type Request struct {
	Prompt string `json:"prompt"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Backend 生成一张图，返回图片字节（PNG、JPEG、GIF、WebP 之一）。实现要自己处理鉴权、超时与重试；
// 返回的错误原样写进工具结果给模型看，不要带密钥。
type Backend interface {
	Name() string
	Generate(ctx context.Context, req Request) ([]byte, error)
}

// Active 是当前使用的后端；nil 表示没配置。接后端时在 main 里按配置赋值，并在 Tools 里加上 Definition。
var Active Backend

const (
	maxRequests = 6
	minSide     = 256
	maxSide     = 4096
)

var Definition = map[string]any{
	"name":        "image_gen",
	"description": "生成 1 到 6 张图片（解释性知识配图）。每项给中文 prompt 与宽高；生成的图存成附件，结果里每张图有编号 [Image #N]，回答正文用 ![说明](tern-image:N) 显示。部分失败时只重试失败项。",
	"parameters": map[string]any{"type": "object", "required": []string{"request_list"}, "properties": map[string]any{
		"request_list": map[string]any{"type": "array", "minItems": 1, "maxItems": maxRequests, "items": map[string]any{
			"type": "object", "required": []string{"prompt", "width", "height"}, "properties": map[string]any{
				"prompt": map[string]string{"type": "string", "description": "完整中文生图 Prompt；图中可见文字用中文引号“”标注"},
				"width":  map[string]any{"type": "integer", "minimum": minSide, "maximum": maxSide},
				"height": map[string]any{"type": "integer", "minimum": minSide, "maximum": maxSide},
			}}},
	}},
}

// Failure 是没生成出来的一项：Index 是 request_list 里的序号（从 1 开始）。
type Failure struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

// Result 是工具结果。Images 的编号（Label）由 agent 主循环接着对话里已有的图往后分配，和 view_image 同一套；
// Path 写成 image_gen #序号，模型据此对应到 request_list 里的哪一项。
type Result struct {
	Images []llm.Image `json:"images"`
	Failed []Failure   `json:"failed,omitempty"`
}

// Run 执行一次 image_gen：各项并发生成，成功的存成附件，失败的逐项报告；全部失败才返回错误。
func Run(ctx context.Context, arguments string) (any, error) {
	if Active == nil {
		return nil, llm.Permanent(errors.New("没有配置图片生成后端"))
	}
	var args struct {
		RequestList []Request `json:"request_list"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil, llm.Permanent(fmt.Errorf("参数类型不对：%v", err))
	}
	if n := len(args.RequestList); n < 1 || n > maxRequests {
		return nil, llm.Permanent(fmt.Errorf("request_list 要有 1 到 %d 项，现在是 %d 项", maxRequests, n))
	}
	for i, req := range args.RequestList {
		if strings.TrimSpace(req.Prompt) == "" {
			return nil, llm.Permanent(fmt.Errorf("第 %d 项缺少 prompt", i+1))
		}
		if req.Width < minSide || req.Width > maxSide || req.Height < minSide || req.Height > maxSide {
			return nil, llm.Permanent(fmt.Errorf("第 %d 项的宽高 %d×%d 超出范围（每边 %d 到 %d）", i+1, req.Width, req.Height, minSide, maxSide))
		}
	}
	images := make([]*llm.Image, len(args.RequestList))
	failures := make([]*Failure, len(args.RequestList))
	var wg sync.WaitGroup
	for i, req := range args.RequestList {
		wg.Add(1)
		go func() {
			defer wg.Done()
			img, err := generate(ctx, i+1, req)
			if err != nil {
				failures[i] = &Failure{Index: i + 1, Error: err.Error()}
				return
			}
			images[i] = &img
		}()
	}
	wg.Wait()
	result := Result{Images: []llm.Image{}}
	for i := range args.RequestList {
		if images[i] != nil {
			result.Images = append(result.Images, *images[i])
		} else {
			result.Failed = append(result.Failed, *failures[i])
		}
	}
	if len(result.Images) == 0 {
		messages := []string{}
		for _, f := range result.Failed {
			messages = append(messages, fmt.Sprintf("第 %d 项：%s", f.Index, f.Error))
		}
		return nil, errors.New("全部生成失败：" + strings.Join(messages, "；"))
	}
	return result, nil
}

func generate(ctx context.Context, index int, req Request) (llm.Image, error) {
	data, err := Active.Generate(ctx, req)
	if err != nil {
		return llm.Image{}, err
	}
	if len(data) > llm.MaxImageBytes {
		return llm.Image{}, fmt.Errorf("生成的图片超过 %d 字节", llm.MaxImageBytes)
	}
	// 和用户附图、view_image 走同一个 PrepareImage：校验格式、必要时缩小，按内容寻址存进附件目录。
	return llm.PrepareImage(data, fmt.Sprintf("image_gen #%d", index))
}
