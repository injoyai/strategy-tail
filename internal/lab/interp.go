package lab

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/script"
	"github.com/traefik/yaegi/interp"
)

// LoadScript 保留 Lab 的脚本加载入口，执行与超时控制由 script 包统一负责。
func LoadScript(src string) ([]core.Variant, error) {
	return script.LoadScript(src)
}

// CheckScript 仅编译校验，不执行脚本初始化。
func CheckScript(src string) error {
	return script.CheckScript(src)
}

// ProjectSymbols 保留原符号表入口，注册内容由 script 包维护。
func ProjectSymbols() interp.Exports {
	return script.ProjectSymbols()
}
