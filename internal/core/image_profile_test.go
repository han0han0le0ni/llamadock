package core

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"airunner/internal/config"
	"airunner/internal/engine/sdcpp"
)

// TestImageUseProfile 验证切换预设会把 model_path / extra_args / 默认出图参数
// 写回主字段并持久化，同时 DTO 能带回预设列表与当前激活 id。
func TestImageUseProfile(t *testing.T) {
	tmp := t.TempDir()
	mgr, err := config.NewManager(tmp)
	if err != nil {
		t.Fatal(err)
	}
	profiles := []config.ImageProfile{
		{ID: "sd15", Name: "SD 1.5", Mode: "single", ModelPath: "E:/sd/v15.safetensors",
			DefaultWidth: 512, DefaultHeight: 512, DefaultSteps: 25, DefaultCfg: 7, DefaultSampler: "euler_a"},
		{ID: "qwen21", Name: "Qwen-Image 2.1", Mode: "components",
			ExtraArgs: `--diffusion-model "E:/sd/qwen.gguf" --llm "E:/sd/qwenvl.gguf" --vae "E:/sd/vae.safetensors"`,
			DefaultWidth: 1024, DefaultHeight: 1024, DefaultSteps: 20, DefaultCfg: 6, DefaultSampler: "euler"},
	}
	if _, err := mgr.Update(func(c *config.Config) {
		c.Image.Enabled = true
		c.Image.Profiles = profiles
		c.Image.ActiveProfile = "sd15"
		c.Image.ModelPath = profiles[0].ModelPath
		c.Image.DefaultWidth = profiles[0].DefaultWidth
	}); err != nil {
		t.Fatal(err)
	}

	c := &Core{
		Cfg: mgr,
		sd:  sdcpp.New(sdcpp.Config{Port: 1235}),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// 切到 Qwen-Image 预设
	dto, err := c.ImageUseProfile(context.Background(), "qwen21")
	if err != nil {
		t.Fatal(err)
	}
	snap := mgr.Snapshot()
	if snap.Image.ActiveProfile != "qwen21" {
		t.Fatalf("active_profile = %q, want qwen21", snap.Image.ActiveProfile)
	}
	if snap.Image.ModelPath != "" {
		t.Fatalf("components 预设的 model_path 应为空, got %q", snap.Image.ModelPath)
	}
	if snap.Image.ExtraArgs != profiles[1].ExtraArgs {
		t.Fatalf("extra_args 未写回: got %q", snap.Image.ExtraArgs)
	}
	if snap.Image.DefaultWidth != 1024 || snap.Image.DefaultSteps != 20 || snap.Image.DefaultCfg != 6 || snap.Image.DefaultSampler != "euler" {
		t.Fatalf("默认出图参数未写回: %+v", snap.Image)
	}
	if len(dto.Profiles) != 2 || dto.ActiveProfile != "qwen21" {
		t.Fatalf("DTO 预设列表/激活 id 不对: profiles=%d active=%q", len(dto.Profiles), dto.ActiveProfile)
	}
	if dto.Defaults.Sampler != "euler" || dto.Defaults.Width != 1024 {
		t.Fatalf("DTO defaults 未跟随预设: %+v", dto.Defaults)
	}

	// 再切回 SD1.5：ExtraArgs 应被清空（single 预设无额外参数）
	if _, err := c.ImageUseProfile(context.Background(), "sd15"); err != nil {
		t.Fatal(err)
	}
	snap = mgr.Snapshot()
	if snap.Image.ModelPath != profiles[0].ModelPath || snap.Image.ExtraArgs != "" {
		t.Fatalf("single 预设回切失败: model=%q extra=%q", snap.Image.ModelPath, snap.Image.ExtraArgs)
	}

	// 不存在的 id 应报错，且不改变当前预设
	if _, err := c.ImageUseProfile(context.Background(), "nope"); err == nil {
		t.Fatal("切到不存在的预设应报错")
	}
	if mgr.Snapshot().Image.ActiveProfile != "sd15" {
		t.Fatal("失败切换不应改变当前预设")
	}
}
