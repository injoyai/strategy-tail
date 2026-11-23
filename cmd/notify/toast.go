package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// toastScript 优先使用 Windows 通知中心，旧系统或调用失败时回退到托盘气泡。
const toastScript = `param(
    [Parameter(Mandatory = $true)][string]$Title,
    [Parameter(Mandatory = $true)][string]$Body
)
$ErrorActionPreference = 'Stop'
try {
    [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
    $template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
    $texts = $template.GetElementsByTagName('text')
    $texts.Item(0).AppendChild($template.CreateTextNode($Title)) | Out-Null
    $texts.Item(1).AppendChild($template.CreateTextNode($Body)) | Out-Null
    $toast = New-Object Windows.UI.Notifications.ToastNotification -ArgumentList $template
    $appId = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
    [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId).Show($toast)
} catch {
    Add-Type -AssemblyName System.Windows.Forms
    Add-Type -AssemblyName System.Drawing
    $notify = New-Object System.Windows.Forms.NotifyIcon
    $notify.Icon = [System.Drawing.SystemIcons]::Information
    $notify.Visible = $true
    $notify.ShowBalloonTip(5000, $Title, $Body, [System.Windows.Forms.ToolTipIcon]::Info)
    Start-Sleep -Seconds 6
    $notify.Dispose()
}
`

var (
	toastOnce sync.Once
	toastFile string
	toastErr  error
)

func toastScriptPath() (string, error) {
	toastOnce.Do(func() {
		toastFile = filepath.Join(os.TempDir(), "strategy-notify-toast.ps1")
		toastErr = os.WriteFile(toastFile, []byte(toastScript), 0600)
	})
	return toastFile, toastErr
}

func notifyDesktop(title, body string) {
	if runtime.GOOS != "windows" {
		return
	}
	if err := showToast(title, body); err != nil {
		log.Printf("桌面通知失败: %v", err)
	}
}

func showToast(title, body string) error {
	script, err := toastScriptPath()
	if err != nil {
		return err
	}
	cmd := exec.Command("powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-WindowStyle", "Hidden", "-File", script,
		"-Title", title, "-Body", body)
	cmd.SysProcAttr = hiddenSysProcAttr()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
