// Package payment abstracts the payment gateway. The mock provider mirrors Razorpay's flow
// (create gateway order → customer pays → signed webhook) so swapping providers is configuration.
package payment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

var ErrBadSignature = errors.New("payment: invalid webhook signature")

type EventType string

const (
	EventCaptured EventType = "payment.captured"
	EventFailed   EventType = "payment.failed"
)

type Event struct {
	ID                string    `json:"id"`
	Type              EventType `json:"type"`
	ProviderOrderID   string    `json:"providerOrderId"`
	ProviderPaymentID string    `json:"providerPaymentId"`
	AmountPaise       int64     `json:"amountPaise"`
}

type Session struct {
	Provider        string
	ProviderOrderID string
	KeyID           string
}

type Provider interface {
	Name() string
	// CreateOrder opens a gateway order for amount; receipt is our order ref.
	CreateOrder(ctx context.Context, receipt string, amountPaise int64) (Session, error)
	// ParseWebhook authenticates and decodes a raw webhook body.
	ParseWebhook(h http.Header, body []byte) (Event, error)
}

const mockSignatureHeader = "X-Mock-Signature"

type Mock struct{ Secret []byte }

func (Mock) Name() string { return "mock" }

func (Mock) CreateOrder(_ context.Context, _ string, _ int64) (Session, error) {
	return Session{Provider: "mock", ProviderOrderID: "mock_order_" + randomHex(10)}, nil
}

func (m Mock) ParseWebhook(h http.Header, body []byte) (Event, error) {
	got, err := hex.DecodeString(h.Get(mockSignatureHeader))
	if err != nil || !hmac.Equal(got, m.sign(body)) {
		return Event{}, ErrBadSignature
	}
	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		return Event{}, fmt.Errorf("payment: decode webhook: %w", err)
	}
	return ev, nil
}

// SignedEvent builds the body and headers the mock gateway would send for ev.
func (m Mock) SignedEvent(ev Event) (http.Header, []byte, error) {
	if ev.ID == "" {
		ev.ID = "evt_" + randomHex(10)
	}
	if ev.ProviderPaymentID == "" && ev.Type == EventCaptured {
		ev.ProviderPaymentID = "pay_" + randomHex(10)
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return nil, nil, err
	}
	h := http.Header{}
	h.Set(mockSignatureHeader, hex.EncodeToString(m.sign(body)))
	return h, body, nil
}

func (m Mock) sign(body []byte) []byte {
	mac := hmac.New(sha256.New, m.Secret)
	mac.Write(body)
	return mac.Sum(nil)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
