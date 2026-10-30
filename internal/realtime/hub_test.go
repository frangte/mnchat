package realtime

import (
	"testing"

	"github.com/frangte/mnchat/internal/domain"
)

func TestHubPublishIsScopedToWorkspace(t *testing.T) {
	h := NewHub()
	a := h.Subscribe(1, 10)
	b := h.Subscribe(1, 11)
	other := h.Subscribe(2, 12)

	h.Publish(1, domain.Event{Type: domain.EventMessageCreated, ChannelID: 5})

	for _, c := range []*Client{a, b} {
		select {
		case <-c.Messages():
		default:
			t.Fatalf("client of user %d did not receive the event", c.userID)
		}
	}
	select {
	case msg := <-other.Messages():
		t.Fatalf("client in another workspace received %s", msg)
	default:
	}
}

func TestHubUnsubscribeAndCloseAreIdempotent(t *testing.T) {
	h := NewHub()
	c := h.Subscribe(1, 10)
	h.Unsubscribe(c)
	h.Unsubscribe(c) // must not panic on double close
	h.Close()

	if _, ok := <-c.Messages(); ok {
		t.Fatal("expected closed channel")
	}
}
