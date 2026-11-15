package modules

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFormatWeather(t *testing.T) {
	w := weatherNow{Temp: 12.4, Feels: 10.06, Humidity: 81.2, Wind: 18, Code: 2, Day: true}
	o := DefaultConfig().WeatherOptions
	if got := formatWeather(o, w); got != "⛅ Partly cloudy 12°C" {
		t.Errorf("default: got %q", got)
	}
	o.ShowFeels, o.ShowHumidity, o.ShowWind = true, true, true
	if got := formatWeather(o, w); got != "⛅ Partly cloudy 12°C · feels 10°C · 💧81% · 💨18 km/h" {
		t.Errorf("everything: got %q", got)
	}
	o.TempUnit, o.WindUnit, o.Decimals = "f", "mph", 1
	if got := formatWeather(o, w); got != "⛅ Partly cloudy 54.3°F · feels 50.1°F · 💧81% · 💨11.2 mph" {
		t.Errorf("imperial: got %q", got)
	}
	o = WeatherOptions{ShowTemp: true, ShowWind: true, WindUnit: "ms", TempUnit: "c"}
	if got := formatWeather(o, weatherNow{Temp: -0.3, Wind: 18, Code: 0}); got != "0°C · 💨5 m/s" {
		t.Errorf("no emoji, m/s, no -0: got %q", got)
	}
	o = WeatherOptions{ShowEmoji: true, ShowCondition: true}
	if got := formatWeather(o, weatherNow{Code: 0, Day: false}); got != "🌙 Clear" {
		t.Errorf("night: got %q", got)
	}
}

func TestWeatherConditionCoversCodes(t *testing.T) {
	for _, code := range []int{0, 1, 2, 3, 45, 48, 51, 53, 55, 56, 57, 61, 63, 65, 66, 67, 71, 73, 75, 77, 80, 81, 82, 85, 86, 95, 96, 99} {
		if text, emoji := weatherCondition(code, true); text == "" || emoji == "" {
			t.Errorf("code %d has no name", code)
		}
	}
}

// useConfig makes c the live configuration for a test without saving it.
func useConfig(t *testing.T, c Config) {
	t.Helper()
	cfgMu.Lock()
	old := cfg
	cfg = c
	cfgMu.Unlock()
	t.Cleanup(func() {
		cfgMu.Lock()
		cfg = old
		cfgMu.Unlock()
	})
}

// fakeOpenMeteo serves the geocoding and forecast endpoints.
func fakeOpenMeteo(t *testing.T, geo, forecast func(w http.ResponseWriter, r *http.Request)) (*atomic.Int32, *atomic.Int32) {
	t.Helper()
	var geoHits, fcHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "Fern" {
			t.Errorf("User-Agent %q", ua)
		}
		if r.URL.Path == "/geo" {
			geoHits.Add(1)
			geo(w, r)
			return
		}
		fcHits.Add(1)
		forecast(w, r)
	}))
	t.Cleanup(srv.Close)
	oldGeo, oldFc := weatherGeoBase, weatherForecastBase
	weatherGeoBase, weatherForecastBase = srv.URL+"/geo", srv.URL+"/forecast"
	t.Cleanup(func() { weatherGeoBase, weatherForecastBase = oldGeo, oldFc })
	weatherMu.Lock()
	weatherCity, weatherState = "\x00", weatherOff
	weatherMu.Unlock()
	return &geoHits, &fcHits
}

const londonGeo = `{"results":[
 {"name":"London","latitude":42.98,"longitude":-81.23,"country":"Canada","country_code":"CA","admin1":"Ontario"},
 {"name":"London","latitude":51.51,"longitude":-0.13,"country":"United Kingdom","country_code":"GB","admin1":"England"}]}`

const sunnyForecast = `{"current":{"temperature_2m":21.4,"apparent_temperature":20.9,"relative_humidity_2m":40,"weather_code":0,"wind_speed_10m":7.2,"is_day":1}}`

func TestLocateWeather(t *testing.T) {
	fakeOpenMeteo(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == "Nowhere" {
			_, _ = w.Write([]byte(`{"generationtime_ms":0.5}`))
			return
		}
		_, _ = w.Write([]byte(londonGeo))
	}, nil)
	p, ok, err := locateWeather("London")
	if err != nil || !ok || p.name != "London, Ontario, Canada" {
		t.Errorf("first match: %+v %v %v", p, ok, err)
	}
	p, ok, _ = locateWeather("London, GB")
	if !ok || p.lat != 51.51 || p.name != "London, England, United Kingdom" {
		t.Errorf("country hint: %+v %v", p, ok)
	}
	if _, ok, err := locateWeather("Nowhere"); ok || err != nil {
		t.Errorf("unknown place: %v %v", ok, err)
	}
	p, ok, _ = locateWeather(" 48.8566, 2.3522 ")
	if !ok || p.lat != 48.8566 || p.lon != 2.3522 {
		t.Errorf("coordinates: %+v %v", p, ok)
	}
}

func TestWeatherTick(t *testing.T) {
	geoHits, fcHits := fakeOpenMeteo(t,
		func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(londonGeo)) },
		func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Get("latitude") != "51.5100" || !strings.Contains(q.Get("current"), "weather_code") {
				t.Errorf("forecast query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(sunnyForecast))
		})
	c := DefaultConfig()
	c.DisplayOptions.Weather = true
	c.WeatherOptions.City = "London, United Kingdom"
	useConfig(t, c)

	now := time.Now()
	weatherTick(now)
	if st := CurrentWeatherStatus(); st.State != weatherOK || st.Line != "☀️ Clear 21°C" || st.Place != "London, England, United Kingdom" {
		t.Fatalf("after first tick: %+v", st)
	}
	// Nothing is asked again until the refresh interval has passed.
	weatherTick(now.Add(5 * time.Minute))
	if geoHits.Load() != 1 || fcHits.Load() != 1 {
		t.Errorf("requests before the interval: geo %d, forecast %d", geoHits.Load(), fcHits.Load())
	}
	weatherTick(now.Add(16 * time.Minute))
	if geoHits.Load() != 1 || fcHits.Load() != 2 {
		t.Errorf("requests after the interval: geo %d, forecast %d", geoHits.Load(), fcHits.Load())
	}
	// Merged into the time line, the weather leaves its own line.
	c.WeatherOptions.InTime = true
	useConfig(t, c)
	if weatherLine(c) != "" || !strings.Contains(clockText(c), "☀️ Clear 21°C") {
		t.Errorf("merge: weather line %q, time line %q", weatherLine(c), clockText(c))
	}
}

func TestWeatherTickFailures(t *testing.T) {
	fail := true
	_, fcHits := fakeOpenMeteo(t,
		func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(londonGeo)) },
		func(w http.ResponseWriter, r *http.Request) {
			if fail {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(sunnyForecast))
		})
	c := DefaultConfig()
	c.DisplayOptions.Weather = true
	c.WeatherOptions.City = "London"
	useConfig(t, c)

	now := time.Now()
	weatherTick(now)
	if st := CurrentWeatherStatus(); st.State != weatherLimited || st.Line != "" {
		t.Fatalf("rate limited: %+v", st)
	}
	weatherTick(now.Add(30 * time.Second)) // still backing off
	if fcHits.Load() != 1 {
		t.Errorf("retried during the back-off: %d requests", fcHits.Load())
	}
	fail = false
	weatherTick(now.Add(61 * time.Second))
	if st := CurrentWeatherStatus(); st.State != weatherOK || fcHits.Load() != 2 {
		t.Errorf("after the back-off: %+v, %d requests", st, fcHits.Load())
	}

	// An unknown city is not looked up again until it changes.
	c.WeatherOptions.City = "Nowhere"
	useConfig(t, c)
	weatherGeoBase = weatherForecastBase // answers without "results"
	weatherTick(now.Add(2 * time.Minute))
	if st := CurrentWeatherStatus(); st.State != weatherNotFound {
		t.Errorf("unknown city: %+v", st)
	}
}

func TestSanitizeWeather(t *testing.T) {
	c := DefaultConfig()
	c.WeatherOptions.TempUnit = "kelvin"
	c.WeatherOptions.WindUnit = "knots"
	c.WeatherOptions.IntervalMin = 1
	c.WeatherOptions.Decimals = 5
	c.WeatherOptions.City = "  New   York \n"
	c.sanitize()
	o := c.WeatherOptions
	if o.TempUnit != "c" || o.WindUnit != "kmh" || o.IntervalMin != 10 || o.Decimals != 1 || o.City != "New York" {
		t.Errorf("got %+v", o)
	}
}
