package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// PoolOptions 熔断参数
type PoolOptions struct {
	MaxFailures     int           // 连续失败熔断阈值（默认 5）
	CircuitDuration time.Duration // 熔断持续时间（默认 10 分钟）
}

// Pool 多模型池（规格 5.6）：按序 fallback + 连续失败熔断。
// 熔断中直接返回错误（AI 链暂停，WARN 已记录；恢复后自动重试）
type Pool struct {
	clients []*Client
	opts    PoolOptions
	mu          sync.Mutex
	failures    int
	circuitOpen bool
	circuitTill time.Time
}

// NewPool 创建模型池；clients[0] 为主模型
func NewPool(opts []ClientOptions, poolOpts PoolOptions) *Pool {
	if poolOpts.MaxFailures <= 0 {
		poolOpts.MaxFailures = 5
	}
	if poolOpts.CircuitDuration <= 0 {
		poolOpts.CircuitDuration = 10 * time.Minute
	}
	clients := make([]*Client, 0, len(opts))
	for _, o := range opts {
		clients = append(clients, NewClient(o))
	}
	return &Pool{clients: clients, opts: poolOpts}
}

// Chat 依次尝试各模型（见 ChatWithModel）
func (p *Pool) Chat(ctx context.Context, system, user string) (string, error) {
	out, _, err := p.ChatWithModel(ctx, system, user)
	return out, err
}

// ChatWithModel 依次尝试各模型；成功时返回输出与"实际命中的模型名"（主 → fallback，
// 供 AIResult.Model 回填追溯——规格 3.2）。全部失败记录一次连续失败（达到阈值开熔断）。
// 熔断中直接返回错误（不请求，AI 链暂停）
func (p *Pool) ChatWithModel(ctx context.Context, system, user string) (string, string, error) {
	if len(p.clients) == 0 {
		return "", "", fmt.Errorf("llm pool: no clients configured")
	}
	p.mu.Lock()
	if p.circuitOpen {
		if time.Now().After(p.circuitTill) {
			p.circuitOpen = false // 熔断期过：放行重试
			p.failures = 0
		} else {
			p.mu.Unlock()
			return "", "", errCircuitOpen(p.circuitTill)
		}
	}
	p.mu.Unlock()

	// 按序尝试：主 → fallback
	var lastErr error
	for i, c := range p.clients {
		out, err := c.Chat(ctx, system, user)
		if err == nil {
			p.recordSuccess()
			if i > 0 {
				slog.Warn("LLM fallback 成功", "model", c.Model())
			}
			return out, c.Model(), nil
		}
		lastErr = err
		slog.Warn("LLM 调用失败", "model", c.Model(), "err", err)
	}
	p.recordFailure()
	return "", "", lastErr
}

// recordSuccess 重置连续失败计数
func (p *Pool) recordSuccess() {
	p.mu.Lock()
	p.failures = 0
	p.mu.Unlock()
}

// recordFailure 累计连续失败；达阈值开熔断
func (p *Pool) recordFailure() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures++
	if p.failures >= p.opts.MaxFailures {
		p.circuitOpen = true
		p.circuitTill = time.Now().Add(p.opts.CircuitDuration)
		slog.Warn("LLM 熔断开启", "failures", p.failures, "duration", p.opts.CircuitDuration)
	}
}

// errCircuitOpen 熔断中错误
func errCircuitOpen(till time.Time) error {
	return &CircuitOpenError{Till: till}
}

// CircuitOpenError 熔断错误（调用方判断是否 AI 暂停）
type CircuitOpenError struct{ Till time.Time }

func (e *CircuitOpenError) Error() string {
	return "LLM 熔断中，至 " + e.Till.Format("15:04:05") + " 恢复"
}
