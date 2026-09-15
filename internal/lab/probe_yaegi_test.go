package lab

import (
	"testing"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

// 探针：验证 Yaegi 能否解释含中文标识符结构体与项目包的脚本（占位空符号表）。
func TestYaegiProbe(t *testing.T) {
	src := `package main

func Hello() string {
	阴线 := "收回"
	return "A" + 阴线
}
`
	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	v, err := i.Eval(src)
	if err != nil {
		t.Fatalf("eval 失败: %v", err)
	}
	rv, err := i.Eval("Hello()")
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	_ = v
	// Yaegi 求值结果可能直接是值或 *interface{}，两种都接受。
	switch p := rv.Interface().(type) {
	case string:
		if p != "A收回" {
			t.Fatalf("got %q", p)
		}
	case *interface{}:
		if got, _ := (*p).(string); got != "A收回" {
			t.Fatalf("got %q", got)
		}
	default:
		t.Fatalf("返回类型异常: %T", rv.Interface())
	}
}
