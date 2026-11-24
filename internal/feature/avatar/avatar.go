// Package avatar has an assistant in avatar mode shown and voiced by its LiveTalking avatar.
package avatar

import (
	"context"
	"slices"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	sharedavatar "github.com/ygelfand/libcountertop/pkg/assistant/avatar"
	"github.com/ygelfand/libcountertop/pkg/display/callview"
	"github.com/ygelfand/libcountertop/pkg/display/cameraview"
	"github.com/ygelfand/libcountertop/pkg/display/doorbellview"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/videopage"
	"github.com/ygelfand/libcountertop/pkg/fetch"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/LANovo/internal/component"
	"github.com/ygelfand/LANovo/internal/config"
	"github.com/ygelfand/LANovo/internal/feature/call"
	"github.com/ygelfand/LANovo/internal/feature/shell"
	"github.com/ygelfand/LANovo/internal/feature/wakeword"
	"github.com/ygelfand/LANovo/internal/hardware/display"
)

// clipWait bounds one LiveTalking request or reply download.
const clipWait = 30 * time.Second

func init() {
	component.Register(sharedcomponent.Network, Get, sharedcomponent.Order(30))
}

type Avatar struct{ *sharedavatar.Avatars }

var Get = sync.OnceValue(func() *Avatar {
	return &Avatar{sharedavatar.New(sharedavatar.Options{
		Slots:     wakeword.Slots,
		Read:      func() schema.Wake { return config.Get().Wake },
		Write:     func(slot int) schema.WakeWriter { return config.Set().Wake(slot) },
		DeviceID:  component.AssistantDevice,
		Client:    fetch.Client(clipWait),
		Display:   display.Get(),
		VideoBusy: videoBusy,
	})}
})

// videoBusy reports that a call or a view playing video owns the hardware video, so the
// assistant shows its usual look. Views that only cover the home screen, such as the photo
// screen or the music app, leave the avatar alone.
func videoBusy() bool {
	if _, calling := call.Get().Now(); calling {
		return true
	}
	return slices.ContainsFunc(shell.Get().Views(), showsVideo)
}

func showsVideo(v sharedshell.View) bool {
	switch v.(type) {
	case *videopage.Page, *cameraview.Live, *cameraview.Grid, *doorbellview.Live,
		*callview.Talk:
		return true
	}
	return false
}

func (a *Avatar) Run(ctx context.Context) error {
	a.Avatars.Run(ctx)
	return nil
}

func (a *Avatar) Entities() []esphome.Entity {
	out := a.Avatars.DeviceEntities()
	for slot := range wakeword.Slots {
		out = append(out, a.Avatars.Entities(slot)...)
	}
	return out
}

func (a *Avatar) Restore(c config.Config) { a.Avatars.Restore(c.Wake.Slots(wakeword.Slots)) }
