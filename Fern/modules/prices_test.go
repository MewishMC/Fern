package modules

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParsePricePair(t *testing.T) {
	cases := map[string]string{
		"usd/eur": "USD/EUR fx", "BTC-USD": "BTC/USD crypto", " eth eur ": "ETH/EUR crypto",
		"shiba-inu/usd": "SHIBA-INU/USD crypto", "SHIBA-INU-USD": "SHIBA-INU/USD crypto", "GBP:JPY": "GBP/JPY fx",
	}
	for in, want := range cases {
		p, ok := parsePricePair(in)
		kind := "fx"
		if p.Crypto {
			kind = "crypto"
		}
		if !ok || p.String()+" "+kind != want {
			t.Errorf("%q = %v %s %v, want %s", in, p, kind, ok, want)
		}
	}
	for _, bad := range []string{"", "USD", "USD/USD", "B/USD", "BTC/US$", "BTC/USDTTT", "a/b/c"} {
		if _, ok := parsePricePair(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if p, _ := parsePricePair("BTC/USD"); p.coinID() != "bitcoin" {
		t.Error("ticker not mapped to its CoinGecko id")
	}
}

func TestSanitizePrices(t *testing.T) {
	o := PriceOptions{Pairs: []string{"btc/usd", "BTC-USD", "nope", "USD/EUR", "a/b", "1/2", "ETH/USD", "SOL/USD", "XRP/USD", "ADA/USD", "DOGE/USD", "LTC/USD", "DOT/USD"}, IntervalMin: 0, Separator: "\n"}
	sanitizePrices(&o)
	if len(o.Pairs) != MaxPricePairs || o.Pairs[0] != "BTC/USD" || o.Pairs[1] != "USD/EUR" {
		t.Errorf("pairs %q", o.Pairs)
	}
	if o.IntervalMin != 2 || o.Separator != " · " {
		t.Errorf("sanitized %+v", o)
	}
}

func TestFormatPrice(t *testing.T) {
	for v, want := range map[float64]string{
		67123.4: "67,123", 1234.6: "1,235", 1234567: "1,234,567", 12.345: "12.35", 0.92134: "0.9213",
		0.0000123456: "0.00001235", 0: "0", -1500: "-1,500",
	} {
		if got := formatPrice(v); got != want {
			t.Errorf("%v = %q, want %q", v, got, want)
		}
	}
}

func TestFormatQuote(t *testing.T) {
	o := defaultPriceOptions()
	btc, _ := parsePricePair("BTC/USD")
	if got := formatQuote(o, btc, priceQuote{Price: 67123.4, Change: 2.14, HasChange: true}); got != "BTC $67,123 ▲2.1%" {
		t.Errorf("btc %q", got)
	}
	fx, _ := parsePricePair("USD/CAD")
	if got := formatQuote(o, fx, priceQuote{Price: 1.3712, Change: -0.31, HasChange: true}); got != "USD 1.37 CAD ▼0.3%" {
		t.Errorf("fx %q", got)
	}
	o.ShowArrow, o.ShowSymbol = false, false
	if got := formatQuote(o, btc, priceQuote{Price: 10, Change: -1, HasChange: true}); got != "BTC 10.00 USD -1.0%" {
		t.Errorf("plain %q", got)
	}
	o.ShowChange = false
	if got := formatQuote(o, btc, priceQuote{Price: 10, Change: -1, HasChange: true}); got != "BTC 10.00 USD" {
		t.Errorf("no change %q", got)
	}
}

func resetPrices(t *testing.T) {
	t.Helper()
	reset := func() {
		priceMu.Lock()
		priceQuotes, priceErrs = map[string]priceQuote{}, map[string]string{}
		priceFX, priceCrypto = priceFeed{}, priceFeed{}
		priceMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func TestPriceTickFetchesAndBacksOff(t *testing.T) {
	resetPrices(t)
	var fxHits, cgHits atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/fx/"):
			fxHits.Add(1)
			if r.URL.Query().Get("base") != "USD" || r.URL.Query().Get("symbols") != "EUR" || !strings.HasSuffix(r.URL.Path, "..") {
				t.Errorf("fx query %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"base":"USD","rates":{"2026-11-13":{"EUR":0.9},"2026-11-12":{"EUR":0.8},"2026-11-16":{"EUR":0.99}}}`))
		case r.URL.Path == "/cg/simple/price":
			cgHits.Add(1)
			if fail.Load() {
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			q := r.URL.Query()
			if q.Get("ids") != "bitcoin,nosuchcoin" || q.Get("vs_currencies") != "usd" || q.Get("include_24hr_change") != "true" {
				t.Errorf("coingecko query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"bitcoin":{"usd":67000,"usd_24h_change":-1.26}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldFX, oldCG := frankfurterBase, coinGeckoBase
	frankfurterBase, coinGeckoBase = srv.URL+"/fx", srv.URL+"/cg"
	t.Cleanup(func() { frankfurterBase, coinGeckoBase = oldFX, oldCG })

	c := DefaultConfig()
	c.DisplayOptions.Prices = true
	c.PriceOptions.Pairs = []string{"BTC/USD", "USD/EUR", "NOSUCHCOIN/USD"}
	useConfig(t, c)
	now := time.Now()
	priceTick(now)
	if got := pricesText(c, now); got != "BTC $67,000 ▼1.3% · USD €0.9900 ▲10.0%" {
		t.Errorf("line %q", got)
	}
	st := CurrentPricesStatus()
	if st.State != "ok" || len(st.Pairs) != 3 || st.Pairs[2].Error != "not found" {
		t.Errorf("status %+v", st)
	}
	// Nothing is due a minute later.
	priceTick(now.Add(time.Minute))
	if fxHits.Load() != 1 || cgHits.Load() != 1 {
		t.Errorf("refetched early: fx %d, crypto %d", fxHits.Load(), cgHits.Load())
	}
	// Crypto is due after its interval; a 429 backs off for at least 5 minutes
	// and keeps the old price.
	fail.Store(true)
	later := now.Add(11 * time.Minute)
	priceTick(later)
	priceTick(later.Add(2 * time.Minute))
	if cgHits.Load() != 2 || fxHits.Load() != 1 {
		t.Errorf("after 429: crypto %d fx %d", cgHits.Load(), fxHits.Load())
	}
	if CurrentPricesStatus().State != "limited" || !strings.Contains(pricesText(c, later), "BTC $67,000") {
		t.Error("rate limit not reported, or the old price was dropped")
	}
	fail.Store(false)
	priceTick(later.Add(6 * time.Minute))
	if cgHits.Load() != 3 || CurrentPricesStatus().State != "ok" {
		t.Errorf("no recovery: %d", cgHits.Load())
	}
	if p, ok := priceNow("btc-usd"); !ok || p != 67000 {
		t.Errorf("priceNow %v %v", p, ok)
	}
}
