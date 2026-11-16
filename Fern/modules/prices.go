package modules

// The price line: currency exchange rates from Frankfurter (the European
// Central Bank's reference rates) and crypto prices from CoinGecko's simple
// price endpoint. Neither needs a key.

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ModulePrices is the price line's id.
const ModulePrices = "prices"

// MaxPricePairs is how many pairs the line can show.
const MaxPricePairs = 8

// Endpoints, variables so tests can use a local server.
var (
	frankfurterBase = "https://api.frankfurter.dev/v1"
	coinGeckoBase   = "https://api.coingecko.com/api/v3"
)

// PriceOptions controls the price line.
type PriceOptions struct {
	Pairs       []string `json:"pairs"` // "USD/EUR", "BTC/USD"
	ShowChange  bool     `json:"show_change"`
	ShowArrow   bool     `json:"show_arrow"`
	ShowSymbol  bool     `json:"show_symbol"` // $ € £ instead of the currency code
	Separator   string   `json:"separator"`
	IntervalMin int      `json:"interval_min"` // minutes between crypto refreshes
	Label       string   `json:"label"`
}

func defaultPriceOptions() PriceOptions {
	return PriceOptions{
		Pairs:       []string{"BTC/USD", "USD/EUR"},
		ShowChange:  true,
		ShowArrow:   true,
		ShowSymbol:  true,
		Separator:   " · ",
		IntervalMin: 10,
	}
}

func sanitizePrices(o *PriceOptions) {
	pairs := make([]string, 0, len(o.Pairs))
	seen := map[string]bool{}
	for _, raw := range o.Pairs {
		p, ok := parsePricePair(raw)
		if !ok || seen[p.String()] || len(pairs) >= MaxPricePairs {
			continue
		}
		seen[p.String()] = true
		pairs = append(pairs, p.String())
	}
	o.Pairs = pairs
	o.Separator = RuneLimit(strings.ReplaceAll(o.Separator, "\n", " "), 8)
	if strings.TrimSpace(o.Separator) == "" {
		o.Separator = " · "
	}
	o.IntervalMin = clampInt(o.IntervalMin, 2, 120)
	o.Label = RuneLimit(strings.TrimSpace(o.Label), 16)
}

// fiatCurrencies are the currencies Frankfurter publishes.
var fiatCurrencies = map[string]bool{
	"AUD": true, "BGN": true, "BRL": true, "CAD": true, "CHF": true, "CNY": true, "CZK": true,
	"DKK": true, "EUR": true, "GBP": true, "HKD": true, "HUF": true, "IDR": true, "ILS": true,
	"INR": true, "ISK": true, "JPY": true, "KRW": true, "MXN": true, "MYR": true, "NOK": true,
	"NZD": true, "PHP": true, "PLN": true, "RON": true, "SEK": true, "SGD": true, "THB": true,
	"TRY": true, "USD": true, "ZAR": true,
}

// coinIDs maps common tickers to CoinGecko ids. Other coins can be named by
// their CoinGecko id, like "shiba-inu/USD".
var coinIDs = map[string]string{
	"BTC": "bitcoin", "ETH": "ethereum", "SOL": "solana", "XRP": "ripple", "ADA": "cardano",
	"DOGE": "dogecoin", "LTC": "litecoin", "DOT": "polkadot", "BNB": "binancecoin",
	"XMR": "monero", "TRX": "tron", "AVAX": "avalanche-2", "LINK": "chainlink",
	"USDT": "tether", "USDC": "usd-coin", "TON": "the-open-network", "SHIB": "shiba-inu",
	"XLM": "stellar", "BCH": "bitcoin-cash", "ATOM": "cosmos", "PEPE": "pepe",
}

// currencySymbols are drawn instead of the code when ShowSymbol is on.
var currencySymbols = map[string]string{
	"USD": "$", "EUR": "€", "GBP": "£", "JPY": "¥", "KRW": "₩", "INR": "₹", "CNY": "¥",
	"ILS": "₪", "PHP": "₱", "TRY": "₺", "THB": "฿", "BTC": "₿",
}

// pricePair is one parsed pair.
type pricePair struct {
	Base, Quote string // upper case
	Crypto      bool
}

func (p pricePair) String() string { return p.Base + "/" + p.Quote }

// coinID is the CoinGecko id of a crypto pair's base.
func (p pricePair) coinID() string {
	if id, ok := coinIDs[p.Base]; ok {
		return id
	}
	return strings.ToLower(p.Base)
}

// parsePricePair reads "USD/EUR", "btc-usd" or "BTC USD". Two currencies
// Frankfurter knows make an exchange rate; anything else is a coin priced in
// the quote currency.
func parsePricePair(raw string) (pricePair, bool) {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	sep := strings.IndexAny(raw, "/ :")
	if sep < 0 {
		// "BTC-USD": the last hyphen, so ids like "SHIBA-INU-USD" still work.
		sep = strings.LastIndex(raw, "-")
	}
	if sep < 0 {
		return pricePair{}, false
	}
	base, quote := strings.TrimSpace(raw[:sep]), strings.TrimSpace(raw[sep+1:])
	if !validPriceCode(base, 40) || !validPriceCode(quote, 5) || strings.Contains(quote, "-") || base == quote {
		return pricePair{}, false
	}
	p := pricePair{Base: base, Quote: quote}
	p.Crypto = !(fiatCurrencies[base] && fiatCurrencies[quote])
	return p, true
}

func validPriceCode(s string, max int) bool {
	if len(s) < 2 || len(s) > max {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// priceQuote is the latest price of one pair.
type priceQuote struct {
	Price     float64
	Change    float64 // percent
	HasChange bool
	At        time.Time
}

// PriceStatus is one pair, for the panel.
type PriceStatus struct {
	Pair      string  `json:"pair"`
	Kind      string  `json:"kind"` // fx or crypto
	Text      string  `json:"text"`
	Price     float64 `json:"price"`
	Change    float64 `json:"change"`
	HasChange bool    `json:"hasChange"`
	Error     string  `json:"error"`
	UpdatedMS int64   `json:"updatedMs"`
}

// PricesStatus is what the panel shows about the price line.
type PricesStatus struct {
	State string        `json:"state"` // off, nopairs, loading, ok, offline, limited
	Pairs []PriceStatus `json:"pairs"`
	Line  string        `json:"line"`
}

// priceFeed is one provider's refresh and backoff state.
type priceFeed struct {
	fetched time.Time
	retryAt time.Time
	fails   int
	err     error
	sig     string // the pairs the last fetch asked for
}

var (
	priceMu     sync.RWMutex
	priceQuotes = map[string]priceQuote{}
	priceErrs   = map[string]string{} // per pair: not found
	priceFX     priceFeed
	priceCrypto priceFeed
	priceWake   = make(chan struct{}, 1)
)

// priceStale is when an old price stops being shown.
const priceStale = 6 * time.Hour

// fxInterval is how often exchange rates are fetched: the ECB publishes once
// a working day, so more often only wastes requests.
const fxInterval = time.Hour

func startPriceWorker() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			priceTick(time.Now())
			select {
			case <-ticker.C:
			case <-priceWake:
			}
		}
	}()
}

// RefreshPrices fetches every pair again now.
func RefreshPrices() {
	priceMu.Lock()
	priceFX, priceCrypto = priceFeed{}, priceFeed{}
	priceMu.Unlock()
	select {
	case priceWake <- struct{}{}:
	default:
	}
}

func configuredPairs(o PriceOptions) (fx, crypto []pricePair) {
	for _, raw := range o.Pairs {
		if p, ok := parsePricePair(raw); ok {
			if p.Crypto {
				crypto = append(crypto, p)
			} else {
				fx = append(fx, p)
			}
		}
	}
	return fx, crypto
}

func pairsSig(pairs []pricePair) string {
	s := make([]string, len(pairs))
	for i, p := range pairs {
		s[i] = p.String()
	}
	sort.Strings(s)
	return strings.Join(s, ",")
}

// priceTick refreshes whichever provider is due. It runs on the price worker
// only.
func priceTick(now time.Time) {
	cfg := GetConfig()
	if !cfg.DisplayOptions.Prices {
		return
	}
	fx, crypto := configuredPairs(cfg.PriceOptions)
	cryptoEvery := time.Duration(cfg.PriceOptions.IntervalMin) * time.Minute
	if len(fx) > 0 && priceDue(&priceFX, pairsSig(fx), now, fxInterval) {
		quotes, missing, err := fetchFX(fx, now)
		priceStore(&priceFX, pairsSig(fx), now, fxInterval, quotes, missing, err)
	}
	if len(crypto) > 0 && priceDue(&priceCrypto, pairsSig(crypto), now, cryptoEvery) {
		quotes, missing, err := fetchCrypto(crypto, now)
		priceStore(&priceCrypto, pairsSig(crypto), now, cryptoEvery, quotes, missing, err)
	}
}

func priceDue(f *priceFeed, sig string, now time.Time, every time.Duration) bool {
	priceMu.RLock()
	defer priceMu.RUnlock()
	if f.sig != sig {
		return !now.Before(f.retryAt) || f.fetched.IsZero()
	}
	return !now.Before(f.retryAt) && now.Sub(f.fetched) >= every
}

// priceStore keeps a fetch's results. Failures back off from one minute up to
// the refresh interval, longer when the provider asked Fern to slow down.
func priceStore(f *priceFeed, sig string, now time.Time, every time.Duration, quotes map[string]priceQuote, missing []string, err error) {
	priceMu.Lock()
	defer priceMu.Unlock()
	f.sig = sig
	if err != nil {
		f.fails++
		wait := time.Minute << min(f.fails-1, 5)
		if wait > every {
			wait = every
		}
		if err == errRateLimited && wait < 5*time.Minute {
			wait = 5 * time.Minute
		}
		f.retryAt, f.err = now.Add(wait), err
		return
	}
	f.fails, f.err, f.retryAt, f.fetched = 0, nil, time.Time{}, now
	for k, q := range quotes {
		priceQuotes[k] = q
		delete(priceErrs, k)
	}
	for _, k := range missing {
		priceErrs[k] = "not found"
	}
}

// fetchFX reads today's rates and the previous working day's from one
// Frankfurter time series per base currency.
func fetchFX(pairs []pricePair, now time.Time) (map[string]priceQuote, []string, error) {
	byBase := map[string][]string{}
	for _, p := range pairs {
		byBase[p.Base] = append(byBase[p.Base], p.Quote)
	}
	out := map[string]priceQuote{}
	var missing []string
	bases := make([]string, 0, len(byBase))
	for b := range byBase {
		bases = append(bases, b)
	}
	sort.Strings(bases)
	start := now.UTC().AddDate(0, 0, -7).Format("2006-01-02")
	for _, base := range bases {
		v := url.Values{}
		v.Set("base", base)
		v.Set("symbols", strings.Join(byBase[base], ","))
		var reply struct {
			Rates map[string]map[string]float64 `json:"rates"`
		}
		status, _, err := getJSON(frankfurterBase+"/"+start+"..?"+v.Encode(), &reply)
		if err != nil {
			return nil, nil, err
		}
		if status != 200 {
			for _, q := range byBase[base] {
				missing = append(missing, base+"/"+q)
			}
			continue
		}
		dates := make([]string, 0, len(reply.Rates))
		for d := range reply.Rates {
			dates = append(dates, d)
		}
		sort.Strings(dates)
		for _, q := range byBase[base] {
			key := base + "/" + q
			var vals []float64
			for _, d := range dates {
				if r, ok := reply.Rates[d][q]; ok && r > 0 {
					vals = append(vals, r)
				}
			}
			if len(vals) == 0 {
				missing = append(missing, key)
				continue
			}
			pq := priceQuote{Price: vals[len(vals)-1], At: now}
			if len(vals) >= 2 {
				prev := vals[len(vals)-2]
				pq.Change, pq.HasChange = (pq.Price-prev)/prev*100, true
			}
			out[key] = pq
		}
	}
	return out, missing, nil
}

// fetchCrypto reads every coin in one CoinGecko request.
func fetchCrypto(pairs []pricePair, now time.Time) (map[string]priceQuote, []string, error) {
	ids, vs := []string{}, []string{}
	seenID, seenVS := map[string]bool{}, map[string]bool{}
	for _, p := range pairs {
		if id := p.coinID(); !seenID[id] {
			seenID[id] = true
			ids = append(ids, id)
		}
		if q := strings.ToLower(p.Quote); !seenVS[q] {
			seenVS[q] = true
			vs = append(vs, q)
		}
	}
	v := url.Values{}
	v.Set("ids", strings.Join(ids, ","))
	v.Set("vs_currencies", strings.Join(vs, ","))
	v.Set("include_24hr_change", "true")
	var reply map[string]map[string]float64
	status, _, err := getJSON(coinGeckoBase+"/simple/price?"+v.Encode(), &reply)
	if err != nil {
		return nil, nil, err
	}
	if status != 200 {
		return nil, nil, httpStatusError(status)
	}
	out := map[string]priceQuote{}
	var missing []string
	for _, p := range pairs {
		coin := reply[p.coinID()]
		q := strings.ToLower(p.Quote)
		price, ok := coin[q]
		if !ok || price <= 0 {
			missing = append(missing, p.String())
			continue
		}
		pq := priceQuote{Price: price, At: now}
		if ch, ok := coin[q+"_24h_change"]; ok && !math.IsNaN(ch) {
			pq.Change, pq.HasChange = ch, true
		}
		out[p.String()] = pq
	}
	return out, missing, nil
}

// formatPrice rounds a price to a sensible number of digits and groups the
// thousands: 67,123 · 1,234.50 · 0.9213 · 0.00001234.
func formatPrice(v float64) string {
	a := math.Abs(v)
	var s string
	switch {
	case a >= 1000:
		s = strconv.FormatFloat(v, 'f', 0, 64)
	case a >= 1:
		s = strconv.FormatFloat(v, 'f', 2, 64)
	case a >= 0.01:
		s = strconv.FormatFloat(v, 'f', 4, 64)
	case a == 0:
		return "0"
	default:
		// Four significant digits.
		digits := int(-math.Floor(math.Log10(a))) + 3
		s = strconv.FormatFloat(v, 'f', min(digits, 12), 64)
	}
	whole, frac, _ := strings.Cut(s, ".")
	neg := strings.HasPrefix(whole, "-")
	whole = strings.TrimPrefix(whole, "-")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// formatQuote draws one pair, like "BTC $67,123 ▲2.1%" or "USD €0.92 ▼0.3%".
func formatQuote(o PriceOptions, p pricePair, q priceQuote) string {
	price := formatPrice(q.Price)
	if sym, ok := currencySymbols[p.Quote]; ok && o.ShowSymbol {
		price = sym + price
	} else {
		price += " " + p.Quote
	}
	s := p.Base + " " + price
	if o.ShowChange && q.HasChange {
		ch := math.Round(q.Change*10) / 10
		mark := ""
		switch {
		case o.ShowArrow && ch > 0:
			mark = "▲"
		case o.ShowArrow && ch < 0:
			mark = "▼"
		case !o.ShowArrow && ch > 0:
			mark = "+"
		case !o.ShowArrow && ch < 0:
			mark = "-"
		}
		if ch == 0 && o.ShowArrow {
			mark = "▬"
		}
		s += " " + mark + strconv.FormatFloat(math.Abs(ch), 'f', 1, 64) + "%"
	}
	return s
}

// pricesText is the line: every pair with a recent price, in the order set.
func pricesText(cfg Config, now time.Time) string {
	if !cfg.DisplayOptions.Prices {
		return ""
	}
	o := cfg.PriceOptions
	priceMu.RLock()
	defer priceMu.RUnlock()
	var parts []string
	for _, raw := range o.Pairs {
		p, ok := parsePricePair(raw)
		if !ok {
			continue
		}
		q, ok := priceQuotes[p.String()]
		if !ok || now.Sub(q.At) > priceStale {
			continue
		}
		parts = append(parts, formatQuote(o, p, q))
	}
	return strings.Join(parts, o.Separator)
}

func pricesLine(cfg Config) string {
	return withLabel(cfg.PriceOptions.Label, pricesText(cfg, time.Now()))
}

// CurrentPricesStatus reports the price line for the panel.
func CurrentPricesStatus() PricesStatus {
	cfg := GetConfig()
	now := time.Now()
	st := PricesStatus{State: "loading", Pairs: []PriceStatus{}}
	priceMu.RLock()
	have := false
	for _, raw := range cfg.PriceOptions.Pairs {
		p, ok := parsePricePair(raw)
		if !ok {
			continue
		}
		ps := PriceStatus{Pair: p.String(), Kind: "fx"}
		if p.Crypto {
			ps.Kind = "crypto"
		}
		if q, ok := priceQuotes[p.String()]; ok {
			ps.Price, ps.Change, ps.HasChange, ps.UpdatedMS = q.Price, q.Change, q.HasChange, q.At.UnixMilli()
			ps.Text = formatQuote(cfg.PriceOptions, p, q)
			have = true
		}
		ps.Error = priceErrs[p.String()]
		st.Pairs = append(st.Pairs, ps)
	}
	feedErr := priceFX.err
	if feedErr == nil {
		feedErr = priceCrypto.err
	}
	priceMu.RUnlock()
	switch {
	case !cfg.DisplayOptions.Prices:
		st.State = "off"
	case len(st.Pairs) == 0:
		st.State = "nopairs"
	case feedErr == errRateLimited:
		st.State = "limited"
	case feedErr != nil:
		st.State = "offline"
	case have:
		st.State = "ok"
	}
	st.Line = pricesText(cfg, now)
	return st
}

// priceNow is the latest price of a pair, for rules.
func priceNow(pair string) (float64, bool) {
	p, ok := parsePricePair(pair)
	if !ok {
		return 0, false
	}
	priceMu.RLock()
	defer priceMu.RUnlock()
	q, ok := priceQuotes[p.String()]
	if !ok || nowFunc().Sub(q.At) > priceStale {
		return 0, false
	}
	return q.Price, true
}

// Rule ids for prices.
const (
	TriggerPrice = "price.level"
	SourcePrices = "price_pairs"
)

func init() {
	lineLabels[ModulePrices] = "Prices"
	RegisterRuleSource(SourcePrices, func() []RuleOption {
		pairs := GetConfig().PriceOptions.Pairs
		out := make([]RuleOption, 0, len(pairs))
		for _, p := range pairs {
			out = append(out, RuleOption{Value: p, Label: p})
		}
		return out
	})
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerPrice, Label: "Price above or below", Category: "Prices",
		Description: fmt.Sprintf("While a pair on the price line is above or below a value. Prices refresh every few minutes; up to %d pairs.", MaxPricePairs),
		Fields: []RuleField{
			{Key: "pair", Label: "Pair", Type: FieldSelect, Source: SourcePrices, Placeholder: "Pick a pair"},
			{Key: "dir", Label: "When", Type: FieldSelect, Options: []RuleOption{{Value: "above", Label: "above"}, {Value: "below", Label: "below"}}, Default: "above"},
			{Key: "value", Label: "Value", Type: FieldNumber, Default: 0.0, Min: 0, Max: 1e12},
		},
		Check: func(p RuleParams) bool {
			price, ok := priceNow(p.String("pair"))
			if !ok {
				return false
			}
			if p.String("dir") == "below" {
				return price < p.Number("value")
			}
			return price > p.Number("value")
		},
	}))
}
