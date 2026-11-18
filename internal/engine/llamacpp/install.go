package llamacpp

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// 内置安装：直接从官方 GitHub Release 拉 Windows 构建并解压到运行时目录。
//
// 为什么不用 /releases/latest：llama.cpp 的 latest 现在只是一个指向 nightly 的
// 空壳（assets 里只有 nightly-tag.txt），真正的二进制挂在各 <build> 标签下。
// 所以这里遍历最近若干个 release，挑第一个带匹配 Windows 产物的。

const githubReleaseAPI = "https://api.github.com/repos/ggml-org/llama.cpp/releases"

// InstallVariant 可安装的构建变体。
type InstallVariant string

// 支持的变体。auto 交给硬件画像决定。
const (
	VariantAuto   InstallVariant = "auto"
	VariantCUDA13 InstallVariant = "cuda-13.4"
	VariantCUDA12 InstallVariant = "cuda-12.4"
	VariantVulkan InstallVariant = "vulkan"
	VariantCPU    InstallVariant = "cpu"
)

// AllVariants 界面下拉用（含 auto）。
func AllVariants() []InstallVariant {
	return []InstallVariant{VariantAuto, VariantCUDA13, VariantCUDA12, VariantVulkan, VariantCPU}
}

// InstallAsset 一个要下载的文件。
type InstallAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// InstallPlan 一次安装要做的事（已解析好，可直接执行）。
type InstallPlan struct {
	Tag       string         `json:"tag"`        // 例如 b11617
	Variant   InstallVariant `json:"variant"`    // 实际选定的变体，例如 cuda-13.4
	TargetDir string         `json:"target_dir"` // 解压落位
	Assets    []InstallAsset `json:"assets"`
	TotalSize int64          `json:"total_size"`
}

// InstallProgress 安装进度快照。
type InstallProgress struct {
	Stage      string `json:"stage"` // resolving / downloading / extracting / done
	Message    string `json:"message"`
	AssetName  string `json:"asset_name,omitempty"`
	AssetIndex int    `json:"asset_index"`
	AssetCount int    `json:"asset_count"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// VariantOrder 由「期望变体 + 硬件后端」推出候选优先级。
//
// auto 会给出**回退链**：优先 CUDA，找不到就退 Vulkan，再退 CPU——
// 这样即使某个 nightly 恰好没出 CUDA 包，也不会整个安装失败。
func VariantOrder(want InstallVariant, backends []string) []InstallVariant {
	if want != "" && want != VariantAuto {
		return []InstallVariant{want}
	}
	has := func(b string) bool {
		for _, x := range backends {
			if strings.EqualFold(x, b) {
				return true
			}
		}
		return false
	}
	switch {
	case has("cuda"):
		return []InstallVariant{VariantCUDA13, VariantCUDA12, VariantVulkan, VariantCPU}
	case has("vulkan"):
		return []InstallVariant{VariantVulkan, VariantCPU}
	default:
		return []InstallVariant{VariantCPU}
	}
}

// ResolveInstallPlan 查询官方 release 并选出可执行的安装计划。
func ResolveInstallPlan(ctx context.Context, want InstallVariant, backends []string) (InstallPlan, error) {
	if runtime.GOOS != "windows" {
		return InstallPlan{}, fmt.Errorf("内置安装目前只支持 Windows：官方只对 Windows 提供打包好的 llama-server.exe")
	}
	if runtime.GOARCH != "amd64" {
		return InstallPlan{}, fmt.Errorf("内置安装目前只支持 64 位 x86（当前架构 %s）", runtime.GOARCH)
	}
	rels, err := fetchReleases(ctx, 30)
	if err != nil {
		return InstallPlan{}, err
	}
	if len(rels) == 0 {
		return InstallPlan{}, fmt.Errorf("GitHub 没有返回任何 release")
	}
	order := VariantOrder(want, backends)
	for _, rel := range rels {
		if strings.TrimSpace(rel.TagName) == "" {
			continue
		}
		for _, v := range order {
			mainName := fmt.Sprintf("llama-%s-bin-win-%s-x64.zip", rel.TagName, v)
			main, ok := findReleaseAsset(rel, mainName)
			if !ok {
				continue
			}
			plan := InstallPlan{
				Tag:     rel.TagName,
				Variant: v,
				Assets:  []InstallAsset{{Name: main.Name, URL: main.URL, Size: main.Size}},
			}
			// CUDA 变体本身不含 cudart / cublas 运行库，必须连同 cudart 包一起装。
			if strings.HasPrefix(string(v), "cuda") {
				cudartName := fmt.Sprintf("cudart-llama-bin-win-%s-x64.zip", v)
				if a, ok := findReleaseAsset(rel, cudartName); ok {
					plan.Assets = append(plan.Assets, InstallAsset{Name: a.Name, URL: a.URL, Size: a.Size})
				}
			}
			for _, a := range plan.Assets {
				plan.TotalSize += a.Size
			}
			plan.TargetDir = filepath.Join(DefaultRuntimeDir(), rel.TagName+"-"+string(v))
			return plan, nil
		}
	}
	return InstallPlan{}, fmt.Errorf("最近 30 个发布里没有找到匹配的 Windows 构建（变体 %s）", want)
}

func findReleaseAsset(rel ghRelease, name string) (ghAsset, bool) {
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return ghAsset{}, false
}

func fetchReleases(ctx context.Context, perPage int) ([]ghRelease, error) {
	url := fmt.Sprintf("%s?per_page=%d", githubReleaseAPI, perPage)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ai-runner")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 GitHub 失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub 接口限流（HTTP %d）：请稍后再试，或手动下载解压到运行时目录", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 接口返回 HTTP %d", resp.StatusCode)
	}
	var rels []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&rels); err != nil {
		return nil, fmt.Errorf("解析 GitHub 响应失败：%w", err)
	}
	return rels, nil
}

// Install 执行安装计划：逐个下载并解压到目标目录。
func Install(ctx context.Context, plan InstallPlan, prog func(InstallProgress)) error {
	if err := os.MkdirAll(plan.TargetDir, 0o755); err != nil {
		return fmt.Errorf("创建运行时目录失败：%w", err)
	}
	client := newDownloadClient()
	for i, a := range plan.Assets {
		tmp, err := downloadAsset(ctx, client, a,
			func(idx, total int, url string) {
				src := fmt.Sprintf("源 %d/%d %s", idx+1, total, urlHost(url))
				if idx < 0 {
					src = "较慢但可用的源 " + urlHost(url)
				}
				report(prog, InstallProgress{
					Stage: "downloading", AssetName: a.Name, AssetIndex: i + 1, AssetCount: len(plan.Assets),
					Total: a.Size,
					Message: fmt.Sprintf("正在下载 %s（%.0f MB）· %s", a.Name, float64(a.Size)/(1<<20), src),
				})
			},
			func(done int64) {
				report(prog, InstallProgress{
					Stage: "downloading", AssetName: a.Name, AssetIndex: i + 1, AssetCount: len(plan.Assets),
					Downloaded: done, Total: a.Size,
					Message: fmt.Sprintf("正在下载 %s（%.0f/%.0f MB）",
						a.Name, float64(done)/(1<<20), float64(a.Size)/(1<<20)),
				})
			})
		if err != nil {
			return err
		}
		report(prog, InstallProgress{
			Stage: "extracting", AssetName: a.Name, AssetIndex: i + 1, AssetCount: len(plan.Assets),
			Total: a.Size, Downloaded: a.Size,
			Message: "正在解压 " + a.Name,
		})
		if err := extractZip(tmp, plan.TargetDir); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("解压 %s 失败：%w", a.Name, err)
		}
		_ = os.Remove(tmp)
	}
	return nil
}

// stallTimeout 连续这么久没有任何新数据就判定「卡死」，换下一个下载源。
//
// 判据来自实测：直连 release-assets.githubusercontent.com 时 HTTP 头能收到、
// 数据一个字节都下不来，连接既不报错也不结束——只靠超时永远等不到结果。
const stallTimeout = 30 * time.Second

// 慢速判定：观察 speedGrace 之后，若平均速度仍低于 minSpeed，换下一个源。
//
// 「慢」和「卡死」是两种病：实测直连能以 ~40 KB/s 慢慢爬（146 MB 要一小时），
// 它不会触发卡死看门狗，却同样不可用。给 12 秒证明自己，够快了才继续。
const (
	minSpeed   = 256 << 10 // 256 KB/s
	speedGrace = 12 * time.Second
)

// errTooSlow 该源能用但太慢（不是致命错误，换源即可）。
var errTooSlow = errors.New("下载速度过慢")

// githubMirrors 下载加速前缀，按顺序尝试，第一个成功的就用它。
// 空串代表直连官方地址。可用环境变量 AIRUNNER_GH_MIRROR 覆盖（逗号分隔）。
var githubMirrors = []string{
	"",
	"https://gh-proxy.com/",
	"https://ghproxy.net/",
	"https://ghfast.top/",
}

func mirrorPrefixes() []string {
	if v, ok := os.LookupEnv("AIRUNNER_GH_MIRROR"); ok {
		out := []string{}
		for _, p := range strings.Split(v, ",") {
			out = append(out, strings.TrimSpace(p))
		}
		return out
	}
	return githubMirrors
}

func newDownloadClient() *http.Client {
	// 大文件不设总超时（靠 ctx 取消 + 卡死看门狗）；
	// 但握手/响应头单独给宽一点，跨镜像时首包本来就会慢。
	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   20 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			ExpectContinueTimeout: 5 * time.Second,
		},
	}
}

// downloadAsset 依次尝试直连与各镜像，返回下载好的临时文件路径。
func downloadAsset(
	ctx context.Context,
	client *http.Client,
	a InstallAsset,
	onAttempt func(idx, total int, url string),
	onProgress func(int64),
) (string, error) {
	pres := mirrorPrefixes()
	if len(pres) == 0 {
		pres = []string{""}
	}
	var lastErr error
	slowFallback := ""
	for i, pre := range pres {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		url := pre + a.URL
		if onAttempt != nil {
			onAttempt(i, len(pres), url)
		}
		tmp, err := downloadOnce(ctx, client, url, a, onProgress, true)
		if err == nil {
			return tmp, nil
		}
		if errors.Is(err, errTooSlow) {
			slowFallback = url // 能用但太慢，留作最后手段
		}
		lastErr = err
	}
	// 所有源都不快：退回其中一个能用的，慢也认了（总比装不上强）。
	if slowFallback != "" {
		if onAttempt != nil {
			onAttempt(-1, len(pres), slowFallback)
		}
		tmp, err := downloadOnce(ctx, client, slowFallback, a, onProgress, false)
		if err == nil {
			return tmp, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的下载地址")
	}
	return "", fmt.Errorf("下载 %s 失败（已尝试 %d 个源）：%w", a.Name, len(pres), lastErr)
}

// downloadOnce 从单个地址下载。
//
// 两道看门狗：卡死（stallTimeout 无数据）与过慢（speedGrace 后仍低于 minSpeed），
// 任一触发都会取消本次尝试并交给下一个源；enforceSpeed=false 时只保留卡死判定。
func downloadOnce(
	ctx context.Context,
	client *http.Client,
	url string,
	a InstallAsset,
	onProgress func(int64),
	enforceSpeed bool,
) (string, error) {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ai-runner")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmp := filepath.Join(os.TempDir(), "airunner-"+filepath.Base(a.Name))
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败：%w", err)
	}
	defer f.Close()

	start := time.Now()
	var done atomic.Int64
	var tooSlow atomic.Bool

	watchdog := time.AfterFunc(stallTimeout, cancel)
	defer watchdog.Stop()
	if enforceSpeed {
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-reqCtx.Done():
					return
				case <-t.C:
					el := time.Since(start).Seconds()
					if el >= speedGrace.Seconds() && float64(done.Load()) < minSpeed*el {
						tooSlow.Store(true)
						cancel()
						return
					}
				}
			}
		}()
	}

	last := time.Now()
	buf := make([]byte, 1<<20)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(stallTimeout)
			if _, werr := f.Write(buf[:n]); werr != nil {
				_ = os.Remove(tmp)
				return "", werr
			}
			cur := done.Add(int64(n))
			// 限流回调：每 300ms 报一次，避免 UI 被刷爆
			if onProgress != nil && time.Since(last) > 300*time.Millisecond {
				last = time.Now()
				onProgress(cur)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = os.Remove(tmp)
			if tooSlow.Load() {
				return "", errTooSlow
			}
			return "", rerr
		}
	}
	if onProgress != nil {
		onProgress(done.Load())
	}
	return tmp, nil
}

func urlHost(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil || u.Host == "" {
		return "直连"
	}
	return u.Host
}

// extractZip 解压到 dst（防目录穿越）。
func extractZip(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()

	cleanDst, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		target := filepath.Join(cleanDst, f.Name)
		if !strings.HasPrefix(target, cleanDst+string(os.PathSeparator)) && target != cleanDst {
			return fmt.Errorf("压缩包包含非法路径：%s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, cerr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if cerr != nil {
			return cerr
		}
	}
	return nil
}

func report(prog func(InstallProgress), p InstallProgress) {
	if prog != nil {
		prog(p)
	}
}

// FindServerExe 在目录下递归查找 llama-server.exe（最多 3 层），找不到返回空串。
func FindServerExe(dir string) string {
	found := scanForExe(dir, 3)
	if len(found) == 0 {
		return ""
	}
	return found[0]
}
