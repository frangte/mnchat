package realtime

import (
	"testing"

	"github.com/frangte/mnchat/internal/domain"
)

type fakeMetrics struct{ connected, disconnected, dropped int }

func (f *fakeMetrics) WSClientConnected()    { f.connected++ }
func (f *fakeMetrics) WSClientDisconnected() { f.disconnected++ }
func (f *fakeMetrics) WSClientDropped()      { f.dropped++ }

func TestHubPublishIsScopedToWorkspace(t *testing.T) {
	h := NewHub(&fakeMetrics{})
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

func TestHubPublishRespectsAudience(t *testing.T) {
	h := NewHub(&fakeMetrics{})
	member := h.Subscribe(1, 10)
	secondTab := h.Subscribe(1, 10)
	outsider := h.Subscribe(1, 11)

	h.Publish(1, domain.Event{Type: domain.EventMessageCreated, ChannelID: 5, Audience: []int64{10}})

	for _, c := range []*Client{member, secondTab} {
		select {
		case <-c.Messages():
		default:
			t.Fatal("member did not receive the event")
		}
	}
	select {
	case msg := <-outsider.Messages():
		t.Fatalf("outsider received %s", msg)
	default:
	}
}

func TestHubEmptyAudienceReachesNobody(t *testing.T) {
	h := NewHub(&fakeMetrics{})
	c := h.Subscribe(1, 10)

	h.Publish(1, domain.Event{Type: domain.EventMessageCreated, ChannelID: 5, Audience: []int64{}})

	select {
	case msg := <-c.Messages():
		t.Fatalf("client received %s", msg)
	default:
	}
}

func TestHubUnsubscribeAndCloseAreIdempotent(t *testing.T) {
	h := NewHub(&fakeMetrics{})
	c := h.Subscribe(1, 10)
	h.Unsubscribe(c)
	h.Unsubscribe(c) // must not panic on double close
	h.Close()

	if _, ok := <-c.Messages(); ok {
		t.Fatal("expected closed channel")
	}
}

func TestHubMetricsFollowClients(t *testing.T) {
	m := &fakeMetrics{}
	h := NewHub(m)
	a := h.Subscribe(1, 10)
	h.Subscribe(2, 11)

	h.Unsubscribe(a)
	h.Unsubscribe(a) // a repeat must not take the gauge below the real count
	if want := (fakeMetrics{connected: 2, disconnected: 1}); *m != want {
		t.Fatalf("metrics = %+v, want %+v", *m, want)
	}

	h.Close()
	if want := (fakeMetrics{connected: 2, disconnected: 2}); *m != want {
		t.Fatalf("after Close, metrics = %+v, want %+v", *m, want)
	}
}

func TestHubCountsDroppedClientOnce(t *testing.T) {
	m := &fakeMetrics{}
	h := NewHub(m)
	slow := h.Subscribe(1, 10)

	for range clientBuffer + 1 {
		h.Publish(1, domain.Event{Type: domain.EventMessageCreated, ChannelID: 5})
	}
	h.Unsubscribe(slow) // the websocket handler still unsubscribes after a drop

	if want := (fakeMetrics{connected: 1, disconnected: 1, dropped: 1}); *m != want {
		t.Fatalf("metrics = %+v, want %+v", *m, want)
	}
}
