package lab

import (
	"fmt"
	"reflect"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"github.com/injoyai/strategy-tail/core"
)

// interp.go Yaegi 解释器封装：加载策略脚本 → []core.Variant。
//
// 脚本契约（见 docs/superpowers/specs/2026-09-05-strategy-lab-design.md §3.1）：
//
//	package main
//	import "github.com/injoyai/strategy-tail/core"
//	func Strategy() []core.Variant { ... }
//
// Yaegi 无法直接 import 编译期项目包，通过 symbols.go 注册 binary 包装符号；
// 解释器构造的 Buyer 实际持有编译侧类型实例（已由 probe_yaegi_binary_test.go 验证），
// 回测引擎可无缝调用。

// LoadScript 解析脚本源码并调用 Strategy() 提取变体列表。
// 语法/类型错误在此阶段暴露；解释器构造的 Buyer 可直接被编译侧引擎调用。
func LoadScript(src string) ([]core.Variant, error) {
	i, err := newInterp()
	if err != nil {
		return nil, err
	}
	if _, err := i.Eval(src); err != nil {
		return nil, fmt.Errorf("脚本编译失败: %w", err)
	}
	v, err := i.Eval("Strategy()")
	if err != nil {
		return nil, fmt.Errorf("调用 Strategy() 失败: %w", err)
	}
	return extractVariants(v)
}

// CheckScript 仅做编译期校验（干跑），不执行 Strategy()。
// 返回 nil 表示脚本语法与类型检查通过。
func CheckScript(src string) error {
	i, err := newInterp()
	if err != nil {
		return err
	}
	if _, err := i.Eval(src); err != nil {
		return fmt.Errorf("脚本编译失败: %w", err)
	}
	return nil
}

// newInterp 创建带项目符号表的解释器实例。
func newInterp() (*interp.Interpreter, error) {
	i := interp.New(interp.Options{GoPath: "D:\\GOPATH"})
	if err := i.Use(stdlib.Symbols); err != nil {
		return nil, fmt.Errorf("加载标准库符号失败: %w", err)
	}
	if err := i.Use(ProjectSymbols()); err != nil {
		return nil, fmt.Errorf("加载项目符号失败: %w", err)
	}
	return i, nil
}

// extractVariants 从 Yaegi 求值结果中提取 []core.Variant。
// Yaegi 返回值可能是裸值或 *interface{} 包装，两种都处理。
func extractVariants(v reflect.Value) ([]core.Variant, error) {
	switch p := v.Interface().(type) {
	case []core.Variant:
		return p, nil
	case *interface{}:
		switch q := (*p).(type) {
		case []core.Variant:
			return q, nil
		default:
			return nil, fmt.Errorf("Strategy() 返回类型异常: %T", *p)
		}
	default:
		return nil, fmt.Errorf("Strategy() 返回类型异常: %T", v.Interface())
	}
}
