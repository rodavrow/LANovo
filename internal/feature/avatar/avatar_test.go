package avatar

import (
	"testing"

	"github.com/ygelfand/libcountertop/pkg/display/cameraview"
	"github.com/ygelfand/libcountertop/pkg/display/idleview"
	"github.com/ygelfand/libcountertop/pkg/display/videopage"
)

func TestTheAvatarGivesWayOnlyToVideo(t *testing.T) {
	if !showsVideo(&videopage.Page{}) || !showsVideo(&cameraview.Live{}) {
		t.Error("a video view does not take the screen from the avatar")
	}
	if showsVideo(&idleview.View{}) {
		t.Error("the photo screen hides the avatar")
	}
}
