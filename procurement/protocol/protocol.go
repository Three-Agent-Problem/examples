// Package protocol holds the data the buyer and the suppliers exchange.
//
// A2A carries the messages; what goes inside is up to the agents. Here every request
// is one data part with a "request" field naming the operation, and replies are data
// parts too, so neither side has to parse prose.
package protocol

import (
	"encoding/json"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// Request kinds a supplier understands.
const (
	KindQuote = "quote"
	KindOrder = "order"
)

// Artifact names the agents use.
const (
	ArtifactQuoteComparison   = "quote-comparison"
	ArtifactFulfilment        = "fulfilment"
	ArtifactOrderConfirmation = "order-confirmation"
	ArtifactPurchaseOrder     = "purchase-order"
)

// DateLayout is the layout of every date field.
const DateLayout = "2006-01-02"

// Request asks a supplier for a quote or places an order against a quote.
type Request struct {
	Request   string `json:"request"`
	Item      string `json:"item"`
	Quantity  int    `json:"quantity"`
	DeliverBy string `json:"deliverBy,omitempty"`
	QuoteID   string `json:"quoteId,omitempty"`
	Buyer     string `json:"buyer,omitempty"`
}

// Quote is a supplier's price for a request.
type Quote struct {
	QuoteID    string  `json:"quoteId"`
	Supplier   string  `json:"supplier"`
	AgentURL   string  `json:"agentUrl"`
	Item       string  `json:"item"`
	Quantity   int     `json:"quantity"`
	UnitPrice  float64 `json:"unitPrice"`
	Total      float64 `json:"total"`
	Currency   string  `json:"currency"`
	LeadDays   int     `json:"leadDays"`
	ShipDate   string  `json:"shipDate"`
	ValidUntil string  `json:"validUntil"`
}

// OrderConfirmation is what a supplier returns once an order has shipped.
type OrderConfirmation struct {
	OrderID  string  `json:"orderId"`
	QuoteID  string  `json:"quoteId"`
	Total    float64 `json:"total"`
	Currency string  `json:"currency"`
	ShipDate string  `json:"shipDate"`
	Tracking string  `json:"tracking"`
}

// Purchase is what the requester asked the buyer for.
type Purchase struct {
	Text          string  `json:"text"`
	Item          string  `json:"item"`
	Quantity      int     `json:"quantity"`
	Budget        float64 `json:"budget,omitempty"`
	Currency      string  `json:"currency"`
	DeliverBy     string  `json:"deliverBy,omitempty"`
	AddToCalendar bool    `json:"addToCalendar"`
}

// Plan is the buyer's decision after comparing quotes. It is stored as the
// quote-comparison artifact, so the buyer can pick it up again after an approval.
type Plan struct {
	Purchase      Purchase `json:"purchase"`
	Quotes        []Quote  `json:"quotes"`
	Chosen        *Quote   `json:"chosen,omitempty"`
	Reason        string   `json:"reason"`
	NeedsApproval bool     `json:"needsApproval"`
}

// PurchaseOrder is the buyer's final result.
type PurchaseOrder struct {
	PONumber        string             `json:"poNumber"`
	Supplier        string             `json:"supplier"`
	Item            string             `json:"item"`
	Quantity        int                `json:"quantity"`
	Total           float64            `json:"total"`
	Currency        string             `json:"currency"`
	ApprovedBy      string             `json:"approvedBy"`
	SupplierTaskID  a2a.TaskID         `json:"supplierTaskId"`
	Confirmation    *OrderConfirmation `json:"confirmation,omitempty"`
	CalendarEventID string             `json:"calendarEventId,omitempty"`
	CalendarNote    string             `json:"calendarNote,omitempty"`
}

// Decode finds the first data part in parts and decodes it into v.
func Decode(parts []*a2a.Part, v any) bool {
	for _, p := range parts {
		if p == nil || p.Data() == nil {
			continue
		}
		raw, err := json.Marshal(p.Data())
		if err != nil {
			return false
		}
		return json.Unmarshal(raw, v) == nil
	}
	return false
}

// DataPart encodes v as a data part. Structs go through JSON first, so the part holds
// plain maps exactly like it will after crossing the wire.
func DataPart(v any) *a2a.Part {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err) // only called with the types above, which always marshal
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		panic(err)
	}
	return a2a.NewDataPart(data)
}

// FindArtifact returns the first artifact with the given name.
func FindArtifact(artifacts []*a2a.Artifact, name string) *a2a.Artifact {
	for _, a := range artifacts {
		if a != nil && a.Name == name {
			return a
		}
	}
	return nil
}

// AddDays returns the date n days after t, formatted with DateLayout.
func AddDays(t time.Time, n int) string {
	return t.AddDate(0, 0, n).Format(DateLayout)
}
