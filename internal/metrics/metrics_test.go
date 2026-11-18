package metrics

import "testing"

func TestTPSWindow(t *testing.T) {
	s := New(10)
	if got := s.TPS(); got != 0 {
		t.Fatalf("初始吞吐应为 0，实际 %v", got)
	}
	// 5 秒窗口内累计 10 个 token → 2 t/s
	s.AddProgress(10)
	if got := s.TPS(); got != 2.0 {
		t.Fatalf("TPS 应为 2.0，实际 %v", got)
	}
	// 完成时的 token 也应计入吞吐窗口与总量
	s.AddTokens(100, 10)
	if got := s.TPS(); got != 4.0 {
		t.Fatalf("TPS 应为 4.0，实际 %v", got)
	}
	sn := s.Snapshot(10)
	if sn.PromptTokens != 100 || sn.CompletionTokens != 10 {
		t.Fatalf("token 统计错误: %+v", sn)
	}
}

func TestConcurrentAndErrors(t *testing.T) {
	s := New(5)
	s.BeginRequest()
	s.BeginRequest()
	if got := s.Snapshot(0).Active; got != 2 {
		t.Fatalf("活跃数应为 2，实际 %d", got)
	}
	s.EndRequest(false)
	s.EndRequest(true)
	sn := s.Snapshot(0)
	if sn.Active != 0 || sn.TotalRequests != 2 || sn.FailedRequests != 1 {
		t.Fatalf("计数错误: %+v", sn)
	}
}

func TestSampleRingIsBounded(t *testing.T) {
	s := New(3)
	for i := 0; i < 10; i++ {
		s.Sample(i, 0, 0)
	}
	sn := s.Snapshot(0)
	if len(sn.Recent) != 3 {
		t.Fatalf("环形缓冲应保留 3 个采样，实际 %d", len(sn.Recent))
	}
	if sn.Recent[2].VRAMUsedMB != 9 {
		t.Fatalf("应保留最新的采样，实际 %+v", sn.Recent[2])
	}
}

func TestPeakTPSTracksHighest(t *testing.T) {
	s := New(10)
	s.Sample(0, 0, 12.5)
	s.Sample(0, 0, 3)
	if got := s.Snapshot(0).PeakTPS; got != 12.5 {
		t.Fatalf("峰值应为历史最高的 12.5，实际 %v", got)
	}
	// 回落不能拉低峰值
	s.Sample(0, 0, 0)
	if got := s.Snapshot(0).PeakTPS; got != 12.5 {
		t.Fatalf("峰值不应被拉低，实际 %v", got)
	}
}

func TestAddTotalsKeepsWindowUntouched(t *testing.T) {
	s := New(10)
	// 流式过程中实时吞吐只进窗口
	s.AddProgress(10)
	// 结算时全额累计总量，但不再写吞吐窗口
	s.AddTotals(100, 20)
	if got := s.TPS(); got != 2.0 {
		t.Fatalf("AddTotals 不应写入吞吐窗口，TPS 应为 2.0，实际 %v", got)
	}
	sn := s.Snapshot(0)
	if sn.PromptTokens != 100 || sn.CompletionTokens != 20 {
		t.Fatalf("总量应按全额累计，实际 %+v", sn)
	}
}

func TestResetClearsPeakAndTotals(t *testing.T) {
	s := New(10)
	s.Sample(0, 0, 8)
	s.AddTotals(50, 5)
	s.Reset()
	sn := s.Snapshot(0)
	if sn.PeakTPS != 0 || sn.PromptTokens != 0 || sn.CompletionTokens != 0 || sn.AvgTPS != 0 {
		t.Fatalf("Reset 后应全部清零，实际 %+v", sn)
	}
}
