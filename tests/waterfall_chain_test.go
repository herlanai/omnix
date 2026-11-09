// M02 waterfall middleware primitives.
package tests

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/herlanai/omnix/pkg/waterfall"
)

// testPayload is passed through and rewritten by middleware stages.
type testPayload struct {
	seq     []string // Record middleware entry and exit order.
	msg     string   // Text that middleware may rewrite.
	blocked bool     // Whether execution was intercepted.
}

// TestWaterfallOrdering verifies registration-order entry and reverse-order exit.
func TestWaterfallOrdering(t *testing.T) {
	p := &testPayload{seq: []string{}}

	chain := waterfall.New[testPayload](
		func(p *testPayload, next waterfall.NextFunc) error {
			p.seq = append(p.seq, "A-in")
			err := next()
			p.seq = append(p.seq, "A-out")
			return err
		},
		func(p *testPayload, next waterfall.NextFunc) error {
			p.seq = append(p.seq, "B-in")
			err := next()
			p.seq = append(p.seq, "B-out")
			return err
		},
		func(p *testPayload, next waterfall.NextFunc) error {
			p.seq = append(p.seq, "C-in")
			err := next()
			p.seq = append(p.seq, "C-out")
			return err
		},
	)

	if err := chain.Run(context.Background(), p); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	want := []string{"A-in", "B-in", "C-in", "C-out", "B-out", "A-out"}
	if strings.Join(p.seq, ",") != strings.Join(want, ",") {
		t.Fatalf("调用顺序 = %v, want %v", p.seq, want)
	}
}

// TestWaterfallShortCircuit stops when middleware omits next.
func TestWaterfallShortCircuit(t *testing.T) {
	var entered int32
	p := &testPayload{seq: []string{}}

	chain := waterfall.New[testPayload](
		func(p *testPayload, next waterfall.NextFunc) error {
			p.seq = append(p.seq, "guard")
			// Omitting next short-circuits execution.
			return nil
		},
		func(p *testPayload, next waterfall.NextFunc) error {
			atomic.AddInt32(&entered, 1)
			return next()
		},
	)

	if err := chain.Run(context.Background(), p); err != nil {
		t.Fatalf("短路场景 Run 应返回 nil, 实际 %v", err)
	}
	if entered != 0 {
		t.Fatalf("短路后后续中间件不应执行, entered = %d", entered)
	}
	if len(p.seq) != 1 {
		t.Fatalf("只有首个中间件应进入, seq = %v", p.seq)
	}
}

// TestWaterfallPayloadRewrite passes rewritten payloads to downstream middleware.
func TestWaterfallPayloadRewrite(t *testing.T) {
	p := &testPayload{msg: "initial"}

	chain := waterfall.New[testPayload](
		func(p *testPayload, next waterfall.NextFunc) error {
			p.msg += "+m1"
			return next()
		},
		func(p *testPayload, next waterfall.NextFunc) error {
			p.msg += "+m2"
			return next()
		},
		func(p *testPayload, next waterfall.NextFunc) error {
			p.msg += "+m3"
			return next()
		},
	)

	if err := chain.Run(context.Background(), p); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if p.msg != "initial+m1+m2+m3" {
		t.Fatalf("改写结果 = %q, want %q", p.msg, "initial+m1+m2+m3")
	}
}

// TestWaterfallErrorPropagation stops and propagates middleware errors.
func TestWaterfallErrorPropagation(t *testing.T) {
	var after int32
	boom := errors.New("boom")
	p := &testPayload{}

	chain := waterfall.New[testPayload](
		func(p *testPayload, next waterfall.NextFunc) error {
			return next()
		},
		func(p *testPayload, next waterfall.NextFunc) error {
			return boom // The second middleware returns an error immediately.
		},
		func(p *testPayload, next waterfall.NextFunc) error {
			atomic.AddInt32(&after, 1)
			return next()
		},
	)

	err := chain.Run(context.Background(), p)
	if !errors.Is(err, boom) {
		t.Fatalf("应传播 boom, 实际 %v", err)
	}
	if after != 0 {
		t.Fatalf("错误后后续中间件不应执行, after = %d", after)
	}
}

// TestWaterfallNextSingleCall panics if one middleware invokes next twice.
func TestWaterfallNextSingleCall(t *testing.T) {
	p := &testPayload{}

	chain := waterfall.New[testPayload](
		func(p *testPayload, next waterfall.NextFunc) error {
			_ = next()
			// The second call must panic.
			_ = next()
			return nil
		},
	)

	defer func() {
		if recover() == nil {
			t.Fatal("重复调用 next() 应 panic")
		}
	}()
	_ = chain.Run(context.Background(), p)
}

// TestWaterfallDownstreamErrorToUpstream lets middleware catch and replace downstream errors.
func TestWaterfallDownstreamErrorToUpstream(t *testing.T) {
	p := &testPayload{}

	chain := waterfall.New[testPayload](
		func(p *testPayload, next waterfall.NextFunc) error {
			err := next()
			if err != nil {
				// Consume the downstream error and replace it.
				return errors.New("wrapped: " + err.Error())
			}
			return nil
		},
		func(p *testPayload, next waterfall.NextFunc) error {
			return errors.New("inner failure")
		},
	)

	err := chain.Run(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "wrapped:") {
		t.Fatalf("应返回改写后的错误, 实际 %v", err)
	}
}

// TestWaterfallEmptyChain returns nil for an empty chain.
func TestWaterfallEmptyChain(t *testing.T) {
	chain := waterfall.New[testPayload]()
	if err := chain.Run(context.Background(), &testPayload{}); err != nil {
		t.Fatalf("空链应返回 nil, 实际 %v", err)
	}
}

// TestWaterfallTypedPayloads verifies independent generic chains with different payload types.
func TestWaterfallTypedPayloads(t *testing.T) {
	type request struct{ url string }
	type approval struct{ ok bool }

	reqChain := waterfall.New[request](
		func(r *request, next waterfall.NextFunc) error {
			r.url = "https://example.com"
			return next()
		},
	)
	apprChain := waterfall.New[approval](
		func(a *approval, next waterfall.NextFunc) error {
			a.ok = true
			return next()
		},
	)

	req := &request{}
	appr := &approval{}
	_ = reqChain.Run(context.Background(), req)
	_ = apprChain.Run(context.Background(), appr)

	if req.url != "https://example.com" || !appr.ok {
		t.Fatalf("两条不同类型的链应独立工作: req=%+v appr=%+v", req, appr)
	}
}
