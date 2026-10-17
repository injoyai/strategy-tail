package lab

import (
	"context"
	"fmt"
	"reflect"
	"time"

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

// 超时后保留令牌，直到重启；EvalWithContext 返回不代表底层求值已经退出。
var scriptLoadGate = make(chan struct{}, 1)

// scriptLoadTimeout 单次脚本加载（排队 + 编译 + Strategy()）的超时上限，
// 防止死循环脚本长期占用 /api/run 等请求链路。
const scriptLoadTimeout = 10 * time.Second

// LoadScript 解析脚本并调用 Strategy；编译侧 Buyer 可直接交给回测引擎。
// shortcut: Yaegi 未提供底层求值退出通知，执行超时后暂停脚本加载；需自动恢复时改用可确认退出的执行方式。
func LoadScript(src string) ([]core.Variant, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scriptLoadTimeout)
	defer cancel()
	return loadScript(ctx, src, scriptLoadGate)
}

func loadScript(ctx context.Context, src string, gate chan struct{}) ([]core.Variant, error) {
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("等待脚本加载超时；如上次脚本加载已超时，请重启服务")
	}

	type loadResult struct {
		variants []core.Variant
		err      error
	}
	timeoutErr := fmt.Errorf("脚本加载超时；为避免残留执行累积，后续脚本加载已暂停，请重启服务")
	done := make(chan loadResult, 1)
	go func() {
		release := true
		defer func() {
			if release {
				<-gate
			}
		}()
		defer func() {
			// 脚本触发的 panic 转为错误返回，不让它传播击穿调用方。
			if p := recover(); p != nil {
				done <- loadResult{err: fmt.Errorf("脚本执行异常: %v", p)}
			}
		}()
		i, err := newInterp()
		if err != nil {
			done <- loadResult{err: err}
			return
		}
		if _, err := i.EvalWithContext(ctx, src); err != nil {
			if ctx.Err() != nil {
				release = false
				done <- loadResult{err: timeoutErr}
				return
			}
			done <- loadResult{err: fmt.Errorf("脚本编译失败: %w", err)}
			return
		}
		v, err := i.EvalWithContext(ctx, "Strategy()")
		if err != nil {
			if ctx.Err() != nil {
				release = false
				done <- loadResult{err: timeoutErr}
				return
			}
			done <- loadResult{err: fmt.Errorf("调用 Strategy() 失败: %w", err)}
			return
		}
		variants, err := extractVariants(v)
		done <- loadResult{variants: variants, err: err}
	}()

	select {
	case res := <-done:
		return res.variants, res.err
	case <-ctx.Done():
		return nil, timeoutErr
	}
}

// CheckScript 仅做编译期校验，不执行全局初始化、init() 或 Strategy()。
// 返回 nil 表示脚本语法与类型检查通过。
func CheckScript(src string) error {
	i, err := newInterp()
	if err != nil {
		return err
	}
	if _, err := i.Compile(src); err != nil {
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
