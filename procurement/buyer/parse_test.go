package buyer

import (
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/three-agent-problem/examples/procurement/protocol"
)

func TestParsePurchase(t *testing.T) {
	tests := []struct {
		text string
		want protocol.Purchase
	}{
		{
			text: "Order 200 hoodies, budget 6000 EUR, deliver by 2026-11-20, add to my calendar",
			want: protocol.Purchase{Item: "hoodie", Quantity: 200, Budget: 6000, DeliverBy: "2026-11-20", AddToCalendar: true},
		},
		{
			text: "Budget 6000 EUR for 200 branded t-shirts",
			want: protocol.Purchase{Item: "t-shirt", Quantity: 200, Budget: 6000},
		},
		{text: "need 50 mugs, budget €1,250", want: protocol.Purchase{Item: "mug", Quantity: 50, Budget: 1250}},
		{text: "order 300 caps with a budget of 6k", want: protocol.Purchase{Item: "cap", Quantity: 300, Budget: 6000}},
		{text: "Order 12 notebooks by 2026-13-40", want: protocol.Purchase{Item: "notebook", Quantity: 12}},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			got, err := parsePurchase(a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(tc.text)))
			if err != nil {
				t.Fatalf("parsePurchase() error = %v", err)
			}
			got.Text, got.Currency = "", ""
			if got != tc.want {
				t.Errorf("parsePurchase() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParsePurchaseNeedsAnItem(t *testing.T) {
	for _, text := range []string{"hello", "budget 6000 EUR", "deliver in 21 days"} {
		if p, err := parsePurchase(a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))); err == nil {
			t.Errorf("parsePurchase(%q) = %+v, want an error", text, p)
		}
	}
}

func TestChoose(t *testing.T) {
	fast := protocol.Quote{Supplier: "Fast", Total: 5400, ShipDate: "2026-11-05"}
	cheap := protocol.Quote{Supplier: "Cheap", Total: 4500, ShipDate: "2026-11-28"}
	tests := []struct {
		name         string
		purchase     protocol.Purchase
		wantSupplier string
		wantApproval bool
	}{
		{name: "deadline excludes the cheap one", purchase: protocol.Purchase{DeliverBy: "2026-11-19"}, wantSupplier: "Fast", wantApproval: true},
		{name: "no deadline picks the cheapest", purchase: protocol.Purchase{}, wantSupplier: "Cheap"},
		{name: "over budget needs approval", purchase: protocol.Purchase{Budget: 4000}, wantSupplier: "Cheap", wantApproval: true},
		{name: "nobody in time", purchase: protocol.Purchase{DeliverBy: "2026-11-01"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := protocol.Plan{Purchase: tc.purchase, Quotes: []protocol.Quote{fast, cheap}}
			choose(&plan, 5000)
			if tc.wantSupplier == "" {
				if plan.Chosen != nil {
					t.Fatalf("choose() picked %s, want none", plan.Chosen.Supplier)
				}
				return
			}
			if plan.Chosen == nil || plan.Chosen.Supplier != tc.wantSupplier || plan.NeedsApproval != tc.wantApproval {
				t.Errorf("choose() = %+v (approval %t), want %s (approval %t); reason %q", plan.Chosen, plan.NeedsApproval, tc.wantSupplier, tc.wantApproval, plan.Reason)
			}
		})
	}
}
