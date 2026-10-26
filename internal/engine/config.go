// Package engine builds what the app sends to the engine and reads what
// comes back: new agents' configs, and each agent's events for the
// dashboard.
package engine

import "encoding/json"

// Settings are the choices a user makes for a new agent. Everything else
// in its config is the engine's default.
type Settings struct {
	Name                    string
	Symbols                 []string
	CapitalUSD              float64
	DecisionIntervalHours   float64
	MaxEventDecisionsPerDay int
}

type agentConfig struct {
	Name                        string   `json:"name"`
	Symbols                     []string `json:"symbols"`
	QuoteAsset                  string   `json:"quote_asset"`
	CapitalUSD                  float64  `json:"capital_usd"`
	MaxPositionWeight           float64  `json:"max_position_weight"`
	MaxInvestedWeight           float64  `json:"max_invested_weight"`
	RebalanceBand               float64  `json:"rebalance_band"`
	MinTradeUSD                 float64  `json:"min_trade_usd"`
	DecisionIntervalHours       float64  `json:"decision_interval_hours"`
	MoveTrigger1HPct            float64  `json:"move_trigger_1h_pct"`
	MoveTriggerSinceDecisionPct float64  `json:"move_trigger_since_decision_pct"`
	EventCooldownMinutes        int      `json:"event_cooldown_minutes"`
	PollSeconds                 int      `json:"poll_seconds"`
	NewsFeeds                   []string `json:"news_feeds"`
	NewsAlertKeywords           []string `json:"news_alert_keywords"`
	MaxEventDecisionsPerDay     int      `json:"max_event_decisions_per_day"`
	NewsItems                   int      `json:"news_items"`
	ScoreHorizonHours           int      `json:"score_horizon_hours"`
	DailyLossLimitPct           float64  `json:"daily_loss_limit_pct"`
	MaxDrawdownPct              float64  `json:"max_drawdown_pct"`
	Effort                      string   `json:"effort"`
	MaxOutputTokens             int      `json:"max_output_tokens"`
	MaxTurns                    int      `json:"max_turns"`
	Samples                     int      `json:"samples"`
	RefusalFallback             bool     `json:"refusal_fallback"`
	InputUSDPerMillion          float64  `json:"input_usd_per_million"`
	OutputUSDPerMillion         float64  `json:"output_usd_per_million"`
	CacheWriteUSDPerMillion     float64  `json:"cache_write_usd_per_million"`
	CacheReadUSDPerMillion      float64  `json:"cache_read_usd_per_million"`
	MaxDailyLLMUSD              float64  `json:"max_daily_llm_usd"`
	MaxMonthlyLLMUSD            float64  `json:"max_monthly_llm_usd"`
}

// MinCapitalUSD is the least capital with which a buy can still place an
// order under the default weights and minimum trade.
const MinCapitalUSD = 50

// AgentConfig is a new agent's config: the engine's defaults with the
// user's choices. The engine adds the model and the data folder.
func AgentConfig(s Settings) json.RawMessage {
	c := agentConfig{
		Name:                        s.Name,
		Symbols:                     s.Symbols,
		QuoteAsset:                  "USDT",
		CapitalUSD:                  s.CapitalUSD,
		MaxPositionWeight:           0.45,
		MaxInvestedWeight:           0.9,
		RebalanceBand:               0.05,
		MinTradeUSD:                 10,
		DecisionIntervalHours:       s.DecisionIntervalHours,
		MoveTrigger1HPct:            3,
		MoveTriggerSinceDecisionPct: 5,
		EventCooldownMinutes:        60,
		PollSeconds:                 30,
		NewsFeeds: []string{
			"https://www.coindesk.com/arc/outboundfeeds/rss/",
			"https://cointelegraph.com/rss",
			"https://decrypt.co/feed",
			"https://www.theblock.co/rss.xml",
			"https://bitcoinmagazine.com/.rss/full/",
			"https://cryptoslate.com/feed/",
		},
		NewsAlertKeywords: []string{"etf", "sec", "hack", "hacked", "exploit", "ban", "lawsuit", "liquidations", "crash", "plunge", "plunges",
			"all-time high", "record high", "fed", "rate cut", "rate hike", "approval", "approves", "blackrock", "reserve", "tariffs", "bankruptcy", "halts"},
		MaxEventDecisionsPerDay: min(8, s.MaxEventDecisionsPerDay),
		NewsItems:               12,
		ScoreHorizonHours:       24,
		DailyLossLimitPct:       10,
		MaxDrawdownPct:          30,
		Effort:                  "high",
		MaxOutputTokens:         16000,
		MaxTurns:                8,
		Samples:                 1,
		InputUSDPerMillion:      4,
		OutputUSDPerMillion:     20,
		CacheWriteUSDPerMillion: 5,
		CacheReadUSDPerMillion:  0.2,
		MaxDailyLLMUSD:          5,
		MaxMonthlyLLMUSD:        100,
	}
	raw, _ := json.Marshal(c)
	return raw
}
