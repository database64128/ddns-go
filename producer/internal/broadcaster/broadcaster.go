package broadcaster

import "github.com/database64128/ddns-go/producer"

// Broadcaster manages message broadcasting to subscribers.
//
// It is not safe for concurrent use. The methods must not be called concurrently.
type Broadcaster struct {
	subscribers []chan producer.Message // Intentionally not send only, so we can drain them.
}

// New creates a new [Broadcaster].
func New() *Broadcaster {
	return &Broadcaster{}
}

// Subscribe returns a subscriber channel for a new subscriber.
func (b *Broadcaster) Subscribe() <-chan producer.Message {
	ch := make(chan producer.Message, 1) // One slot for the latest message.
	b.subscribers = append(b.subscribers, ch)
	return ch
}

// Broadcast sends a message to all subscribers.
// It always drains the subscriber channel before sending, so the operation never blocks,
// and the subscriber can always receive the latest message.
func (b *Broadcaster) Broadcast(msg producer.Message) {
	for _, ch := range b.subscribers {
		select {
		case <-ch:
		default:
		}
		ch <- msg
	}
}
