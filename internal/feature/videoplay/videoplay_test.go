package videoplay

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	sharedvideoplay "github.com/ygelfand/libcountertop/pkg/media/videoplay"
)

// A documentation address (RFC 5737): public, and never dialed because the check refuses first.
const publicURL = "http://203.0.113.7/video.mp4"

func always(v bool) func() bool { return func() bool { return v } }

func TestOptionsLeaveTheHTTPClientToThePlayer(t *testing.T) {
	if o := options(always(false)); o.HTTP != nil {
		t.Error("options set an HTTP client; only the player's own refuses at dial time")
	}
}

func TestOptionsGiveThePlayerAScreenAndSound(t *testing.T) {
	o := options(always(false))

	if o.Output == nil || o.Target == nil || o.Resample == nil {
		t.Errorf("options left a piece out: output %t, target %t, resample %t",
			o.Output != nil, o.Target != nil, o.Resample != nil)
	}
}

func TestUnderLocalOnlyAPublicURLIsRefused(t *testing.T) {
	p := sharedvideoplay.New(options(always(true)))

	err := p.Play(context.Background(), publicURL, "Test", 0)

	if !errors.Is(err, sharedvideoplay.ErrBlocked) {
		t.Fatalf("Play(%s) = %v, want %v", publicURL, err, sharedvideoplay.ErrBlocked)
	}
	if got := p.State(); got != sharedvideoplay.Idle {
		t.Errorf("after a refused url the state is %q, want %q", got, sharedvideoplay.Idle)
	}
}

func TestLocalOnlyIsAskedAgainForEveryVideo(t *testing.T) {
	var asked atomic.Int32
	p := sharedvideoplay.New(options(func() bool {
		asked.Add(1)
		return true
	}))

	_ = p.Play(context.Background(), publicURL, "", 0)
	first := asked.Load()
	_ = p.Play(context.Background(), publicURL, "", 0)

	if first == 0 || asked.Load() <= first {
		t.Errorf("LocalOnly was asked %d then %d times; it must be read at every check",
			first, asked.Load())
	}
}
