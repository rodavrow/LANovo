// Package videoplay lets Home Assistant play a video from a URL on the screen.
package videoplay

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/audio/resample"
	"github.com/ygelfand/libcountertop/pkg/display/video"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	sharedvideoplay "github.com/ygelfand/libcountertop/pkg/media/videoplay"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/LANovo/internal/board"
	"github.com/ygelfand/LANovo/internal/component"
	"github.com/ygelfand/LANovo/internal/feature/network"
	"github.com/ygelfand/LANovo/internal/feature/playout"
	"github.com/ygelfand/LANovo/internal/hardware/display"
	"github.com/ygelfand/LANovo/internal/hardware/speaker"
)

func init() { component.Register(sharedcomponent.Device, Get) }

var get = sync.OnceValue(func() *sharedvideoplay.Player {
	return sharedvideoplay.New(options(localOnly))
})

func Get() *sharedvideoplay.Player { return get() }

// localOnly reads network.internet each time it is asked, so a change applies to a playing video.
func localOnly() bool { return network.Get().Internet().Local() }

// options leaves HTTP unset: the player's own client has no proxy and, under Local only, refuses
// to connect off the local network at dial time, which a client from fetch cannot be made to do.
func options(localOnly func() bool) sharedvideoplay.Options {
	return sharedvideoplay.Options{
		Output: func(ended func()) playback.Output {
			o := playout.New()
			o.SetEnded(ended)
			return o
		},
		Target: func() playback.Target {
			return video.Target(display.Get(), board.Current().MaxFPS)
		},
		Resample: func(from int) func([]int16) []int16 {
			return resample.NewRational(from, speaker.Rate, speaker.Channels).Run
		},
		LocalOnly: localOnly,
	}
}
