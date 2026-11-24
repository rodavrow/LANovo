package voice

import (
	"testing"

	sharedavatar "github.com/ygelfand/libcountertop/pkg/assistant/avatar"
)

// The avatar finds these by type assertion on the turn's speaker, so losing one silently
// breaks lip sync or stopping.
func TestTheVoiceSpeakerTimesAndDrainsTheVoiceForTheAvatar(t *testing.T) {
	var s any = voiceSpeaker{}
	if _, ok := s.(sharedavatar.TimedSink); !ok {
		t.Error("the voice speaker does not time its queue")
	}
	if _, ok := s.(sharedavatar.Drainer); !ok {
		t.Error("the voice speaker cannot drop its queued voice")
	}
}
