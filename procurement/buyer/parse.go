package buyer

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/three-agent-problem/examples/procurement/protocol"
)

var (
	quantityItemRe = regexp.MustCompile(`(?i)\b(\d{1,6})\s+(?:branded\s+|custom\s+)?([a-z][a-z-]{1,30})`)
	budgetRe       = regexp.MustCompile(`(?i)budget\s*(?:of\s*)?(?:€|eur\s*)?\s*([\d.,]+)\s*(?:k\b)?`)
	dateRe         = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
)

// parsePurchase reads a request such as
// "Order 200 hoodies, budget 6000 EUR, deliver by 2026-11-20, add to my calendar".
// A data part with a protocol.Purchase is accepted as well.
func parsePurchase(msg *a2a.Message) (protocol.Purchase, error) {
	var p protocol.Purchase
	if protocol.Decode(msg.Parts, &p) && p.Item != "" && p.Quantity > 0 {
		p.Currency = "EUR"
		return p, nil
	}

	var texts []string
	for _, part := range msg.Parts {
		if part != nil && part.Text() != "" {
			texts = append(texts, part.Text())
		}
	}
	text := strings.Join(texts, " ")
	p = protocol.Purchase{Text: text, Currency: "EUR"}

	for _, m := range quantityItemRe.FindAllStringSubmatch(text, -1) {
		item := strings.ToLower(m[2])
		if notItems[item] {
			continue // "6000 EUR", "21 days"
		}
		p.Quantity, _ = strconv.Atoi(m[1])
		p.Item = singular(item)
		break
	}
	if p.Item == "" {
		return p, errors.New(`I could not find what to buy. Try "Order 200 hoodies, budget 6000 EUR, deliver by 2026-11-20".`)
	}

	if m := budgetRe.FindStringSubmatch(text); m != nil {
		amount := strings.ReplaceAll(m[1], ",", "")
		if v, err := strconv.ParseFloat(amount, 64); err == nil {
			if strings.HasSuffix(strings.ToLower(strings.TrimSpace(m[0])), "k") {
				v *= 1000
			}
			p.Budget = v
		}
	}
	if m := dateRe.FindStringSubmatch(text); m != nil {
		if _, err := time.Parse(protocol.DateLayout, m[1]); err == nil {
			p.DeliverBy = m[1]
		}
	}
	p.AddToCalendar = strings.Contains(strings.ToLower(text), "calendar")
	return p, nil
}

// notItems are words that follow a number without being something to buy.
var notItems = map[string]bool{
	"eur": true, "euro": true, "euros": true, "usd": true, "dollars": true,
	"k": true, "day": true, "days": true, "week": true, "weeks": true,
}

func singular(word string) string {
	switch {
	case strings.HasSuffix(word, "shirts"), strings.HasSuffix(word, "hoodies"), strings.HasSuffix(word, "caps"), strings.HasSuffix(word, "mugs"):
		return strings.TrimSuffix(word, "s")
	case len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss"):
		return strings.TrimSuffix(word, "s")
	}
	return word
}
