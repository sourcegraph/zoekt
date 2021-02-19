package stream

import "github.com/google/zoekt"

// Sender is the interface that wraps the basic Send method.
type Sender interface {
	Send(sr *zoekt.SearchResult)
}

// SenderFunc is an adapter to allow the use of ordinary functions as Senders. If
// f is a function with the appropriate signature, SenderFunc(f) is a Sender that
// calls f.
type SenderFunc func(result *zoekt.SearchResult)

func (f SenderFunc) Send(result *zoekt.SearchResult) {
	f(result)
}
