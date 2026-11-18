//go:build windows

package nativedlg

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// 走 PowerShell + WinForms 而不是引入 CGO / GUI 依赖：
//   - 本程序本来就只面向 Windows 发行（llama-server 也只有 win 包）；
//   - 不引入任何新的第三方依赖，go.mod 保持不变；
//   - 命令行脚本保持**纯 ASCII**（Windows PowerShell 5.1 按 ANSI 解析脚本，
//     中文写进脚本体里会乱码），标题与初始路径一律经环境变量传入
//     （Go 在 Windows 上以 UTF-16 设置环境变量，中文标题不会坏）。
//
// -STA 是必须的：FolderBrowserDialog / OpenFileDialog 都是 COM 单线程套间组件。
const (
	psCommon = `Add-Type -AssemblyName System.Windows.Forms | Out-Null;`
	psFolder = psCommon +
		`$d = New-Object System.Windows.Forms.FolderBrowserDialog;` +
		`$d.Description = $env:AIRUNNER_DLG_TITLE;` +
		`$d.ShowNewFolderButton = $true;` +
		`if ($env:AIRUNNER_DLG_INITIAL -and (Test-Path -LiteralPath $env:AIRUNNER_DLG_INITIAL)) { $d.SelectedPath = $env:AIRUNNER_DLG_INITIAL };` +
		`if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($d.SelectedPath) }`
	psFile = psCommon +
		`$d = New-Object System.Windows.Forms.OpenFileDialog;` +
		`$d.Title = $env:AIRUNNER_DLG_TITLE;` +
		`if ($env:AIRUNNER_DLG_FILTER) { $d.Filter = $env:AIRUNNER_DLG_FILTER };` +
		`$d.CheckFileExists = $true;` +
		`if ($env:AIRUNNER_DLG_INITIAL -and (Test-Path -LiteralPath $env:AIRUNNER_DLG_INITIAL)) { $d.InitialDirectory = $env:AIRUNNER_DLG_INITIAL };` +
		`if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($d.FileName) }`
)

func pickFolder(title, initial string) (string, error) {
	return runPowerShellDialog(psFolder, title, initial, "")
}

func pickFile(title, initial, filter string) (string, error) {
	return runPowerShellDialog(psFile, title, initial, filter)
}

func runPowerShellDialog(script, title, initial, filter string) (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(),
		"AIRUNNER_DLG_TITLE="+title,
		"AIRUNNER_DLG_INITIAL="+initial,
		"AIRUNNER_DLG_FILTER="+filter,
	)
	// 别让一个黑色控制台窗口闪出来。
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("打开原生选择框失败：%w", err)
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", ErrCanceled
	}
	return p, nil
}
