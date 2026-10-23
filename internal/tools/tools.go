package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
	"time"

	"github.com/xuzhao123/tern/internal/llm"
)

var Definitions = []map[string]any{
	{"name": "calculator", "description": "计算四则运算、括号、sqrt 和 floor，返回 result 数值。不支持乘方（^、**、pow 都不行），乘方请写成连乘，如 2*2*2。", "parameters": llm.Parameters("expression")},
	{"name": "get_current_datetime", "description": "返回 Asia/Shanghai 当前日期、时间和中文星期，无需参数。", "parameters": llm.Parameters("")},
}

// Run 执行内置工具；未知名字返回错误。
// 调用方（agent）负责 panic 恢复、参数是否为 JSON 对象的检查与重试。
func Run(ctx context.Context, name, arguments string) (any, error) {
	switch name {
	case "calculator":
		var args struct {
			Expression string `json:"expression"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return nil, llm.Permanent(errors.New("expression 需要是字符串"))
		}
		value, err := Calculate(args.Expression)
		if err != nil {
			return nil, llm.Permanent(err) // 表达式本身有问题，重算也一样
		}
		return map[string]any{"result": value}, nil
	case "get_current_datetime":
		now := time.Now().In(shanghai)
		weekdays := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
		return map[string]any{"datetime": now.Format(time.RFC3339), "date": now.Format("2006-01-02"), "weekday": weekdays[now.Weekday()]}, nil
	default:
		return nil, llm.Permanent(fmt.Errorf("未知工具：%s", name))
	}
}

// 只解释数学 AST；不会执行表达式里出现的 Go 代码。
func Calculate(expression string) (float64, error) {
	if expression == "" || len(expression) > 1024 {
		return 0, errors.New("expression 需要是1到1024字节的数学表达式")
	}
	node, err := parser.ParseExpr(expression)
	if err != nil {
		return 0, errors.New("无法解析数学表达式")
	}
	value, err := evaluate(node)
	if err == nil && (math.IsNaN(value) || math.IsInf(value, 0)) {
		return 0, errors.New("结果不是有限数值")
	}
	return value, err
}

func evaluate(node ast.Expr) (float64, error) {
	switch n := node.(type) {
	case *ast.BasicLit:
		if n.Kind == token.INT || n.Kind == token.FLOAT {
			return strconv.ParseFloat(n.Value, 64)
		}
	case *ast.ParenExpr:
		return evaluate(n.X)
	case *ast.UnaryExpr:
		value, err := evaluate(n.X)
		if err != nil {
			return 0, err
		}
		if n.Op == token.ADD {
			return value, nil
		}
		if n.Op == token.SUB {
			return -value, nil
		}
	case *ast.BinaryExpr:
		left, err := evaluate(n.X)
		if err != nil {
			return 0, err
		}
		right, err := evaluate(n.Y)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, errors.New("除数不能为0")
			}
			return left / right, nil
		}
	case *ast.CallExpr:
		name, ok := n.Fun.(*ast.Ident)
		if !ok || len(n.Args) != 1 || n.Ellipsis.IsValid() {
			break
		}
		value, err := evaluate(n.Args[0])
		if err != nil {
			return 0, err
		}
		if name.Name == "sqrt" {
			return math.Sqrt(value), nil
		}
		if name.Name == "floor" {
			return math.Floor(value), nil
		}
	}
	return 0, errors.New("只支持数值、四则运算、括号、sqrt 和 floor；乘方请写成连乘")
}

// 与记忆模块相同的固定东八区；各自定义，工具包不依赖记忆包。
var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)
