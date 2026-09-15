package ratelimit

import (
	"context"
	"testing"
	"time"
)

// 不限速（rate<=0）必须立即返回：小批量抓取不该被拖慢。
func TestUnlimitedReturnsImmediately(t *testing.T) {
	l := New(0, 0)
	start := time.Now()
	for i := 0; i < 50; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("不限速却等了 %v", elapsed)
	}
}

// burst 之内的请求不该等待；超出的按 1/rate 秒排队。
func TestBurstThenThrottle(t *testing.T) {
	l := New(2, 3) // 每秒 2 个，允许 3 个突发
	var slept []time.Duration
	// 假时钟：sleep 必须推进时间，否则令牌永远不回填（测试会空转）。
	clock := time.Unix(0, 0)
	l.now = func() time.Time { return clock }
	l.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		clock = clock.Add(d)
		return nil
	}
	for i := 0; i < 5; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(slept) != 2 {
		t.Fatalf("突发 3 之后应有 2 次等待，实际 %d 次（%v）", len(slept), slept)
	}
	// 缺 1 个令牌、每秒回填 2 个 => 等 0.5s
	if slept[0] != 500*time.Millisecond {
		t.Fatalf("等待时长应为 500ms，实际 %v", slept[0])
	}
}

// ctx 取消要能中断等待，而不是死等。
func TestWaitHonorsContext(t *testing.T) {
	l := New(1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	if err := l.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	err := l.Wait(ctx)
	if err == nil {
		t.Fatal("取消后应返回错误")
	}
}
