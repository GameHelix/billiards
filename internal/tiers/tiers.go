// Package tiers defines what each subscription tier allows, as the plan the
// engine enforces. Payments are not here yet; tiers are set by the CLI.
package tiers

import (
	"fmt"
	"strings"

	"github.com/candylario/trader/api"
)

type Tier struct {
	Name  string
	Title string
	Plan  api.Plan
}

// Default is the tier of every new account.
const Default = "trial"

// All, cheapest first.
var All = []Tier{
	{Name: "trial", Title: "Trial", Plan: api.Plan{Symbols: []string{"BTCUSDT", "ETHUSDT"}, MaxAgents: 1, MinDecisionIntervalHours: 4, MaxEventDecisionsPerDay: 4}},
	{Name: "standard", Title: "Standard", Plan: api.Plan{Symbols: []string{"BTCUSDT", "ETHUSDT"}, MaxAgents: 1, MinDecisionIntervalHours: 4, MaxEventDecisionsPerDay: 8, Live: true}},
	{Name: "pro", Title: "Pro", Plan: api.Plan{Symbols: []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "XRPUSDT"}, MaxAgents: 3, MinDecisionIntervalHours: 1, MaxEventDecisionsPerDay: 8, Live: true}},
}

func Get(name string) (Tier, bool) {
	for _, t := range All {
		if t.Name == name {
			return t, true
		}
	}
	return Tier{}, false
}

func Names() []string {
	var names []string
	for _, t := range All {
		names = append(names, t.Name)
	}
	return names
}

// Coin is the base asset of a USDT pair: BTC for BTCUSDT.
func Coin(symbol string) string { return strings.TrimSuffix(symbol, "USDT") }

// Describe says what the tier allows in one line.
func (t Tier) Describe() string {
	var coins []string
	for _, s := range t.Plan.Symbols {
		coins = append(coins, Coin(s))
	}
	agents := "1 agent"
	if t.Plan.MaxAgents != 1 {
		agents = fmt.Sprintf("%d agents", t.Plan.MaxAgents)
	}
	money := "testnet only"
	if t.Plan.Live {
		money = "real money allowed"
	}
	return fmt.Sprintf("%s · %s · a decision every %gh or slower, plus %d a day on moves or news · %s",
		strings.Join(coins, ", "), agents, t.Plan.MinDecisionIntervalHours, t.Plan.MaxEventDecisionsPerDay, money)
}
