package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const dialogScript = `param(
    [string]$Initial = '',
    [Parameter(Mandatory = $true)][string]$OutFile
)
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
$form = New-Object System.Windows.Forms.Form
$form.Text = '策略卖点提醒 · 服务器地址'
$form.StartPosition = 'CenterScreen'
$form.FormBorderStyle = 'FixedDialog'
$form.TopMost = $true
$form.MaximizeBox = $false
$form.MinimizeBox = $false
$form.ClientSize = New-Object System.Drawing.Size(460, 150)
$label = New-Object System.Windows.Forms.Label
$label.Text = '家里 Lab 地址（例如 http://192.168.1.106）'
$label.SetBounds(20, 18, 420, 20)
$box = New-Object System.Windows.Forms.TextBox
$box.SetBounds(20, 44, 420, 26)
$box.Text = $Initial
$hint = New-Object System.Windows.Forms.Label
$hint.Text = '保存后会自动重连，无需重启程序。'
$hint.ForeColor = [System.Drawing.Color]::Gray
$hint.SetBounds(20, 76, 420, 20)
$ok = New-Object System.Windows.Forms.Button
$ok.Text = '保存'
$ok.SetBounds(270, 106, 80, 28)
$ok.DialogResult = [System.Windows.Forms.DialogResult]::OK
$cancel = New-Object System.Windows.Forms.Button
$cancel.Text = '取消'
$cancel.SetBounds(360, 106, 80, 28)
$cancel.DialogResult = [System.Windows.Forms.DialogResult]::Cancel
$form.Controls.AddRange(@($label, $box, $hint, $ok, $cancel))
$form.AcceptButton = $ok
$form.CancelButton = $cancel
$form.Add_Shown({ $box.SelectAll(); $box.Focus() })
if ($form.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
    [System.IO.File]::WriteAllText($OutFile, $box.Text.Trim(), (New-Object System.Text.UTF8Encoding($false)))
    exit 0
}
exit 1
`

// promptServer 弹出 Windows 配置窗口，返回用户填写的地址；取消时返回错误。
func promptServer(initial string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("配置窗口仅支持 Windows")
	}
	script := filepath.Join(os.TempDir(), "strategy-notify-dialog.ps1")
	// PowerShell 5.1 依赖 BOM 识别 UTF-8，否则中文会乱码。
	content := append([]byte{0xEF, 0xBB, 0xBF}, dialogScript...)
	if err := os.WriteFile(script, content, 0600); err != nil {
		return "", err
	}
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("strategy-notify-server-%d.txt", time.Now().UnixNano()))
	defer os.Remove(outFile)
	cmd := exec.Command("powershell.exe",
		"-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden",
		"-File", script, "-Initial", initial, "-OutFile", outFile)
	cmd.SysProcAttr = hiddenSysProcAttr()
	if err := cmd.Run(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
