//go:build !windows

package nativedlg

import (
	"os/exec"
	"strings"
)

// 非 Windows 平台尽力而为：zenity（GNOME）→ kdialog（KDE）→ osascript（macOS）。
// 一个都没有就明确报错，让界面回落成「手动填写路径」，而不是给一个静默失败。
func pickFolder(title, initial string) (string, error) {
	if p, ok, err := tryLinuxFolder(title, initial); ok {
		return p, err
	}
	return tryMacFolder()
}

func pickFile(title, initial, filter string) (string, error) {
	if p, ok, err := tryLinuxFile(title, initial, filter); ok {
		return p, err
	}
	return tryMacFile()
}

func tryLinuxFolder(title, initial string) (string, bool, error) {
	type candidate struct {
		bin  string
		args []string
	}
	for _, c := range []candidate{
		{"zenity", []string{"--file-selection", "--directory", "--title=" + title}},
		{"kdialog", []string{"--getexistingdirectory", initial, "--title", title}},
	} {
		if _, err := exec.LookPath(c.bin); err != nil {
			continue
		}
		return run(c.bin, c.args...)
	}
	return "", false, nil
}

func tryLinuxFile(title, initial, filter string) (string, bool, error) {
	if _, err := exec.LookPath("zenity"); err == nil {
		return run("zenity", "--file-selection", "--title="+title)
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		return run("kdialog", "--getopenfilename", initial, filter, "--title", title)
	}
	return "", false, nil
}

func tryMacFolder() (string, error) {
	return runMac(`POSIX path of (choose folder)`)
}

func tryMacFile() (string, error) {
	return runMac(`POSIX path of (choose file)`)
}

func runMac(script string) (string, error) {
	if _, err := exec.LookPath("osascript"); err != nil {
		return "", ErrUnsupported
	}
	p, ok, err := run("osascript", "-e", script)
	if !ok {
		return "", err
	}
	return strings.TrimSuffix(p, "/"), nil
}

// run 执行外部选择器；ok=false 表示该工具不存在（交给下一个候选）。
func run(bin string, args ...string) (string, bool, error) {
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		// 用户取消时 zenity/kdialog 会返回非 0，且没有输出。
		if len(strings.TrimSpace(string(out))) == 0 {
			return "", true, ErrCanceled
		}
		return "", true, err
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", true, ErrCanceled
	}
	return p, true, nil
}
