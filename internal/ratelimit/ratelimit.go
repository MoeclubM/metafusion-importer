// Package ratelimit 提供抓取与写库两侧共用的令牌桶限速。
//
// 为什么要自己写：插件需要"按来源站点可配置"的速率（Bangumi / MusicBrainz / TMDB 各自不同），
// 而且同一进程里往往要同时限制两条链路（抓来源、写目录），因此把桶做成显式对象，
// 谁需要就自己持有一个，而不是依赖某个全局开关。
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter 是并发安全的令牌桶：按 RatePerSecond 持续回填，最多积攒 Burst 个令牌。
type Limiter struct {
	rate   float64 // 每秒回填令牌数
	burst  float64
	mu     sync.Mutex
	tokens float64
	last   time.Time
	// now 可在测试中替换。
	now func() time.Time
	// sleep 可在测试中替换，避免真的等待。
	sleep func(context.Context, time.Duration) error
}

// New 建一个限速器：ratePerSecond <= 0 表示不限速（抓取量很小或来源方给了白名单）。
func New(ratePerSecond float64, burst int) *Limiter {
	if burst <= 0 {
		burst = 1
	}
	return &Limiter{
		rate:   ratePerSecond,
		burst:  float64(burst),
		tokens: float64(burst),
		// last 留零值：首次 Wait 时才锚定时间，避免"构造后久未使用"被算成
		// 从构造那一刻起持续回填（也不受构造与使用之间时钟跳变影响）。
		now:   time.Now,
		sleep: sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Wait 取一个令牌；不限速时直接返回。
func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil || l.rate <= 0 {
		return nil
	}
	for {
		l.mu.Lock()
		now := l.now()
		if l.last.IsZero() {
			l.last = now
		}
		l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
		l.last = now
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		need := time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
		l.mu.Unlock()
		if err := l.sleep(ctx, need); err != nil {
			return err
		}
	}
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
