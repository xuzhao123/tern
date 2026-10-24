package mcp

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
)

// 内置 MCP 计算器的工具描述。
const calculatorDescription = "计算四则运算、括号、sqrt 和 floor，返回 result 数值。不支持乘方（^、**、pow 都不行），乘方请写成连乘，如 2*2*2。"

// 只解释数学 AST；不会执行表达式里出现的 Go 代码。
func calculate(expression string) (float64, error) {
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
