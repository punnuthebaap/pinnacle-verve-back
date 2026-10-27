package payment

import "testing"

func TestMockWebhookSignature(t *testing.T) {
	m := Mock{Secret: []byte("s3cret")}
	h, body, err := m.SignedEvent(Event{Type: EventCaptured, ProviderOrderID: "mock_order_1", AmountPaise: 100})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := m.ParseWebhook(h, body)
	if err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if ev.ProviderOrderID != "mock_order_1" || ev.Type != EventCaptured || ev.ID == "" {
		t.Fatalf("unexpected event %+v", ev)
	}

	tampered := append([]byte{}, body...)
	tampered[len(tampered)-2] = '9'
	if _, err := m.ParseWebhook(h, tampered); err != ErrBadSignature {
		t.Fatalf("tampered body accepted: %v", err)
	}
	if _, err := (Mock{Secret: []byte("other")}).ParseWebhook(h, body); err != ErrBadSignature {
		t.Fatalf("wrong secret accepted: %v", err)
	}
}
