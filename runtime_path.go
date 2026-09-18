package common

import (
	"os"
	"path/filepath"
)

const runtimeRootEnv = "STRATEGY_TAIL_ROOT"

var runtimeRoot = discoverRuntimeRoot()

// RuntimeRoot 返回配置、数据与输出相对路径的项目根目录。
// 优先使用 STRATEGY_TAIL_ROOT；否则从当前目录、可执行文件目录向上查找 go.mod。
// 找不到模块根时保留当前目录，兼容容器内 /app 作为工作目录的部署方式。
func RuntimeRoot() string {
	return runtimeRoot
}

// ResolveRuntimePath 将相对运行时路径固定解析到项目根目录。
// 绝对路径保持不变，便于通过配置挂载仓库外的大型数据目录。
func ResolveRuntimePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(runtimeRoot, path)
}

func discoverRuntimeRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	executableDir := ""
	if executable, err := os.Executable(); err == nil {
		executableDir = filepath.Dir(executable)
	}
	return selectRuntimeRoot(cwd, executableDir, os.Getenv(runtimeRootEnv))
}

func selectRuntimeRoot(cwd, executableDir, override string) string {
	if override != "" {
		if abs, err := filepath.Abs(override); err == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(override)
	}
	if root, ok := findModuleRoot(cwd); ok {
		return root
	}
	if executableDir != "" {
		if root, ok := findModuleRoot(executableDir); ok {
			return root
		}
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(cwd)
}

func findModuleRoot(start string) (string, bool) {
	if start == "" {
		return "", false
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		info, statErr := os.Stat(filepath.Join(dir, "go.mod"))
		if statErr == nil && !info.IsDir() {
			return filepath.Clean(dir), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// withRuntimeRoot 仅在包初始化的单线程阶段切换工作目录，让仍依赖 cwd 的
// 上游 TDX 构造器从项目 data/config 目录初始化；返回前始终恢复调用方目录。
func withRuntimeRoot(fn func() error) (err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if filepath.Clean(cwd) == runtimeRoot {
		return fn()
	}
	if err := os.Chdir(runtimeRoot); err != nil {
		return err
	}
	defer func() {
		if restoreErr := os.Chdir(cwd); err == nil && restoreErr != nil {
			err = restoreErr
		}
	}()
	return fn()
}
