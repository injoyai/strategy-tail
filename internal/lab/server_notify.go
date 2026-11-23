package lab

import (
	"net/http"
	"os"
	"path/filepath"
)

// notifierCandidates 是 Windows 提醒客户端的查找位置：
// 容器镜像由 Dockerfile 复制到工作目录，本地开发使用 bin/ 下的交叉编译产物。
var notifierCandidates = []string{
	"strategy-notify.exe",
	filepath.Join("bin", "strategy-notify.exe"),
}

func notifierPath() string {
	for _, path := range notifierCandidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func (s *Server) handleNotifyDownload(w http.ResponseWriter, r *http.Request) {
	path := notifierPath()
	if path == "" {
		http.Error(w, "未找到 Windows 提醒客户端，请先构建 bin/strategy-notify.exe 或使用部署镜像", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="strategy-notify.exe"`)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}
