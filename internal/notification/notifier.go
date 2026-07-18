package notification

import "context"

type Message struct {
	To    string
	Title string
	Body  string
	Data  map[string]string
}

// Notifier is the single interface for sending push notifications.
// Swap the implementation (Expo, FCM, APNs) without touching callers.
type Notifier interface {
	Send(ctx context.Context, msg Message) error
	SendBatch(ctx context.Context, msgs []Message) error
}
